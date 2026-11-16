# 2. 基础设施与装配

上一章生成的项目把数据保存在内存中，重启就没了。一个真正的应用需要数据库，通常还需要 Redis、缓存和队列，这些统称为**基础设施**。

**这一章要做的：**

1. 用 Docker 在你的电脑上启动这些服务；
2. 把数据库的地址写进配置文件；
3. 把“打开数据库、创建路由、启动服务”这些准备工作（我们称为**装配**）从 `main.go` 中整理到 `internal/bootstrap` 包中。

这一章没有新的功能，但之后的每一章都会在这个结构上添加内容。

阅读前需要了解：[第 1 章](01-project.md)；[context](../concepts/go-primer.md#context) 和 [defer](../concepts/go-primer.md#cleanup) 的用法。

- [启动基础设施](#infrastructure)
- [配置](#configuration)
- [装配应用](#assembling)
- [入口](#entrypoint)
- [检查一下](#checking)
- [小结](#summary)

<a id="infrastructure"></a>
## 启动基础设施

一个常规项目会用到四个服务：

| 服务 | 用途 | 在哪里用到 |
| --- | --- | --- |
| PostgreSQL | 业务数据 | 第 3 章 |
| Redis | 限流、锁 | 第 6 章、[限流](../libraries/rate-limiting.md) |
| Memcached | 缓存 | [缓存](../libraries/cache.md) |
| NSQ | 队列 | [队列](../background/queue.md) |

我们用 Docker Compose 启动它们。Docker 把一个服务和它需要的运行环境打包成**镜像**，在你的电脑上以**容器**的形式运行，不需要一个个安装和配置这些软件。Docker Compose 用一个 YAML 文件描述要启动哪些容器。

在项目根目录创建 `compose.yaml`：

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
      test: pg_isready -U blog -h 127.0.0.1
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

`-d` 让服务在后台运行；`--wait` 等到服务都准备好才返回，判断的依据是每个服务的 `healthcheck`。

文件中的几个要点：

- `ports` 中的 `127.0.0.1:5432:5432` 把容器的端口映射到你电脑的 `5432` 端口，并且只允许本机访问。程序用 `localhost:5432` 就能连上数据库。
- `volumes` 把数据保存在 Docker 的**卷**中。`docker compose down` 停止并删除容器时，数据仍然保留；加上 `-v` 才会连数据一起删除。
- PostgreSQL 的 `wal_level=logical` 是[队列](../background/queue.md)需要的设置，现在不用关心。

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

`dsn` 是数据库的连接地址，包含用户名、密码、主机、端口和数据库名。`${DATABASE_URL:-…}` 读取环境变量 `DATABASE_URL`，没有设置时使用 `:-` 后面的默认值。这样开发时直接用默认值连接上面启动的 PostgreSQL；部署到生产环境时，用环境变量提供真正的地址和密码，配置文件不用改，也不会把生产环境的密码提交到代码仓库。项目根目录的 `.env` 文件中的变量也会被读取，它只留在你的电脑上，不提交。

程序要读取这个文件，需要一个对应的 Go 结构体：YAML 中的每一段对应结构体的一个字段，字段上的 `yaml:"..."` 标签写着它在文件中的名字（见[结构体标签](../concepts/go-primer.md#struct-tags)）。新建 `internal/bootstrap/config.go`：

```go title="internal/bootstrap/config.go"
// Package bootstrap 装配应用：读取配置，打开数据库等资源，把它们交给 HTTP 服务和各个业务模块。
package bootstrap

import (
    "github.com/go-tenon/tenon/database"
    "github.com/go-tenon/tenon/httpx"
)

// Config 是应用的配置：config/app.yaml。
type Config struct {
    HTTP     httpx.Config    `yaml:"http"`
    Database database.Config `yaml:"database"`
    // DevServers 是各前端开发服务器的地址，只在 tenon dev 时有值。
    DevServers map[string]string `yaml:"dev_servers"`
}
```

- `httpx.Config`、`database.Config` 是框架定义的配置段。文件中没有写的字段使用默认值，例如数据库连接池的大小。（连接池：程序预先和数据库建立几个连接，每次查询借一个、用完还回去，不必每次都重新连接。）
- 加载时会检查配置：缺少必填的项、写错了字段名，启动时就会报错，并指出是哪一项。
- `DevServers` 不用你填写。开发时 `tenon dev` 把各前端开发服务器的地址写进 `.tenon/dev.yaml`，叠加在 `app.yaml` 之上，程序据此把页面请求转给前端的开发服务器。

<a id="assembling"></a>
## 装配应用

模板把所有代码都放在 `main.go` 中。现在这样没问题，但应用会越来越大：要打开数据库、Redis、缓存、队列；程序退出时要按正确的顺序关闭它们；除了 HTTP 服务，以后还会有处理后台任务的 worker，它们需要同样的数据库连接。如果都写在 `main.go` 中，它很快会变得又长又难改。

所以我们把装配集中到 `internal/bootstrap` 包中，分成三个文件：`config.go`（上面已经写好）、`app.go`（打开和关闭资源）、`http.go`（HTTP 服务）。

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

逐个看这几部分：

- **`App`** 保存各部分共用的资源，现在只有数据库连接 `db`。之后的章节需要新的资源时，就在 `open` 中打开它，保存到 `App` 中。
- **`open`** 打开资源，同时把“怎样关闭它”登记到 `stack` 中。程序退出时，`stack` 按打开的相反顺序关闭资源，例如先关闭依赖数据库的部分，再关闭数据库。如果打开到一半失败了（例如数据库连不上），已经打开的资源也会被关闭，不会泄漏。
- **`part`** 是一个函数类型，表示进程中的一个运行部分，例如 HTTP 服务。它先做准备工作，返回一个一直运行、直到 ctx 被取消的 `run` 函数（函数作为值，见[把方法当作值传递](../concepts/go-primer.md#method-values)）。
- **`run`** 把这些串起来：打开资源，准备好各个部分，然后一直运行到收到停止信号（`Ctrl-C` 或 `SIGTERM`），最后由 `defer` 关闭资源。
- **`Serve`** 是 `serve` 命令调用的函数，它只运行 HTTP 服务一个部分。以后加上处理后台任务的 worker 时，再写一个只运行 worker 的 `Work`，见[队列](../background/queue.md#running-workers)。

接着，新建 `internal/bootstrap/http.go`，装配 HTTP 服务：

```go title="internal/bootstrap/http.go"
package bootstrap

import (
    "context"
    "net/http"

    "github.com/go-chi/chi/v5"

    "github.com/go-tenon/tenon/command"
    "github.com/go-tenon/tenon/config"
    "github.com/go-tenon/tenon/health"
    "github.com/go-tenon/tenon/httpx"
    "github.com/go-tenon/tenon/httpx/rest"

    "example.com/blog/apps/web"
    "example.com/blog/internal/todo"
)

// HTTP 装配 HTTP 服务，serve 命令运行它。
func (a *App) HTTP(d *command.Daemon) (func(context.Context) error, error) {
    var draining httpx.Draining
    router := a.Router(&draining)
    ln, err := d.Listen("tcp", a.cfg.HTTP.Addr)
    if err != nil {
        return nil, err
    }
    return func(ctx context.Context) error {
        return httpx.Serve(ctx, a.cfg.HTTP, ln, router, httpx.OnDraining(draining.Set))
    }, nil
}

// Router 构造应用的全部路由：健康检查、/api 下的接口和单页应用。
// 它只保存 App 的字段、不使用它们，所以 DocRouter 可以用一个没有打开任何资源的 App 调用它。
func (a *App) Router(draining *httpx.Draining) chi.Router {
    router := chi.NewRouter()
    router.Use(httpx.Defaults(a.cfg.HTTP)...)
    router.Get("/livez", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
    router.Handle("/readyz", httpx.Ready(httpx.ReadyOptions{
        Draining: draining,
        Checks: []health.Check{
            {Name: "database", Ping: func(ctx context.Context) error { return a.db.PingContext(ctx) }},
        },
    }))
    router.Route("/api", func(r chi.Router) {
        r.Use(httpx.Timeout(a.cfg.HTTP.Timeout))
        api := rest.New(r, rest.Options{Title: "Blog API", Version: "1.0.0", BasePath: "/api"})
        todo.Register(api, todo.NewStore())
    })
    router.Handle("/*", httpx.NewSPA(web.Dist, httpx.SPAOptions{DevServer: a.cfg.DevServers["web"]}))
    return router
}

// DocRouter 用默认配置和没有打开任何资源的 App 构造路由，openapi、routes、client 命令用它。
func DocRouter() chi.Routes {
    return (&App{cfg: config.Default[Config]()}).Router(nil)
}
```

- **`HTTP`** 就是上面说的一个 `part`：它监听端口，返回运行服务的函数。收到停止信号时，服务先把 `draining` 标记为“正在停止”，再等正在处理的请求完成（排空）。
- **`/livez` 和 `/readyz`** 是健康检查，部署平台定期访问它们，判断程序是否还活着、能否接收请求。`/readyz` 会检查数据库，数据库连不上、或者程序正在停止时返回 `503`。
- **`/api`** 下是 JSON 接口，`rest.New` 在这个子路由上创建 API。现在注册的还是模板的待办接口，第 4 章换成文章。
- **其余的请求**交给前端页面：开发时转给前端的开发服务器，部署时直接提供打包进程序的文件。
- **`DocRouter`** 在不打开任何资源的情况下构造全部路由，`tenon` 用它生成接口文档和客户端。为此，`Router` 中只能保存 `App` 的字段、不能立即使用它们。数据库的检查写成了一个闭包 `func(ctx context.Context) error { return a.db.PingContext(ctx) }`，原因也在这里：`DocRouter` 中的 `a.db` 是 `nil`，如果直接写 `a.db.PingContext`，构造路由时就会 panic；写成闭包后，要到真的执行检查时才会用到 `a.db`。

<a id="entrypoint"></a>
## 入口

现在，把 `cmd/api/main.go` 改成下面这样。它只声明有哪些命令，具体做什么都在 `bootstrap` 中：

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
    root.AddCommand(httpcmd.Commands(bootstrap.DocRouter)...)
    command.Execute(root)
}
```

和第 1 章相比，`main.go` 变短了，它只回答“程序有哪些命令”：

- **`serve`**：加载配置，调用 `bootstrap.Serve`。
- **`config`**：打印加载后的完整配置。配置不符合预期时用它检查：`go run ./cmd/api config`。
- **`openapi`、`routes`、`client`**：导出接口文档、列出全部路由、生成客户端，`tenon` 在开发时调用它们。

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

<a id="summary"></a>
## 小结

这一章没有添加新的功能，但打下了以后各章的基础：

- 数据库等服务由 Docker Compose 启动；
- 配置从 `config/app.yaml` 和环境变量中加载，写错时启动就会报错；
- 资源在 `open` 中打开，程序退出时按相反的顺序关闭；
- HTTP 服务能平滑地停止，部署平台能通过 `/readyz` 检查它是否就绪。

下一章，我们创建第一张数据表。

深入了解：[配置](../general/configuration.md)、[依赖装配](../concepts/dependencies.md)、[健康检查](../http/lifecycle.md#health-checks)。
