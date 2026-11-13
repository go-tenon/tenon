package reader

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/apitoken"
	"github.com/go-tenon/tenon/database"
	"github.com/go-tenon/tenon/errorx"
	"github.com/go-tenon/tenon/identity"
	"github.com/go-tenon/tenon/login"
)

// Type 是读者这套账号体系。
const Type identity.SubjectType = "reader"

var ErrDisabled = errorx.Define(errorx.Unauthenticated, "reader.disabled")

// Reader 是读者账号。手机号和 Apple 账号在 login 的表中。
type Reader struct {
	bun.BaseModel `bun:"table:readers"`

	ID        string    `bun:"id,pk"`
	Name      string    `bun:"name,notnull"`
	Disabled  bool      `bun:"disabled,notnull"`
	CreatedAt time.Time `bun:"created_at,notnull"`
}

func (r *Reader) Ref() identity.Ref { return identity.Ref{Type: Type, ID: r.ID} }

// Resolve 拒绝已停用或已注销的读者。读者没有角色，数据按“归属本人”授权。
func Resolve(db bun.IDB) identity.Resolver {
	return func(ctx context.Context, sub identity.Subject) (identity.Subject, error) {
		var disabled bool
		err := db.NewSelect().Model((*Reader)(nil)).Column("disabled").Where("id = ?", sub.ID).Scan(ctx, &disabled)
		if errors.Is(err, sql.ErrNoRows) || err == nil && disabled {
			return identity.Subject{}, ErrDisabled.New()
		}
		return sub, err
	}
}

// SignUp 用完成凭证注册读者。
type SignUp struct {
	db     *bun.DB
	logins *login.Realm
}

func NewSignUp(db *bun.DB, logins *login.Realm) *SignUp { return &SignUp{db: db, logins: logins} }

func (a *SignUp) Handle(ctx context.Context, proof, name string) (identity.Subject, error) {
	r := &Reader{ID: uuid.Must(uuid.NewV7()).String(), Name: name, CreatedAt: time.Now().UTC()}
	var out login.Outcome
	err := database.RunInTx(ctx, a.db, nil, func(ctx context.Context, tx database.Tx) error {
		if _, err := tx.NewInsert().Model(r).Exec(ctx); err != nil {
			return err
		}
		var err error
		out, err = a.logins.Register(ctx, tx, proof, r.Ref())
		return err
	})
	return out.Subject, err // 读者不要求二次验证，Register 总是返回 Subject
}

// Delete 注销读者：删除账号行和全部登录凭据，再撤销全部 token。
type Delete struct {
	db     *bun.DB
	logins *login.Realm
	tokens *apitoken.Store
}

func NewDelete(db *bun.DB, logins *login.Realm, tokens *apitoken.Store) *Delete {
	return &Delete{db: db, logins: logins, tokens: tokens}
}

func (a *Delete) Handle(ctx context.Context, ref identity.Ref) error {
	err := database.RunInTx(ctx, a.db, nil, func(ctx context.Context, tx database.Tx) error {
		if _, err := tx.NewDelete().Model((*Reader)(nil)).Where("id = ?", ref.ID).Exec(ctx); err != nil {
			return err
		}
		return a.logins.Forget(ctx, tx, ref)
	})
	if err != nil {
		return err
	}
	_, err = a.tokens.RevokeSubject(ctx, ref)
	return err
}
