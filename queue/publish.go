package queue

import (
	"context"
	"encoding/json"
	"time"

	"github.com/cockroachdb/errors"
	"github.com/google/uuid"
	"github.com/uptrace/bun"
	"go.opentelemetry.io/otel/propagation"

	"github.com/go-tenon/tenon/logging"
)

// insertBatch 是一条语句写入 queue_outbox 的最多任务数。
const insertBatch = 1000

// PublishOption 调整 Topic.Publish 发布的任务。
type PublishOption func(*publishOptions)

// publishOptions 是 Publish 的选项。
type publishOptions struct {
	at       time.Time
	dedupKey string
}

// Delay 让任务在 d 之后才被处理。
func Delay(d time.Duration) PublishOption {
	at := time.Now().Add(d)
	return func(o *publishOptions) { o.at = at }
}

// At 让任务在 t 之后才被处理，t 早于当前时间时立即处理。
func At(t time.Time) PublishOption {
	return func(o *publishOptions) { o.at = t }
}

// DedupKey 指定任务的去重键：在 JetStream stream 的去重窗口内，同一个 topic 下相同 key 的重复发布会被合并为一条任务。
// key 为空时不启用自定义去重键，仍按任务 UUID 去重。
func DedupKey(key string) PublishOption {
	return func(o *publishOptions) { o.dedupKey = key }
}

// propagated 是从发布方 context 带到处理函数 context 的日志字段。
var propagated = []string{logging.AttrRequestID}

// publish 把 payloads 作为任务写入 db 的 queue_outbox 表，每个 payload 一个任务，以新的 UUIDv7 作为任务 ID。
// ctx 中的 request_id 日志字段和 OpenTelemetry 链路（W3C traceparent）写入任务元数据，处理时还原到处理函数的 context 中。
// db 是事务时，任务随事务提交，回滚时一起取消；Worker 批量转发到 JetStream 后从表里删除。
func publish[T any](ctx context.Context, db bun.IDB, topic string, payloads []T, opts ...PublishOption) error {
	if db == nil {
		panic("queue: nil database")
	}
	if !validName(topic) {
		return errors.Newf("queue: topic %q must be 1 to 64 letters, digits, '.', '_' or '-', without empty segments", topic)
	}
	if len(payloads) == 0 {
		return nil
	}
	var o publishOptions
	for _, opt := range opts {
		opt(&o)
	}
	now := time.Now().UTC()
	at := now
	if o.at.After(now) {
		at = o.at.UTC()
	}
	metadata := map[string]string{}
	for _, a := range logging.ContextAttrs(ctx) {
		for _, key := range propagated {
			if a.Key == key {
				metadata[key] = a.Value.String()
			}
		}
	}
	traceContext.Inject(ctx, propagation.MapCarrier(metadata))
	if o.dedupKey != "" {
		metadata[dedupMetaKey] = o.dedupKey
	}

	rows := make([]outboxJob, len(payloads))
	for i, p := range payloads {
		data, err := json.Marshal(p)
		if err != nil {
			return errors.Wrapf(err, "queue: encode %q payload", topic)
		}
		rows[i] = outboxJob{
			ID:          uuid.Must(uuid.NewV7()),
			Topic:       topic,
			Payload:     data,
			Metadata:    metadata,
			AvailableAt: at,
		}
	}
	return errors.Wrapf(insertOutbox(ctx, db, rows), "queue: publish to %q", topic)
}

// insertOutbox 分批把 rows 插入 queue_outbox。
func insertOutbox(ctx context.Context, db bun.IDB, rows []outboxJob) error {
	for start := 0; start < len(rows); start += insertBatch {
		batch := rows[start:min(start+insertBatch, len(rows))]
		if _, err := db.NewInsert().Model(&batch).Exec(ctx); err != nil {
			return err
		}
	}
	return nil
}
