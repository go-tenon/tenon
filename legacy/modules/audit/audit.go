// Package audit 是审计日志：记录谁在什么时间、从哪里、对什么做了什么。
//
// 记录在 audit_logs 表中，只追加。请求相关的信息（IP、User-Agent、request_id）由 Middleware
// 放入 context，操作者默认取当前登录用户（auth.UserID），业务代码只需要写动作与对象：
//
//	audits := audit.New(a.DB)
//	a.Router.Use(audit.Middleware)
//
//	audits.Log(ctx, audit.Entry{Action: "order.refund", ResourceType: "order", ResourceID: id,
//		Data: map[string]any{"amount": "128.00"}})
//
// 不保存给人看的文字：显示时把动作翻译为摘要，消息 ID 为 "audit." + 动作，模板参数为 Data，例如
//
//	audit.order.refund: "退款 ¥{{.amount}}"
//
// 没有对应消息时显示动作本身。模块可以为自己的事件提供摘要（如 account 的 audit.account.login）。
//
// 需要与业务数据同时成功或失败时，在业务事务中调用 RecordTx。查询接口 ListEndpoint 需要 audit.view 能力，
// MineEndpoint 列出当前用户自己的记录（设置页的“安全日志”）。定期清理旧记录用 Prune。
//
// 迁移：把 audit.Migrations 加入 app.Options.Publish。
package audit

import (
	"context"
	"embed"
	"io/fs"
	"log/slog"
	"net/http"
	"time"
	"unicode/utf8"
	"uuid"

	"github.com/cockroachdb/errors"
	"github.com/uptrace/bun"

	"github.com/ryan053857/go-kit/database"
	"github.com/ryan053857/go-kit/httpx"
	"github.com/ryan053857/go-kit/httpx/auth"
	"github.com/ryan053857/go-kit/i18n"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// Migrations 是本模块的迁移。
var Migrations = database.MigrationSource{Name: "audit", FS: mustSub(migrationsFS, "migrations")}

//go:embed locales/*.yaml
var locales embed.FS

func init() { i18n.MustLoad(locales) }

// View 是查看全部审计日志的能力（权限）。
const View = "audit.view"

// Entry 是一条待写入的审计记录。
type Entry struct {
	Action       string         // 必填，建议用“模块.动作”，如 order.refund
	ActorID      uuid.UUID      // 操作者；零值时取当前登录用户，没有登录用户则为空（系统操作）
	ResourceType string         // 对象类型，如 order
	ResourceID   string         // 对象 ID
	Data         map[string]any // 细节，也是摘要的模板参数；不要放密码、token 等敏感信息
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
	Data         map[string]any `bun:"data,type:jsonb,notnull"`
	Failed       bool           `bun:"failed,notnull"`
	IP           string         `bun:"ip,notnull"`
	UserAgent    string         `bun:"user_agent,notnull"`
	RequestID    string         `bun:"request_id,notnull"`
}

// Service 写入与查询审计日志。
type Service struct {
	db  *bun.DB
	now func() time.Time
}

// New 返回 Service。
func New(db *bun.DB) *Service { return &Service{db: db, now: time.Now} }

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
		Data: e.Data, Failed: e.Failed, RequestID: httpx.RequestIDFrom(ctx)}
	if l.ActorID == uuid.Nil() {
		l.ActorID, _ = auth.UserID(ctx)
	}
	if l.Data == nil {
		l.Data = map[string]any{}
	}
	if info, ok := ctx.Value(requestKey{}).(requestInfo); ok {
		l.IP, l.UserAgent = info.ip, info.userAgent
	}
	l.CreatedAt = s.now()
	_, err := db.NewInsert().Model(l).Exec(ctx)
	return errors.Wrapf(err, "audit: record %s", e.Action)
}

// Log 写入一条审计记录，失败时只记录错误日志。用于业务已经完成、不应因审计失败而报错的场景。
func (s *Service) Log(ctx context.Context, e Entry) {
	if err := s.Record(ctx, e); err != nil {
		slog.ErrorContext(ctx, "audit log failed", "action", e.Action, "err", err)
	}
}

// Summary 返回记录给人看的摘要：按 ctx 的语言翻译 "audit." + 动作，没有对应消息时为动作本身。
func Summary(ctx context.Context, l *Log) string {
	id := "audit." + l.Action
	if s := i18n.T(ctx, id, l.Data); s != id {
		return s
	}
	return l.Action
}

// Filter 是查询条件，零值字段不参与过滤。
type Filter struct {
	ActorID      uuid.UUID
	Action       string // 精确匹配
	ResourceType string
	ResourceID   string
	From, To     time.Time // [From, To)
	database.Cursor
}

// List 按时间倒序返回记录。返回的 next 非零时，作为下一页的 Cursor.Before。
func (s *Service) List(ctx context.Context, f Filter) ([]Log, uuid.UUID, error) {
	logs := []Log{}
	q := s.db.NewSelect().Model(&logs)
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
	next, err := database.Paginate(ctx, q, &logs, f.Cursor)
	return logs, next, errors.Wrap(err, "audit: list")
}

// Prune 删除早于 olderThan 之前的记录，返回删除的条数。由产品的定时任务调用。
func (s *Service) Prune(ctx context.Context, olderThan time.Duration) (int64, error) {
	res, err := s.db.NewDelete().Model((*Log)(nil)).Where("created_at < ?", s.now().Add(-olderThan)).Exec(ctx)
	if err != nil {
		return 0, errors.Wrap(err, "audit: prune")
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
