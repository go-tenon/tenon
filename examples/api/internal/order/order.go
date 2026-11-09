package order

import (
	"context"
	"database/sql"
	"time"

	"github.com/google/uuid"
	"github.com/uptrace/bun"

	"github.com/ryan053857/mygoravel/database"
	"github.com/ryan053857/mygoravel/fault"
	"github.com/ryan053857/mygoravel/identity"
)

var (
	ErrNotFound  = fault.Define(fault.NotFound, "order.not_found")
	ErrForbidden = fault.Define(fault.PermissionDenied, "order.forbidden")
	ErrConflict  = fault.Define(fault.Conflict, "order.conflict")
)

// Order 是领域模型。
type Order struct {
	ID        uuid.UUID `bun:"id,pk"`
	OwnerID   string    `bun:"owner_id,notnull"`
	Title     string    `bun:"title,notnull"`
	CreatedAt time.Time `bun:"created_at,notnull"`
}

// Service 提供订单用例。
type Service struct {
	db *bun.DB
}

// New 构造服务。
func New(db *bun.DB) *Service { return &Service{db: db} }

// CreateParams 是创建参数。
type CreateParams struct {
	Title string
}

// Create 创建属于当前主体的订单。
func (s *Service) Create(ctx context.Context, sub identity.Subject, p CreateParams) (*Order, error) {
	if p.Title == "" {
		return nil, fault.Define(fault.InvalidArgument, "order.invalid_title").New()
	}
	o := &Order{
		ID:        uuid.Must(uuid.NewV7()),
		OwnerID:   sub.ID,
		Title:     p.Title,
		CreatedAt: time.Now().UTC(),
	}
	err := database.RunInTx(ctx, s.db, nil, func(ctx context.Context, tx bun.Tx) error {
		_, err := tx.NewInsert().Model(o).Exec(ctx)
		return err
	})
	if err != nil {
		return nil, err
	}
	return o, nil
}

// Get 读取订单并校验资源所有权。
func (s *Service) Get(ctx context.Context, sub identity.Subject, id uuid.UUID) (*Order, error) {
	var o Order
	err := s.db.NewSelect().Model(&o).Where("id = ?", id).Scan(ctx)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrNotFound.New()
		}
		return nil, err
	}
	if o.OwnerID != sub.ID {
		return nil, ErrForbidden.New()
	}
	return &o, nil
}

// ListParams 游标分页。
type ListParams struct {
	Limit  int
	Before *uuid.UUID
}

// ListResult 列表结果。
type ListResult struct {
	Items []Order
	Next  *uuid.UUID
}

// List 列出当前主体的订单（created_at,id 倒序）。
func (s *Service) List(ctx context.Context, sub identity.Subject, p ListParams) (*ListResult, error) {
	if p.Limit <= 0 || p.Limit > 100 {
		p.Limit = 20
	}
	q := s.db.NewSelect().Model((*Order)(nil)).Where("owner_id = ?", sub.ID).OrderExpr("id DESC").Limit(int64(p.Limit + 1))
	if p.Before != nil {
		q = q.Where("id < ?", *p.Before)
	}
	var items []Order
	if err := q.Scan(ctx, &items); err != nil {
		return nil, err
	}
	res := &ListResult{Items: items}
	if len(items) > p.Limit {
		next := items[p.Limit-1].ID
		res.Items = items[:p.Limit]
		res.Next = &next
	}
	return res, nil
}
