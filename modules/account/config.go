package account

import (
	"time"

	"github.com/ryan053857/go-kit/config"
)

// Config 是账号模块的配置，产品放在自己的配置中：
//
//	account:
//	  jwt_secret: ${JWT_SECRET}   # 至少 32 字节的随机串，例如 openssl rand -base64 48
type Config struct {
	JWTSecret config.Secret `yaml:"jwt_secret"` // access token 的 HS256 签名密钥，至少 32 字节
	Issuer    string        `yaml:"issuer"`     // access token 的 iss，默认 "go-kit"

	AccessTTL    time.Duration `yaml:"access_ttl"`    // access token 有效期，默认 15m
	RefreshTTL   time.Duration `yaml:"refresh_ttl"`   // refresh token 有效期（每次刷新后顺延），默认 7 天
	RememberTTL  time.Duration `yaml:"remember_ttl"`  // 登录时勾选“记住我”的 refresh token 有效期，默认 30 天
	RefreshGrace time.Duration `yaml:"refresh_grace"` // 刷新后旧 refresh token 仍可使用的时间（应对网络重试），默认 30s
	ResetTTL     time.Duration `yaml:"reset_ttl"`     // 重置密码链接有效期，默认 1h
	VerifyTTL    time.Duration `yaml:"verify_ttl"`    // 邮箱验证链接有效期，默认 24h

	// VerifyEmail 为 true 时，注册后自动发送邮箱验证邮件（需注册 TopicEmailVerification 的处理函数）。
	// 是否要求验证后才能使用由产品决定，见 User.EmailVerified。
	VerifyEmail bool `yaml:"verify_email"`

	// EncryptionKey 用于加密两步验证的密钥，base64 编码的 32 字节（openssl rand -base64 32）。
	// 为空时由 JWTSecret 派生，此时更换 JWTSecret 会使已启用的两步验证全部失效。
	EncryptionKey config.Secret `yaml:"encryption_key"`

	// APIKeyPrefix 是 API Key 的前缀，默认 "sk"，生成的 Key 形如 sk_xxxx。
	// Bearer token 以该前缀加下划线开头时按 API Key 校验，否则按 access token 校验。
	APIKeyPrefix string `yaml:"api_key_prefix"`

	// CheckSession 为 true 时，每次校验 access token 都查询会话是否仍存在，踢下线立即生效；
	// 为 false（默认）时不查库，踢下线最长在 AccessTTL 后生效。
	CheckSession bool `yaml:"check_session"`

	MinPasswordLength int `yaml:"min_password_length"` // 默认 8

	// AvatarBaseURL 是头像地址的前缀，头像地址为 AvatarBaseURL + 对象键（如 avatars/<用户>/<id>.jpg）。
	// 默认 "/"，即由应用通过 Avatars.Handler 提供（挂在 /avatars/*）；使用 CDN 时填 CDN 地址。
	AvatarBaseURL string `yaml:"avatar_base_url"`
}

// DefaultConfig 返回带默认值的 Config（JWTSecret 为空，必须由产品配置）。
func DefaultConfig() Config {
	return Config{
		Issuer:            "go-kit",
		AccessTTL:         15 * time.Minute,
		RefreshTTL:        7 * 24 * time.Hour,
		RememberTTL:       30 * 24 * time.Hour,
		RefreshGrace:      30 * time.Second,
		ResetTTL:          time.Hour,
		VerifyTTL:         24 * time.Hour,
		APIKeyPrefix:      "sk",
		MinPasswordLength: 8,
		AvatarBaseURL:     "/",
	}
}
