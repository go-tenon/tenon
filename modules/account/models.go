package account

import (
	"time"
	"uuid"

	"github.com/uptrace/bun"

	"github.com/ryan053857/go-kit/database"
)

// User 是一个账号。产品特有的用户字段放在自己的表中（以 user_id 关联），不要修改 users 表。
type User struct {
	bun.BaseModel `bun:"table:users,alias:u"`
	database.Model

	Email        string    `bun:"email,notnull"`
	Name         string    `bun:"name,notnull"`
	PasswordHash string    `bun:"password_hash,notnull"`
	DisabledAt   time.Time `bun:"disabled_at,nullzero"`
	LastLoginAt  time.Time `bun:"last_login_at,nullzero"`
}

// Disabled 报告账号是否已停用。
func (u *User) Disabled() bool { return !u.DisabledAt.IsZero() }

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
