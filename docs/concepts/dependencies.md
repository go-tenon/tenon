# 依赖装配

Tenon 没有服务容器、全局注册表，也没有基于反射的自动注入。业务代码通过构造函数参数声明它需要什么，入口按顺序创建这些依赖并传进去。读入口的代码就能看清应用由哪些部分组成，缺少依赖时编译不通过，资源由谁打开、由谁关闭也一目了然。

- [构造函数注入](#constructor-injection)
- [在入口中组装应用](#assembling-the-application)
    - [多个运行部分](#running-parts)
    - [只在执行时装配的命令](#deferred-commands)
- [关闭资源](#closing-resources)
- [在测试中替换依赖](#replacing-dependencies-in-tests)
- [API 参考](#api-reference)

<a id="constructor-injection"></a>
## 构造函数注入

[业务动作](actions.md)在构造函数中接收依赖，保存在字段中：

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

依赖是社区组件的原生类型，这里是 Bun 的 `*bun.DB`，而不是框架包装过的类型。方法的第一个参数是 `ctx`，把请求的取消信号和截止时间传给数据库查询等下游调用。

> [!IMPORTANT]
> 构造函数只保存依赖，不使用它们。生成 OpenAPI 文档、列出路由和声明命令时，动作会用零值依赖构造，见[只在执行时装配的命令](#deferred-commands)。

<a id="assembling-the-application"></a>
## 在入口中组装应用

入口先创建被依赖的对象，再创建依赖它们的对象。下面是一个只有一个接口的完整程序：

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
    router.Use(httpx.Defaults(cfg.HTTP, nil, nil)...)
    api := rest.New(rest.Options{Title: "Blog API", Version: "1.0.0", Router: router})
    count := huma.Operation{
        OperationID: "article-count",
        Method:      http.MethodGet,
        Path:        "/articles/count",
    }
    rest.Public()(&count)
    rest.Register(api, count, NewCount(db).HTTP)

    return httpx.ListenAndServe(ctx, cfg.HTTP, router, httpx.ServeOptions{})
}

func main() {
    root := command.Root("api", "示例")
    root.AddCommand(command.Run("serve", "启动 HTTP 服务", serve))
    command.Execute(root)
}
```

- `command.Run` 加载配置，收到停止信号时取消 `ctx`；
- 数据库打开后立即把关闭操作登记到 `stack`，`serve` 返回时关闭；
- `httpx.ListenAndServe` 在排空请求之后才返回，所以正在处理的请求不会因为数据库先关闭而失败。

每个路由都要声明访问策略，这里用 `rest.Public()` 声明为公开接口，需要登录的接口见[认证](../security/authentication.md)。

<a id="running-parts"></a>
### 多个运行部分

应用变大后，入口通常放在 `internal/bootstrap` 包中，分成两部分：`App` 保存各部分共用的资源，由 `open` 打开；HTTP 服务、[队列 worker](../libraries/queue.md#running-workers)、[定时任务调度器](../libraries/scheduling.md#running-the-scheduler)各是 `App` 的一个方法。[入门教程](../tutorial/02-infrastructure.md#assembling)按这个结构写出了完整的 `app.go` 和 `http.go`：

```go
// open 打开各部分共用的资源，把关闭操作登记到 stack。
func open(ctx context.Context, cfg Config, stack *cleanup.Stack) (*App, error) {
    db, err := database.Open(ctx, cfg.Database)
    if err != nil {
        return nil, err
    }
    stack.MustAdd("database", func(context.Context) error { return db.Close() })
    return &App{cfg: cfg, db: db}, nil
}

// HTTP 装配 HTTP 服务并取得监听端口，返回运行它的函数。
func (a *App) HTTP(d *command.Daemon) (func(context.Context) error, error) {
    router := chi.NewRouter()
    // ... 中间件与路由
    ln, err := d.Listen("tcp", a.cfg.HTTP.Addr)
    if err != nil {
        return nil, err
    }
    return func(ctx context.Context) error {
        return httpx.Serve(ctx, a.cfg.HTTP, ln, router, httpx.ServeOptions{})
    }, nil
}
```

`run` 先装配全部部分，再用 `command.Daemon` 同时运行它们：

```go
func run(ctx context.Context, cfg Config, parts ...func(*App, *command.Daemon) (func(context.Context) error, error)) (err error) {
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
    for i, part := range parts {
        if runs[i], err = part(app, d); err != nil {
            return err
        }
    }
    return d.Run(ctx, runs...)
}

func Serve(ctx context.Context, cfg Config) error { return run(ctx, cfg, (*App).HTTP) }
func Start(ctx context.Context, cfg Config) error {
    return run(ctx, cfg, (*App).HTTP, (*App).Worker, (*App).Scheduler)
}
```

每个部分分两步：先装配好它需要的一切，包括取得监听端口；再返回运行它的函数。装配阶段的错误（路由冲突、端口被占用）在开始运行之前就返回，[不停机升级](../installation/deployment.md#zero-downtime-upgrades)时出错的新版本不会接管服务。`d.Run` 让各部分共用一个 `ctx`，一个出错时其余的一起停止。

> [!TIP]
> 多个部分都用到的资源（数据库、Redis、缓存）放进 `open`；只有一个部分用到的（例如 HTTP 中间件）在它自己的方法里创建。

<a id="deferred-commands"></a>
### 只在执行时装配的命令

构造命令行时还没有加载配置，也没有打开数据库。`command.Bind` 先用零值的 `App` 声明命令，执行其中一个命令时再调用 `open`，用装配好的 `App` 重新声明并执行，命令返回后关闭资源：

```go
func Commands() []*cobra.Command {
    return command.Bind(open, func(a *App) []*cobra.Command {
        return []*cobra.Command{
            article.Commands(a.db),
        }
    })
}
```

`--help` 和参数错误不会打开数据库。写法和注意事项见[命令行](../general/commands.md#custom-commands)。

<a id="closing-resources"></a>
## 关闭资源

只有一两个资源时，`defer` 就够了。资源多了以后，`cleanup.Stack` 保证：

- 按**登记的逆序**关闭，后打开的先关闭；
- 打开到一半失败时，已经打开的也会关闭；
- 每个清理函数只执行一次，关闭时的错误和运行时的错误合并返回，`errors.Is` 能匹配其中任何一个；
- 清理使用的 context 保留传入 context 的值和截止时间，但不继承它的取消信号：收到停止信号后清理仍能进行。没有截止时间时，清理总共最多 10 秒。

```go
var stack cleanup.Stack
defer func() { err = stack.CloseWith(context.Background(), err) }()

rdb, err := rueidis.NewClient(opt)
if err != nil {
    return err
}
stack.MustAdd("redis", func(context.Context) error {
    rdb.Close()
    return nil
})
```

> [!NOTE]
> `cleanup.Stack` 由入口串行使用：构建阶段登记，退出阶段关闭。不要在多个 goroutine 中同时操作它。

<a id="replacing-dependencies-in-tests"></a>
## 在测试中替换依赖

大多数测试直接使用真实的依赖，例如用 `dbtest` 创建一个真实的测试数据库，见[测试](../testing/database.md#database-tests)。

确实需要替换一个依赖时（例如对象存储），由使用方定义一个只包含所需方法的小接口，构造函数接收这个接口：

```go
type ImageStore interface {
    Put(ctx context.Context, key string, data []byte) error
}

func NewSetCover(db *bun.DB, images ImageStore) *SetCover {
    return &SetCover{db: db, images: images}
}
```

测试中传入一个内存实现：

```go
type memoryImages map[string][]byte

func (m memoryImages) Put(_ context.Context, key string, data []byte) error {
    m[key] = data
    return nil
}

setCover := article.NewSetCover(db, memoryImages{})
```

不必为每个依赖预先定义接口，真正需要替换时再引入。

<a id="api-reference"></a>
## API 参考

### cleanup.Stack

包 `github.com/go-tenon/tenon/cleanup`。零值可用。

```go
func (s *Stack) Add(name string, fn func(context.Context) error) error
func (s *Stack) MustAdd(name string, fn func(context.Context) error)
```

登记一个具名的清理函数，名称出现在错误信息中。`Stack` 已经关闭时 `Add` 返回错误，`MustAdd` panic。

```go
func (s *Stack) Close(ctx context.Context) error
func (s *Stack) CloseWith(ctx context.Context, runErr error) error
```

按逆序执行全部清理函数，返回合并的错误。`CloseWith` 把 `runErr` 一起合并，适合在 `defer` 中使用。

### command.Daemon

包 `github.com/go-tenon/tenon/command`。

```go
func NewDaemon(logger *slog.Logger) (*Daemon, error)
func (d *Daemon) Listen(network, addr string) (net.Listener, error)
func (d *Daemon) Run(ctx context.Context, runs ...func(context.Context) error) error
func (d *Daemon) Stop()
```

常驻进程：`Listen` 取得监听端口，`Run` 报告就绪后同时运行 `runs`，直到停止。在 systemd 的 `Type=notify-reload` 服务中运行时，`Run` 向 systemd 报告就绪，收到 `SIGHUP` 时不停机升级，见[部署](../installation/deployment.md#zero-downtime-upgrades)；不在其中时，`Run` 等同于 `command.Concurrently`。一个进程只创建一个 `Daemon`，所有 `Listen` 都在 `Run` 之前调用，退出前调用 `Stop`。

### command.Concurrently

```go
func Concurrently(ctx context.Context, runs ...func(context.Context) error) error
```

同时运行 `runs`，它们收到同一个派生的 `ctx`。`ctx` 被取消或任何一个函数返回时，派生的 `ctx` 随之取消。在停止之前就返回 `nil` 的函数视为异常退出，返回值包含 `command.ErrStoppedEarly`。

### command.Bind

```go
func Bind[C, D any](
    open func(ctx context.Context, cfg C, stack *cleanup.Stack) (*D, error),
    declare func(*D) []*cobra.Command,
) []*cobra.Command
```

返回 `declare` 声明的命令，依赖推迟到执行时装配，见[只在执行时装配的命令](#deferred-commands)。
