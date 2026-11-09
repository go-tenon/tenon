package app

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"sync"
	"time"

	"github.com/alexedwards/scs/postgresstore"
	"github.com/alexedwards/scs/v2"
	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"
	"github.com/uptrace/bun"

	"github.com/ryan053857/go-kit/database"
	"github.com/ryan053857/go-kit/httpx"
	"github.com/ryan053857/go-kit/httpx/rest"
	"github.com/ryan053857/go-kit/mail"
	"github.com/ryan053857/go-kit/messaging"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// SessionMigrations 是 Cookie 会话表的迁移。Main 的 migrate publish 总会发布它，
// 不使用 Main 的产品需要自己把它传给 database.PublishMigrations。
var SessionMigrations = database.MigrationSource{Name: "session", FS: mustSub(migrationsFS, "migrations")}

// Options 描述一个产品。
type Options struct {
	// Name 为服务名：日志的 service 字段、命令名、OpenAPI 标题。
	Name string
	// Version 为版本号，通常由 -ldflags 注入：日志的 version 字段、OpenAPI 版本。
	Version string

	// Migrations 为产品的迁移（通常是 migrations 包 embed 的 FS），启动时自动执行。
	Migrations fs.FS
	// MigrationsDir 为源码中迁移目录的路径，供 migrate create / publish 写入文件，默认 "migrations"。
	MigrationsDir string
	// Publish 为产品使用的 go-kit 模块的迁移，由 migrate publish 复制到 MigrationsDir。
	// SessionMigrations 总会发布，不需要列出。
	Publish []database.MigrationSource

	// Setup 在数据库连接、迁移完成后调用，产品在这里构造服务、挂载中间件与路由。
	Setup func(a *App) error
}

// App 是装配好的应用，Setup 通过它取得共享的依赖。
type App struct {
	Config   *Config
	DB       *bun.DB
	Sessions *scs.SessionManager
	Mailer   mail.Mailer
	// Bus 处理异步消息：在 Setup 中用 messaging.Handle 注册处理函数，Serve 时与 HTTP 服务一起运行。
	Bus *messaging.Bus

	// Router 为根路由，已挂载 RequestID、AccessLog 等全局中间件（顺序见 New）。
	// 产品可以继续 Use 中间件，但必须在注册任何路由、调用 API() 之前（chi 的规则）。
	Router chi.Router

	opts   Options
	api    huma.API
	store  *postgresstore.PostgresStore
	closed bool
}

var setErrorHandler sync.Once

// New 连接数据库、执行 opts.Migrations、组装 Router 并调用 opts.Setup，返回可以处理请求的 App。
// 调用方负责 Close。Main 的 serve 命令即用它启动；集成测试也可以直接调用它，再用 a.Router 处理请求。
//
// Router 的全局中间件依次为：RealIP、RequestID、AccessLog、Recover、SecureHeaders、
// CSRF 防护（http.CrossOriginProtection）、会话加载（Sessions.LoadAndSave）。
// 同时把 huma.NewErrorWithContext 替换为 rest.LogServerErrors 包装后的版本（进程内只替换一次）。
func New(ctx context.Context, cfg *Config, opts Options) (*App, error) {
	return build(ctx, cfg, opts, true, true)
}

