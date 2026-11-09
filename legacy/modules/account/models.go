package account

import (
	"time"
	"uuid"

	"github.com/uptrace/bun"

	"github.com/ryan053857/go-kit/database"
)

// UserFields 是账号模块要求 users 表具有的列。users 表归产品所有（迁移由 account.Migrations 发布后归产品），
// 产品的 User 模型嵌入它，再加上自己的字段：
//
//	type User struct {
//		bun.BaseModel `bun:"table:users,alias:u"`
//		account.UserFields
//
//		Name string `bun:"name,notnull"`
//	}
//
// 账号模块只读写这些列，不会碰产品加的列。
type UserFields struct {
	database.Model

	Email           string    `bun:"email,notnull"`         // 小写，唯一约束 users_email_key
	PasswordHash    string    `bun:"password_hash,notnull"` // argon2id；为空表示不能用密码登录
	EmailVerifiedAt time.Time `bun:"email_verified_at,nullzero"`
	DisabledAt      time.Time `bun:"disabled_at,nullzero"`
	LastLoginAt     time.Time `bun:"last_login_at,nullzero"`
	Locale          string    `bun:"locale,notnull"`     // 用户选择的语言，空表示跟随请求
	AvatarKey       string    `bun:"avatar_key,notnull"` // 头像在 storage 中的对象键，空表示没有头像
}

// Disabled 报告账号是否已停用。
func (u *UserFields) Disabled() bool { return !u.DisabledAt.IsZero() }

// EmailVerified 报告邮箱是否已验证。
func (u *UserFields) EmailVerified() bool { return !u.EmailVerifiedAt.IsZero() }

// HasPassword 报告账号是否设置了密码（只用第三方登录注册的账号没有密码）。
func (u *UserFields) HasPassword() bool { return u.PasswordHash != "" }

// User 是账号模块看到的用户：只有 UserFields 中的列。
type User struct {
	bun.BaseModel `bun:"table:users,alias:u"`
	UserFields
}

// 会话类型。
const (
	KindWeb = "web" // 网页登录，会话 ID 保存在 Cookie 会话中
	KindAPI = "api" // App / 第三方登录，持有 refresh token
)

// Session 是一次登录。
type Session struct {
	bun.BaseModel `bun:"table:user_sessions,alias:s"`
	database.Model

	UserID        uuid.UUID `bun:"user_id,notnull,type:uuid"`
	Kind          string    `bun:"kind,notnull"`
	TokenHash     string    `bun:"token_hash,nullzero"`
	PrevTokenHash string    `bun:"prev_token_hash,nullzero"`
	RotatedAt     time.Time `bun:"rotated_at,nullzero"`
	Remember      bool      `bun:"remember,notnull"`
	IP            string    `bun:"ip,notnull"`
	UserAgent     string    `bun:"user_agent,notnull"`
	LastSeenAt    time.Time `bun:"last_seen_at,notnull"`
	ExpiresAt     time.Time `bun:"expires_at,notnull"`
}

type resetToken struct {
	bun.BaseModel `bun:"table:password_reset_tokens"`
	database.CreatedModel

	UserID    uuid.UUID `bun:"user_id,notnull,type:uuid"`
	TokenHash string    `bun:"token_hash,notnull"`
	ExpiresAt time.Time `bun:"expires_at,notnull"`
	UsedAt    time.Time `bun:"used_at,nullzero"`
}

type mfa struct {
	bun.BaseModel `bun:"table:user_mfa,alias:m"`

	UserID        uuid.UUID `bun:"user_id,pk,type:uuid"`
	CreatedAt     time.Time `bun:"created_at,notnull"`
	UpdatedAt     time.Time `bun:"updated_at,notnull"`
	Secret        string    `bun:"secret,notnull"`
	RecoveryCodes []string  `bun:"recovery_codes,array,notnull"`
	LastStep      int64     `bun:"last_step,notnull"`
	EnabledAt     time.Time `bun:"enabled_at,nullzero"`
}

// APIKey 是用户创建的 API Key。token 原文只在创建时返回一次。
type APIKey struct {
	bun.BaseModel `bun:"table:user_api_keys,alias:k"`
	database.CreatedModel

	UserID     uuid.UUID `bun:"user_id,notnull,type:uuid"`
	Name       string    `bun:"name,notnull"`
	TokenHash  string    `bun:"token_hash,notnull"`
	Hint       string    `bun:"hint,notnull"`
	Scopes     []string  `bun:"scopes,array,notnull"`
	LastUsedAt time.Time `bun:"last_used_at,nullzero"`
	ExpiresAt  time.Time `bun:"expires_at,nullzero"`
}

// Identity 是账号绑定的第三方登录账号。
type Identity struct {
	bun.BaseModel `bun:"table:user_identities,alias:i"`
	database.CreatedModel

	UserID   uuid.UUID `bun:"user_id,notnull,type:uuid"`
	Provider string    `bun:"provider,notnull"`
	Subject  string    `bun:"subject,notnull"`
	Email    string    `bun:"email,notnull"`
	Name     string    `bun:"name,notnull"`
}
