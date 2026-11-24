# 2. 基础设施与装配

这一章用 Docker 启动 PostgreSQL、Redis 和 NATS，把数据库地址写进配置，再把打开资源、创建路由、启动服务这些装配代码从 `main.go` 移到 `internal/bootstrap` 包中。之后各章都在这个结构上添加内容。

<a id="infrastructure"></a>
## 启动基础设施

PostgreSQL 存业务数据（第 3 章起），Redis 用于[缓存](../libraries/cache.md)，NATS 用于[队列](../background/queue.md)。在项目根目录创建 `compose.yaml`：

```yaml title="compose.yaml"
# 开发和测试用的基础设施：docker compose up -d --wait
name: blog

services:
  postgres:
    image: postgres:18
    environment:
      POSTGRES_USER: blog
      POSTGRES_PASSWORD: blog
    ports: ["127.0.0.1:5432:5432"]
    volumes: [postgres:/var/lib/postgresql]
    healthcheck:
      test: pg_isready -U blog -h 127.0.0.1
      interval: 1s
      retries: 60

  redis:
    image: redis:8-alpine
    command: [redis-server, --save, "", --appendonly, "no"]
    ports: ["127.0.0.1:6379:6379"]

  nats:
    image: nats:2.15-alpine
    command: [nats-server, -js, -sd, /data]
    ports: ["127.0.0.1:4222:4222"]
    volumes: [nats:/data]

volumes:
  postgres:
  nats:
```

```bash
docker compose up -d --wait
```

`--wait` 等到各服务的 `healthcheck` 通过才返回。数据保存在卷中，`docker compose down -v` 才会删除。

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

`${DATABASE_URL:-…}` 读取环境变量 `DATABASE_URL`，没有设置时用 `:-` 后面的默认值：开发时连接上面启动的 PostgreSQL，生产环境用环境变量提供真正的地址。项目根目录的 `.env` 中的变量也会被读取，它不提交。

新建 `internal/bootstrap/config.go`，定义与配置文件对应的结构体：

```go title="internal/bootstrap/config.go"
// Package bootstrap 装配应用：读取配置，打开数据库等资源，把它们交给 HTTP 服务和各个业务模块。
package bootstrap

import (
    "github.com/go-tenon/tenon/database"
    "github.com/go-tenon/tenon/httpx"
)

// Config 是应用的配置：config/app.yaml。
type Config struct {
    HTTP       httpx.Config      `yaml:"http"`
    Database   database.Config   `yaml:"database"`
    DevServers map[string]string `yaml:"dev_servers"` // 各前端开发服务器的地址，只在 tenon dev 时有值
}
```

- `httpx.Config`、`database.Config` 是框架的配置段，没有写的字段用默认值；写错字段名时启动就报错。
- `DevServers` 由 `tenon dev` 写进 `.tenon/dev.yaml`，叠加在 `app.yaml` 之上，不用填写。

<a id="assembling"></a>
## 装配应用

`internal/bootstrap` 分三个文件：`config.go`、`app.go`（打开和关闭资源）、`http.go`（HTTP 服务）。新建 `internal/bootstrap/app.go`：

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

// Open 安装日志器，连接数据库，把关闭操作登记到 stack。
func Open(ctx context.Context, cfg Config, stack *cleanup.Stack) (*App, error) {
    slog.SetDefault(logging.New(logging.Config{}))

    db, err := database.Open(ctx, cfg.Database)
    if err != nil {
        return nil, err
    }
    stack.MustAdd("database", func(context.Context) error { return db.Close() })

    return &App{cfg: cfg, db: db}, nil
}

// Parts 是常驻进程的运行部分：serve 运行全部部分，serve <名字> 只运行其中一个。
func Parts(a *App) []command.Part {
    return []command.Part{
        {Name: "http", Short: "接口和页面", Start: a.HTTP},
    }
}
```

- `App` 保存各部分共用的资源。以后需要新的资源时，在 `Open` 中打开、保存到 `App`。
- `Open` 把关闭操作登记到 `stack`：退出时按打开的相反顺序关闭；打开到一半失败时，已打开的资源也会关闭。
- `Parts` 列出进程中常驻的部分。`Start` 做好准备工作，返回一个运行到收到停止信号才返回的函数。以后加上[队列 worker](../background/queue.md#running-workers) 时在这里加一行。

`command.Serve` 调用 `Open` 和 `Parts`，运行到收到 `Ctrl-C` 或 `SIGTERM`，再关闭资源。

新建 `internal/bootstrap/http.go`：

```go title="internal/bootstrap/http.go"
package bootstrap

import (
    "context"
    "net/http"

    "github.com/go-chi/chi/v5"

    "github.com/go-tenon/tenon/command"
    "github.com/go-tenon/tenon/config"
    "github.com/go-tenon/tenon/frontend"
    "github.com/go-tenon/tenon/health"
    "github.com/go-tenon/tenon/httpx"
    "github.com/go-tenon/tenon/httpx/rest"

    "example.com/blog/apps"
    "example.com/blog/internal/todo"
)

