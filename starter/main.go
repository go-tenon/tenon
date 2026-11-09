// Command starter 是新产品的起点：用 go-kit 的 app 包组装服务，用 account 模块实现注册登录，
// 页面用 templ + shadcn-templ + HTMX，API 用 huma。新产品复制本目录，改掉模块路径后在此基础上开发。
//
// 需要 Postgres 18+。在本目录运行：
//
//	DATABASE_URL=postgres://postgres:postgres@localhost:5432/starter?sslmode=disable go run .
//	go run . --help
//
// 然后浏览器打开 http://localhost:8080 注册账号；API 文档在 http://localhost:8080/docs。
// 找回密码的邮件默认只写入日志（mail.driver: log）。
//
// 修改 .templ 或样式后重新生成（生成结果已提交，只运行不需要这些工具）：
//
//	task generate   # 需要 task 与 Tailwind CSS v4 独立 CLI，见 Taskfile.yml
package main

import (
	"github.com/ryan053857/go-kit/app"
	"github.com/ryan053857/go-kit/database"
	"github.com/ryan053857/go-kit/modules/account"
	"github.com/ryan053857/go-kit/starter/migrations"
)

var version = "dev" // 由 -ldflags "-X main.version=..." 注入

func main() {
	cfg := DefaultConfig()
	app.Main(&cfg, options(&cfg))
}

func options(cfg *Config) app.Options {
	return app.Options{
		Name:       "starter",
		Version:    version,
		Migrations: migrations.FS,
		Publish:    []database.MigrationSource{account.Migrations},
		Setup:      func(a *app.App) error { return setup(a, cfg) },
	}
}
