// Command service 演示产品如何在 main 中组装本仓库的各个模块：
// config、logger、httpx（chi 中间件与 Server）、httpx/auth（Session + Bearer）、
// httpx/rest（huma API）与 httpx/web（templ + HTMX 页面）。
//
// 在仓库根目录运行：
//
//	go run ./examples/service -config examples/service/config.yaml
//	LOG_FORMAT=text go run ./examples/service -config examples/service/config.yaml
//
// 然后：
//
//	浏览器打开 http://localhost:8080 ，用 admin / admin 登录
//	API 文档：http://localhost:8080/docs
//	curl -H 'Authorization: Bearer dev-token' http://localhost:8080/api/v1/notes
//
// 修改 .templ 文件后重新生成（生成的 _templ.go 已提交，运行示例不需要 templ 命令）：
//
//	go run github.com/a-h/templ/cmd/templ@v0.3.1100 generate -path examples/service
package main

import (
	"context"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/alexedwards/scs/v2"
	"github.com/alexedwards/scs/v2/memstore"
	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"

	"github.com/ryan053857/go-kit/config"
	"github.com/ryan053857/go-kit/examples/service/account"
	"github.com/ryan053857/go-kit/examples/service/note"
	"github.com/ryan053857/go-kit/examples/service/user"
	"github.com/ryan053857/go-kit/examples/service/views"
	"github.com/ryan053857/go-kit/httpx"
	"github.com/ryan053857/go-kit/httpx/auth"
	"github.com/ryan053857/go-kit/httpx/rest"
	"github.com/ryan053857/go-kit/httpx/web"
	"github.com/ryan053857/go-kit/logger"
)

const (
	serviceName = "example-service"
	version     = "dev" // 通常由 -ldflags 注入
)

func main() {
	path := flag.String("config", "config.yaml", "path to the configuration file")
	flag.Parse()

	cfg := DefaultConfig()
	if err := config.Load(*path, &cfg); err != nil {
		slog.Error("load config", "err", err)
		os.Exit(1)
	}

	// LevelVar 允许运行时调整级别（例如重新加载配置时）。
	var level slog.LevelVar
	level.Set(cfg.Log.Level)
	slog.SetDefault(logger.New(logger.Options{
		Level:  &level,
		Format: cfg.Log.Format,
	}).With("service", serviceName, "version", version))

	// Secret 字段会被打码，整个配置可以安全地写入日志。
	slog.Info("config loaded", "config", cfg)

	if err := run(cfg); err != nil {
		slog.Error("service stopped", "err", err)
		os.Exit(1)
	}
}

func run(cfg Config) error {
	// 全局错误处理：显式替换 huma 与 web 的默认实现。
	huma.NewErrorWithContext = rest.LogServerErrors(huma.NewErrorWithContext)
	web.ErrorHandler = views.ErrorHandler

	// 依赖。实际产品在这里用 database.Open 连接数据库、database.Migrate 执行迁移，并传给各模块。
	users := user.NewStore(cfg.Demo.Name, string(cfg.Demo.Password), string(cfg.Demo.APIToken))
	notes := note.NewStore()

	sessions := scs.New()
	sessions.Store = memstore.New() // 实际产品使用 postgresstore.New(db.DB) 或 redisstore
	sessions.Lifetime = cfg.Session.Lifetime
	sessions.Cookie.Secure = cfg.Session.SecureCookie

	// 全局中间件：顺序见 httpx 包文档。
	r := chi.NewRouter()
	r.Use(
		httpx.RealIP(cfg.HTTP.TrustedProxies...),
		httpx.RequestID,
		httpx.AccessLog(nil),
		httpx.Recover(nil),
		httpx.SecureHeaders,
		http.NewCrossOriginProtection().Handler, // Cookie 会话的 CSRF 防护
		sessions.LoadAndSave,
		auth.LoadSession(sessions, users.Load),
	)

	r.Handle("/static/*", views.Static)
	r.Handle("/vendor/*", views.Vendor)

	// REST API：/api/v1，文档在 /docs。
	apiConfig := huma.DefaultConfig("Notes API", version)
	apiConfig.Components.SecuritySchemes = rest.SecuritySchemes(sessions.Cookie.Name)
	api := humachi.New(r, apiConfig)
	api.UseMiddleware(rest.Authorize(api, users.VerifyToken))
	rest.Mount(huma.NewGroup(api, "/api/v1"), note.APIActions(notes)...)

	// 页面。
	account.MountPages(r, sessions, users)
	r.Group(func(r chi.Router) {
		r.Use(auth.RequireLogin[*user.User]("/login"))
		note.MountPages(r, notes)
	})

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return httpx.NewServer(cfg.HTTP, r).Run(ctx)
}