// HTTP 装配 HTTP 服务。apps 中的前端由 frontend.Host 按挂载路径提供：服务端渲染的前端由
// Go 托管的 SSR 进程渲染，它们渲染时经内部 socket 调用同一组路由。
func (a *App) HTTP(d *command.Daemon) (func(context.Context) error, error) {
    var router chi.Router
    // router 在 pages 创建之后才构造好，SSR 进程调用接口时经这个函数转给它。
    api := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { router.ServeHTTP(w, r) })
    pages, err := frontend.New(apps.Dist, frontend.Options{API: api, DevServers: a.cfg.DevServers})
    if err != nil {
        return nil, err
    }
    router = a.Router(pages.Handler())
    ln, err := d.Listen("tcp", a.cfg.HTTP.Addr)
    if err != nil {
        return nil, err
    }
    return func(ctx context.Context) error {
        return pages.Run(ctx, func(ctx context.Context) error {
            return httpx.Serve(ctx, a.cfg.HTTP, ln, router)
        })
    }, nil
}

// Router 构造应用的全部路由：健康检查、/api 下的接口和页面。
// 它只保存 App 的字段、不使用它们，所以 DocRouter 可以用一个没有打开任何资源的 App 调用它。
func (a *App) Router(pages http.Handler) chi.Router {
    router := chi.NewRouter()
    router.Use(httpx.Defaults(a.cfg.HTTP)...)
    router.Get("/livez", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
    router.Handle("/readyz", httpx.Ready(httpx.ReadyOptions{
        Checks: []health.Check{
            {Name: "database", Ping: func(ctx context.Context) error { return a.db.PingContext(ctx) }},
        },
    }))
    router.Route("/api", func(r chi.Router) {
        r.Use(httpx.Timeout(a.cfg.HTTP.Timeout))
        api := rest.New(r, rest.Options{Title: "Blog API", Version: "1.0.0", BasePath: "/api"})
        todo.RegisterRoutes(api, todo.NewStore())
    })
    if pages != nil {
        router.Handle("/*", pages)
    }
    return router
}

// DocRouter 用默认配置和没有打开任何资源的 App 构造路由，openapi、routes、client 命令用它。
func DocRouter() chi.Routes {
    return (&App{cfg: config.Default[Config]()}).Router(nil)
}

```

- `HTTP` 是 `Parts` 中的 `Start`：监听端口，返回运行服务的函数。收到停止信号时先停止接收新连接，再等正在处理的请求完成。
- `/livez` 只要程序能响应就返回 `204`；`/readyz` 还检查数据库，数据库连不上或程序正在停止时返回 `503`。
- `/api` 下是 JSON 接口，现在注册的是模板的待办接口，第 4 章换成文章。
- 其余请求交给 `pages`：静态文件由 Go 返回，页面由 Go 托管的 SSR 进程渲染，开发时转给前端开发服务器，见[托管前端与服务端渲染](../frontend/hosting.md)。
- `DocRouter` 不打开任何资源就构造全部路由，`tenon` 用它生成接口文档和客户端。所以 `Router` 中只保存 `App` 的字段、不立即使用：数据库检查写成闭包，`a.db` 为 `nil` 时构造路由不会 panic。

<a id="entrypoint"></a>
## 入口

把 `cmd/blog/main.go` 改成只声明命令：

```go title="cmd/blog/main.go"
// Command blog 是博客的入口。
package main

import (
    "github.com/go-tenon/tenon/command"
    "github.com/go-tenon/tenon/command/httpcmd"
    "github.com/go-tenon/tenon/httpx"

    "example.com/blog/internal/bootstrap"
)

func main() {
    root := command.Root("blog", "博客")
    root.AddCommand(
        command.Serve(bootstrap.Open, bootstrap.Parts),
        command.Config[bootstrap.Config](),
        httpcmd.Health(func(cfg bootstrap.Config) httpx.Config { return cfg.HTTP }),
    )
    // openapi、routes、client：导出接口文档、列出路由、生成客户端，go tool tenon 调用它们。
    root.AddCommand(httpcmd.Commands(bootstrap.DocRouter)...)
    command.Execute(root)
}
```

- `serve`：加载配置，调用 `bootstrap.Open`，运行 `bootstrap.Parts` 中的全部部分；`serve http` 只运行 HTTP 服务。
- `config show`：打印加载后的完整配置，例如 `go run ./cmd/blog config show`。
- `health`：请求本机的 `/livez`，用作 Docker 的健康检查，见[部署](../installation/deployment.md#health-checks)。
- `openapi`、`routes`、`client`：导出接口文档、列出路由、生成客户端，`go tool tenon` 调用它们。

`bun run dev` 编译时会补全 `go.mod`；直接用 `go run ./cmd/blog …` 之前先运行一次 `go mod tidy`。

<a id="checking"></a>
## 检查一下

```bash
bun run dev
```

在另一个终端检查就绪状态：

```bash
curl localhost:8080/readyz
```

```json
{"status":"ready","checks":{"database":"ok"}}
```

`docker compose stop postgres` 之后再请求，返回 `503`，`database` 是 `failed`；`docker compose start postgres` 恢复。
