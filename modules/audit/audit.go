// Package audit 是审计日志模块：记录谁在什么时间、从哪里、对什么做了什么。
//
// 记录在 audit_logs 表中，只追加。请求相关的信息（IP、User-Agent、request_id）由 Middleware
// 放入 context，操作者默认取当前登录用户，因此业务代码只需要写动作与对象：
//
//	audits := audit.New(a.DB, account.CurrentUserID)
//	a.Router.Use(audit.Middleware)
//	users.OnEvent(audits.AccountEvent) // 登录、改密、两步验证等账号事件
//
//	audits.Log(ctx, audit.Entry{Action: "order.refund", ResourceType: "order", ResourceID: id,
//		Summary: "退款 ¥128.00", Data: map[string]any{"reason": reason}})
//
// 需要与业务数据同时成功或失败时，在业务事务中调用 RecordTx。
//
// 查询接口：ListEndpoint 供管理后台使用（产品负责只让管理员访问），
// MineEndpoint 列出当前用户自己的记录（设置页的“安全日志”）。
// 定期清理旧记录用 Prune。
//
// 迁移：把 audit.Migrations 加入 app.Options.Publish。
package audit

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"time"
	"unicode/utf8"
	"uuid"

	"github.com/uptrace/bun"

	"github.com/ryan053857/go-kit/database"
	"github.com/ryan053857/go-kit/httpx"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// Migrations 是本模块的迁移。
var Migrations = database.MigrationSource{Name: "audit", FS: mustSub(migrationsFS, "migrations")}

// Entry 是一条待写入的审计记录。
type Entry struct {
	Action       string         // 必填，建议用“模块.动作”，如 order.refund
	ActorID      uuid.UUID      // 操作者；零值时取当前登录用户，没有登录用户则为空（系统操作）
	ResourceType string         // 对象类型，如 order
	ResourceID   string         // 对象 ID
	Summary      string         // 给人看的描述
	Data         map[string]any // 细节，不要放密码、token 等敏感信息
	Failed       bool           // 操作失败（如登录失败、权限不足）
}

// Log 是一条审计记录。
type Log struct {
	bun.BaseModel `bun:"table:audit_logs,alias:l"`
	database.CreatedModel

	ActorID      uuid.UUID      `bun:"actor_id,nullzero,type:uuid"`
	Action       string         `bun:"action,notnull"`
	ResourceType string         `bun:"resource_type,notnull"`
	ResourceID   string         `bun:"resource_id,notnull"`
	Summary      string         `bun:"summary,notnull"`
	Data         map[string]any `bun:"data,type:jsonb,notnull"`
	Failed       bool           `bun:"failed,notnull"`
	IP           string         `bun:"ip,notnull"`
	UserAgent    string         `bun:"user_agent,notnull"`
	RequestID    string         `bun:"request_id,notnull"`
}

// Service 写入与查询审计日志。
type Service struct {
	db    *bun.DB
	actor func(context.Context) (uuid.UUID, bool)
}

// New 返回 Service。actor 返回当前登录用户的 ID（如 account.CurrentUserID），用于 Entry.ActorID 为空时；可为 nil。
func New(db *bun.DB, actor func(context.Context) (uuid.UUID, bool)) *Service {
	return &Service{db: db, actor: actor}
}

// Record 写入一条审计记录。
func (s *Service) Record(ctx context.Context, e Entry) error {
	return s.RecordTx(ctx, s.db, e)
}

