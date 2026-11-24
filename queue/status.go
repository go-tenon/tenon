package queue

import (
	"context"
	"database/sql"
	"time"

	"github.com/cockroachdb/errors"
	"github.com/uptrace/bun"
)

// Status 是队列在数据库一侧的状态：queue_outbox 中的待转发任务和延迟任务。
// JetStream 中等待处理和处理中的任务由 NATS 统计，见 Admin.Status。
type Status struct {
	Pending       int64     // 已到时间、等待 Worker 转发到 JetStream 的任务数
	Deferred      int64     // 还没到执行时间的延迟任务数
	OldestPending time.Time // 最早一条待转发任务的可用时间；Pending 为 0 时为零值
}

// ReadStatus 读取 db 所在数据库的队列 outbox 状态。
func ReadStatus(ctx context.Context, db bun.IDB) (Status, error) {
	now := time.Now().UTC()
	var row struct {
		Pending       int64        `bun:"pending"`
		Deferred      int64        `bun:"deferred"`
		OldestPending sql.NullTime `bun:"oldest_pending"`
	}
	err := db.NewRaw(`
SELECT
    count(*) FILTER (WHERE available_at <= ?) AS pending,
    count(*) FILTER (WHERE available_at > ?)  AS deferred,
    min(available_at) FILTER (WHERE available_at <= ?) AS oldest_pending
FROM queue_outbox`, now, now, now).Scan(ctx, &row)
	if err != nil {
		return Status{}, errors.Wrap(err, "queue: read outbox status")
	}
	s := Status{Pending: row.Pending, Deferred: row.Deferred}
	if row.OldestPending.Valid {
		s.OldestPending = row.OldestPending.Time.UTC()
	}
	return s, nil
}

// checkTables 确认队列所需的 queue_outbox 和 queue_failed_jobs 表已通过迁移创建。
func checkTables(ctx context.Context, db bun.IDB) error {
	var outbox, failed bool
	err := db.NewRaw("SELECT to_regclass('queue_outbox') IS NOT NULL, to_regclass('queue_failed_jobs') IS NOT NULL").
		Scan(ctx, &outbox, &failed)
	if err != nil {
		return errors.Wrap(err, "queue: check tables")
	}
	if !outbox || !failed {
		return errors.New("queue: queue tables do not exist: run the queue migrations")
	}
	return nil
}
