package editor

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/database"
	"github.com/go-tenon/tenon/errorx"
	"github.com/go-tenon/tenon/identity"
	"github.com/go-tenon/tenon/login"
)

// Type 是编辑这套账号体系。
const Type identity.SubjectType = "editor"

var ErrDisabled = errorx.Define(errorx.Unauthenticated, "editor.disabled")

// Editor 是编辑账号。邮箱、密码和二次验证都在 login 的表中，这里只有业务字段。
type Editor struct {
	bun.BaseModel `bun:"table:editors"`

	ID        string    `bun:"id,pk"`
	Name      string    `bun:"name,notnull"`
	Admin     bool      `bun:"admin,notnull"` // 可以邀请其他编辑
	Disabled  bool      `bun:"disabled,notnull"`
	CreatedAt time.Time `bun:"created_at,notnull"`
}

func (e *Editor) Ref() identity.Ref { return identity.Ref{Type: Type, ID: e.ID} }

// Resolve 每个请求重新读取编辑：停用后立即失效，权限随 Admin 变化。
func Resolve(db bun.IDB) identity.Resolver {
	return func(ctx context.Context, sub identity.Subject) (identity.Subject, error) {
		var e Editor
		err := db.NewSelect().Model(&e).Where("id = ?", sub.ID).Scan(ctx)
		if errors.Is(err, sql.ErrNoRows) || err == nil && e.Disabled {
			return identity.Subject{}, ErrDisabled.New()
		}
		if err != nil {
			return identity.Subject{}, err
		}
		sub.Permissions = []string{"articles:*"}
		if e.Admin {
			sub.Permissions = append(sub.Permissions, "editors:invite")
		}
		return sub, nil
	}
}

// Invite 由管理员创建编辑账号，并向邮箱发送设置密码的邀请。
type Invite struct {
	db     *bun.DB
	logins *login.Realm
}

func NewInvite(db *bun.DB, logins *login.Realm) *Invite { return &Invite{db: db, logins: logins} }

func (a *Invite) Handle(ctx context.Context, name, email string) (*Editor, error) {
	e := &Editor{ID: uuid.Must(uuid.NewV7()).String(), Name: name, CreatedAt: time.Now().UTC()}
	err := database.RunInTx(ctx, a.db, nil, func(ctx context.Context, tx database.Tx) error {
		if _, err := tx.NewInsert().Model(e).Exec(ctx); err != nil {
			return err
		}
		if err := a.logins.Attach(ctx, tx, e.Ref(), login.Email(email), false); err != nil {
			return err // 邮箱已被其他编辑使用：login.ErrTaken
		}
		// 提交之后才发送邀请，事务回滚时不会发出邮件。
		tx.AfterCommit(func(ctx context.Context) error {
			_, err := a.logins.SendCode(ctx, login.Reset, login.Email(email))
			return err
		})
		return nil
	})
	return e, err
}
