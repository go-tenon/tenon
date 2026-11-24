package queue

import (
	"context"
	"time"

	"github.com/cockroachdb/errors"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// secondBuckets 是等待时长和处理耗时的直方图分桶（秒），从 5 毫秒到 1 小时。
var secondBuckets = []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30, 60, 300, 900, 3600}

// jobMetrics 是 Worker 记录的两个直方图。
type jobMetrics struct {
	wait, duration metric.Float64Histogram
}

// newJobMetrics 在 mp 上创建任务的指标：等待时间、执行时间和结果计数。
func newJobMetrics(mp metric.MeterProvider) jobMetrics {
	meter := mp.Meter(scopeName)
	wait, err := meter.Float64Histogram("queue.job.wait", metric.WithUnit("s"),
		metric.WithDescription("Time a job waited between becoming available and starting."),
		metric.WithExplicitBucketBoundaries(secondBuckets...))
	if err != nil {
		panic(errors.Wrap(err, "queue: create queue.job.wait"))
	}
	duration, err := meter.Float64Histogram("queue.job.duration", metric.WithUnit("s"),
		metric.WithDescription("Time spent handling a job, by outcome."),
		metric.WithExplicitBucketBoundaries(secondBuckets...))
	if err != nil {
		panic(errors.Wrap(err, "queue: create queue.job.duration"))
	}
	return jobMetrics{wait: wait, duration: duration}
}

// statusTimeout 是采集指标时读取队列状态的超时。
const statusTimeout = 5 * time.Second

// observeRelay 创建转发和 outbox 的指标：转发的任务数，以及采集时读取的待转发和延迟任务数。
func observeRelay(mp metric.MeterProvider, w *Worker) {
	meter := mp.Meter(scopeName)
	jobs, err := meter.Int64Counter("queue.relay.jobs", metric.WithUnit("{job}"),
		metric.WithDescription("Jobs relayed from queue_outbox to JetStream."))
	if err != nil {
		panic(errors.Wrap(err, "queue: create queue.relay.jobs"))
	}
	pending, err := meter.Int64ObservableGauge("queue.outbox.pending", metric.WithUnit("{job}"),
		metric.WithDescription("Due jobs waiting in queue_outbox to be relayed to JetStream."))
	if err != nil {
		panic(errors.Wrap(err, "queue: create queue.outbox.pending"))
	}
	deferred, err := meter.Int64ObservableGauge("queue.outbox.deferred", metric.WithUnit("{job}"),
		metric.WithDescription("Scheduled jobs waiting in queue_outbox until available_at."))
	if err != nil {
		panic(errors.Wrap(err, "queue: create queue.outbox.deferred"))
	}
	_, err = meter.RegisterCallback(func(ctx context.Context, o metric.Observer) error {
		ctx, cancel := context.WithTimeout(ctx, statusTimeout)
		defer cancel()
		s, err := ReadStatus(ctx, w.db)
		if err != nil {
			return nil
		}
		o.ObserveInt64(pending, s.Pending)
		o.ObserveInt64(deferred, s.Deferred)
		return nil
	}, pending, deferred)
	if err != nil {
		panic(errors.Wrap(err, "queue: register relay metrics"))
	}
	w.relayJobs = jobs
}

// countRelayed 记录转发到 JetStream 的任务数。
func (w *Worker) countRelayed(ctx context.Context, n int) {
	if w.relayJobs != nil && n > 0 {
		w.relayJobs.Add(ctx, int64(n))
	}
}

// record 是记录指标的 Observer。
func (m jobMetrics) record(ctx context.Context, e Event) {
	job := attribute.NewSet(attrSystem.String(system), attrDestination.String(e.Job.Topic))
	if e.Kind == JobStarted {
		if !e.Job.AvailableAt.IsZero() {
			m.wait.Record(ctx, max(time.Since(e.Job.AvailableAt), 0).Seconds(), metric.WithAttributeSet(job))
		}
		return
	}
	attrs := append(job.ToSlice(), attrOutcome.String(e.Kind.String()))
	m.duration.Record(ctx, e.Duration.Seconds(), metric.WithAttributes(attrs...))
}
