# 应用生命周期

这一页从整体上介绍一个 Tenon 应用的一生：它怎样启动，运行时由哪些部分组成，一次调用的 context 里带着什么，停止时怎样做到不中断正在进行的工作，以及部署平台怎样判断它是否正常。这些对 HTTP 服务和后台任务都一样。

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

数据库连接池、Redis 客户端这些资源都由程序的入口创建和关闭。无论运行的是 HTTP 服务还是后台任务，应用都按下面的顺序启动：

1. 读取并校验[配置](../general/configuration.md)；
2. 打开数据库等资源，每打开一个，就登记对应的关闭操作；
3. 构造[业务动作](actions.md)，交给要运行的各个部分：HTTP 服务注册路由，worker 注册任务的处理函数，调度器登记定时任务；
4. 同时运行这些部分，直到收到停止信号。

这些都是普通的 Go 代码，没有隐藏的步骤，写法见[依赖装配](dependencies.md#assembling-the-application)。

> [!NOTE]
> 迁移不在启动时自动执行，而是部署时的一个独立步骤，见[迁移](../database/migrations.md)。

<a id="parts"></a>
## 运行的部分

应用的进程由一个或几个一直运行的**部分**组成。每个部分是一种入口，把外部的调用转换为对业务动作的调用：

| 部分 | 做什么 | 见 |
| --- | --- | --- |
| HTTP 服务 | 处理接口请求和页面 | [HTTP 服务的生命周期](../http/lifecycle.md) |
| 队列 worker | 执行后台任务 | [启动 worker](../background/queue.md#running-workers) |
| 定时任务调度器 | 到点执行定时任务 | [运行调度器](../background/scheduling.md#running-the-scheduler) |
| 实时频道 | 承接 WebSocket 连接，可选 | [单独部署实时连接](../libraries/realtime.md#separate-process) |

启动哪个命令，就运行哪些部分：`serve` 只运行 HTTP 服务，`start` 在一个进程中运行 HTTP 服务、worker 和调度器。只运行 `work` 和 `schedule` 的进程不对外提供 HTTP 接口，就是一个纯后台服务，见[在一个进程中运行多个部分](../general/commands.md#running-together)。

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

应用收到 `SIGTERM` 或 Ctrl+C 时，我们希望它**先做完**手上的工作，而不是直接退出。入口取消 `ctx`，各部分同时开始平滑停止：

- **HTTP 服务**：就绪检查开始返回失败，停止接收新连接，等正在处理的请求完成，见[平滑停止](../http/lifecycle.md#graceful-shutdown)；
- **worker**：停止领取新任务，等处理中的任务写回结果，见[任务的租约与停止](../background/queue.md#leases-and-shutdown)；
- **调度器**：停止触发新任务，等执行中的任务结束，见[失败与停止](../background/scheduling.md#failures-and-shutdown)。

每个部分最多等待各自配置的 `shutdown_timeout`，超时后取消还没完成的工作的 `ctx`。全部部分都返回之后，入口才按打开的逆序关闭数据库等资源，所以正在进行的工作在结束前始终可以使用它们，见[关闭资源](dependencies.md#closing-resources)。

> [!WARNING]
> 平滑停止依赖 context 的传递。数据库查询和外部调用都要使用传入的 `ctx`，超时后它们才能随之停下；自己启动的 goroutine 也要监听 context，停机时一起结束。

<a id="health-checks"></a>
## 健康检查

部署平台（如 Kubernetes）和负载均衡器通常用两类检查判断实例的状态：

- **存活检查**（liveness）：进程是否还能响应，失败时平台重启它。它只确认进程能响应，不检查数据库等依赖：数据库故障时重启应用无济于事。
- **就绪检查**（readiness）：现在能否处理工作，失败时平台不再把流量转给这个实例。它检查必需的依赖是否可用。

要检查哪些依赖、怎样执行检查，由与传输方式无关的 `health` 包负责；HTTP 服务用 `httpx.Ready` 把结果作为就绪检查返回，写法见[健康检查](../http/lifecycle.md#health-checks)。只运行队列 worker 或调度器的进程没有 HTTP 服务，可以另开一个只提供健康检查的端口，见[没有 HTTP 服务的进程](../http/lifecycle.md#probe-listener)。

<a id="troubleshooting"></a>
## 常见问题

**按 Ctrl+C 后程序要等很久才退出**

有请求、任务或 goroutine 没有响应 `ctx` 的取消。检查数据库查询、外部调用是否都传入了 `ctx`，自己启动的 goroutine 是否在 `ctx.Done()` 时返回。最长等待时间由各部分的 `shutdown_timeout` 决定。

**一个部分出错退出后，整个进程都停了**

这是预期的行为：一个部分运行时出错，其余部分随之平滑停止，进程以非 0 状态退出，交给 systemd、Kubernetes 等重启，见[在一个进程中运行多个部分](../general/commands.md#running-together)。
