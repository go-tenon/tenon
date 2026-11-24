package queue

import (
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

// scopeName 是队列指标和链路的 instrumentation scope。
const scopeName = "github.com/go-tenon/tenon/queue"

// 指标和 span 的属性名。消息系统和 topic 沿用 OpenTelemetry 消息语义约定的名称，指标和链路按同一个字段筛选。
const (
	attrSystem      = attribute.Key("messaging.system")
	attrDestination = attribute.Key("messaging.destination.name")
	attrOutcome     = attribute.Key("queue.job.outcome")
)

// system 是 messaging.system 属性的值。
const system = "nats"

// WithTelemetry 让 Worker 报告 OpenTelemetry 指标和链路。
//
// 指标的属性 messaging.system 是 nats，messaging.destination.name 是 topic：
//
//   - queue.job.wait（直方图，秒）：任务从可以执行到开始处理等了多久。重试、租约到期或停止时放回、由 JetStream 重新投递的任务不记录。
//   - queue.job.duration（直方图，秒）：处理耗时，属性 queue.job.outcome 是结果：
//     succeeded、retrying、failed、released。按结果计数就是吞吐和失败率。
//
// 转发和 outbox 的指标没有 messaging 属性：
//
//   - queue.relay.jobs（计数）：从 queue_outbox 转发到 JetStream 的任务数。
//   - queue.outbox.pending（仪表）：queue_outbox 中已到期、等待转发的任务数。
//   - queue.outbox.deferred（仪表）：queue_outbox 中尚未到期的延迟任务数。
//
// 每次执行是一个 consumer span，名称是 "process <topic>"，接在发布任务时的链路之后：
// HTTP 请求中发布的任务，执行时出现在同一条链路中。处理失败或重试时 span 标记为错误。
// 不设置时，任务仍然带着发布方的链路：处理函数的 ctx 中是发布方的 span，处理函数自己创建的 span 接在它之后。
//
// JetStream 中积压的任务数由 NATS 统计，用 nats 命令行查看。mp 或 tp 为 nil 时 panic；只需要其中一种时，另一个传 noop 实现。
func WithTelemetry(mp metric.MeterProvider, tp trace.TracerProvider) WorkerOption {
	if mp == nil || tp == nil {
		panic("queue: nil meter or tracer provider")
	}
	metrics := newJobMetrics(mp)
	tracer := tp.Tracer(scopeName)
	return func(w *Worker) {
		w.tracer = tracer
		w.observers = append(w.observers, metrics.record)
		observeRelay(mp, w)
	}
}
