package order

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"log/slog"
	"time"
	"unicode/utf8"

	"github.com/cockroachdb/errors"
	"github.com/eko/gocache/lib/v4/cache"
	"github.com/eko/gocache/lib/v4/store"
	"github.com/google/uuid"
	"github.com/uptrace/bun"

	"github.com/ryan053857/mygoravel/database"
	"github.com/ryan053857/mygoravel/fault"
	"github.com/ryan053857/mygoravel/identity"
)

var (
	ErrNotFound        = fault.Define(fault.NotFound, "order.not_found")
	ErrForbidden       = fault.Define(fault.PermissionDenied, "order.forbidden")
	ErrUnauthenticated = fault.Define(fault.Unauthenticated, "auth.unauthenticated")
	ErrInvalidTitle    = fault.Define(fault.InvalidArgument, "order.invalid_title")
	ErrInvalidCursor   = fault.Define(fault.InvalidArgument, "order.invalid_cursor")
	ErrConflict        = fault.Define(fault.Conflict, "order.conflict")
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
	db   *bun.DB
	opts Options
}

// Options supplies optional native query caching.
type Options struct {
	Cache     cache.CacheInterface[string]
	Namespace string
	TTL       time.Duration
	Logger    *slog.Logger
}

// New constructs the order service without opening resources.
func New(db *bun.DB, options ...Options) *Service {
	opts := Options{Namespace: "orders", TTL: time.Minute}
	if len(options) > 0 {
		opts = options[0]
	}
	if opts.Cache != nil && (opts.Namespace == "" || opts.TTL <= 0) {
		panic("order: cache requires namespace and positive TTL")
	}
	return &Service{db: db, opts: opts}
}
func (s *Service) cacheKey(id uuid.UUID) string { return s.opts.Namespace + ":order:v1:" + id.String() }
func (s *Service) cacheFailure(ctx context.Context, operation string) {
	if s.opts.Logger != nil {
		s.opts.Logger.WarnContext(ctx, "order cache degraded", "operation", operation)
	}
}
func validateSubject(sub identity.Subject) error {
	if sub.ID == "" {
		return ErrUnauthenticated.New()
	}
	return nil
}
func validateTitle(title string) error {
	if title == "" || utf8.RuneCountInString(title) > 120 {
		return ErrInvalidTitle.New()
	}
	return nil
}

// CreateParams 是创建参数。
type CreateParams struct {
	Title string
}

// Create 创建属于当前主体的订单。
func (s *Service) Create(ctx context.Context, sub identity.Subject, p CreateParams) (*Order, error) {
	if err := validateSubject(sub); err != nil {
		return nil, err
	}
	if err := validateTitle(p.Title); err != nil {
		return nil, err
	}
	o := &Order{
		ID:        uuid.Must(uuid.NewV7()),
		OwnerID:   sub.ID,
		Title:     p.Title,
		CreatedAt: time.Now().UTC(),
	}
	err := s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		_, err := tx.NewInsert().Model(o).Exec(ctx)
		return err
	})
	if err != nil {
		if database.IsUniqueViolation(err, "orders_owner_title_key") {
			return nil, ErrConflict.Wrap(err)
		}
		return nil, err
	}
	return o, nil
}

// Get 读取订单并校验资源所有权。
func (s *Service) Get(ctx context.Context, sub identity.Subject, id uuid.UUID) (*Order, error) {
	if err := validateSubject(sub); err != nil {
		return nil, err
	}
	var o Order
	if s.opts.Cache != nil {
		raw, err := s.opts.Cache.Get(ctx, s.cacheKey(id))
		if err == nil {
			if json.Unmarshal([]byte(raw), &o) == nil && o.ID == id && o.OwnerID != "" {
				if o.OwnerID != sub.ID {
					return nil, ErrForbidden.New()
				}
				return &o, nil
			}
			s.cacheFailure(ctx, "decode")
		} else {
			var miss store.NotFound
			if !errors.As(err, &miss) {
				s.cacheFailure(ctx, "get")
			}
		}
	}
	err := s.db.NewSelect().Model(&o).Where("id = ?", id).Scan(ctx)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound.New()
		}
		return nil, err
	}
	if o.OwnerID != sub.ID {
		return nil, ErrForbidden.New()
	}
	if s.opts.Cache != nil {
		raw, err := json.Marshal(o)
		if err != nil {
			s.cacheFailure(ctx, "encode")
		} else if err = s.opts.Cache.Set(ctx, s.cacheKey(id), string(raw), store.WithExpiration(s.opts.TTL)); err != nil {
			s.cacheFailure(ctx, "set")
		}
	}
	return &o, nil
}

// ListParams 游标分页。
type ListParams struct {
	Limit  int
	Before string
}

// ListResult 列表结果。
type ListResult struct {
	Items []Order
	Next  string
}

// List 列出当前主体的订单（created_at,id 倒序）。
func (s *Service) List(ctx context.Context, sub identity.Subject, p ListParams) (*ListResult, error) {
	if err := validateSubject(sub); err != nil {
		return nil, err
	}
	if p.Limit <= 0 || p.Limit > 100 {
		p.Limit = 20
	}
	q := s.db.NewSelect().Model((*Order)(nil)).Where("owner_id = ?", sub.ID).OrderExpr("created_at DESC, id DESC").Limit(int64(p.Limit + 1))
	if p.Before != "" {
		raw, err := base64.RawURLEncoding.DecodeString(p.Before)
		var cursor cursor
		if err != nil || json.Unmarshal(raw, &cursor) != nil || cursor.Version != 1 || cursor.Owner != sub.ID || cursor.ID == uuid.Nil || cursor.CreatedAt.IsZero() {
			return nil, ErrInvalidCursor.New()
		}
		q = q.Where("(created_at, id) < (?, ?)", cursor.CreatedAt, cursor.ID)
	}
	var items []Order
	if err := q.Scan(ctx, &items); err != nil {
		return nil, err
	}
	res := &ListResult{Items: items}
	if len(items) > p.Limit {
		last := items[p.Limit-1]
		raw, _ := json.Marshal(cursor{1, sub.ID, last.CreatedAt, last.ID})
		next := base64.RawURLEncoding.EncodeToString(raw)
		res.Items = items[:p.Limit]
		res.Next = next
	}
	return res, nil
}

type cursor struct {
	Version   int
	Owner     string
	CreatedAt time.Time
	ID        uuid.UUID
}

// Update authorizes inside the database transaction and invalidates only after commit.
// Query caching is eventually consistent: concurrent fills may retain old data until TTL.
func (s *Service) Update(ctx context.Context, sub identity.Subject, id uuid.UUID, p CreateParams) (*Order, error) {
	if err := validateSubject(sub); err != nil {
		return nil, err
	}
	if err := validateTitle(p.Title); err != nil {
		return nil, err
	}
	var o Order
	err := s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if err := tx.NewSelect().Model(&o).Where("id = ?", id).For("UPDATE").Scan(ctx); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrNotFound.New()
			}
			return err
		}
		if o.OwnerID != sub.ID {
			return ErrForbidden.New()
		}
		o.Title = p.Title
		_, err := tx.NewUpdate().Model(&o).Column("title").WherePK().Exec(ctx)
		return err
	})
	if err != nil {
		if database.IsUniqueViolation(err, "orders_owner_title_key") {
			return nil, ErrConflict.Wrap(err)
		}
		return nil, err
	}
	if s.opts.Cache != nil {
		if err := s.opts.Cache.Delete(ctx, s.cacheKey(id)); err != nil {
			s.cacheFailure(ctx, "delete")
		}
	}
	return &o, nil
}
