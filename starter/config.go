package main

import (
	"github.com/ryan053857/go-kit/app"
	"github.com/ryan053857/go-kit/modules/account"
)

// Config 内嵌 app.Config（log、http、database、session、mail），其后是产品自己的配置。
type Config struct {
	app.Config `yaml:",inline"`

	// BaseURL 是对外访问地址，用于邮件中的链接，如 https://app.example.com。
	BaseURL string         `yaml:"base_url"`
	Account account.Config `yaml:"account"`
}

func DefaultConfig() Config {
	return Config{Config: app.DefaultConfig(), Account: account.DefaultConfig()}
}
