# 2. 基础设施与装配

- [启动基础设施](#infrastructure)
- [配置](#configuration)
- [装配应用](#assembling)
- [入口](#entrypoint)
- [检查一下](#checking)

<a id="infrastructure"></a>
## 启动基础设施

博客会用到四个服务：

| 服务 | 用途 | 从哪一章开始用 |
| --- | --- | --- |
| PostgreSQL | 业务数据 | 第 3 章 |
| Redis | 登录限流，之后还有锁 | 第 6 章 |
| Memcached | 缓存 | 第 9 章 |
| NSQ | 队列 | 第 10 章 |

在项目根目录创建 `compose.yaml`，一次全部启动：

```yaml title="compose.yaml"
# 开发和测试用的基础设施：docker compose up -d --wait
name: blog

services:
  postgres:
    image: postgres:18
    # 队列经由逻辑复制转发任务
    command: [postgres, -c, wal_level=logical]
    environment:
      POSTGRES_USER: blog
      POSTGRES_PASSWORD: blog
    ports: ["127.0.0.1:5432:5432"]
    volumes: [postgres:/var/lib/postgresql]
    healthcheck:
      test: pg_isready -U blog
      interval: 1s
      retries: 60

  redis:
    image: redis:8
    ports: ["127.0.0.1:6379:6379"]
    healthcheck:
      test: redis-cli ping
      interval: 1s
      retries: 30

  memcached:
    image: memcached:1.6-alpine
    ports: ["127.0.0.1:11211:11211"]

  nsqd:
    image: nsqio/nsq:v1.3.0
    command: [/nsqd, --mem-queue-size=0, --data-path=/data]
    ports: ["127.0.0.1:4150:4150", "127.0.0.1:4151:4151"]
    volumes: [nsqd:/data]

volumes:
  postgres:
  nsqd:
```

```bash
docker compose up -d --wait
```

`--wait` 等到服务都准备好才返回。数据保存在 Docker 的卷中，`docker compose down` 停止服务时不会丢；加上 `-v` 才会连数据一起删除。

<a id="configuration"></a>
## 配置

应用的配置写在 `config/app.yaml` 中：

```yaml title="config/app.yaml"
# 应用的配置。${VAR:-默认值} 读取环境变量，生产环境用环境变量覆盖开发时的默认值。
http:
  addr: ":8080"

database:
  dsn: ${DATABASE_URL:-postgres://blog:blog@localhost:5432/blog?sslmode=disable}
```

`${DATABASE_URL:-…}` 读取环境变量 `DATABASE_URL`，没有设置时使用后面的默认值。开发时直接用默认值连接上面启动的 PostgreSQL，生产环境用环境变量覆盖它，配置文件不用改。项目根目录的 `.env` 文件中的变量也会被读取，它不提交到版本库。

配置文件对应一个 Go 结构体。新建 `internal/bootstrap/config.go`：

```go title="internal/bootstrap/config.go"
// Package bootstrap 装配应用：读取配置，打开数据库等资源，把它们交给 HTTP 服务和各个业务模块。
package bootstrap

import (
    "github.com/go-tenon/tenon/database"
    "github.com/go-tenon/tenon/httpx"
)

// Config 是应用的配置：config/app.yaml，开发时再叠加 config/local.yaml。
type Config struct {
    HTTP     httpx.Config    `yaml:"http"`
    Database database.Config `yaml:"database"`
    // FrontendDevServer 非空时把页面请求转给 Vite 的开发服务器，只在 config/local.yaml 中设置。
    FrontendDevServer string `yaml:"frontend_dev_server"`
}
```

`httpx.Config`、`database.Config` 是框架定义的配置段，没有写的字段使用默认值，例如数据库的连接池大小。加载时会检查配置：缺少必填项、写错字段名都会在启动时报错，并指出是哪一项。`config/local.yaml` 只在开发时叠加在 `app.yaml` 之上，它设置的 `frontend_dev_server` 对应 `FrontendDevServer`。

<a id="assembling"></a>
## 装配应用

模板把所有代码都放在 `main.go` 中。应用会越来越大：数据库、Redis、缓存、队列……还会有多个进程，例如处理队列任务的 worker。我们把装配集中到 `internal/bootstrap` 包中。

新建 `internal/bootstrap/app.go`：

```go title="internal/bootstrap/app.go"
package bootstrap

import (
    "context"
    "log/slog"

    "github.com/uptrace/bun"

    "github.com/go-tenon/tenon/cleanup"
    "github.com/go-tenon/tenon/command"
    "github.com/go-tenon/tenon/database"
    "github.com/go-tenon/tenon/logging"
)

// App 是各个运行部分共用的资源，每个进程只装配一次。
type App struct {
    cfg Config
    db  *bun.DB
}

// open 安装日志器，连接数据库，把关闭操作登记到 stack。
func open(ctx context.Context, cfg Config, stack *cleanup.Stack) (*App, error) {
    slog.SetDefault(logging.New(logging.Config{}))

    db, err := database.Open(ctx, cfg.Database)
    if err != nil {
        return nil, err
    }
    stack.MustAdd("database", func(context.Context) error { return db.Close() })

    return &App{cfg: cfg, db: db}, nil
}

// part 是进程中的一个运行部分，例如 HTTP 服务：装配时返回 run，run 一直运行到 ctx 被取消。
type part func(*App, *command.Daemon) (run func(context.Context) error, err error)

// run 装配 App 和 parts，然后在同一个进程中运行 parts，直到收到停止信号，最后关闭资源。
func run(ctx context.Context, cfg Config, parts ...part) (err error) {
    d, err := command.NewDaemon(nil)
    if err != nil {
        return err
    }
    defer d.Stop()

    var stack cleanup.Stack
    defer func() { err = stack.CloseWith(context.Background(), err) }()

    app, err := open(ctx, cfg, &stack)
    if err != nil {
        return err
    }
    runs := make([]func(context.Context) error, len(parts))
    for i, p := range parts {
        if runs[i], err = p(app, d); err != nil {
            return err
        }
    }
    return d.Run(ctx, runs...)
}

// Serve 运行 HTTP 服务，serve 命令调用它。
func Serve(ctx context.Context, cfg Config) error {
    return run(ctx, cfg, (*App).HTTP)
}
```

- `App` 保存各部分共用的资源，现在只有数据库连接池。之后每章需要新的资源时，在 `open` 中打开它，保存到 `App` 中。
- `open` 打开资源，同时把关闭操作登记到 `stack`。进程退出时，资源按打开的相反顺序关闭。打开到一半失败时，已经打开的也会关闭。
- `run` 打开资源，装配进程要运行的各个部分，然后一直运行到收到停止信号（`Ctrl-C` 或 `SIGTERM`）。`Serve` 只运行 HTTP 服务；第 10 章加上处理队列任务的 worker 时，再写一个只运行 worker 的 `Work`。

新建 `internal/bootstrap/http.go`，装配 HTTP 服务：

```go title="internal/bootstrap/http.go"
package bootstrap

import (
    "context"
    "net/http"

    "github.com/go-chi/chi/v5"

    "github.com/go-tenon/tenon/command"
    "github.com/go-tenon/tenon/httpx"
    "github.com/go-tenon/tenon/httpx/rest"

    "example.com/blog/frontend"
    "example.com/blog/internal/todo"
)

// API 是 JSON API 的选项，挂在 /api 下。serve、openapi、client 命令共用它。
var API = rest.Options{Title: "Blog API", Version: "1.0.0", BasePath: "/api"}

// Routes 注册全部接口，serve 和生成文档共用它。
func Routes(api *rest.API, a *App) {
    todo.Register(api, todo.NewStore())
}

// DocRoutes 以文档模式注册全部接口，openapi 和 client 命令用它。文档模式只登记路由，
// 不调用处理器，所以用一个没有打开任何资源的 App。
func DocRoutes(api *rest.API) { Routes(api, &App{}) }

// DocRouter 在文档模式下构造全部路由，routes 命令用它。
func DocRouter() chi.Routes {
    router := chi.NewRouter()
    router.Route("/api", func(r chi.Router) {
        opts := API
        opts.Mode, opts.Router = rest.ModeOpenAPI, r
        DocRoutes(rest.New(opts))
    })
    return router
}

// HTTP 装配 HTTP 服务：健康检查、/api 下的接口和单页应用。
func (a *App) HTTP(d *command.Daemon) (func(context.Context) error, error) {
    spa, err := httpx.NewSPA(frontend.Dist, httpx.SPAOptions{DevServer: a.cfg.FrontendDevServer})
    if err != nil {
        return nil, err
    }
    var draining httpx.Draining
    router := chi.NewRouter()
    router.Use(httpx.Defaults(a.cfg.HTTP, nil, nil)...)
    router.Get("/livez", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
    router.Handle("/readyz", httpx.Ready(httpx.ReadyOptions{
        Draining: &draining,
        Checks:   []httpx.Check{{Name: "database", Ping: a.db.PingContext}},
    }))
    router.Route("/api", func(r chi.Router) {
        r.Use(httpx.Timeout(a.cfg.HTTP.Timeout))
        opts := API
        opts.Router = r
        Routes(rest.New(opts), a)
    })
    router.Handle("/*", spa)

    ln, err := d.Listen("tcp", a.cfg.HTTP.Addr)
    if err != nil {
        return nil, err
    }
    return func(ctx context.Context) error {
        return httpx.Serve(ctx, a.cfg.HTTP, ln, router, httpx.ServeOptions{OnDraining: draining.Set})
    }, nil
}
```

- `/livez` 和 `/readyz` 是健康检查，部署平台用它们判断进程是否存活、能否接收请求。`/readyz` 检查数据库，数据库连不上时返回 `503`。
- `/api` 下是 JSON 接口，`Routes` 注册它们。现在还是模板的待办接口，第 4 章换成文章。
- 其余请求交给 `httpx.SPA`：开发时转给 Vite，部署时读取嵌入程序的构建产物。
- `DocRoutes` 和 `DocRouter` 不连接任何服务就能注册全部路由，`tenon` 用它们生成接口文档和客户端。所以 `Routes` 中只能保存 `App` 的字段，不能在注册时使用它们。

<a id="entrypoint"></a>
## 入口

把 `cmd/api/main.go` 改成下面这样。它只声明有哪些命令，具体做什么都在 `bootstrap` 中：

```go title="cmd/api/main.go"
// Command api 是博客的入口。
package main

import (
    "github.com/go-tenon/tenon/command"
    "github.com/go-tenon/tenon/command/httpcmd"

    "example.com/blog/internal/bootstrap"
)

func main() {
    root := command.Root("api", "博客")
    root.AddCommand(
        command.Run("serve", "启动 HTTP 服务", bootstrap.Serve),
        command.Config[bootstrap.Config](),
    )
    // openapi、routes、client：导出接口文档、列出路由、生成客户端，go tool tenon 调用它们。
    root.AddCommand(httpcmd.Commands(bootstrap.API, bootstrap.DocRoutes, bootstrap.DocRouter)...)
    command.Execute(root)
}
```

- `serve` 加载配置，调用 `bootstrap.Serve`。
- `config` 打印加载后的完整配置，检查配置时有用：`go run ./cmd/api config`。
- `openapi`、`routes`、`client` 导出接口文档、列出路由、生成客户端，`tenon` 在开发时调用它们。

代码导入了新的包，可能用到 `go.mod` 中还没有的依赖。`bun run dev` 编译时会自动补全 `go.mod`；直接用 `go run ./cmd/api …` 运行命令之前，先运行一次 `go mod tidy`。

<a id="checking"></a>
## 检查一下

```bash
bun run dev
```

页面和上一章一样。在另一个终端检查就绪状态：

```bash
curl localhost:8080/readyz
```

```json
{"status":"ready","checks":{"database":"ok"}}
```

停止 PostgreSQL（`docker compose stop postgres`）再请求一次，返回 `503`，`database` 变为 `failed`。再 `docker compose start postgres`，恢复正常。

深入了解：[配置](../configuration.md)、[依赖装配](../dependencies.md)、[健康检查](../lifecycle.md#health-checks)。