// RecordTx 在 db（可以是 bun.Tx）中写入一条审计记录，用于与业务数据在同一事务中提交。
func (s *Service) RecordTx(ctx context.Context, db bun.IDB, e Entry) error {
	if e.Action == "" {
		return errors.New("audit: action is required")
	}
	l := &Log{ActorID: e.ActorID, Action: e.Action, ResourceType: e.ResourceType, ResourceID: e.ResourceID,
		Summary: e.Summary, Data: e.Data, Failed: e.Failed, RequestID: httpx.RequestIDFrom(ctx)}
	if l.ActorID == uuid.Nil() && s.actor != nil {
		l.ActorID, _ = s.actor(ctx)
	}
	if l.Data == nil {
		l.Data = map[string]any{}
	}
	if info, ok := ctx.Value(requestKey{}).(requestInfo); ok {
		l.IP, l.UserAgent = info.ip, info.userAgent
	}
	if _, err := db.NewInsert().Model(l).Exec(ctx); err != nil {
		return fmt.Errorf("audit: record %s: %w", e.Action, err)
	}
	return nil
}

// Log 写入一条审计记录，失败时只记录错误日志。用于业务已经完成、不应因审计失败而报错的场景。
func (s *Service) Log(ctx context.Context, e Entry) {
	if err := s.Record(ctx, e); err != nil {
		slog.ErrorContext(ctx, "audit log failed", "action", e.Action, "err", err)
	}
}

// Filter 是查询条件，零值字段不参与过滤。
type Filter struct {
	ActorID      uuid.UUID
	Action       string // 精确匹配
	ResourceType string
	ResourceID   string
	From, To     time.Time // [From, To)
	Before       uuid.UUID // 游标：只返回 ID 小于它的记录（即更早的记录）
	Limit        int       // 默认 50，最大 200
}

// List 按时间倒序返回记录。返回的 next 非零时，把它作为下一页的 Filter.Before。
func (s *Service) List(ctx context.Context, f Filter) (logs []Log, next uuid.UUID, err error) {
	limit := f.Limit
	switch {
	case limit <= 0:
		limit = 50
	case limit > 200:
		limit = 200
	}
	logs = []Log{}
	q := s.db.NewSelect().Model(&logs).OrderExpr("id DESC").Limit(int64(limit + 1))
	if f.ActorID != uuid.Nil() {
		q = q.Where("actor_id = ?", f.ActorID)
	}
	if f.Action != "" {
		q = q.Where("action = ?", f.Action)
	}
	if f.ResourceType != "" {
		q = q.Where("resource_type = ?", f.ResourceType)
	}
	if f.ResourceID != "" {
		q = q.Where("resource_id = ?", f.ResourceID)
	}
	if !f.From.IsZero() {
		q = q.Where("created_at >= ?", f.From)
	}
	if !f.To.IsZero() {
		q = q.Where("created_at < ?", f.To)
	}
	if f.Before != uuid.Nil() {
		q = q.Where("id < ?", f.Before)
	}
	if err := q.Scan(ctx); err != nil {
		return nil, uuid.Nil(), fmt.Errorf("audit: list: %w", err)
	}
	if len(logs) > limit {
		logs = logs[:limit]
		next = logs[limit-1].ID
	}
	return logs, next, nil
}

// Prune 删除早于 olderThan 之前的记录，返回删除的条数。由产品的定时任务调用。
func (s *Service) Prune(ctx context.Context, olderThan time.Duration) (int64, error) {
	res, err := s.db.NewDelete().Model((*Log)(nil)).Where("created_at < ?", time.Now().Add(-olderThan)).Exec(ctx)
	if err != nil {
		return 0, fmt.Errorf("audit: prune: %w", err)
	}
	n, _ := res.RowsAffected()
	return n, nil
}

type requestKey struct{}

type requestInfo struct{ ip, userAgent string }

// Middleware 把客户端 IP 与 User-Agent 放入 context，供 Record 使用。挂在 httpx.RealIP 之后。
func Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := context.WithValue(r.Context(), requestKey{}, requestInfo{ip: httpx.ClientIP(r), userAgent: truncate(r.UserAgent(), 512)})
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func truncate(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n])
}

func mustSub(fsys fs.FS, dir string) fs.FS {
	sub, err := fs.Sub(fsys, dir)
	if err != nil {
		panic(err)
	}
	return sub
}
