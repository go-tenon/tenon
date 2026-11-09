// Package notification 是站内通知模块：给用户发通知、列出、未读数、标记已读。
//
//	notes := notification.New(a.DB)
//	notes.Send(ctx, userID, notification.Notification{Type: "order.shipped", Title: "订单已发货",
//		Body: "订单 #1024 已由顺丰发出", URL: "/orders/1024"})
//
// 与业务数据同时提交时用 SendTx。邮件、短信等其他渠道由产品自己发送（可在同一事务中发布消息）。
// 页面上的未读数可以用 HTMX 定时刷新（hx-trigger="every 30s"）；需要即时推送时在 OnSend 回调中处理。
//
// 迁移：把 notification.Migrations 加入 app.Options.Publish。
package notification

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"strings"
	"time"
	"uuid"

	"github.com/uptrace/bun"

	"github.com/ryan053857/go-kit/database"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// Migrations 是本模块的迁移。
var Migrations = database.MigrationSource{Name: "notification", FS: mustSub(migrationsFS, "migrations")}

// ErrNotFound 表示通知不存在或不属于该用户。
var ErrNotFound = errors.New("notification: not found")

// Notification 是一条站内通知。发送时填写 Type、Title 及可选的 Body、URL、Data。
type Notification struct {
	bun.BaseModel `bun:"table:notifications,alias:n"`
	database.CreatedModel

	UserID uuid.UUID      `bun:"user_id,notnull,type:uuid"`
	Type   string         `bun:"type,notnull"`
	Title  string         `bun:"title,notnull"`
	Body   string         `bun:"body,notnull"`
	URL    string         `bun:"url,notnull"`
	Data   map[string]any `bun:"data,type:jsonb,notnull"`
	ReadAt time.Time      `bun:"read_at,nullzero"`
}

// Read 报告通知是否已读。
func (n *Notification) Read() bool { return !n.ReadAt.IsZero() }

// Service 管理站内通知。
type Service struct {
	db *bun.DB
	// OnSend 可选，在通知写入（事务提交前，SendTx 时）后调用，用于即时推送。
	OnSend func(ctx context.Context, n *Notification)
}

// New 返回 Service。
func New(db *bun.DB) *Service { return &Service{db: db} }

// Send 给 userIDs 中的每个用户发送一条通知。
func (s *Service) Send(ctx context.Context, n Notification, userIDs ...uuid.UUID) error {
	return s.SendTx(ctx, s.db, n, userIDs...)
}

// SendTx 在 db（可以是 bun.Tx）中发送通知，用于与业务数据在同一事务中提交。
func (s *Service) SendTx(ctx context.Context, db bun.IDB, n Notification, userIDs ...uuid.UUID) error {
	if n.Type == "" || strings.TrimSpace(n.Title) == "" {
		return errors.New("notification: type and title are required")
	}
	if len(userIDs) == 0 {
		return nil
	}
	if n.Data == nil {
		n.Data = map[string]any{}
	}
	list := make([]Notification, len(userIDs))
	for i, id := range userIDs {
		list[i] = n
		list[i].ID = uuid.Nil()
		list[i].UserID = id
	}
	if _, err := db.NewInsert().Model(&list).Exec(ctx); err != nil {
		return fmt.Errorf("notification: send %s: %w", n.Type, err)
	}
	if s.OnSend != nil {
		for i := range list {
			s.OnSend(ctx, &list[i])
		}
	}
	return nil
}

// ListOptions 是列表的查询条件。
type ListOptions struct {
	UnreadOnly bool
	Before     uuid.UUID // 游标：只返回 ID 小于它的通知
	Limit      int       // 默认 20，最大 100
}

// List 按时间倒序返回用户的通知。返回的 next 非零时，把它作为下一页的 Before。
func (s *Service) List(ctx context.Context, userID uuid.UUID, opt ListOptions) (list []Notification, next uuid.UUID, err error) {
	limit := opt.Limit
	switch {
	case limit <= 0:
		limit = 20
	case limit > 100:
		limit = 100
	}
	list = []Notification{}
	q := s.db.NewSelect().Model(&list).Where("user_id = ?", userID).OrderExpr("id DESC").Limit(int64(limit + 1))
	if opt.UnreadOnly {
		q = q.Where("read_at IS NULL")
	}
	if opt.Before != uuid.Nil() {
		q = q.Where("id < ?", opt.Before)
	}
	if err := q.Scan(ctx); err != nil {
		return nil, uuid.Nil(), fmt.Errorf("notification: list: %w", err)
	}
	if len(list) > limit {
		list = list[:limit]
		next = list[limit-1].ID
	}
	return list, next, nil
}

// UnreadCount 返回用户的未读通知数。
func (s *Service) UnreadCount(ctx context.Context, userID uuid.UUID) (int, error) {
	n, err := s.db.NewSelect().Model((*Notification)(nil)).Where("user_id = ? AND read_at IS NULL", userID).Count(ctx)
	if err != nil {
		return 0, fmt.Errorf("notification: unread count: %w", err)
	}
	return int(n), nil
}

// MarkRead 把用户的一条通知标记为已读；不存在时返回 ErrNotFound，已读时返回 nil。
func (s *Service) MarkRead(ctx context.Context, userID, id uuid.UUID) error {
	res, err := s.db.NewUpdate().Model((*Notification)(nil)).
		Set("read_at = COALESCE(read_at, ?)", time.Now()).
		Where("id = ? AND user_id = ?", id, userID).Exec(ctx)
	if err != nil {
		return fmt.Errorf("notification: mark read: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// MarkAllRead 把用户的全部通知标记为已读。
func (s *Service) MarkAllRead(ctx context.Context, userID uuid.UUID) error {
	_, err := s.db.NewUpdate().Model((*Notification)(nil)).Set("read_at = ?", time.Now()).
		Where("user_id = ? AND read_at IS NULL", userID).Exec(ctx)
	if err != nil {
		return fmt.Errorf("notification: mark all read: %w", err)
	}
	return nil
}

// Delete 删除用户的一条通知，不存在时返回 ErrNotFound。
func (s *Service) Delete(ctx context.Context, userID, id uuid.UUID) error {
	res, err := s.db.NewDelete().Model((*Notification)(nil)).Where("id = ? AND user_id = ?", id, userID).Exec(ctx)
	if err != nil {
		return fmt.Errorf("notification: delete: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// Prune 删除早于 olderThan 之前的已读通知，返回删除的条数。由产品的定时任务调用。
func (s *Service) Prune(ctx context.Context, olderThan time.Duration) (int64, error) {
	res, err := s.db.NewDelete().Model((*Notification)(nil)).
		Where("read_at IS NOT NULL AND created_at < ?", time.Now().Add(-olderThan)).Exec(ctx)
	if err != nil {
		return 0, fmt.Errorf("notification: prune: %w", err)
	}
	n, _ := res.RowsAffected()
	return n, nil
}

func mustSub(fsys fs.FS, dir string) fs.FS {
	sub, err := fs.Sub(fsys, dir)
	if err != nil {
		panic(err)
	}
	return sub
}
