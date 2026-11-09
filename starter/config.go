package main

import (
	"github.com/ryan053857/go-kit/app"
	"github.com/ryan053857/go-kit/config"
)

// Config 内嵌 app.Config（log、http、database、session），其后是产品自己的配置。
type Config struct {
	app.Config `yaml:",inline"`

	// Demo 是演示账号，实际产品从数据库读取用户。
	Demo struct {
		Name     string        `yaml:"name"`
		Password config.Secret `yaml:"password"`
		APIToken config.Secret `yaml:"api_token"`
	} `yaml:"demo"`
}

func DefaultConfig() Config {
	return Config{Config: app.DefaultConfig()}
}
