package account

import (
	"context"
	"database/sql"
	"embed"
	"io/fs"
	"net/http"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
	"uuid"

	"github.com/cockroachdb/errors"
	"github.com/uptrace/bun"

	"github.com/ryan053857/go-kit/database"
	"github.com/ryan053857/go-kit/httpx"
	"github.com/ryan053857/go-kit/i18n"
	"github.com/ryan053857/go-kit/support/crypt"
	"github.com/ryan053857/go-kit/support/password"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// Migrations 是本模块的迁移，产品把它加入 app.Options.Publish 后用 migrate publish 复制到迁移目录。
// 其中 create_users 是 users 表的模板：发布后归产品所有，可以加入产品自己的列。
var Migrations = database.MigrationSource{Name: "account", FS: mustSub(migrationsFS, "migrations")}

//go:embed locales/*.yaml
var locales embed.FS

func init() { i18n.MustLoad(locales) }

// 领域错误，已登记 HTTP 状态码与多语言提示（见 httpx.DefineError）。
var (
	ErrEmailTaken         = httpx.DefineError(http.StatusConflict, "account.email_taken")
	ErrInvalidCredentials = httpx.DefineError(http.StatusUnauthorized, "account.invalid_credentials")
	ErrWrongPassword      = httpx.DefineError(http.StatusUnprocessableEntity, "account.wrong_password") // 修改密码时当前密码错误
	ErrUserDisabled       = httpx.DefineError(http.StatusForbidden, "account.user_disabled")
	ErrWeakPassword       = httpx.DefineError(http.StatusUnprocessableEntity, "account.weak_password")
	ErrInvalidToken       = httpx.DefineError(http.StatusUnauthorized, "account.invalid_token")
	ErrInvalidLink        = httpx.DefineError(http.StatusBadRequest, "account.invalid_link") // 重置密码、验证邮箱的链接无效或已过期
	ErrNotFound           = httpx.DefineError(http.StatusNotFound, "account.not_found")
	ErrInvalidMFACode     = httpx.DefineError(http.StatusUnprocessableEntity, "account.invalid_mfa_code")
	ErrMFAEnabled         = httpx.DefineError(http.StatusConflict, "account.mfa_enabled")
	ErrMFANotEnabled      = httpx.DefineError(http.StatusConflict, "account.mfa_not_enabled")
	ErrLastLoginMethod    = httpx.DefineError(http.StatusConflict, "account.last_login_method")
	ErrAPIKeyNotAllowed   = httpx.DefineError(http.StatusForbidden, "account.api_key_not_allowed")
	ErrInvalidImage       = httpx.DefineError(http.StatusUnprocessableEntity, "account.invalid_image")
	ErrIdentityTaken      = httpx.DefineError(http.StatusConflict, "account.identity_taken")
	ErrEmailRequired      = httpx.DefineError(http.StatusUnprocessableEntity, "account.email_required")
	ErrRegistrationClosed = httpx.DefineError(http.StatusForbidden, "account.registration_closed")
	ErrInvalidExpiry      = httpx.DefineError(http.StatusUnprocessableEntity, "account.invalid_expiry") // API Key 的过期时间早于现在
)

// Actions 是由产品实现的账号操作（类似 Laravel Fortify 发布到应用中的 Action），示例中有默认实现。
type Actions struct {
	// CreateOAuthUser 在第三方登录且没有对应账号时创建用户：在 tx 中写 users 表（Email 已规范化、
	// 邮箱已验证时 EmailVerifiedAt 有值），返回用户 ID。为 nil 时第三方登录不能自动注册，返回 ErrRegistrationClosed。
	CreateOAuthUser func(ctx context.Context, tx bun.Tx, u *UserFields, ou *OAuthUser) (uuid.UUID, error)
}

// Service 实现账号相关的业务流程，页面与 API 共用。
type Service struct {
	db        *bun.DB
	cfg       Config
	actions   Actions
	cipher    *crypt.Cipher
	now       func() time.Time
	listeners []func(context.Context, Event)
}

// New 返回 Service。cfg 通常从 DefaultConfig 开始，由配置文件覆盖；JWTSecret 必须配置。
func New(db *bun.DB, cfg Config, actions Actions) (*Service, error) {
	if len(cfg.JWTSecret) < 32 {
		return nil, errors.New("account: jwt_secret must be at least 32 bytes")
	}
	cfg = cfg.withDefaults()
	key := deriveKey(cfg.JWTSecret, "mfa")
	if cfg.EncryptionKey != "" {
		var err error
		if key, err = crypt.ParseKey(string(cfg.EncryptionKey)); err != nil {
			return nil, errors.Wrap(err, "account: encryption_key")
		}
	}
	c, err := crypt.New(key)
	if err != nil {
		return nil, errors.Wrap(err, "account: encryption_key")
	}
	return &Service{db: db, cfg: cfg, actions: actions, cipher: c, now: time.Now}, nil
}

// HashPassword 返回密码的哈希（argon2id），产品创建用户时写入 UserFields.PasswordHash。
func HashPassword(plain string) string { return password.Hash(plain) }

// NormalizeEmail 返回规范化（去空白、小写）的邮箱，写入 users 表前调用。
func NormalizeEmail(email string) string { return strings.ToLower(strings.TrimSpace(email)) }

// CheckPassword 检查密码是否符合要求（长度），不符合时返回 ErrWeakPassword。
func (s *Service) CheckPassword(p string) error {
	if utf8.RuneCountInString(p) < s.cfg.MinPasswordLength {
		return httpx.WithData(ErrWeakPassword, map[string]any{"Min": s.cfg.MinPasswordLength})
	}
	return nil
}

// Register 在一个事务中调用 create 创建用户：create 由产品实现，校验输入并写 users 表（可以同时写其他表），
// 返回用户 ID。之后 go-kit 在同一事务中发送邮箱验证（Config.VerifyEmail 时），提交后发出 EventRegistered。
// 邮箱已存在（违反 users_email_key）时返回 ErrEmailTaken。
//
//	id, err := users.Register(ctx, func(ctx context.Context, tx bun.Tx) (uuid.UUID, error) {
//		if err := users.CheckPassword(in.Password); err != nil {
//			return uuid.Nil(), err
//		}
//		u := &User{Name: in.Name}
//		u.Email, u.PasswordHash = account.NormalizeEmail(in.Email), account.HashPassword(in.Password)
//		_, err := tx.NewInsert().Model(u).Exec(ctx)
//		return u.ID, err
//	})
func (s *Service) Register(ctx context.Context, create func(ctx context.Context, tx bun.Tx) (uuid.UUID, error)) (uuid.UUID, error) {
	var id uuid.UUID
	err := s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		var err error
		if id, err = create(ctx, tx); err != nil {
			return err
		}
		return s.afterCreate(ctx, tx, id)
	})
	if err != nil {
		if database.IsUniqueViolation(err, "users_email_key") {
			return uuid.Nil(), ErrEmailTaken
		}
		return uuid.Nil(), errors.Wrap(err, "account: register")
	}
	s.emit(ctx, Event{Type: EventRegistered, UserID: id})
	return id, nil
}

