// Command starter 是新产品的起点：演示如何用 go-kit 的 app 包组装服务，
// 包含 REST API、HTMX 页面、Cookie 会话登录与 Bearer token 鉴权。
// 新产品复制本目录，改掉模块路径后在此基础上开发。
//
// 需要 Postgres 18+。在本目录运行：
//
//	DATABASE_URL=postgres://postgres:postgres@localhost:5432/starter?sslmode=disable go run .
//	go run . --help
//
// 然后：
//
//	浏览器打开 http://localhost:8080 ，用 admin / admin 登录
//	API 文档：http://localhost:8080/docs
//	curl -H 'Authorization: Bearer dev-token' http://localhost:8080/api/v1/notes
//
// 修改 .templ 文件后重新生成（生成的 _templ.go 已提交，运行示例不需要 templ 命令）：
//
//	go run github.com/a-h/templ/cmd/templ@v0.3.1100 generate
package main

import (
	"github.com/ryan053857/go-kit/app"
	"github.com/ryan053857/go-kit/starter/migrations"
)

var version = "dev" // 由 -ldflags "-X main.version=..." 注入

func main() {
	cfg := DefaultConfig()
	app.Main(&cfg, app.Options{
		Name:       "starter",
		Version:    version,
		Migrations: migrations.FS,
		Setup:      func(a *app.App) error { return setup(a, &cfg) },
	})
}
