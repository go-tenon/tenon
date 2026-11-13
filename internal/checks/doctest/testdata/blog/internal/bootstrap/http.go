package bootstrap

import (
	"context"

	"example.com/blog/internal/article"
	"example.com/blog/internal/auth"
	"example.com/blog/internal/user"
	"github.com/go-chi/chi/v5"
	"github.com/go-tenon/tenon/command"
	"github.com/go-tenon/tenon/httpx"
	"github.com/go-tenon/tenon/httpx/rest"
	"github.com/go-tenon/tenon/identity"
	"github.com/uptrace/bun"
)

// API 是应用的 API 选项，serve、openapi 和 routes 命令共用它。API 只接受博客用户这一套账号。
var API = rest.Options{Title: "Blog API", Version: "1.0.0", Accounts: identity.Accounts{user.Type: nil}}

// Routes 注册应用的全部路由，serve、openapi 和 routes 命令共用它。
func Routes(api *rest.API, validate rest.TokenValidator, db *bun.DB) {
	article.Register(api, rest.RequireBearer(api, validate, "articles"), db)
}

// DocRoutes 在文档模式下注册路由。文档模式不调用处理器和 token 校验器，
// 所以不需要配置和数据库。
func DocRoutes(api *rest.API) {
	Routes(api, auth.Validator(auth.Config{}), nil)
}

// DocRouter 在文档模式下构造全部路由，routes 命令用它列出路由表。
func DocRouter() chi.Routes {
	router := chi.NewRouter()
	opts := API
	opts.Mode, opts.Router = rest.ModeOpenAPI, router
	DocRoutes(rest.New(opts))
	return router
}

// HTTP 安装中间件、注册路由并取得监听端口，返回的函数运行 HTTP 服务，直到 ctx 被取消。
func (a *App) HTTP(d *command.Daemon) (func(context.Context) error, error) {
	router := chi.NewRouter()
	router.Use(httpx.Telemetry(a.meters, a.tracers))
	router.Use(httpx.Defaults(a.cfg.HTTP, nil, nil)...)
	router.Use(httpx.Timeout(a.cfg.HTTP.Timeout))
	opts := API
	opts.Router = router
	api := rest.New(opts)
	Routes(api, auth.Validator(a.cfg.Auth), a.db)

	ln, err := d.Listen("tcp", a.cfg.HTTP.Addr)
	if err != nil {
		return nil, err
	}
	return func(ctx context.Context) error {
		return httpx.Serve(ctx, a.cfg.HTTP, ln, api.Handler(), httpx.ServeOptions{})
	}, nil
}