// afterCreate 在创建用户的事务中发送邮箱验证。
func (s *Service) afterCreate(ctx context.Context, tx bun.Tx, id uuid.UUID) error {
	if !s.cfg.VerifyEmail {
		return nil
	}
	u := new(User)
	if err := tx.NewSelect().Model(u).Where("id = ?", id).Scan(ctx); err != nil {
		return errors.Wrap(err, "account: load created user")
	}
	if u.EmailVerified() {
		return nil
	}
	if u.Locale == "" {
		u.Locale = i18n.Locale(ctx) // 注册时请求的语言
	}
	return s.publishVerification(ctx, tx, u)
}

// Authenticate 校验邮箱与密码并更新最后登录时间。
// 邮箱不存在或密码错误时返回 ErrInvalidCredentials；账号已停用时返回 ErrUserDisabled（只在密码正确时）。
func (s *Service) Authenticate(ctx context.Context, email, plainPassword string) (*User, error) {
	u := new(User)
	err := s.db.NewSelect().Model(u).Where("email = ?", NormalizeEmail(email)).Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		// 用户不存在时也计算一次哈希，避免通过响应时间判断邮箱是否已注册。
		_, _ = password.Verify(plainPassword, dummyHash())
		s.emit(ctx, Event{Type: EventLoginFailed, Email: NormalizeEmail(email)})
		return nil, ErrInvalidCredentials
	}
	if err != nil {
		return nil, errors.Wrap(err, "account: authenticate")
	}
	ok, err := password.Verify(plainPassword, u.PasswordHash)
	if err != nil && !errors.Is(err, password.ErrInvalidHash) {
		return nil, errors.Wrap(err, "account: authenticate")
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
		return nil, errors.Wrap(err, "account: authenticate")
	}
	return u, nil
}

