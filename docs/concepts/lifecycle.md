# 应用生命周期

一个 Tenon 应用怎样启动、由哪些运行部分组成、一次调用的 context 中带着什么，HTTP 请求经过哪些环节，停止时怎样排空正在进行的工作，以及部署平台用的健康检查。

包：`command`、`cleanup`、`httpx`、`health`。

<a id="startup"></a>
## 启动顺序

`serve` 命令（`command.Serve`）按下面的顺序启动，HTTP 服务和后台任务相同：

1. 读取并校验[配置](../general/configuration.md)；
2. `bootstrap.Open` 打开数据库等资源，每打开一个就把关闭操作登记到 `cleanup.Stack`；
3. 调用每个运行部分的 `Start` 装配它：HTTP 服务创建 API、构造[业务动作](actions.md)、注册路由并取得监听端口，RPC 服务登记端点，worker 登记任务的处理函数，调度器登记定时任务；
4. 全部装配成功后同时运行这些部分，直到收到停止信号。

任何一个部分装配失败时，没有部分开始运行。写法见[依赖装配](dependencies.md#running-parts)。

> [!IMPORTANT]
> 迁移不在启动时执行，是部署时单独的一步 `migrate up`，见[迁移](../database/migrations.md)。

<a id="parts"></a>
## 运行的部分：`command.Part`

进程由 `bootstrap.Parts` 中登记的一个或几个部分组成，每个部分把外部的调用转换为对业务动作的调用：

| 部分 | 做什么 | 见 |
| --- | --- | --- |
| HTTP 服务 | 接口、页面和实时频道的连接 | [请求的处理过程](#request-flow) |
| RPC 服务 | 其他服务经 NATS 发来的调用 | [运行 RPC 服务](../rpc/endpoints.md#running) |
| 队列 worker | 执行后台任务 | [启动 worker](../background/queue.md#running-workers) |
| 定时任务调度器 | 到点执行定时任务 | [运行调度器](../background/scheduling.md#running-the-scheduler) |
| 实时频道 | 单独承接 WebSocket 连接，可选 | [单独部署实时连接](../libraries/realtime.md#separate-process) |

`serve` 运行全部部分（`Standalone` 的除外），`serve worker` 只运行一个部分，见 [serve 命令](../general/commands.md#serve-command)。同一个进程中的各部分共用 `Open` 打开的资源，例如同一个数据库连接池。一个部分运行时出错，其余部分随之平滑停止，进程以非 0 状态退出，由 systemd 或 Docker 重启。

<a id="call-context"></a>
## 一次调用的 context

每个入口在调用业务动作之前，把这次调用的信息放进 `ctx`。业务代码只从 `ctx` 中读取，不关心调用来自哪个入口：

| 信息 | 读取 | HTTP 接口 | 队列任务 | 定时任务 |
| --- | --- | --- | --- | --- |
| 日志关联字段 | 用 `ctx` 记录日志时自动带上 | `request_id` | 发布方的 `request_id`，以及 `message_id`、`attempt` | `task` |
| 链路 | OpenTelemetry 的 span（启用[可观测性](../general/telemetry.md)时） | 接续请求头 `traceparent`，没有时新开始 | 接续发布方的链路 | 每次执行新开始 |
| 调用者 | `identity.FromContext` | 认证中间件写入 | 处理函数自己构造，见[队列任务](actions.md#as-jobs) | 同左 |
| 语言 | `i18n.Locale` | 按 `Accept-Language` 协商 | 处理函数自己写入，见[在 HTTP 之外使用](../general/localization.md#outside-http) | 同左 |

日志的写法见[日志](../general/logging.md#request-correlation)。业务动作的 `Handle` 通过参数接收调用者，见[编写动作](actions.md#writing-actions)。

<a id="request-flow"></a>
## HTTP 请求的处理过程

安装了 `httpx.Defaults` 和 `httpx.Timeout` 之后，一个请求依次经过：

```text
确定请求 ID（X-Request-ID），写入安全响应头
  → 解析客户端地址（只信任配置的代理）
  → 按 Accept-Language 协商语言
  → 开始记录访问日志
  → panic 恢复，返回 500
  → 限制请求体大小，限制等待数据库连接的时间，设置请求超时
  → 应用安装的其他中间件（例如限流）
  → 路由匹配
  → 认证（只在需要认证的路由上）
  → Huma 解析并校验输入
  → 处理器调用业务动作
  → 错误转换为统一的错误响应
```

以 `PUT /articles/{id}` 为例，请求在最先失败的一步返回：

- 没有有效令牌：认证返回 `401`；
- 标题为空：校验输入返回 `422`，到不了处理器；
- slug 已被占用：处理器返回的业务错误转换为 `409`。

没有登录、标题又为空的请求得到 `401`：认证在校验之前。中间件见[中间件](../http/middleware.md)，认证见[认证](../security/authentication.md)，输入见[请求](../http/requests.md)和[校验](../http/requests.md)，错误的转换见[错误处理](../general/errors.md)。

<a id="graceful-shutdown"></a>
## 平滑停止

进程收到 `SIGTERM` 或 Ctrl+C 时取消 `ctx`，各部分同时开始平滑停止：

- **HTTP 服务**：就绪检查返回失败，停止接收新连接，等正在处理的请求完成，见[HTTP 服务的排空](#draining)；
- **worker**：停止领取新任务，等处理中的任务写回结果，见[任务的租约与停止](../background/queue.md#leases-and-shutdown)；
- **调度器**：停止触发新任务，等执行中的任务结束，见[失败与停止](../background/scheduling.md#failures-and-shutdown)。

每个部分最多等待各自配置的 `shutdown_timeout`，超时后取消未完成工作的 `ctx`。全部部分返回之后，入口才按打开的逆序关闭数据库等资源，正在进行的工作在结束前始终可以使用它们，见[关闭资源](dependencies.md#closing-resources)。

> [!WARNING]
> 数据库查询和外部调用都要使用传入的 `ctx`，自己启动的 goroutine 要在 `ctx.Done()` 时返回。否则停止时它们不会停下，进程要等满 `shutdown_timeout` 才退出。

<a id="draining"></a>
### HTTP 服务的排空：`httpx.Serve`

`httpx.Serve` 在 `ln` 上运行 `router`，直到 `ctx` 被取消。`ln` 由 `command.Daemon` 的 `Listen` 取得，见[多个运行部分](dependencies.md#running-parts)；简单的程序用 `httpx.ListenAndServe` 自己监听 `cfg.Addr`：

```go
err := httpx.Serve(ctx, cfg.HTTP, ln, router)
```

`ctx` 被取消时（收到停止信号，或[不停机升级](../installation/operations.md#zero-downtime-upgrades)时交给了新进程），`httpx.Serve` 按以下顺序停止：

1. 开始排空：关闭 `httpx.DrainStarted(ctx)` 返回的通道，[就绪检查](#health-checks)从这一刻起返回 `503`，SSE 等长连接监听这个通道主动结束；
2. 停止接收新连接，等待正在处理的请求完成；
3. 超过 `http.shutdown_timeout`（默认 10 秒）仍未完成的请求，取消它们的 context 并强制关闭连接，日志中出现 `context canceled`。

`httpx.Serve` 返回之后，入口才关闭数据库等资源。部署平台等待进程退出的时间要比 `http.shutdown_timeout` 长，见[部署](../installation/deployment.md#graceful-shutdown)。

<a id="health-checks"></a>
## 健康检查：`/livez` 与 `/readyz`

- **存活检查** `/livez`：进程能响应就返回 `204`，不检查依赖：数据库故障时重启应用无济于事。Docker Swarm 替换检查失败的容器，[health 命令](../general/commands.md#health-command)请求它。
- **就绪检查** `/readyz`：必需的依赖都可用、进程没有在排空时返回 `200`，否则返回 `503`，反向代理据此不再把请求转给这个实例。

两个路由由应用在 `bootstrap` 的 `Router` 中注册。`a.db` 是 `Open` 打开的连接池，`a.rdb` 是 Redis 客户端：

```go
router.Get("/livez", func(w http.ResponseWriter, _ *http.Request) {
    w.WriteHeader(http.StatusNoContent)
})
router.Handle("/readyz", httpx.Ready(httpx.ReadyOptions{
    Checks: []health.Check{
        {Name: "database", Ping: func(ctx context.Context) error { return a.db.PingContext(ctx) }},
        {Name: "cache", Ping: func(ctx context.Context) error { return a.rdb.Ping(ctx).Err() }, Optional: true},
    },
}))
```

- `Ping` 写成闭包。`openapi`、`routes` 命令用零值的 `App` 构造同一套路由，`a.db.PingContext` 这样的方法值在 `a.db` 为 `nil` 时取值就 panic。
- 只放处理请求必需的依赖。缓存这类坏了也能降级运行的依赖标为 `Optional`，失败只出现在结果中，实例继续接收流量。只有部分接口用到的服务（PDF、向量检索等）由各自的接口返回错误。
- 全部检查并行执行，每项最多 1 秒（`ReadyOptions.Timeout`），超时按失败处理。
- 响应体列出每项的结果，例如 `{"status":"ready","checks":{"cache":"failed","database":"ok"}}`。失败的原因只在状态变化时记录在日志中：变为失败记 `Warn`，恢复记 `Info`。
- 探测很频繁，在[访问日志](../http/middleware.md#http-configuration)的 `ignore_paths` 中排除这两个路径。

只运行 worker、调度器或 RPC 的进程没有健康检查：它们出错时进程以非 0 状态退出，由 systemd 或 Docker 的重启策略处理，见[部署](../installation/deployment.md#health-checks)。

<a id="health-checker"></a>
### 在 HTTP 之外检查依赖：`health.Checker`

`health` 包与传输方式无关，`httpx.Ready` 用它执行检查。HTTP 之外的入口需要报告健康状态时（例如 gRPC 的健康检查服务），直接使用它。`checks` 是要检查的 `[]health.Check`，写法见[健康检查](#health-checks)：

```go
checker := health.New(health.Options{Checks: checks})
report := checker.Run(ctx)
if !report.Ready {
    // 按入口的协议报告“未就绪”
}
```

`report.Ready` 表示必需的检查都通过，`report.Results` 列出每一项的结果和失败原因。
