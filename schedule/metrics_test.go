package schedule_test

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/cockroachdb/errors"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	tracenoop "go.opentelemetry.io/otel/trace/noop"

	"github.com/go-tenon/tenon/schedule"
	"github.com/go-tenon/tenon/schedule/scheduletest"
)

// brokenStore 是总是出错的状态存储。
type brokenStore struct{}

func (brokenStore) Claim(context.Context, string, time.Time, string, time.Duration, bool) (schedule.ClaimResult, error) {
	return 0, errors.New("store down")
}
func (brokenStore) AcquireRunning(context.Context, string, string, time.Duration) (bool, error) {
	return false, errors.New("store down")
}
func (brokenStore) Refresh(context.Context, string, string, time.Duration) (bool, error) {
	return false, nil
}
func (brokenStore) Release(context.Context, string, string) error { return nil }
func (brokenStore) States(context.Context) (map[string]schedule.TaskState, error) {
	return nil, errors.New("store down")
}

func TestMetrics(t *testing.T) {
	ctx := context.Background()
	reader := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	quiet := slog.New(slog.DiscardHandler)
	newScheduler := func(store schedule.Store) *schedule.Scheduler {
		return schedule.New(store, schedule.DefaultConfig(), quiet, schedule.WithTelemetry(mp, tracenoop.NewTracerProvider()))
	}

	s := newScheduler(scheduletest.NewStore())
	s.Add("ok", "@everysecond", func(context.Context) error { return nil })
	s.Add("fails", "@everysecond", func(context.Context) error { return errors.New("bad") })
	for i := range 2 {
		if err := s.RunDue(ctx, base.Add(time.Duration(i)*time.Second)); err == nil {
			t.Fatal("expected the failing task's error")
		}
	}

	// 上一次还没结束时被跳过。
	overlapping := newScheduler(scheduletest.NewStore())
	release, started := make(chan struct{}), make(chan struct{})
	overlapping.Add("slow", "@everysecond", func(context.Context) error {
		close(started)
		<-release
		return nil
	})
	done := make(chan error, 1)
	go func() { done <- overlapping.RunDue(ctx, base) }()
	<-started
	if err := overlapping.RunDue(ctx, base.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}

	broken := newScheduler(brokenStore{})
	broken.Add("ok", "@everysecond", func(context.Context) error { return nil })
	if err := broken.RunDue(ctx, base); err == nil {
		t.Fatal("expected the store error")
	}

	var rm metricdata.ResourceMetrics
	if err := reader.Collect(ctx, &rm); err != nil {
		t.Fatal(err)
	}
	got := map[string]int64{}
	for _, m := range rm.ScopeMetrics[0].Metrics {
		switch d := m.Data.(type) {
		case metricdata.Histogram[float64]:
			for _, dp := range d.DataPoints {
				task, _ := dp.Attributes.Value("schedule.task")
				outcome, _ := dp.Attributes.Value("schedule.task.outcome")
				got[m.Name+" "+task.AsString()+" "+outcome.AsString()] = int64(dp.Count)
			}
		case metricdata.Sum[int64]:
			for _, dp := range d.DataPoints {
				task, _ := dp.Attributes.Value("schedule.task")
				reason, _ := dp.Attributes.Value("schedule.task.skip_reason")
				got[m.Name+" "+task.AsString()+" "+reason.AsString()] = dp.Value
			}
		}
	}
	want := map[string]int64{
		"schedule.task.duration ok succeeded":   2,
		"schedule.task.duration fails failed":   2,
		"schedule.task.duration slow succeeded": 1,
		"schedule.task.skipped slow overlap":    1,
		"schedule.task.skipped ok store_failed": 1,
	}
	if len(got) != len(want) {
		t.Errorf("got %v", got)
	}
	for k, n := range want {
		if got[k] != n {
			t.Errorf("%s = %d, want %d", k, got[k], n)
		}
	}
}
