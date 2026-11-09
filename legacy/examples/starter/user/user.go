// Package user 是产品自己的用户：users 表归产品所有，在 go-kit 要求的列（account.UserFields）之外，
// 可以随意加字段（本示例加了 name）。注册、第三方登录建号、资料修改都在这里，账号模块只负责登录等流程。
package user

import (
	"context"
	"database/sql"
	"strings"
	"uuid"

	"github.com/cockroachdb/errors"
	"github.com/uptrace/bun"

	"github.com/ryan053857/go-kit/modules/account"
	"github.com/ryan053857/go-kit/modules/notification"
)

// User 是产品的用户模型。
type User struct {
	bun.BaseModel `bun:"table:users,alias:u"`
	account.UserFields

	Name string `bun:"name,notnull"`
}

// DisplayName 返回名称，未设置时返回邮箱。
func (u *User) DisplayName() string {
	if u.Name != "" {
		return u.Name
	}
	return u.Email
}

// Recipient 返回通知的收件人。
func (u *User) Recipient() notification.Recipient {
	return notification.Recipient{UserID: u.ID, Email: u.Email, Name: u.DisplayName(), Locale: u.Locale}
}

// Store 读写产品的用户。
type Store struct {
	db       *bun.DB
	accounts *account.Service
}

// NewStore 返回 Store。
func NewStore(db *bun.DB, accounts *account.Service) *Store {
	return &Store{db: db, accounts: accounts}
}

// Get 返回用户，不存在时返回 account.ErrNotFound。
func (s *Store) Get(ctx context.Context, id uuid.UUID) (*User, error) {
	u := new(User)
	err := s.db.NewSelect().Model(u).Where("id = ?", id).Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, account.ErrNotFound
	}
	return u, errors.Wrap(err, "user: get")
}

// Register 用邮箱、密码与名称注册（产品的注册逻辑），返回用户 ID。
func (s *Store) Register(ctx context.Context, email, password, name string) (uuid.UUID, error) {
	return s.accounts.Register(ctx, func(ctx context.Context, tx bun.Tx) (uuid.UUID, error) {
		return s.create(ctx, tx, email, password, name)
	})
}

// create 在注册事务中写 users 表。
func (s *Store) create(ctx context.Context, tx bun.Tx, email, password, name string) (uuid.UUID, error) {
	if err := s.accounts.CheckPassword(password); err != nil {
		return uuid.Nil(), err
	}
	u := &User{Name: strings.TrimSpace(name)}
	u.Email, u.PasswordHash = account.NormalizeEmail(email), account.HashPassword(password)
	_, err := tx.NewInsert().Model(u).Exec(ctx)
	return u.ID, err
}

// CreateOAuthUser 是 account.Actions.CreateOAuthUser：第三方登录首次登录时建号，名称取第三方账号的名称。
func (s *Store) CreateOAuthUser(ctx context.Context, tx bun.Tx, fields *account.UserFields, ou *account.OAuthUser) (uuid.UUID, error) {
	u := &User{UserFields: *fields, Name: strings.TrimSpace(ou.Name)}
	_, err := tx.NewInsert().Model(u).Exec(ctx)
	return u.ID, err
}

// UpdateName 修改名称。
func (s *Store) UpdateName(ctx context.Context, id uuid.UUID, name string) (*User, error) {
	_, err := s.db.NewUpdate().Model((*User)(nil)).Set("name = ?", strings.TrimSpace(name)).
		Set("updated_at = now()").Where("id = ?", id).Exec(ctx)
	if err != nil {
		return nil, errors.Wrap(err, "user: update name")
	}
	return s.Get(ctx, id)
}
