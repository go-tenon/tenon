package account

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
	"uuid"

	"github.com/uptrace/bun"

	"github.com/ryan053857/go-kit/database"
	"github.com/ryan053857/go-kit/support/crypt"
	"github.com/ryan053857/go-kit/support/password"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// Migrations 是本模块的迁移，产品把它加入 app.Options.Publish 后用 migrate publish 复制到迁移目录。
var Migrations = database.MigrationSource{Name: "account", FS: mustSub(migrationsFS, "migrations")}

// 领域错误。Endpoint 已把它们转换为对应的 HTTP 状态码；产品自己的页面按需转换。
var (
	ErrEmailTaken         = errors.New("account: email already registered")
	ErrInvalidCredentials = errors.New("account: invalid email or password")
	ErrUserDisabled       = errors.New("account: user disabled")
	ErrWeakPassword       = errors.New("account: password too short")
	ErrInvalidToken       = errors.New("account: invalid or expired token")
	ErrNotFound           = errors.New("account: not found")
	ErrInvalidMFACode     = errors.New("account: invalid mfa code")
	ErrMFAEnabled         = errors.New("account: mfa already enabled")
	ErrMFANotEnabled      = errors.New("account: mfa not enabled")
	ErrLastLoginMethod    = errors.New("account: cannot remove the last login method")
	ErrForbidden          = errors.New("account: not allowed with this credential")
)

// Service 实现账号相关的全部业务逻辑，页面与 API 共用。
type Service struct {
	db        *bun.DB
	cfg       Config
	cipher    *crypt.Cipher
	now       func() time.Time
	listeners []func(context.Context, Event)
}

// NewService 返回使用 db 的 Service。cfg 通常从 DefaultConfig 开始，由配置文件覆盖。
func NewService(db *bun.DB, cfg Config) (*Service, error) {
	if len(cfg.JWTSecret) < 32 {
		return nil, errors.New("account: jwt_secret must be at least 32 bytes")
	}
	key := deriveKey(cfg.JWTSecret, "mfa")
	if cfg.EncryptionKey != "" {
		var err error
		if key, err = crypt.ParseKey(string(cfg.EncryptionKey)); err != nil {
			return nil, fmt.Errorf("account: encryption_key: %w", err)
		}
	}
	c, err := crypt.New(key)
	if err != nil {
		return nil, fmt.Errorf("account: encryption_key: %w", err)
	}
	if cfg.APIKeyPrefix == "" {
		cfg.APIKeyPrefix = "sk"
	}
	return &Service{db: db, cfg: cfg, cipher: c, now: time.Now}, nil
}

// Register 创建账号。邮箱不区分大小写，已存在时返回 ErrEmailTaken；密码过短时返回 ErrWeakPassword。
// Config.VerifyEmail 为 true 时在同一事务中发布 TopicEmailVerification 消息。
func (s *Service) Register(ctx context.Context, email, plainPassword, name string) (*User, error) {
	if err := s.checkPassword(plainPassword); err != nil {
		return nil, err
	}
	u := &User{Email: normalizeEmail(email), Name: strings.TrimSpace(name), PasswordHash: password.Hash(plainPassword)}
	if err := s.createUser(ctx, u, nil); err != nil {
		return nil, err
	}
	return u, nil
}

// createUser 插入账号，可在同一事务中执行 then（如绑定第三方账号）。
func (s *Service) createUser(ctx context.Context, u *User, then func(context.Context, bun.Tx) error) error {
	err := s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if _, err := tx.NewInsert().Model(u).Exec(ctx); err != nil {
			return err
		}
		if then != nil {
			if err := then(ctx, tx); err != nil {
				return err
			}
		}
		if s.cfg.VerifyEmail && !u.EmailVerified() {
			return s.publishVerification(ctx, tx, u)
		}
		return nil
	})
	if err != nil {
		if database.IsUniqueViolation(err, "users_email_key") {
			return ErrEmailTaken
		}
		return fmt.Errorf("account: register: %w", err)
	}
	s.emit(ctx, Event{Type: EventRegistered, UserID: u.ID})
	return nil
}

