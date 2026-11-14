# 请求生命周期

了解一个工具的工作原理，用起来就会更有把握。本页从整体上介绍一个 Tenon 应用怎样启动、一个请求从进入到返回会经过哪些环节，以及应用停止时怎样做到不丢请求。

如果有些概念现在还不太理解，不必担心，先建立一个整体的印象即可。每一部分的细节都在链接的页面中。

- [启动](#startup)
- [请求的处理过程](#request-flow)
- [平滑停止](#graceful-shutdown)
- [健康检查](#health-checks)
- [API 参考](#api-reference)

<a id="startup"></a>
## 启动

应用的入口拥有全部的运行时资源。一个需要数据库的 API 按下面的顺序启动：

1. 读取并校验[配置](../general/configuration.md)；
2. 打开数据库等资源，每打开一个，就登记对应的关闭操作；
3. 创建 API，构造[业务动作](actions.md)并注册路由，取得监听端口；
4. 调用 `httpx.Serve` 开始处理请求。

这些都是普通的 Go 代码，没有隐藏的步骤，写法见[依赖装配](dependencies.md)。

> [!NOTE]
> 迁移不在启动时自动执行，而是部署时的一个独立步骤，见[迁移](../database/migrations.md)。

<a id="request-flow"></a>
## 请求的处理过程

安装了 `httpx.Defaults` 和 `httpx.Timeout` 之后，一个请求从进入到返回，会依次经过这些环节：

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

我们以修改文章的请求 `PUT /articles/{id}` 为例，看看不同的请求会在哪一步结束：

- 没有有效令牌的请求在认证这一步返回 `401`；
- 标题为空的请求在验证输入这一步返回 `422`，处理器不会被调用；
- 处理器返回的业务错误（例如 slug 已被占用）在最后一步转换为 `409` 等响应。

前半段是安装在 Router 上的[中间件](../http/middleware.md)，作用于所有请求；认证由路由声明的策略决定，见[认证](../security/authentication.md)；输入见[请求](../http/requests.md)和[验证](../http/validation.md)；错误的转换见[错误处理](../general/errors.md)。

值得一提的是，中间件、语言目录和日志器都是在创建 API 时显式传入的。所以同一个进程中的两个 API 可以有各自不同的配置，互不影响。

<a id="graceful-shutdown"></a>
## 平滑停止

应用收到 `SIGTERM` 或 Ctrl+C 时，我们希望它**不要粗暴地中断**正在处理的请求。`httpx.Serve` 负责这件事：

```go
var draining httpx.Draining

err := httpx.Serve(ctx, cfg.HTTP, ln, handler, httpx.ServeOptions{
    OnDraining: draining.Set,
})
```

当 `ctx` 被取消时（收到停止信号，或者[不停机升级](../installation/deployment.md#zero-downtime-upgrades)时交给了新进程），它按以下顺序停止：

1. 调用 `OnDraining`，让[就绪检查](#health-checks)开始返回失败；
2. 停止接收新连接，等待正在处理的请求完成，这个过程叫**排空**；
3. 超过 `shutdown_timeout`（默认 10 秒）仍未完成的请求，取消它们的 context 并强制关闭连接。

`httpx.Serve` 返回之后，入口才关闭数据库等资源，正在处理的请求在结束前始终可以访问数据库。

`ln` 通常由 `command.Daemon` 取得，见[依赖装配](dependencies.md#assembling-the-application)。不需要不停机升级的简单程序，用 `httpx.ListenAndServe` 自己监听 `cfg.Addr`。

> [!WARNING]
> 平滑停止依赖 context 的传递。数据库查询和外部调用都要使用请求的 `ctx`，否则超时后它们不会停下来；忽略 context 的 goroutine 会在停机后继续运行。

<a id="health-checks"></a>
## 健康检查

部署平台（如 Kubernetes）和负载均衡器通常用两类接口判断应用的状态：

- **存活检查**（liveness）：进程是否还能响应，失败时平台重启它。直接返回成功，不要检查依赖：数据库故障时重启应用无济于事。
- **就绪检查**（readiness）：现在能否处理请求，失败时停止向这个实例转发流量。它检查是否正在排空，以及必需的依赖是否可用。

Tenon 不会自动注册这两个路由，因为路径由部署环境决定。下面把它们注册在 `/livez` 和 `/readyz`：

```go
router.Get("/livez", func(w http.ResponseWriter, _ *http.Request) {
    w.WriteHeader(http.StatusNoContent)
})
router.Handle("/readyz", httpx.Ready(httpx.ReadyOptions{
    Draining: &draining,
    Checks: []httpx.Check{
        {Name: "database", Ping: db.PingContext},
        {Name: "redis", Ping: func(ctx context.Context) error {
            return rdb.Do(ctx, rdb.B().Ping().Build()).Error()
        }},
        {Name: "cache", Ping: func(context.Context) error { return mc.Ping() }, Optional: true},
    },
}))
```

`draining` 与传给 `ServeOptions.OnDraining` 的是同一个。全部检查并行执行，每项最多 1 秒，超时按失败处理。必需的检查都通过时返回 `200`，否则返回 `503`；正在排空时不再检查，直接返回 `503`。响应体列出每项的结果：

```json
{"status":"ready","checks":{"cache":"failed","database":"ok","redis":"ok"}}
```

失败的原因只记录在日志中，而且只在状态变化时记录：从通过变为失败记 `Warn`，恢复时记 `Info`。

> [!IMPORTANT]
> 只把处理请求**必需**的依赖放进检查。缓存这类坏了也能降级运行的依赖标为 `Optional`，否则缓存的一次短暂故障会让全部实例同时被摘除。只有部分接口用到的服务（PDF、向量检索等）不要放进来，它们的故障由各自的接口返回错误。

> [!TIP]
> 探测很频繁，在[访问日志](../http/middleware.md#http-configuration)的 `ignore_paths` 中排除这两个路径。

<a id="api-reference"></a>
## API 参考

包 `github.com/go-tenon/tenon/httpx`。

### Serve

```go
func Serve(ctx context.Context, cfg Config, ln net.Listener, handler http.Handler, opts ServeOptions) error
```

- **参数**
    - `ctx`：取消时开始平滑停止
    - `cfg`：服务器的超时等参数；不使用其中的 `Addr`，但 `cfg` 仍需合法
    - `ln`：监听端口，通常由 `command.Daemon` 取得
    - `handler`：处理请求的 Router
    - `opts`：见下面的 `ServeOptions`
- **返回**：监听出错时的错误；平滑停止后返回 `nil`

在 `ln` 上运行 `handler`，直到 `ctx` 被取消或监听出错，然后按[平滑停止](#graceful-shutdown)中的顺序停止。返回时 `ln` 已经关闭。

### ListenAndServe

```go
func ListenAndServe(ctx context.Context, cfg Config, handler http.Handler, opts ServeOptions) error
```

与 `Serve` 相同，只是自己监听 `cfg.Addr`。适合不需要不停机升级的简单程序。

### ServeOptions

```go
type ServeOptions struct {
    Logger     *slog.Logger
    OnDraining func()
}
```

`Logger` 记录排空开始和服务器停止，为 `nil` 时使用 `slog.Default()`。`OnDraining` 在开始排空之前调用，通常是 `Draining.Set`。

### Draining

```go
func (d *Draining) Set()
func (d *Draining) Load() bool
```

排空标志，零值即可使用。把 `Set` 传给 `ServeOptions.OnDraining`，把同一个值的地址传给 `ReadyOptions.Draining`。

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
    Checks   []Check
    Timeout  time.Duration
    Logger   *slog.Logger
}
```

`Draining` 为 `nil` 时不检查排空。`Timeout` 是每项检查的超时，默认 1 秒。`Logger` 记录检查结果的变化。

### Check

```go
type Check struct {
    Name     string
    Ping     func(ctx context.Context) error
    Optional bool
}
```

`Name` 出现在结果和日志中；`Ping` 返回错误表示不可用；`Optional` 为 `true` 时，失败只在结果中报告，不让就绪检查失败。
