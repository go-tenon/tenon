package notification

import (
	"context"
	"encoding/json"
	"time"
	"uuid"

	"github.com/cockroachdb/errors"
	"github.com/uptrace/bun"

	"github.com/ryan053857/go-kit/database"
)

// Message 是一条站内信。
type Message struct {
	bun.BaseModel `bun:"table:notifications,alias:n"`
	database.CreatedModel

	UserID uuid.UUID      `bun:"user_id,notnull,type:uuid"`
	Type   string         `bun:"type,notnull"`
	Title  string         `bun:"title,notnull"`
	Body   string         `bun:"body,notnull"`
	URL    string         `bun:"url,notnull"`
	Data   map[string]any `bun:"data,type:jsonb,notnull"` // 通知的原始字段
	ReadAt time.Time      `bun:"read_at,nullzero"`
}

// Read 报告是否已读。
func (m *Message) Read() bool { return !m.ReadAt.IsZero() }

// ListOptions 是站内信列表的条件。
type ListOptions struct {
	UnreadOnly bool
	database.Cursor
}

// List 按时间倒序返回用户的站内信。返回的 next 非零时，作为下一页的 Cursor.Before。
func (s *Service) List(ctx context.Context, userID uuid.UUID, opt ListOptions) ([]Message, uuid.UUID, error) {
	list := []Message{}
	q := s.db.NewSelect().Model(&list).Where("user_id = ?", userID)
	if opt.UnreadOnly {
		q = q.Where("read_at IS NULL")
	}
	next, err := database.Paginate(ctx, q, &list, opt.Cursor)
	return list, next, errors.Wrap(err, "notification: list")
}

// Get 返回用户的一条站内信，不存在时返回 ErrNotFound。
func (s *Service) Get(ctx context.Context, userID, id uuid.UUID) (*Message, error) {
	m := new(Message)
	err := s.db.NewSelect().Model(m).Where("id = ? AND user_id = ?", id, userID).Scan(ctx)
	if isNoRows(err) {
		return nil, ErrNotFound
	}
	return m, errors.Wrap(err, "notification: get")
}

// UnreadCount 返回用户的未读站内信数。
func (s *Service) UnreadCount(ctx context.Context, userID uuid.UUID) (int, error) {
	n, err := s.db.NewSelect().Model((*Message)(nil)).Where("user_id = ? AND read_at IS NULL", userID).Count(ctx)
	return int(n), errors.Wrap(err, "notification: unread count")
}

// MarkRead 把用户的一条站内信标记为已读；不存在时返回 ErrNotFound，已读时返回 nil。
func (s *Service) MarkRead(ctx context.Context, userID, id uuid.UUID) error {
	res, err := s.db.NewUpdate().Model((*Message)(nil)).
		Set("read_at = COALESCE(read_at, ?)", s.now()).
		Where("id = ? AND user_id = ?", id, userID).Exec(ctx)
	if err != nil {
		return errors.Wrap(err, "notification: mark read")
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// MarkAllRead 把用户的全部站内信标记为已读。
func (s *Service) MarkAllRead(ctx context.Context, userID uuid.UUID) error {
	_, err := s.db.NewUpdate().Model((*Message)(nil)).Set("read_at = ?", s.now()).
		Where("user_id = ? AND read_at IS NULL", userID).Exec(ctx)
	return errors.Wrap(err, "notification: mark all read")
}

// Delete 删除用户的一条站内信，不存在时返回 ErrNotFound。
func (s *Service) Delete(ctx context.Context, userID, id uuid.UUID) error {
	res, err := s.db.NewDelete().Model((*Message)(nil)).Where("id = ? AND user_id = ?", id, userID).Exec(ctx)
	if err != nil {
		return errors.Wrap(err, "notification: delete")
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// Prune 删除早于 olderThan 之前的已读站内信，返回删除的条数。由产品的定时任务调用。
func (s *Service) Prune(ctx context.Context, olderThan time.Duration) (int64, error) {
	res, err := s.db.NewDelete().Model((*Message)(nil)).
		Where("read_at IS NOT NULL AND created_at < ?", s.now().Add(-olderThan)).Exec(ctx)
	if err != nil {
		return 0, errors.Wrap(err, "notification: prune")
	}
	n, _ := res.RowsAffected()
	return n, nil
}

// jsonMap 把通知的字段转为 map，保存到站内信的 data 列。
func jsonMap(n Notification) (map[string]any, error) {
	raw, err := json.Marshal(n)
	if err != nil {
		return nil, err
	}
	m := map[string]any{}
	return m, json.Unmarshal(raw, &m)
}
