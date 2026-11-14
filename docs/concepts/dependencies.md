# 依赖装配

应用中的每一部分都依赖别的东西：业务动作需要数据库，发送通知需要邮件服务，限流需要 Redis。依赖装配说的就是这件事：这些依赖由谁创建、怎样交到需要它们的代码手里、最后由谁关闭。

Tenon 的做法很简单：业务代码通过**构造函数的参数**声明它需要什么，应用的**入口**按顺序创建这些依赖并传进去。没有服务容器，没有全局注册表，也没有基于反射的自动注入。

- [为什么这样做](#why)
- [构造函数注入](#constructor-injection)
- [在入口中组装应用](#assembling-the-application)
    - [多个运行部分](#running-parts)
    - [只在执行时装配的命令](#deferred-commands)
- [关闭资源](#closing-resources)
- [在测试中替换依赖](#replacing-dependencies-in-tests)
- [API 参考](#api-reference)

<a id="why"></a>
## 为什么这样做

举一个例子会更清楚。假设“获取文章”需要查询数据库，最直接的写法是在用到的地方自己打开连接：

```go
func GetArticle(ctx context.Context, id uuid.UUID) (*Article, error) {
    db, err := database.Open(ctx, loadConfig().Database)
    if err != nil {
        return nil, err
    }
    defer db.Close()
    // 查询文章……
}
```

这样能工作，但每个请求都会打开一个新的连接池，很快就会把数据库的连接用光。于是你把连接池放进一个全局变量，在启动时打开一次：

```go
var DB *bun.DB // 在 main 中赋值

func GetArticle(ctx context.Context, id uuid.UUID) (*Article, error) {
    // 使用 DB 查询文章……
}
```

这也能工作，直到你开始写测试。测试想用一个独立的测试数据库，就得在测试开始前修改全局的 `DB`，而并行运行的测试会互相覆盖它。再往后，只是打印帮助的命令，也会因为某个包在初始化时用到了 `DB` 而去连接数据库；读代码的人也很难回答“这个函数到底用到了哪些资源”。

构造函数注入把这些问题一次解决了。依赖写在构造函数的参数里，谁需要什么一目了然；入口在启动时创建一次连接池，传给所有需要它的代码；测试只要传入另一个连接池即可：

```go
type Get struct {
    db *bun.DB
}

func NewGet(db *bun.DB) *Get {
    return &Get{db: db}
}
```

此外，缺少依赖时代码根本无法编译；资源由谁打开、由谁关闭，看入口的代码就清楚了。

<a id="constructor-injection"></a>
## 构造函数注入

[业务动作](actions.md)在构造函数中接收依赖，并保存在结构体的字段中。它的方法使用这些字段：

```go
func (a *Get) Handle(ctx context.Context, id uuid.UUID) (*Article, error) {
    item := new(Article)
    err := a.db.NewSelect().Model(item).Where("id = ?", id).Scan(ctx)
    // ...
}
```

注意这里接收的是 Bun 原生的 `*bun.DB`。在 Tenon 中，你使用的始终是社区组件的原生对象，学到的用法在别处同样适用。

方法的第一个参数是 `ctx`。它携带请求的取消信号和截止时间，应当一路传给数据库查询等下游调用，这样请求超时或客户端断开时，查询也会随之停止。

> [!IMPORTANT]
> 构造函数只**保存**依赖，执行时才**使用**它们。生成 OpenAPI 文档、列出路由和声明命令时，业务动作会被构造，但传入的依赖都是零值，见[多个运行部分](#running-parts)和[只在执行时装配的命令](#deferred-commands)。

<a id="assembling-the-application"></a>
## 在入口中组装应用

入口负责创建所有的依赖。顺序很自然：先创建被依赖的对象，再创建依赖它们的对象。下面是一个只有一个接口的完整程序，你可以直接运行它：

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
    root := command.Root("api", "示例")
    root.AddCommand(command.Run("serve", "启动 HTTP 服务", serve))
    command.Execute(root)
}
```

我们按顺序看一下这段代码做了什么：

1. `command.Run` 加载配置文件，在收到停止信号时取消 `ctx`；
2. `serve` 打开数据库，并**立即**把关闭操作登记到 `stack`，`serve` 返回时它会被执行；
3. 创建 Router 和 API，构造业务动作 `Count`，把它的 `HTTP` 方法注册为处理器；
4. `httpx.ListenAndServe` 开始处理请求，直到收到停止信号。

`httpx.ListenAndServe` 会等正在处理的请求全部完成之后才返回，所以数据库总是在最后一个请求结束之后才关闭。

每个路由都要声明访问策略，这里用 `rest.Public()` 声明为公开接口。需要登录的接口见[认证](../security/authentication.md)。

<a id="running-parts"></a>
### 多个运行部分

应用变大以后，入口通常放在 `internal/bootstrap` 包中，并分成两部分：`App` 保存各部分共用的资源，由 `open` 打开；HTTP 服务、[队列 worker](../libraries/queue.md#running-workers)、[定时任务调度器](../libraries/scheduling.md#running-the-scheduler)各是 `App` 的一个方法。[入门教程](../tutorial/02-infrastructure.md#assembling)按这个结构写出了完整的 `app.go` 和 `http.go`：

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
    router := a.Router()
    ln, err := d.Listen("tcp", a.cfg.HTTP.Addr)
    if err != nil {
        return nil, err
    }
    return func(ctx context.Context) error {
        return httpx.Serve(ctx, a.cfg.HTTP, ln, router)
    }, nil
}

// Router 构造应用的全部路由：中间件、JSON API 和页面。它只保存 App 的字段、不使用它们。
func (a *App) Router() chi.Router {
    router := chi.NewRouter()
    // ... 中间件与路由
    return router
}

// DocRouter 用默认配置和没有打开任何资源的 App 构造路由，openapi、routes、client 命令用它。
func DocRouter() chi.Routes {
    return (&App{cfg: config.Default[Config]()}).Router()
}
```

`Router` 有两个调用者：`HTTP` 用打开了资源的 `App` 运行服务；`DocRouter` 用零值的 `App` 构造同一套路由，交给 [`httpcmd.Commands`](../general/commands.md#declaring-the-cli) 生成接口文档和客户端、列出路由。所以导出的文档总是与运行的服务一致，生成文档只需要源代码。相应地，`Router` 中只保存依赖，处理请求时才使用它们：例如健康检查的 `Ping` 要写成闭包，见[健康检查](lifecycle.md#health-checks)。

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

你会注意到，每个部分都分两步：先装配好它需要的一切，包括取得监听端口；再返回运行它的函数。这样做的好处是，路由冲突、端口被占用这类错误在开始运行之前就会返回，[不停机升级](../installation/deployment.md#zero-downtime-upgrades)时，旧版本继续服务，直到新版本装配成功。

`d.Run` 让各部分共用同一个 `ctx`，其中一个出错时，其余的会一起停止。

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

`--help` 和参数错误在打开数据库之前就返回。写法和注意事项见[命令行](../general/commands.md#custom-commands)。

<a id="closing-resources"></a>
## 关闭资源

只有一两个资源时，用 `defer` 关闭就足够了。资源多了以后，事情会变得复杂：后打开的资源应当先关闭；启动到一半失败时，已经打开的资源也要释放；关闭时出现的错误要报告出来。`cleanup.Stack` 就是为此设计的。

每成功打开一个资源，就登记一个关闭它的函数：

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

`CloseWith` 会按**登记的逆序**执行这些函数，每个只执行一次，并把运行时的错误和所有关闭时的错误合并返回，`errors.Is` 可以匹配其中任何一个。

关闭时使用的 context 保留传入 context 的值和截止时间，但**不继承它的取消信号**。这一点很重要：应用正是因为收到停止信号才退出的，如果关闭操作也随之取消，资源就来不及正常关闭了。传入的 context 没有截止时间时，关闭总共最多用 10 秒。

> [!NOTE]
> `cleanup.Stack` 由入口串行使用：在构建阶段登记，在退出阶段关闭，始终在同一个 goroutine 中操作。

<a id="replacing-dependencies-in-tests"></a>
## 在测试中替换依赖

在 Tenon 中，大多数测试直接使用真实的依赖，例如用 `dbtest` 为每个测试创建一个真实的数据库，见[测试](../testing/database.md#database-tests)。这比模拟数据库更可靠，也更简单。

当你确实需要替换某个依赖时（例如不想在测试中真的上传到对象存储），遵循 Go 的惯例：**由使用方定义接口**。在业务包中声明一个只包含所需方法的小接口，构造函数接收这个接口：

```go
type ImageStore interface {
    Put(ctx context.Context, key string, data []byte) error
}

func NewSetCover(db *bun.DB, images ImageStore) *SetCover {
    return &SetCover{db: db, images: images}
}
```

测试中传入一个简单的内存实现即可：

```go
type memoryImages map[string][]byte

func (m memoryImages) Put(_ context.Context, key string, data []byte) error {
    m[key] = data
    return nil
}

setCover := article.NewSetCover(db, memoryImages{})
```

接口在真正需要替换实现时再引入，代码会简单得多。

<a id="api-reference"></a>
## API 参考

### cleanup.Stack.MustAdd

```go
func (s *Stack) MustAdd(name string, fn func(context.Context) error)
```

- **参数**
    - `name`：资源的名称，出现在错误信息中
    - `fn`：关闭资源的函数

包 `github.com/go-tenon/tenon/cleanup`，`Stack` 的零值可以直接使用。登记一个关闭函数；`Stack` 已经关闭时 panic。`Add` 与它相同，只是以返回错误代替 panic。

### cleanup.Stack.CloseWith

```go
func (s *Stack) CloseWith(ctx context.Context, runErr error) error
```

- **参数**
    - `ctx`：保留它的值和截止时间，不继承它的取消信号
    - `runErr`：运行时的错误，与关闭时的错误合并
- **返回**：合并后的错误，没有错误时为 `nil`

按登记的逆序执行全部关闭函数。适合在 `defer` 中使用；只关闭资源时用 `Close(ctx)`。

### command.Daemon

```go
func NewDaemon(logger *slog.Logger) (*Daemon, error)
func (d *Daemon) Listen(network, addr string) (net.Listener, error)
func (d *Daemon) Run(ctx context.Context, runs ...func(context.Context) error) error
func (d *Daemon) Stop()
```

包 `github.com/go-tenon/tenon/command`。常驻进程：`Listen` 取得监听端口，`Run` 报告就绪后同时运行 `runs`，直到停止。

在 systemd 的 `Type=notify-reload` 服务中运行时，`Run` 向 systemd 报告就绪，收到 `SIGHUP` 时不停机升级，见[部署](../installation/deployment.md#zero-downtime-upgrades)；不在其中时，`Run` 等同于 `command.Concurrently`。

> [!IMPORTANT]
> 一个进程只创建一个 `Daemon`。所有的 `Listen` 都要在 `Run` 之前调用，进程退出前调用 `Stop`。

### command.Concurrently

```go
func Concurrently(ctx context.Context, runs ...func(context.Context) error) error
```

- **参数**
    - `runs`：同时运行的函数，它们收到同一个派生的 `ctx`
- **返回**：合并后的错误

`ctx` 被取消或者任何一个函数返回时，派生的 `ctx` 随之取消，其余的函数据此停止。在停止之前就返回 `nil` 的函数视为异常退出，返回值包含 `command.ErrStoppedEarly`。

### command.Bind

```go
func Bind[C, D any](
    open func(ctx context.Context, cfg C, stack *cleanup.Stack) (*D, error),
    declare func(*D) []*cobra.Command,
) []*cobra.Command
```

- **参数**
    - `open`：执行命令时装配依赖的函数，与 `serve` 用的是同一个
    - `declare`：声明命令的函数；构造命令行时收到零值的 `*D`
- **返回**：声明的命令

依赖推迟到执行时才装配，见[只在执行时装配的命令](#deferred-commands)。
