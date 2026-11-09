package main

import (
	"github.com/ryan053857/go-kit/app"
	"github.com/ryan053857/go-kit/modules/account"
	"github.com/ryan053857/go-kit/modules/file"
)

// Config 内嵌 app.Config（log、http、database、session、mail、storage），其后是产品自己的配置。
type Config struct {
	app.Config `yaml:",inline"`

	// BaseURL 是对外访问地址，用于邮件中的链接与第三方登录的回调地址，如 https://app.example.com。
	BaseURL string         `yaml:"base_url"`
	Account account.Config `yaml:"account"`
	Files   file.Config    `yaml:"files"`
	// Admins 是管理员的邮箱，可以访问 /admin 下的系统设置与审计日志。
	Admins []string `yaml:"admins"`
	// GitHub 第三方登录，client_id 为空时不启用。
	GitHub account.OAuthClient `yaml:"github"`
}

func DefaultConfig() Config {
	return Config{Config: app.DefaultConfig(), Account: account.DefaultConfig(), Files: file.DefaultConfig()}
}
