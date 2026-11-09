package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"hash/fnv"
	"sync"
)

// ErrNotLeader 表示当前实例不是 leader。
var ErrNotLeader = errors.New("database: not the leader")

// Leader 用 Postgres session 级 advisory lock 在多个实例中选出一个 leader：
// 第一个拿到锁的实例一直持有一个专用连接，直到连接断开或调用 Close，其他实例的 IsLeader 返回 ErrNotLeader。
//
// 它实现了 github.com/go-co-op/gocron/v2 的 Elector 接口，用于让定时任务只在一个实例上运行
// （app 包已为 a.Scheduler 配置好）。
type Leader struct {
	db  *sql.DB
	key int64

	mu   sync.Mutex
	conn *sql.Conn
}

// NewLeader 返回以 name 为锁名的 Leader。不同用途使用不同的 name。
func NewLeader(db *sql.DB, name string) *Leader {
	h := fnv.New64a()
	h.Write([]byte("go-kit/leader/" + name))
	return &Leader{db: db, key: int64(h.Sum64())}
}

// IsLeader 报告当前实例是否为 leader：已持有锁时确认连接仍然可用，否则尝试获取锁。
// 不是 leader 时返回 ErrNotLeader，数据库出错时返回其他错误。
func (l *Leader) IsLeader(ctx context.Context) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.conn != nil {
		if err := l.conn.PingContext(ctx); err == nil {
			return nil
		}
		_ = l.conn.Close() // 连接已断开，锁随之释放，重新竞争
		l.conn = nil
	}
	conn, err := l.db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("database: leader: %w", err)
	}
	var ok bool
	if err := conn.QueryRowContext(ctx, "SELECT pg_try_advisory_lock($1)", l.key).Scan(&ok); err != nil {
		_ = conn.Close()
		return fmt.Errorf("database: leader: %w", err)
	}
	if !ok {
		_ = conn.Close()
		return ErrNotLeader
	}
	l.conn = conn
	return nil
}

// Close 释放锁（如果持有）。
func (l *Leader) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.conn == nil {
		return nil
	}
	_, err := l.conn.ExecContext(context.Background(), "SELECT pg_advisory_unlock($1)", l.key)
	if cerr := l.conn.Close(); err == nil {
		err = cerr
	}
	l.conn = nil
	return err
}
