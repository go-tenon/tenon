# 依赖装配

业务代码通过构造函数的参数声明它需要的依赖；应用的入口（`internal/bootstrap`）创建这些依赖、传进去，在退出时关闭它们。创建和传递都是普通的 Go 代码。

包：`cleanup`、`command`。

- 依赖都通过参数显式传递。包级变量只用于只读的值，例如错误定义和正则表达式。
- 业务代码接收社区组件的原生类型，例如 `*bun.DB`、`*slog.Logger`、`*nats.Conn`。

<a id="constructor-injection"></a>
## 构造函数注入

[业务动作](actions.md)在构造函数中接收依赖，保存在字段中，方法执行时使用：

```go
type Get struct {
    db *bun.DB
}

func NewGet(db *bun.DB) *Get {
    return &Get{db: db}
}

func (a *Get) Handle(ctx context.Context, id uuid.UUID) (*Article, error) {
    item := new(Article)
    err := a.db.NewSelect().Model(item).Where("id = ?", id).Scan(ctx)
    // ...
}
```

- 方法的第一个参数是 `ctx`，一路传给数据库查询和外部调用：请求超时、客户端断开或进程停止时，它们随之停下。
- 构造函数只保存依赖，执行时才使用它们。生成 OpenAPI 文档、列出路由和声明命令时，动作用零值的依赖构造，构造时使用依赖会 panic（`nil pointer dereference`）。
- 一个动作需要另一个模块的能力时，在自己的包中声明只包含所需方法的小接口，构造函数接收这个接口，见[在测试中替换依赖](#replacing-dependencies-in-tests)。

<a id="assembling-the-application"></a>
## 在入口中组装应用

入口先创建被依赖的对象，再创建依赖它们的对象。下面是只有一个接口的完整程序：

```go
package main

import (
    "context"
    "net/http"

    "github.com/danielgtaylor/huma/v2"
    "github.com/go-chi/chi/v5"
    "github.com/uptrace/bun"

    "github.com/go-tenon/tenon/cleanup"
    "github.com/go-tenon/tenon/command"
    "github.com/go-tenon/tenon/database"
    "github.com/go-tenon/tenon/httpx"
    "github.com/go-tenon/tenon/httpx/rest"
)

type Config struct {
    HTTP     httpx.Config    `yaml:"http"`
    Database database.Config `yaml:"database"`
}

// Count 是一个业务动作：统计文章数。
type Count struct{ db *bun.DB }

func NewCount(db *bun.DB) *Count { return &Count{db: db} }

type CountOutput struct {
    Body struct {
        Total int64 `json:"total"`
    }
}

func (a *Count) HTTP(ctx context.Context, _ *struct{}) (*CountOutput, error) {
    n, err := a.db.NewSelect().Table("articles").Count(ctx)
    if err != nil {
        return nil, err
    }
    out := &CountOutput{}
    out.Body.Total = int64(n)
    return out, nil
}

func serve(ctx context.Context, cfg Config) (err error) {
    var stack cleanup.Stack
    defer func() { err = stack.CloseWith(context.Background(), err) }()

    db, err := database.Open(ctx, cfg.Database)
    if err != nil {
        return err
    }
    stack.MustAdd("database", func(context.Context) error { return db.Close() })

    router := chi.NewRouter()
    router.Use(httpx.Defaults(cfg.HTTP)...)
    api := rest.New(router, rest.Options{Title: "Blog API", Version: "1.0.0"})
    count := huma.Operation{
        OperationID: "article-count",
        Method:      http.MethodGet,
        Path:        "/articles/count",
    }
    rest.Public()(&count)
    rest.Register(api, count, NewCount(db).HTTP)

    return httpx.ListenAndServe(ctx, cfg.HTTP, router)
}

func main() {
    root := command.Root("blog", "示例")
    root.AddCommand(command.Run("serve", "启动 HTTP 服务", serve))
    command.Execute(root)
}
```

- `command.Run` 加载配置，进程收到 `SIGINT`、`SIGTERM` 时取消 `ctx`。
- 每打开一个资源，紧接着把关闭它的函数登记到 `stack`，见[关闭资源](#closing-resources)。
- `httpx.ListenAndServe` 等正在处理的请求全部完成才返回，数据库在最后一个请求结束之后才关闭。
- 每个路由都要声明访问策略，这里用 `rest.Public()` 声明为公开接口，见[认证](../security/authentication.md)。

有了几个常驻的运行部分以后，打开资源、运行各部分、关闭资源由 `command.Serve` 完成，见[多个运行部分](#running-parts)。

<a id="running-parts"></a>
## `bootstrap` 的 `Open` 与 `Parts`：多个运行部分

`internal/bootstrap` 中，`App` 保存各部分共用的资源，由 `Open` 打开；HTTP 服务、[RPC 服务](../rpc/endpoints.md#running)、[队列 worker](../background/queue.md#running-workers)、[定时任务调度器](../background/scheduling.md#running-the-scheduler)各是 `App` 的一个方法，在 `Parts` 中登记。完整的 `app.go`、`http.go` 见[入门教程](../tutorial/02-infrastructure.md#assembling)：

```go
// Open 打开各部分共用的资源，把关闭操作登记到 stack。
func Open(ctx context.Context, cfg Config, stack *cleanup.Stack) (*App, error) {
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
        {Name: "worker", Short: "处理队列任务", Start: a.Worker},
    }
}

// HTTP 装配 HTTP 服务并取得监听端口，返回运行它的函数。
func (a *App) HTTP(d *command.Daemon) (func(context.Context) error, error) {
    router := a.Router()
    ln, err := d.Listen("tcp", a.cfg.HTTP.Addr)
    if err != nil {
        return nil, err
    }
    return func(ctx context.Context) error {
        return httpx.Serve(ctx, a.cfg.HTTP, ln, router)
    }, nil
}

// DocRouter 用默认配置和没有打开任何资源的 App 构造路由，openapi、routes、client 命令用它。
func DocRouter() chi.Routes {
    return (&App{cfg: config.Default[Config]()}).Router()
}
```

`main.go` 用 `command.Serve(bootstrap.Open, bootstrap.Parts)` 创建 `serve` 命令：调用 `Open`，依次调用各部分的 `Start` 装配，全部装配成功后同时运行，收到停止信号后按逆序关闭资源，见[serve 命令](../general/commands.md#serve-command)。

- `Start` 先装配好一切（包括取得监听端口），再返回运行的函数。路由冲突、端口被占用在开始运行前返回，[不停机升级](../installation/operations.md#zero-downtime-upgrades)时旧版本继续服务。
- `Router` 只保存 `App` 的字段：`DocRouter` 用零值的 `App` 调用它，交给 [`httpcmd.Commands`](../general/commands.md#declaring-the-cli) 离线生成文档和客户端。健康检查的 `Ping` 写成闭包，见[健康检查](lifecycle.md#health-checks)。
- 带钩子或同时挂在多个入口（HTTP、worker、命令、MCP、RPC）的业务动作，在 `App` 上用工厂方法统一构造（例如 `a.publishArticle()`），各入口共享同一个装配点，避免漏传钩子，见[在 bootstrap 中连接钩子](actions.md#wiring-hooks)。
- `serve --help` 用零值的 `App` 调用 `Parts`，所以 `Parts` 中只取方法值。
- 多个部分都用到的资源（数据库、NATS、缓存）在 `Open` 中打开；只有一个部分用到的（例如 HTTP 中间件）在它自己的方法中创建。

<a id="deferred-commands"></a>
## `command.Bind`：只在执行时装配的命令

构造命令行时还没有加载配置。`command.Bind` 先用零值的 `App` 声明命令；执行其中一个命令时调用 `Open`，用装配好的 `App` 重新声明并执行，命令返回后关闭资源。`bootstrap/commands.go`：

```go
func Commands() []*cobra.Command {
    return command.Bind(Open, func(a *App) []*cobra.Command {
        return []*cobra.Command{
            article.Commands(a.db),
        }
    })
}
```

`--help` 和参数错误在打开数据库之前返回。写法见[自定义命令](../general/commands.md#custom-commands)。

<a id="closing-resources"></a>
## `cleanup.Stack`：关闭资源

每成功打开一个资源，紧接着登记关闭它的函数。`cfg` 是应用的配置：

```go
var stack cleanup.Stack
defer func() { err = stack.CloseWith(context.Background(), err) }()

nc, err := natsx.Connect(cfg.NATS, "blog")
if err != nil {
    return err
}
stack.MustAdd("nats", func(context.Context) error {
    nc.Close()
    return nil
})
```

- `CloseWith` 按登记的逆序执行关闭函数，每个只执行一次，启动到一半失败时已打开的资源同样关闭。
- 返回值合并了运行时的错误和所有关闭时的错误，`errors.Is` 能匹配其中任何一个；日志中的第一个错误是失败的原因。
- 关闭用的 context 保留传入 context 的值和截止时间，不继承取消：进程正是因为停止信号才退出，关闭操作要能完成。没有截止时间时最多用 10 秒。
- `Stack` 由入口在同一个 goroutine 中串行使用。`command.Serve` 和 `command.Bind` 把 `stack` 传给 `Open`，`Open` 只登记、不关闭。

<a id="replacing-dependencies-in-tests"></a>
## 在测试中替换依赖

测试优先使用真实的依赖，例如用 `dbtest` 为每个测试创建真实的数据库，见[测试](../testing/testing.md#database-tests)。需要替换某个依赖时（例如不真的上传到对象存储），在业务包中声明只包含所需方法的小接口，构造函数接收它：

```go
type ImageStore interface {
    Put(ctx context.Context, key string, data []byte) error
}

func NewSetCover(db *bun.DB, images ImageStore) *SetCover {
    return &SetCover{db: db, images: images}
}
```

测试中传入内存实现，`db` 是 `dbtest.Open` 创建的测试库：

```go
type memoryImages map[string][]byte

func (m memoryImages) Put(_ context.Context, key string, data []byte) error {
    m[key] = data
    return nil
}

setCover := article.NewSetCover(db, memoryImages{})
```

接口在真正需要替换实现时再引入。