func build(ctx context.Context, cfg *Config, opts Options, connect, migrate bool) (_ *App, err error) {
	setErrorHandler.Do(func() {
		huma.NewErrorWithContext = rest.LogServerErrors(huma.NewErrorWithContext)
	})

	a := &App{Config: cfg, opts: opts}
	defer func() {
		if err != nil {
			_ = a.Close()
		}
	}()

	if connect {
		if a.DB, err = database.Open(ctx, cfg.Database); err != nil {
			return nil, err
		}
		if migrate && opts.Migrations != nil {
			if err = database.Migrate(ctx, a.DB.DB, opts.Migrations); err != nil {
				return nil, err
			}
		}
	} else if a.DB, err = database.New(cfg.Database); err != nil {
		return nil, err
	}

	if a.Mailer, err = mail.New(cfg.Mail); err != nil {
		return nil, err
	}
	if a.Bus, err = messaging.New(a.DB.DB); err != nil {
		return nil, err
	}

	a.Sessions = scs.New()
	a.Sessions.Cookie.Name = cfg.Session.CookieName
	a.Sessions.Cookie.Secure = cfg.Session.SecureCookie
	a.Sessions.Lifetime = cfg.Session.Lifetime
	a.Sessions.IdleTimeout = cfg.Session.IdleTimeout
	if connect {
		a.store = postgresstore.NewWithCleanupInterval(a.DB.DB, 5*time.Minute)
		a.Sessions.Store = a.store
	}

	r := chi.NewRouter()
	r.Use(
		httpx.RealIP(cfg.HTTP.TrustedProxies...),
		httpx.RequestID,
		httpx.AccessLog(nil),
		httpx.Recover(nil),
		httpx.SecureHeaders,
		http.NewCrossOriginProtection().Handler,
		a.Sessions.LoadAndSave,
	)
	a.Router = r

	if opts.Setup != nil {
		if err = opts.Setup(a); err != nil {
			return nil, fmt.Errorf("app: setup: %w", err)
		}
	}
	return a, nil
}

// API 返回挂在 Router 上的 huma API，首次调用时创建：OpenAPI 文档在 /openapi.json，
// 交互式文档在 /docs，并声明了 rest.SecuritySchemes 中的 Bearer 与 Session 两种认证方式。
//
// 创建 API 会在 Router 上注册文档路由，因此 Router.Use 必须在首次调用 API 之前完成。
func (a *App) API() huma.API {
	if a.api == nil {
		c := huma.DefaultConfig(a.opts.Name, a.opts.Version)
		c.Components.SecuritySchemes = rest.SecuritySchemes(a.Sessions.Cookie.Name)
		a.api = humachi.New(a.Router, c)
	}
	return a.api
}

// Close 释放数据库连接与会话清理任务，可以重复调用。
func (a *App) Close() error {
	if a.closed {
		return nil
	}
	a.closed = true
	if a.store != nil {
		a.store.StopCleanup()
	}
	if a.DB != nil {
		return a.DB.Close()
	}
	return nil
}

// Serve 运行消息处理函数与 HTTP 服务，直到 ctx 取消或其中之一出错；/readyz 会检查数据库连接。
// 消息处理开始后才开始接受 HTTP 请求。停止时先停 HTTP（等待进行中的请求），再停消息处理。
func (a *App) Serve(ctx context.Context) error {
	busCtx, stopBus := context.WithCancel(context.WithoutCancel(ctx))
	defer stopBus()
	httpCtx, stopHTTP := context.WithCancel(ctx)
	defer stopHTTP()

	busErr := make(chan error, 1)
	go func() {
		err := a.Bus.Run(busCtx)
		stopHTTP() // 消息处理出错时停止 HTTP
		busErr <- err
	}()
	select {
	case <-a.Bus.Running():
	case <-httpCtx.Done():
		stopBus()
		return <-busErr
	}

	srv := httpx.NewServer(a.Config.HTTP, a.Router)
	srv.ReadyCheck = a.DB.PingContext
	httpErr := srv.Run(httpCtx)
	stopBus()
	return errors.Join(httpErr, <-busErr)
}

func (o Options) migrationsDir() string {
	if o.MigrationsDir == "" {
		return "migrations"
	}
	return o.MigrationsDir
}

func (o Options) publishSources() []database.MigrationSource {
	return append([]database.MigrationSource{SessionMigrations}, o.Publish...)
}

func mustSub(fsys fs.FS, dir string) fs.FS {
	sub, err := fs.Sub(fsys, dir)
	if err != nil {
		panic(err)
	}
	return sub
}

var errNoMigrations = errors.New("app: Options.Migrations is nil")