// User 按 ID 返回账号模块看到的用户（只有 UserFields 中的列），不存在时返回 ErrNotFound。
// 产品需要自己的字段时查询自己的 User 模型。
func (s *Service) User(ctx context.Context, id uuid.UUID) (*User, error) {
	u := new(User)
	err := s.db.NewSelect().Model(u).Where("id = ?", id).Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, errors.Wrap(err, "account: get user")
	}
	return u, nil
}

// SetLocale 保存用户选择的语言（异步发送的邮件、通知使用它）；locale 为空表示跟随请求。
func (s *Service) SetLocale(ctx context.Context, userID uuid.UUID, locale string) error {
	if locale != "" {
		locale = i18n.Match(locale)
	}
	_, err := s.db.NewUpdate().Model((*User)(nil)).Set("locale = ?", locale).Set("updated_at = ?", s.now()).
		Where("id = ?", userID).Exec(ctx)
	return errors.Wrap(err, "account: set locale")
}

// ChangePassword 校验当前密码后设置新密码，并注销除 keepSessionID 以外的全部会话。
// 当前密码错误时返回 ErrWrongPassword。账号还没有密码时（只用第三方登录）不校验 current，直接设置。
func (s *Service) ChangePassword(ctx context.Context, userID, keepSessionID uuid.UUID, current, newPassword string) error {
	if err := s.CheckPassword(newPassword); err != nil {
		return err
	}
	u, err := s.User(ctx, userID)
	if err != nil {
		return err
	}
	if u.HasPassword() {
		if ok, _ := password.Verify(current, u.PasswordHash); !ok {
			return ErrWrongPassword
		}
	}
	u.PasswordHash = password.Hash(newPassword)
	err = s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if _, err := tx.NewUpdate().Model(u).Column("password_hash", "updated_at").WherePK().Exec(ctx); err != nil {
			return err
		}
		_, err := tx.NewDelete().Model((*Session)(nil)).
			Where("user_id = ? AND id <> ?", userID, keepSessionID).Exec(ctx)
		return err
	})
	if err != nil {
		return errors.Wrap(err, "account: change password")
	}
	s.emit(ctx, Event{Type: EventPasswordChanged, UserID: userID})
	return nil
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
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return ErrNotFound
		}
		if disabled {
			if _, err := tx.NewDelete().Model((*Session)(nil)).Where("user_id = ?", userID).Exec(ctx); err != nil {
				return err
			}
		}
		return nil
	})
	if errors.Is(err, ErrNotFound) {
		return err
	}
	if err != nil {
		return errors.Wrap(err, "account: set disabled")
	}
	e := Event{Type: EventEnabled, UserID: userID}
	if disabled {
		e.Type = EventDisabled
	}
	s.emit(ctx, e)
	return nil
}

var dummyHash = sync.OnceValue(func() string { return password.Hash("dummy password") })

func mustSub(fsys fs.FS, dir string) fs.FS {
	sub, err := fs.Sub(fsys, dir)
	if err != nil {
		panic(err)
	}
	return sub
}
