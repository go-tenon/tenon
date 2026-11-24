package queue_test

import (
	"context"
	"testing"
	"time"

	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	tracenoop "go.opentelemetry.io/otel/trace/noop"

	"github.com/go-tenon/tenon/queue"
	"github.com/go-tenon/tenon/queue/queuetest"
)

// gauges 采集一次，返回 int64 仪表和计数的值。
func gauges(t *testing.T, reader *sdkmetric.ManualReader) map[string]int64 {
	t.Helper()
	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatal(err)
	}
	got := map[string]int64{}
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			switch data := m.Data.(type) {
			case metricdata.Gauge[int64]:
				for _, p := range data.DataPoints {
					got[m.Name] = p.Value
				}
			case metricdata.Sum[int64]:
				for _, p := range data.DataPoints {
					got[m.Name] = p.Value
				}
			}
		}
	}
	return got
}

func TestRelayMetrics(t *testing.T) {
	t.Parallel()
	db := openDB(t)
	cfg := queuetest.Config(t)
	reader := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	done := make(chan struct{}, 2)
	start(t, db, cfg, func(w *queue.Worker) {
		jobs.Handle(w, func(context.Context, payload) error {
			done <- struct{}{}
			return nil
		})
	}, queue.WithTelemetry(mp, tracenoop.NewTracerProvider()))

	publish(t, db, 1)
	publish(t, db, 2, queue.Delay(200*time.Millisecond))
	publish(t, db, 3, queue.Delay(time.Hour))
	for range 2 {
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Fatal("jobs not handled")
		}
	}
	// 任务进入 JetStream 并被处理完时，relayOnce 删除 outbox 行的事务可能仍在提交途中，等待其可见。
	var got map[string]int64
	eventually(t, 5*time.Second, func() bool {
		got = gauges(t, reader)
		return got["queue.relay.jobs"] == 2 && got["queue.outbox.pending"] == 0 && got["queue.outbox.deferred"] == 1
	})
}