// Authenticate 校验邮箱与密码并更新最后登录时间。
// 邮箱不存在或密码错误时返回 ErrInvalidCredentials；账号已停用时返回 ErrUserDisabled（只在密码正确时）。
func (s *Service) Authenticate(ctx context.Context, email, plainPassword string) (*User, error) {
	u := new(User)
	err := s.db.NewSelect().Model(u).Where("email = ?", normalizeEmail(email)).Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		// 用户不存在时也计算一次哈希，避免通过响应时间判断邮箱是否已注册。
		_, _ = password.Verify(plainPassword, dummyHash())
		s.emit(ctx, Event{Type: EventLoginFailed, Email: normalizeEmail(email)})
		return nil, ErrInvalidCredentials
	}
	if err != nil {
		return nil, fmt.Errorf("account: authenticate: %w", err)
	}
	ok, err := password.Verify(plainPassword, u.PasswordHash)
	if err != nil && !errors.Is(err, password.ErrInvalidHash) {
		return nil, fmt.Errorf("account: authenticate: %w", err)
	}
	if !ok {
		s.emit(ctx, Event{Type: EventLoginFailed, UserID: u.ID, Email: u.Email})
		return nil, ErrInvalidCredentials
	}
	if u.Disabled() {
		return nil, ErrUserDisabled
	}

	u.LastLoginAt = s.now()
	cols := []string{"last_login_at"}
	if password.NeedsRehash(u.PasswordHash) {
		u.PasswordHash = password.Hash(plainPassword)
		cols = append(cols, "password_hash", "updated_at")
	}
	if _, err := s.db.NewUpdate().Model(u).Column(cols...).WherePK().Exec(ctx); err != nil {
		return nil, fmt.Errorf("account: authenticate: %w", err)
	}
	return u, nil
}

// User 按 ID 返回账号，不存在时返回 ErrNotFound。
func (s *Service) User(ctx context.Context, id uuid.UUID) (*User, error) {
	u := new(User)
	err := s.db.NewSelect().Model(u).Where("id = ?", id).Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("account: get user: %w", err)
	}
	return u, nil
}

// UpdateName 修改账号名称。
func (s *Service) UpdateName(ctx context.Context, userID uuid.UUID, name string) (*User, error) {
	u, err := s.User(ctx, userID)
	if err != nil {
		return nil, err
	}
	u.Name = strings.TrimSpace(name)
	if _, err := s.db.NewUpdate().Model(u).Column("name", "updated_at").WherePK().Exec(ctx); err != nil {
		return nil, fmt.Errorf("account: update name: %w", err)
	}
	return u, nil
}

// ChangePassword 校验当前密码后设置新密码，并注销除 keepSessionID 以外的全部会话。
// 当前密码错误时返回 ErrInvalidCredentials。账号还没有密码时（只用第三方登录）不校验 current，直接设置。
func (s *Service) ChangePassword(ctx context.Context, userID, keepSessionID uuid.UUID, current, newPassword string) error {
	if err := s.checkPassword(newPassword); err != nil {
		return err
	}
	u, err := s.User(ctx, userID)
	if err != nil {
		return err
	}
	if u.HasPassword() {
		if ok, _ := password.Verify(current, u.PasswordHash); !ok {
			return ErrInvalidCredentials
		}
	}
	u.PasswordHash = password.Hash(newPassword)
	err = s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if _, err := tx.NewUpdate().Model(u).Column("password_hash", "updated_at").WherePK().Exec(ctx); err != nil {
			return fmt.Errorf("account: change password: %w", err)
		}
		_, err := tx.NewDelete().Model((*Session)(nil)).
			Where("user_id = ? AND id <> ?", userID, keepSessionID).Exec(ctx)
		if err != nil {
			return fmt.Errorf("account: change password: %w", err)
		}
		return nil
	})
	if err == nil {
		s.emit(ctx, Event{Type: EventPasswordChanged, UserID: userID})
	}
	return err
}

// SetDisabled 停用或启用账号。停用时同时注销其全部会话。
func (s *Service) SetDisabled(ctx context.Context, userID uuid.UUID, disabled bool) error {
	err := s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		q := tx.NewUpdate().Model((*User)(nil)).Where("id = ?", userID).Set("updated_at = ?", s.now())
		if disabled {
			q = q.Set("disabled_at = ?", s.now())
		} else {
			q = q.Set("disabled_at = NULL")
		}
		res, err := q.Exec(ctx)
		if err != nil {
			return fmt.Errorf("account: set disabled: %w", err)
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return ErrNotFound
		}
		if disabled {
			if _, err := tx.NewDelete().Model((*Session)(nil)).Where("user_id = ?", userID).Exec(ctx); err != nil {
				return fmt.Errorf("account: set disabled: %w", err)
			}
		}
		return nil
	})
	if err == nil {
		e := Event{Type: EventEnabled, UserID: userID}
		if disabled {
			e.Type = EventDisabled
		}
		s.emit(ctx, e)
	}
	return err
}

func (s *Service) checkPassword(p string) error {
	if utf8.RuneCountInString(p) < s.cfg.MinPasswordLength {
		return ErrWeakPassword
	}
	return nil
}

func normalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

var dummyHash = sync.OnceValue(func() string { return password.Hash("dummy password") })

func mustSub(fsys fs.FS, dir string) fs.FS {
	sub, err := fs.Sub(fsys, dir)
	if err != nil {
		panic(err)
	}
	return sub
}
