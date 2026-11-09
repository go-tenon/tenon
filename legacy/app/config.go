package app

import (
	"log/slog"
	"time"

	"github.com/ryan053857/go-kit/database"
	"github.com/ryan053857/go-kit/httpx"
	"github.com/ryan053857/go-kit/logger"
	"github.com/ryan053857/go-kit/mail"
	"github.com/ryan053857/go-kit/storage"
)

// Config 是 app 需要的配置。产品定义自己的配置结构体并内嵌它：
//
//	type Config struct {
//		app.Config `yaml:",inline"`
//		Billing    BillingConfig `yaml:"billing"`
//	}
//
//	cfg := Config{Config: app.DefaultConfig()}
//	app.Main(&cfg, opts)
//
// 对应的 YAML：
//
//	log:
//	  level: info
//	  format: json
//	http:
//	  addr: ":8080"
//	database:
//	  dsn: ${DATABASE_URL}
//	session:
//	  lifetime: 24h
//	mail:
//	  driver: log
//	storage:
//	  driver: local
//	billing: ...
type Config struct {
	Log      LogConfig       `yaml:"log"`
	HTTP     httpx.Config    `yaml:"http"`
	Database database.Config `yaml:"database"`
	Session  SessionConfig   `yaml:"session"`
	Mail     mail.Config     `yaml:"mail"`
	Storage  storage.Config  `yaml:"storage"`
}

// LogConfig 是日志配置。
type LogConfig struct {
	Level  slog.Level `yaml:"level"`  // debug | info | warn | error，默认 info
	Format string     `yaml:"format"` // json | text，默认 json
}

// SessionConfig 是 Cookie 会话（scs）的配置。
type SessionConfig struct {
	CookieName   string        `yaml:"cookie_name"`   // 默认 "session"
	Lifetime     time.Duration `yaml:"lifetime"`      // 会话最长有效期，默认 24h
	IdleTimeout  time.Duration `yaml:"idle_timeout"`  // 无访问多久后失效，默认 0（不限制）
	SecureCookie bool          `yaml:"secure_cookie"` // 只通过 HTTPS 发送 Cookie，默认 true；本地 HTTP 调试时设为 false
}

// DefaultConfig 返回带默认值的 Config（database.dsn 为空，必须由产品配置）。
func DefaultConfig() Config {
	return Config{
		Log:      LogConfig{Level: slog.LevelInfo, Format: logger.FormatJSON},
		HTTP:     httpx.DefaultConfig(),
		Database: database.DefaultConfig(),
		Session: SessionConfig{
			CookieName:   "session",
			Lifetime:     24 * time.Hour,
			SecureCookie: true,
		},
	}
}

// AppConfig 返回 c 本身。产品配置内嵌 Config 后自动获得此方法，Main 借此取到 app 的配置。
func (c *Config) AppConfig() *Config { return c }

// Configurer 由内嵌了 Config 的产品配置（指针）实现。
type Configurer interface {
	AppConfig() *Config
}
