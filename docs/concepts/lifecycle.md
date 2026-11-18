# 应用生命周期

这一页从整体上介绍一个 Tenon 应用的一生：它怎样启动，运行时由哪些部分组成，一次调用的 context 里带着什么，停止时怎样做到不中断正在进行的工作，以及部署工具怎样判断它是否正常。这些对 HTTP 服务和后台任务都一样。

先建立一个整体的印象即可，每一部分的细节都在链接的页面中。HTTP 服务特有的部分，例如一个请求经过哪些中间件、`httpx.Serve` 怎样排空，见 [HTTP 服务的生命周期](../http/lifecycle.md)。

阅读前需要了解：[安装](../installation/installing.md)中的三步程序；[context](go-primer.md#context)。

- [启动](#startup)
- [运行的部分](#parts)
- [一次调用的 context](#call-context)
- [平滑停止](#graceful-shutdown)
- [健康检查](#health-checks)
- [常见问题](#troubleshooting)

<a id="startup"></a>
## 启动

数据库连接池、NATS 连接这些资源都由程序的入口创建和关闭。无论运行的是 HTTP 服务还是后台任务，应用都按下面的顺序启动：

1. 读取并校验[配置](../general/configuration.md)；
2. 打开数据库等资源，每打开一个，就登记对应的关闭操作；
3. 构造[业务动作](actions.md)，交给要运行的各个部分：HTTP 服务注册路由，RPC 服务登记端点，worker 注册任务的处理函数，调度器登记定时任务；
4. 同时运行这些部分，直到收到停止信号。

这些都是普通的 Go 代码，没有隐藏的步骤，写法见[依赖装配](dependencies.md#assembling-the-application)。

> [!NOTE]
> 迁移不在启动时自动执行，而是部署时的一个独立步骤，见[迁移](../database/migrations.md)。

<a id="parts"></a>
## 运行的部分

应用的进程由一个或几个一直运行的**部分**组成。每个部分是一种入口，把外部的调用转换为对业务动作的调用：

| 部分 | 做什么 | 见 |
| --- | --- | --- |
| HTTP 服务 | 处理接口请求和页面，也承接实时频道的连接 | [HTTP 服务的生命周期](../http/lifecycle.md) |
| RPC 服务 | 处理其他服务经 NATS 发来的调用 | [运行 RPC 服务](../rpc/endpoints.md#running) |
| 队列 worker | 执行后台任务 | [启动 worker](../background/queue.md#running-workers) |
| 定时任务调度器 | 到点执行定时任务 | [运行调度器](../background/scheduling.md#running-the-scheduler) |
| 实时频道 | 单独承接 WebSocket 连接，可选 | [单独部署实时连接](../libraries/realtime.md#separate-process) |

每个部分在 `bootstrap` 的 `Parts` 中登记。`serve` 运行全部部分，`serve worker` 只运行一个部分：只运行 worker 或调度器的进程不对外提供 HTTP 接口，就是一个纯后台服务，见[serve：运行常驻进程](../general/commands.md#serve-command)。

同一个进程中的各部分共用第 2 步打开的资源，例如同一个数据库连接池。

<a id="call-context"></a>
## 一次调用的 context

每个入口在调用业务动作之前，都把这次调用的信息放进 `ctx`。业务代码只从 `ctx` 中读取它们，不关心调用来自哪个入口：

| 信息 | 读取 | HTTP 接口 | 队列任务 | 定时任务 |
| --- | --- | --- | --- | --- |
| 日志关联字段 | 用 `ctx` 记录日志时自动带上 | `request_id` | 发布方的 `request_id`，以及 `message_id`、`attempt` | `task` |
| 链路 | OpenTelemetry 的 span（启用[可观测性](../general/telemetry.md)时） | 接续请求头 `traceparent` 中的链路，没有时新开始 | 接续发布方的链路 | 每次执行新开始 |
| 调用者 | `identity.FromContext` | 认证中间件写入 | 处理函数自己构造，见[队列任务](actions.md#as-jobs) | 同左 |
| 语言 | `i18n.Locale` | 按 `Accept-Language` 协商 | 处理函数自己写入，见[在 HTTP 之外使用](../general/localization.md#outside-http) | 同左 |

所以同一个 `Handle` 方法在接口和任务中都能记录带关联字段的日志，见[日志](../general/logging.md#request-correlation)。调用者则由 `Handle` 的参数显式传入，见[编写动作](actions.md#writing-actions)。

<a id="graceful-shutdown"></a>
## 平滑停止

应用收到 `SIGTERM` 或 Ctrl+C 时，我们希望它**先做完**手上的工作再退出。入口取消 `ctx`，各部分同时开始平滑停止：

- **HTTP 服务**：就绪检查开始返回失败，停止接收新连接，等正在处理的请求完成，见[平滑停止](../http/lifecycle.md#graceful-shutdown)；
- **worker**：停止领取新任务，等处理中的任务写回结果，见[任务的租约与停止](../background/queue.md#leases-and-shutdown)；
- **调度器**：停止触发新任务，等执行中的任务结束，见[失败与停止](../background/scheduling.md#failures-and-shutdown)。

每个部分最多等待各自配置的 `shutdown_timeout`，超时后取消还没完成的工作的 `ctx`。全部部分都返回之后，入口才按打开的逆序关闭数据库等资源，所以正在进行的工作在结束前始终可以使用它们，见[关闭资源](dependencies.md#closing-resources)。

> [!WARNING]
> 平滑停止依赖 context 的传递。数据库查询和外部调用都要使用传入的 `ctx`，超时后它们才能随之停下；自己启动的 goroutine 也要监听 context，停机时一起结束。

<a id="health-checks"></a>
## 健康检查

部署工具和反向代理通常用两类检查判断实例的状态：

- **存活检查**（liveness）：进程是否还能响应。Docker Swarm 会替换检查失败的容器。它只确认进程能响应，不检查数据库等依赖：数据库故障时重启应用无济于事。
- **就绪检查**（readiness）：现在能否处理请求，失败时反向代理不再把请求转给这个实例。它检查必需的依赖是否可用，进程开始停止时也立即失败。

这两个检查都由 HTTP 服务提供（`/livez` 和 `/readyz`）：要检查哪些依赖、怎样执行检查，由与传输方式无关的 `health` 包负责，HTTP 服务用 `httpx.Ready` 把结果作为就绪检查返回，写法见[健康检查](../http/lifecycle.md#health-checks)。只运行 worker、调度器或 RPC 的进程不需要健康检查：它们出错时进程以非 0 状态退出，由 systemd 或 Docker 的重启策略处理，见[部署](../installation/deployment.md#health-checks)。

<a id="troubleshooting"></a>
## 常见问题

**按 Ctrl+C 后程序要等很久才退出**

有请求、任务或 goroutine 没有响应 `ctx` 的取消。检查数据库查询、外部调用是否都传入了 `ctx`，自己启动的 goroutine 是否在 `ctx.Done()` 时返回。最长等待时间由各部分的 `shutdown_timeout` 决定。

**一个部分出错退出后，整个进程都停了**

这是预期的行为：一个部分运行时出错，其余部分随之平滑停止，进程以非 0 状态退出，交给 systemd、Docker 等重启，见[serve：运行常驻进程](../general/commands.md#serve-command)。
