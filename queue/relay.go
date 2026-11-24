package queue

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/cockroachdb/errors"
	"github.com/google/uuid"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/uptrace/bun"
)

const (
	// relayBatch 是单次事务从 queue_outbox 转发到 JetStream 的最多任务数。
	relayBatch = 500
	// relayMaxInterval 是队列空闲时退避后的最长轮询间隔。
	relayMaxInterval = 500 * time.Millisecond
	// relayErrorWait 是转发失败后重试的等待时间。
	relayErrorWait = time.Second
)

// relayMinInterval 是发现任务后重置的轮询间隔，测试中可以缩短。
var relayMinInterval = 50 * time.Millisecond

// relay 持续从 queue_outbox 读取已到时间的任务并转发到 JetStream，直到 ctx 结束。
//
// 多个 Worker 通过 FOR UPDATE SKIP LOCKED 并行领取互不重叠的批次；
// 有满批任务时立即拉取下一批，空闲时按指数退避到 relayMaxInterval。
func (w *Worker) relay(ctx context.Context) {
	interval := relayMinInterval
	for ctx.Err() == nil {
		n, err := w.relayOnce(ctx)
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			w.logger.Error("relay jobs failed", "error", err)
			wait(ctx, relayErrorWait)
			continue
		}
		if n >= relayBatch {
			interval = relayMinInterval
			continue
		}
		if n > 0 {
			interval = relayMinInterval
		} else {
			interval = min(interval*2, relayMaxInterval)
		}
		wait(ctx, interval)
	}
}

// relayOnce 在一个事务中锁住最多 relayBatch 条已到期的 outbox 任务，转发到 JetStream 并删除它们。
// 遇到体积超限或非法编码等永久错误时，单条转入 queue_failed_jobs 并删除 outbox 行，避免阻塞其余任务。
func (w *Worker) relayOnce(ctx context.Context) (int, error) {
	var processed int
	var published int
	err := w.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		var rows []outboxJob
		err := tx.NewSelect().Model(&rows).
			Where("o.available_at <= ?", time.Now().UTC()).
			OrderExpr("o.available_at, o.id").
			Limit(relayBatch).
			For("UPDATE SKIP LOCKED").
			Scan(ctx)
		if err != nil || len(rows) == 0 {
			return err
		}

		type inFlight struct {
			row    outboxJob
			future jetstream.PubAckFuture
		}
		pending := make([]inFlight, 0, len(rows))
		doneIDs := make([]uuid.UUID, 0, len(rows))

		for _, row := range rows {
			dedup := row.Metadata[dedupMetaKey]
			meta := row.Metadata
			if dedup != "" {
				meta = make(map[string]string, len(row.Metadata)-1)
				for k, v := range row.Metadata {
					if k != dedupMetaKey {
						meta[k] = v
					}
				}
			}
			body, err := json.Marshal(envelope{
				ID:          row.ID,
				Payload:     row.Payload,
				Metadata:    meta,
				AvailableAt: row.AvailableAt,
			})
			if err != nil {
				if recErr := w.recordPoison(ctx, tx, row, errors.Wrapf(err, "queue: encode %q job", row.Topic)); recErr != nil {
					return recErr
				}
				doneIDs = append(doneIDs, row.ID)
				continue
			}
			id := msgID(row.ID, row.CreatedAt)
			if dedup != "" {
				id = "dedup:" + row.Topic + ":" + dedup
			}
			f, err := w.publishAsync(row.Topic, body, id)
			if err != nil {
				if isPermanentPublishError(err) {
					if recErr := w.recordPoison(ctx, tx, row, errors.Wrapf(err, "queue: publish %q job", row.Topic)); recErr != nil {
						return recErr
					}
					doneIDs = append(doneIDs, row.ID)
					continue
				}
				return errors.Wrapf(err, "queue: publish to %q", row.Topic)
			}
			pending = append(pending, inFlight{row: row, future: f})
		}

		if len(pending) > 0 {
			ackCtx, cancel := context.WithTimeout(ctx, publishTimeout)
			defer cancel()
			for _, item := range pending {
				select {
				case <-item.future.Ok():
					doneIDs = append(doneIDs, item.row.ID)
					published++
				case err := <-item.future.Err():
					if isPermanentPublishError(err) {
						if recErr := w.recordPoison(ctx, tx, item.row, errors.Wrapf(err, "queue: publish %q job", item.row.Topic)); recErr != nil {
							return recErr
						}
						doneIDs = append(doneIDs, item.row.ID)
						continue
					}
					return errors.Wrap(err, "queue: publish jobs")
				case <-ackCtx.Done():
					return errors.Wrap(ackCtx.Err(), "queue: wait for publish acks")
				}
			}
		}

		if len(doneIDs) > 0 {
			if _, err := tx.NewDelete().Model((*outboxJob)(nil)).Where("id IN (?)", bun.List(doneIDs)).Exec(ctx); err != nil {
				return errors.Wrap(err, "queue: delete relayed outbox jobs")
			}
		}
		processed = len(doneIDs)
		return nil
	})
	if err != nil {
		return 0, err
	}
	w.countRelayed(ctx, published)
	return processed, nil
}

// recordPoison 在转发事务中把无法发送到 JetStream 的永久坏消息转入失败任务表。
func (w *Worker) recordPoison(ctx context.Context, tx bun.Tx, row outboxJob, cause error) error {
	w.logger.Error("move unpublishable job to failed jobs", "topic", row.Topic, "id", row.ID, "error", cause)
	return recordFailed(ctx, tx, failedJob{
		ID:       row.ID,
		Topic:    row.Topic,
		Payload:  row.Payload,
		Metadata: row.Metadata,
		Attempts: 0,
		Error:    fmt.Sprintf("%+v", cause),
	})
}

// isPermanentPublishError 报告发布到 JetStream 时的错误是否属于单条消息自身的永久错误，
// 例如消息体超过服务器 max_payload 或 subject 不合法。
func isPermanentPublishError(err error) bool {
	return errors.Is(err, nats.ErrMaxPayload) || errors.Is(err, nats.ErrBadSubject)
}
