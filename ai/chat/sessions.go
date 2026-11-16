package chat

import (
	"context"
	"database/sql"
	"sync"

	"github.com/cockroachdb/errors"
	"github.com/uptrace/bun"
	"trpc.group/trpc-go/trpc-agent-go/session"
	tpg "trpc.group/trpc-go/trpc-agent-go/session/postgres"
	storage "trpc.group/trpc-go/trpc-agent-go/storage/postgres"
)

// NewSessions 创建把对话保存在 PostgreSQL 中的 session.Service，使用应用的数据库连接池，表由 Migrations 创建。
//
// 它使用 trpc-agent-go 的 session/postgres，表名前缀为 agent_，不自动建表。
// opts 原样传入，例如 tpg.WithSessionTTL 让对话过期。Close 不关闭 db。
func NewSessions(db *bun.DB, opts ...tpg.ServiceOpt) (session.Service, error) {
	install.Do(installBuilder)
	base := []tpg.ServiceOpt{
		tpg.WithExtraOptions(shared{db.DB}),
		tpg.WithTablePrefix("agent"),
		tpg.WithSkipDBInit(true),
	}
	return tpg.NewService(append(base, opts...)...)
}

// shared 通过 ExtraOptions 把应用的连接池交给 client builder。
type shared struct{ db *sql.DB }

var install sync.Once

// installBuilder 包装 trpc-agent-go 的全局 client builder：参数中带有 shared 时使用应用的连接池，
// 否则交给原来的 builder，应用中其他直接使用 trpc-agent-go 的地方保持原样。
func installBuilder() {
	prev := storage.GetClientBuilder()
	storage.SetClientBuilder(func(ctx context.Context, opts ...storage.ClientBuilderOpt) (storage.Client, error) {
		var o storage.ClientBuilderOpts
		for _, opt := range opts {
			opt(&o)
		}
		for _, x := range o.ExtraOptions {
			if s, ok := x.(shared); ok {
				return client(s), nil
			}
		}
		return prev(ctx, opts...)
	})
}

// client 在应用的连接池上实现 storage.Client，行为与 trpc-agent-go 的默认实现相同，只是 Close 不关闭连接池。
type client struct{ db *sql.DB }

func (c client) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	return c.db.ExecContext(ctx, query, args...)
}

func (c client) Query(ctx context.Context, handler storage.HandlerFunc, query string, args ...any) error {
	rows, err := c.db.QueryContext(ctx, query, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	if err := handler(rows); err != nil {
		return err
	}
	return rows.Err()
}

func (c client) Transaction(ctx context.Context, fn storage.TxFunc) (err error) {
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() {
		if p := recover(); p != nil {
			_ = tx.Rollback()
			panic(p)
		}
	}()
	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

func (client) Close() error { return nil }

// sessionTables 是 Migrations 中按 user_id 保存对话的表。
var sessionTables = []string{
	"agent_session_states", "agent_session_events", "agent_session_track_events",
	"agent_session_summaries", "agent_user_states",
}

// EraseUser 在 db 中删除 userID 在全部应用（app_name）中的对话、摘要和用户状态，用于注销账号。
// trpc-agent-go 删除对话时只标记 deleted_at，EraseUser 直接删除这些行。
// userID 是 Runs 的方法收到的 userID，由应用约定，例如主体的 ID；登记到 privacy 时包一层：
//
//	{Name: "ai_sessions", Erase: func(ctx context.Context, db bun.IDB, ref identity.Ref) error {
//	    return chat.EraseUser(ctx, db, ref.ID)
//	}}
func EraseUser(ctx context.Context, db bun.IDB, userID string) error {
	if userID == "" {
		return errors.New("chat: erase user: empty user ID")
	}
	for _, table := range sessionTables {
		if _, err := db.NewDelete().TableExpr(table).Where("user_id = ?", userID).Exec(ctx); err != nil {
			return errors.Wrapf(err, "chat: erase user from %s", table)
		}
	}
	return nil
}
