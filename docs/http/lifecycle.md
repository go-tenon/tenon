# HTTP 服务的生命周期

这一页介绍 HTTP 服务这个部分：一个请求从进入到返回经过哪些环节，`httpx.Serve` 停止时怎样做到不中断正在处理的请求，以及怎样提供部署平台需要的健康检查。应用整体怎样启动、由哪些部分组成、各部分怎样一起停止，见[应用生命周期](../concepts/lifecycle.md)。

遇到这些问题时，这一页能帮你找到原因：请求为什么返回了 401 而不是 422？停机时为什么有请求被中断？部署平台为什么认为实例没有就绪？先建立一个整体的印象即可，每一部分的细节都在链接的页面中。

阅读前需要了解：[应用生命周期](../concepts/lifecycle.md)；[安装](../installation/installing.md)中的三步程序。

- [启动](#startup)
- [请求的处理过程](#request-flow)
- [平滑停止](#graceful-shutdown)
- [健康检查](#health-checks)
    - [没有 HTTP 服务的进程](#probe-listener)
- [常见问题](#troubleshooting)
- [API 参考](#api-reference)

<a id="startup"></a>
## 启动

应用读取配置、打开数据库等资源之后（见[启动](../concepts/lifecycle.md#startup)），HTTP 服务这个部分按下面的顺序开始工作：

1. 创建 API，构造[业务动作](../concepts/actions.md)并注册路由；
2. 取得监听端口；
3. 调用 `httpx.Serve` 开始处理请求。

这些都是普通的 Go 代码，没有隐藏的步骤，写法见[依赖装配](../concepts/dependencies.md)。

<a id="request-flow"></a>
## 请求的处理过程

一个请求进入程序后，先经过一层层的**中间件**，再到达你的处理函数。可以把中间件想象成套在处理函数外面的一层层包装：请求从外向内穿过每一层，响应再从内向外返回。任何一层都可以直接返回响应，请求就不会再往里走。

安装了 `httpx.Defaults` 和 `httpx.Timeout` 之后，一个请求会依次经过这些环节：

```text
确定请求 ID，写入安全响应头
  → 解析客户端地址（只信任配置的代理）
  → 按 Accept-Language 协商语言
  → 开始记录访问日志
  → panic 恢复
  → 限制请求体大小，设置请求超时
  → 你安装的其他中间件（例如限流）
  → 路由匹配
  → Bearer 认证（只在需要认证的路由上）
  → Huma 解析并验证输入
  → 处理器调用业务动作
  → 错误转换为统一的错误响应
```

其中几个环节的含义：

- **请求 ID**：给每个请求一个唯一的编号，写在响应头 `X-Request-ID` 和这个请求的每一行日志中，排查问题时按它查找。
- **按 Accept-Language 协商语言**：浏览器在 `Accept-Language` 请求头中告诉服务端用户偏好的语言，错误提示按它翻译。
- **panic 恢复**：处理函数中发生 panic 时，返回 `500`，而不是让整个程序崩溃。
- **Bearer 认证**：检查请求头 `Authorization: Bearer <令牌>` 中的令牌，确认请求来自谁。

顺序很重要。以修改文章的请求 `PUT /articles/{id}` 为例，看看不同的请求会在哪一步结束：

- 没有有效令牌的请求在认证这一步返回 `401`；
- 标题为空的请求在验证输入这一步返回 `422`，到不了处理器；
- 处理器返回的业务错误（例如 slug 已被占用）在最后一步转换为 `409` 等响应。

所以一个没有登录、标题又为空的请求，得到的是 `401` 而不是 `422`：认证在验证输入之前。

前半段是安装在 Router 上的[中间件](middleware.md)，作用于所有请求；认证由路由声明的策略决定，见[认证](../security/authentication.md)；输入见[请求](requests.md)和[验证](validation.md)；错误的转换见[错误处理](../general/errors.md)。

值得一提的是，中间件、语言目录和日志器都是在创建 API 时显式传入的。所以同一个进程中的两个 API 可以有各自独立的配置。

<a id="graceful-shutdown"></a>
## 平滑停止

应用收到 `SIGTERM` 或 Ctrl+C 时，各个部分同时开始[平滑停止](../concepts/lifecycle.md#graceful-shutdown)。对 HTTP 服务来说，就是**先处理完**正在处理的请求。`httpx.Serve` 负责这件事：

```go
var draining httpx.Draining

err := httpx.Serve(ctx, cfg.HTTP, ln, handler, httpx.OnDraining(draining.Set))
```

`draining` 是一个标志，表示“正在停止”。当 `ctx` 被取消时（收到停止信号，或者[不停机升级](../installation/operations.md#zero-downtime-upgrades)时交给了新进程），`httpx.Serve` 按以下顺序停止：

1. 调用 `OnDraining`，让[就绪检查](#health-checks)开始返回失败；
2. 停止接收新连接，等待正在处理的请求完成，这个过程叫**排空**；
3. 超过 `shutdown_timeout`（默认 10 秒）仍未完成的请求，取消它们的 context 并强制关闭连接。

`httpx.Serve` 返回之后，入口才关闭数据库等资源，正在处理的请求在结束前始终可以访问数据库。

`ln` 通常由 `command.Daemon` 取得，见[依赖装配](../concepts/dependencies.md#assembling-the-application)。简单的程序用 `httpx.ListenAndServe` 自己监听 `cfg.Addr`。

> [!WARNING]
> 平滑停止依赖 context 的传递。数据库查询和外部调用都要使用请求的 `ctx`，超时后它们才能随之停下，见[平滑停止](../concepts/lifecycle.md#graceful-shutdown)。

<a id="health-checks"></a>
## 健康检查

部署平台用存活检查和就绪检查判断实例的状态，两者的区别见[健康检查](../concepts/lifecycle.md#health-checks)。HTTP 服务的就绪检查还要看是否正在排空：开始排空之后返回失败，平台就不再向这个实例转发新的请求。

路径由部署环境决定，所以这两个路由由应用自己注册。下面把它们注册在 `/livez` 和 `/readyz`：

```go
router.Get("/livez", func(w http.ResponseWriter, _ *http.Request) {
    w.WriteHeader(http.StatusNoContent)
})
router.Handle("/readyz", httpx.Ready(httpx.ReadyOptions{
    Draining: &draining,
    Checks: []health.Check{
        {Name: "database", Ping: func(ctx context.Context) error { return db.PingContext(ctx) }},
        {Name: "redis", Ping: func(ctx context.Context) error {
            return rdb.Do(ctx, rdb.B().Ping().Build()).Error()
        }},
        {Name: "cache", Ping: func(context.Context) error { return mc.Ping() }, Optional: true},
    },
}))
```

`draining` 与传给 `httpx.OnDraining` 的是同一个。每个 `Ping` 都写成闭包，到检查时才使用依赖：`openapi`、`routes` 命令用 `nil` 依赖构造同一套路由（见[依赖装配](../concepts/dependencies.md#assembling-the-application)），`db.PingContext` 这样的方法值在 `db` 为 `nil` 时取值就会 panic。检查项 `health.Check` 和执行检查的 `health.Checker` 在与传输方式无关的 `health` 包中，`httpx.Ready` 只负责排空的判断和响应格式。全部检查并行执行，每项最多 1 秒，超时按失败处理。必需的检查都通过时返回 `200`，否则返回 `503`；正在排空时直接返回 `503`。响应体列出每项的结果：

```json
{"status":"ready","checks":{"cache":"failed","database":"ok","redis":"ok"}}
```

失败的原因只记录在日志中，而且只在状态变化时记录：从通过变为失败记 `Warn`，恢复时记 `Info`。

> [!IMPORTANT]
> 只把处理请求**必需**的依赖放进检查。缓存这类坏了也能降级运行的依赖标为 `Optional`，这样缓存的短暂故障只体现在结果中，实例继续接收流量。只有部分接口用到的服务（PDF、向量检索等）的故障由各自的接口返回错误。

> [!TIP]
> 探测很频繁，在[访问日志](middleware.md#http-configuration)的 `ignore_paths` 中排除这两个路径。

<a id="probe-listener"></a>
### 没有 HTTP 服务的进程

分开部署时（见[一个进程，还是三个进程](../installation/deployment.md#process-forms)），只运行 `work` 或 `schedule` 的进程没有 HTTP 服务，也就没有 `/readyz`。在 Kubernetes 这类平台上，它们同样需要健康检查：

- **存活检查**让平台发现卡死的进程并重启它。
- **就绪检查**决定滚动升级能否继续：新版本的实例连不上数据库时一直不就绪，平台暂停升级，旧版本的实例继续处理任务。worker 不接收请求，所以就绪检查只影响升级，不影响分流。

给这类进程加一个运行部分 `Probes`，在单独的地址上只提供这两个路径。先把检查项提成 `App` 的方法，HTTP 服务的 `/readyz` 改为 `Checks: a.checks()`，两边共用：

```go
// checks 是就绪检查的依赖，HTTP 服务的 /readyz 和 Probes 共用。
func (a *App) checks() []health.Check {
    return []health.Check{
        {Name: "database", Ping: func(ctx context.Context) error { return a.db.PingContext(ctx) }},
    }
}

// Probes 在 cfg.ProbeAddr 上只提供存活检查和就绪检查，给没有 HTTP 服务的进程使用。
// ProbeAddr 为空时不监听，返回的函数一直等到停止。
func (a *App) Probes(d *command.Daemon) (func(context.Context) error, error) {
    if a.cfg.ProbeAddr == "" {
        return func(ctx context.Context) error { <-ctx.Done(); return nil }, nil
    }
    mux := http.NewServeMux()
    mux.HandleFunc("GET /livez", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
    mux.Handle("GET /readyz", httpx.Ready(httpx.ReadyOptions{Checks: a.checks()}))
    ln, err := d.Listen("tcp", a.cfg.ProbeAddr)
    if err != nil {
        return nil, err
    }
    return func(ctx context.Context) error {
        return httpx.Serve(ctx, a.cfg.HTTP, ln, mux)
    }, nil
}

func Work(ctx context.Context, cfg Config) error {
    return run(ctx, cfg, (*App).Worker, (*App).Probes)
}

func Schedule(ctx context.Context, cfg Config) error {
    return run(ctx, cfg, (*App).Scheduler, (*App).Probes)
}
```

`Config` 中加上 ``ProbeAddr string `yaml:"probe_addr"` ``，配置文件中由环境变量决定，默认不监听：

```yaml
probe_addr: ${PROBE_ADDR:-}
```

- **只在需要的平台上打开。** 容器中每个进程有自己的网络，设置 `PROBE_ADDR=:8080`，探测的端口和路径与 HTTP 服务相同，部署配置可以照搬。用 systemd 在同一台机器上运行 `work` 和 `schedule` 时不设置它：两个进程会争用同一个端口，而 systemd 通过 [`command.Daemon`](../concepts/dependencies.md#running-parts) 的就绪通知和退出状态管理进程，用不到 HTTP 探测。
- **`serve` 和 `start` 不需要它**，HTTP 服务自己就有这两个路由。
- **检查项按进程的需要选择。** worker 和调度器都需要数据库；HTTP 服务独有的依赖不必放进来。
- `httpx.Serve` 沿用 `http` 段的超时设置，停止时探测端口和其他部分同时开始关闭。

<a id="troubleshooting"></a>
## 常见问题

**停机时日志中出现 `context canceled`，有请求失败**

这些请求在 `shutdown_timeout` 内没有完成，被取消了。检查是否有很慢的请求；需要时调大 `http.shutdown_timeout`，并让部署平台等待的时间比它更长，见[平滑停止](../installation/deployment.md#graceful-shutdown)。

**`/readyz` 一直返回 `503`**

看响应体中哪一项是 `failed`，再在日志中找到失败的原因。缓存这类可以降级的依赖，标为 `Optional`。

**`openapi` 或 `routes` 命令 panic：`nil pointer dereference`**

就绪检查中直接写了 `db.PingContext` 这样的方法值。这些命令用 `nil` 依赖构造路由，改成闭包 `func(ctx context.Context) error { return db.PingContext(ctx) }`，见上文的[健康检查](#health-checks)。

<a id="api-reference"></a>
## API 参考

包 `github.com/go-tenon/tenon/httpx`。

### Serve

```go
func Serve(ctx context.Context, cfg Config, ln net.Listener, handler http.Handler, opts ...ServeOption) error
```

- **参数**
    - `ctx`：取消时开始平滑停止
    - `cfg`：服务器的超时等参数；不使用其中的 `Addr`，但 `cfg` 仍需合法
    - `ln`：监听端口，通常由 `command.Daemon` 取得
    - `handler`：处理请求的 Router
    - `opts`：见下面的 `Logger`、`OnDraining`
- **返回**：监听出错时的错误；平滑停止后返回 `nil`

在 `ln` 上运行 `handler`，直到 `ctx` 被取消或监听出错，然后按[平滑停止](#graceful-shutdown)中的顺序停止。返回时 `ln` 已经关闭。

### ListenAndServe

```go
func ListenAndServe(ctx context.Context, cfg Config, handler http.Handler, opts ...ServeOption) error
```

与 `Serve` 相同，只是自己监听 `cfg.Addr`。适合简单的程序。

### Logger、OnDraining

```go
func Logger(l *slog.Logger) LoggerOption
func OnDraining(fn func()) ServeOption
```

`Serve` 和 `ListenAndServe` 的选项。`Logger` 让排空开始和服务器停止记录到 `l`，不传时使用 `slog.Default()`；它也可以传给 [`Defaults`](middleware.md#default-middleware)。`OnDraining` 在开始排空之前调用 `fn`，通常是 `Draining.Set`。

### Draining

```go
func (d *Draining) Set()
func (d *Draining) Load() bool
```

排空标志，零值即可使用。把 `Set` 传给 `OnDraining`，把同一个值的地址传给 `ReadyOptions.Draining`。

### Ready

```go
func Ready(opts ReadyOptions) http.Handler
```

- **参数**
    - `opts`：见下面的 `ReadyOptions`
- **返回**：就绪检查的处理器

检查的名称为空、重复或者 `Ping` 为 `nil` 时 panic。

### ReadyOptions

```go
type ReadyOptions struct {
    Draining *Draining
    Checks   []health.Check
    Timeout  time.Duration
    Logger   *slog.Logger
}
```

`Draining` 为 `nil` 时跳过排空检查。`Timeout` 是每项检查的超时，默认 1 秒。`Logger` 记录检查结果的变化。

### health.Check、health.Checker

包 `github.com/go-tenon/tenon/health`。

```go
type Check struct {
    Name     string
    Ping     func(ctx context.Context) error
    Optional bool
}

func New(opts Options) *Checker
func (c *Checker) Run(ctx context.Context) Report
```

`Name` 出现在结果和日志中；`Ping` 返回错误表示不可用；`Optional` 为 `true` 时，失败只在结果中报告，不让就绪检查失败。

`Checker` 是 `httpx.Ready` 内部使用的检查器，不依赖 HTTP。`Run` 并行执行全部检查，返回的 `Report` 中 `Ready` 报告必需的检查是否都通过，`Results` 列出每一项的结果和失败原因；日志只在结果变化时记录。HTTP 之外的入口需要报告健康状态时（例如 gRPC 的健康检查服务），直接使用它：

```go
checker := health.New(health.Options{Checks: checks})
report := checker.Run(ctx)
if !report.Ready {
    // 按入口的协议报告“未就绪”
}
```
