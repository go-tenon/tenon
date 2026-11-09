package main

import (
	"log/slog"
	"time"

	"github.com/ryan053857/go-kit/config"
	"github.com/ryan053857/go-kit/httpx"
	"github.com/ryan053857/go-kit/logger"
)

// Config 由产品自己定义。log 级别直接使用 slog.Level，可从 "debug"、"info" 等解码；
// http 一节直接嵌入 httpx.Config。
type Config struct {
	Log struct {
		Level  slog.Level `yaml:"level"`
		Format string     `yaml:"format"`
	} `yaml:"log"`

	HTTP httpx.Config `yaml:"http"`

	Session struct {
		Lifetime     time.Duration `yaml:"lifetime"`
		SecureCookie bool          `yaml:"secure_cookie"` // 生产环境（HTTPS）应为 true
	} `yaml:"session"`

	// Demo 是演示账号，实际产品从数据库读取用户。
	Demo struct {
		Name     string        `yaml:"name"`
		Password config.Secret `yaml:"password"`
		APIToken config.Secret `yaml:"api_token"`
	} `yaml:"demo"`
}

func DefaultConfig() Config {
	var c Config
	c.Log.Level = slog.LevelInfo
	c.Log.Format = logger.FormatJSON
	c.HTTP = httpx.DefaultConfig()
	c.Session.Lifetime = 24 * time.Hour
	return c
}
