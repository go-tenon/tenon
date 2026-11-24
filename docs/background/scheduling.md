# 定时任务

`schedule` 包在代码中声明定时任务，按 cron 表达式在调度器所在的进程中执行。任务是一个 `func(ctx context.Context) error`。部署多个实例时，同一个任务的同一次触发只在一个实例上执行，由状态存储（`schedule/pgschedule`）按单调递增的触发水位和运行租约协调。

包：`schedule`、`schedule/pgschedule`、`schedule/scheduletest`、`schedule/schedulecmd`、`schedule/scheduleapi`。

<a id="example-usage"></a>
## 示例

`db` 是入口打开的 `*bun.DB`，`cfg.Schedule` 是配置中的 `schedule` 段；使用前发布 `pgschedule.Migrations()`：

```go
sched := schedule.New(pgschedule.New(db), cfg.Schedule, nil)
sched.Add("reports:daily", "0 0 2 * * *", buildDailyReport, // 6 段，第一段是秒：每天 02:00:00
    schedule.Describe("生成前一天的报表"))
sched.Add("articles:publish-scheduled", "@every 1m", publishScheduled.Handle)

err := sched.Run(ctx) // 一直运行到 ctx 被取消
```

<a id="defining-tasks"></a>
## 用 `Scheduler.Add` 定义定时任务

`schedule.New(store, cfg, logger)` 创建调度器（`logger` 为 `nil` 时用 `slog.Default()`），`Add` 注册任务。任务通常是[业务动作](../concepts/actions.md#as-scheduled-tasks)的 `Handle` 方法，由业务模块的 `RegisterTasks` 登记。`db` 是 `*bun.DB`：

```go
sched.Add("articles:publish-scheduled", "* * * * *", article.NewPublishScheduled(db).Handle,
    schedule.Label("定时发布文章"),         // 显示名称，悬停时展示内部任务名
    schedule.Describe("发布到了预定时间的文章")) // 说明显示在任务列表中
```

- 名称在调度器中唯一，也是 `schedule_tasks` 表的主键，写成 `模块:动作`。
- 名称为空或重复、表达式不合法、在 `Run` 之后调用时，`Add` panic，应用启动时就会发现。
- 其他选项：`schedule.In(loc)` 换时区（见[时区](#timezones)），`schedule.Timeout(d)`、`schedule.AllowOverlap()` 见[防止重叠](#preventing-overlaps)，`schedule.EveryInstance()` 见[多个实例](#multiple-instances)。

<a id="expressions"></a>
### cron 表达式

| 写法 | 含义 |
| --- | --- |
| `*/5 * * * *` | 5 段，分钟精度，在第 0 秒触发 |
| `*/10 * * * * *` | 6 段，第一段是秒：每 10 秒 |
| `@hourly`、`@daily`、`@weekly`、`@monthly`、`@everysecond` | 预定义写法，`@daily` 即 `0 0 * * *` |
| `@every 15s`、`@every 90s` | 固定间隔，至少 1 秒，必须是整秒 |

- 完整语法（包括 `L`、`W`、`#`）见 [gronx](https://github.com/adhocore/gronx#cron-expression)。
- `@every` 按 Unix 时间对齐：`@every 15s` 总是在第 0、15、30、45 秒触发，与进程何时启动无关，所有实例算出的触发时刻相同。

<a id="timezones"></a>
### 时区：`schedule.In`

表达式按 `time.Local` 计算，Go 程序从环境变量 `TZ` 读取它，例如 `TZ=Asia/Shanghai`；容器镜像通常是 UTC。单个任务按其他时区计算时用 `schedule.In`，`loc` 是 `time.LoadLocation("Asia/Shanghai")` 返回的 `*time.Location`：

```go
sched.Add("reports:daily", "0 3 * * *", reports.Generate, schedule.In(loc))
```

<a id="running-the-scheduler"></a>
## 运行调度器

调度器是常驻进程的一个[运行部分](../concepts/dependencies.md#running-parts)。把 `pgschedule.Migrations()` 加入[发布命令](../database/migrations.md#publishing-framework-migrations)，在入口的 `bootstrap` 包中把创建调度器和注册任务写在 `tasks` 方法中：调度器、[`schedules` 命令](#schedules-command)和[后台页面](#admin-pages)都用它，任务只定义一次：

```go
// tasks 创建调度器并注册全部定时任务。
func (a *App) tasks() *schedule.Scheduler {
    sched := schedule.New(pgschedule.New(a.db), a.cfg.Schedule, nil)
    article.RegisterTasks(sched, a.db) // 业务模块的任务
    // 框架数据的清理等不属于某个模块的任务直接写在这里，见“清理框架数据”
    return sched
}

// Scheduler 装配调度器，返回的函数按时执行定时任务，直到 ctx 被取消。
func (a *App) Scheduler(*command.Daemon) (func(context.Context) error, error) {
    return a.tasks().Run, nil
}
```

在 `Parts` 中登记为 `{Name: "scheduler", Short: "执行定时任务", Start: a.Scheduler}`，`serve` 运行全部部分，`serve scheduler` 只运行它，见[serve 命令](../general/commands.md#serve-command)。

- `Run` 每秒检查一次到期的任务，每个任务在自己的 goroutine 中执行，互不耽误。没有注册任务时 `Run` 返回错误。
- 单独部署调度器时不需要健康检查，见[部署](../installation/deployment.md#health-checks)。

<a id="multiple-instances"></a>
## 多个实例

每个任务在 `schedule_tasks` 表中永久占一行，保存单调递增的触发水位 `last_tick` 和运行租约。多个实例同时到达同一触发时刻时，只有原子把 `last_tick` 推进到该时刻的那个实例执行（或报告一次重叠跳过）：

- 所有进程共用同一个状态存储（`pgschedule.New(db)`），去重覆盖全部实例，也覆盖[不停机升级](../installation/operations.md#upgrade-overlap)时新旧进程的重叠期。
- 因为水位单调递增，时钟落后的实例不会重复执行已经执行过的旧触发；每个任务永久一行，不产生过期垃圾行，也不需要额外清理。

每个实例各执行一次的任务（例如清理本机的临时目录）用 `schedule.EveryInstance()`：

```go
sched.Add("tmp:cleanup", "@hourly", cleanTmpDir, schedule.EveryInstance())
```

<a id="preventing-overlaps"></a>
## 防止重叠与超时：`schedule.Timeout`

任务上一次还没结束时，下一次触发被跳过并记一条 `info` 日志，全集群只记一次跳过。执行期间调度器在 `schedule_tasks` 中持有运行租约并每 20 秒续期；若租约丢失（`Refresh` 返回 false），调度器立即以 `schedule.ErrLockLost` 取消任务的 `ctx`。持有租约的实例崩溃时租约在一分钟内过期。确实需要并行执行的任务用 `schedule.AllowOverlap()`。

卡住的任务（例如一直等待外部请求）一直持有租约，之后的触发都被跳过。用 `schedule.Timeout` 限制每次执行的时长：

```go
sched.Add("reports:daily", "0 3 * * *", reports.Generate, schedule.Timeout(30*time.Minute))
```

超时后任务的 `ctx` 以 `schedule.ErrTimeout` 取消，这次执行记为失败，租约随之释放。任务要把 `ctx` 传给数据库查询和 HTTP 请求才能及时结束；不理会 `ctx` 的任务一直运行到自己返回。

<a id="failures-and-shutdown"></a>
## 失败、错过的触发与停止

- 任务返回错误或 panic 时，调度器记一条 `error` 日志，等下一次照常触发，不重试。
- 调度落后（例如进程被暂停）不到一分钟时，错过的触发补执行一次；部署、重启期间错过的触发直接跳过。
- 收到停止信号后，调度器停止触发新任务，等待执行中的任务结束，最多 [`shutdown_timeout`](#configuration)，超时后取消它们的 `ctx`。
- 日志带有 `component=schedule`；任务的 `ctx` 中带有 `task` 字段，`slog.InfoContext(ctx, ...)` 记录的日志自动带上它。

需要失败重试或耗时较长的工作，在任务中把它[发布到队列](queue.md#publishing-jobs)，由 worker 执行和重试。`report.Generate` 是 `queue.Topic[time.Time]`，`a.db` 是 `*bun.DB`：

```go
sched.Add("reports:daily", "0 3 * * *", func(ctx context.Context) error {
    return report.Generate.Publish(ctx, a.db, time.Now().AddDate(0, 0, -1))
})
```

<a id="listing-tasks"></a>
## 查看任务：`Tasks`、`Running` 与 `States`

`a.tasks` 是[注册全部任务的方法](#running-the-scheduler)，下面两个调用在任何进程中都可以用：

```go
for _, t := range a.tasks().Tasks() { // 只读取定义
    t.Name, t.Description, t.Expr     // "reports:daily"、"生成前一天的报表"、"0 3 * * *"
    t.Next(time.Now())                // 下一次执行的时间；t.Prev 是上一次应当执行的时间
}
running, err := a.tasks().Running(ctx) // 单次查询返回各任务此刻是否在任何一个实例上执行
```

`Running` 和 `States` 一次批量查询状态存储，要与调度器共用存储。使用 `EveryInstance` 或 `AllowOverlap` 的任务不登记执行状态，不在 `Running` 结果中。

<a id="schedules-command"></a>
### `schedules` 命令

`schedulecmd.New` 返回 `schedules` 命令，在 [bootstrap.Commands](../concepts/actions.md#as-commands) 中声明。传入方法值 `a.tasks`：声明命令时 `App` 还没有装配，执行时才调用它：

```go
schedulecmd.New(a.tasks),
```

`./bin/blog schedules` 每个任务一行：名称、表达式、下一次执行的时间、是否正在执行（不登记执行状态的任务显示 `-`）和说明。

<a id="run-history"></a>
### 执行记录：`scheduleapi.Observer`

`scheduleapi.Observer(db)` 把每次执行和跳过写进 `schedule_runs` 表，每次执行一行。把 `scheduleapi.Migrations()` 加入[发布命令](../database/migrations.md#publishing-framework-migrations)，在 `tasks` 中加上 Observer 和清理任务：

```go
func (a *App) tasks() *schedule.Scheduler {
    sched := schedule.New(pgschedule.New(a.db), a.cfg.Schedule, nil, schedule.WithObserver(scheduleapi.Observer(a.db)))
    // ……
    sched.Add("schedule:prune-runs", "@daily", schedule.Cleanup(scheduleapi.Cleanup(a.db, 7*24*time.Hour), 1000),
        schedule.Describe("删除 7 天前的执行记录"))
    return sched
}
```

把事件送到别处（指标、告警）时自己写一个 `schedule.Observer`，`WithObserver` 可以用多次：

- 事件的 `Kind`：`TaskStarted`、`TaskSucceeded`、`TaskFailed`（`Err` 是原因）、`TaskSkipped`（`SkipReason` 是 `overlap` 或 `store_failed`）。只有执行或跳过这次触发的实例报告。
- `Tick` 是这次触发应当执行的时刻，`Started` 是实际开始的时间。[立即执行](#running-manually)的事件 `Manual` 为 `true`，`Tick` 为零值。
- 结束事件在释放运行租约之后发出；Observer 在执行任务的 goroutine 中同步调用，较重的工作缓冲后批量处理。

<a id="admin-pages"></a>
### 后台页面：`scheduleapi.Register`

`scheduleapi.Register` 在 HTTP 入口注册定时任务的接口，`@go-tenon/ui` 的 [`SchedulePanel`](../frontend/features.md#schedule-panel) 是使用它们的页面。`api` 是 `rest.New` 创建的 API，`tokens` 是 `*apitoken.Store`，第二个参数保护全部接口：

```go
scheduleapi.Register(api, rest.RequireToken(api, tokens.Validate, "jobs:manage"), a.db, a.tasks)
```

- `GET /schedules`（`schedule-list`）：全部任务，带下一次执行、是否正在执行、上一次的结果和耗时、是否错过了触发。
- `POST /schedules/{name}/run`（`schedule-run`）：[立即执行](#running-manually)，返回 202。
- `GET /schedules/runs`（`schedule-runs`）：执行记录，按 `task`、`status` 筛选，分页。
- `a.tasks` 只用来读取任务的定义和状态，HTTP 进程不运行调度器。
- 应当执行的时间过去一分钟后，这次触发既没有被领取（依据 `schedule_tasks.last_tick` 和执行记录）也没有正在执行时，任务标为错过（`missed`），即使 `schedule_runs` 已按保留期清理也准确。

<a id="running-manually"></a>
### 立即执行：`scheduleapi.HandleRunNow`

`schedule-run` 接口把任务名发布到队列（topic `scheduleapi.RunNow`），由 worker 执行，耗时的任务不占用请求。worker 进程注册它的处理函数，`w` 是 `*queue.Worker`：

```go
scheduleapi.HandleRunNow(w, a.tasks)
```

- 处理函数调用 `Scheduler.RunNow`：不按表达式，也不推进触发水位；事件照常交给 Observer，执行记录中标为手动。
- 没有 `AllowOverlap` 的任务照样防止重叠：正在任何一个实例上执行时这次不执行，记一行跳过。
- 失败不重试，和按表达式触发一样等下一次。

<a id="framework-cleanups"></a>
## 清理框架数据：`schedule.Cleanup`

框架保存在数据库中的数据都提供 `func(ctx, limit) (int, error)` 形式的清理函数，每次最多删除 `limit` 行：有固定过期时间的是 `Cleanup` 方法本身，按保留期删除的由 `Cleanup(保留期)` 返回。`schedule.Cleanup(fn, limit)` 把它包装成任务，反复调用直到一次删除的数量少于 `limit`。在 [`tasks`](#running-the-scheduler) 中注册，`a.tokens` 是 `*apitoken.Store`：

```go
sched.Add("apitoken:cleanup", "@hourly", schedule.Cleanup(a.tokens.Cleanup, 1000))
```

| 数据 | 清理函数 | 建议频率 |
| --- | --- | --- |
| [API 令牌](../security/api-tokens.md#cleanup) | `apitoken.Store` 的 `Cleanup` | 每小时 |
| [登录验证码](../security/login.md#cleanup) | 每套账号的 `login.Realm` 的 `Cleanup` | 每小时 |
| [原子锁](../libraries/locks.md#postgres-store) | `pglock.New(db)` 的 `Cleanup` | 每小时 |
| [限流计数](../libraries/rate-limiting.md#postgres-limiter) | 任意一个 `pglimit.New(…)` 的 `Cleanup`（删除所有限流器的） | 每小时 |
| [未绑定的 passkey 和过期的仪式](../security/login.md#passkeys) | `passkey.Store` 的 `Cleanup` | 每天 |
| [文件](../libraries/filesystem.md#cleanup) | 文件服务的 `Cleanup` | 每 5 分钟 |
| [操作日志](../features/activity.md#retention) | `activity.Cleanup(db, 保留期)` | 每天 |
| [通知](../features/notifications.md#retention) | `notification.Cleanup(db, 保留期)` | 每天 |
| [推送设备](../features/push.md#cleanup) | `push.Cleanup(db, 保留期)` | 每天 |
| [后台操作](../features/operations.md#cleanup)（包括导入和导出） | `operation.New(db)` 的 `Cleanup(保留期)` | 每天 |
| [OAuth 授权码](../security/oauth-server.md#assembling) | `oauthserver.New(…)` 的 `Cleanup` | 每小时 |
| [OAuth 动态注册的客户端](../security/oauth-server.md#dynamic-registration) | `oauthserver.New(…)` 的 `CleanupClients(保留期)` | 每天 |
| [Webhook 投递记录](../features/webhooks.md#retention) | `webhook.Cleanup(db, 保留期)` | 每天 |
| [失败任务](queue.md#failed-jobs) | `queue.NewFailedJobs(db).Cleanup(保留期)` | 每天 |
| [定时任务的执行记录](#run-history) | `scheduleapi.Cleanup(db, 保留期)` | 每天 |
| [冷静期已过的注销账号](../features/privacy.md#erasing) | 每套账号体系的 `privacy.New(…)` 的 `EraseDue` | 每 10 分钟 |

在线成员保存在 NATS 中，由 NATS 自动删除过期的记录。

<a id="testing"></a>
## 测试：`RunDue`

`RunDue(ctx, at)` 执行在时刻 `at` 到期的任务，等它们结束后返回合并的错误，规则与 `Run` 相同。`scheduletest.NewStore()` 是内存状态存储，`ctx` 是 `t.Context()`：

```go
sched := schedule.New(scheduletest.NewStore(), schedule.DefaultConfig(), nil)
var ran bool
sched.Add("reports:daily", "0 3 * * *", func(context.Context) error {
    ran = true
    return nil
}, schedule.In(time.UTC))

err := sched.RunDue(ctx, time.Date(2026, 11, 11, 3, 0, 0, 0, time.UTC)) // 03:00 到期：ran 为 true
```

任务本身是普通的函数，直接调用它测试。

<a id="configuration"></a>
## 配置

`schedule.Config` 放进根配置，字段标签为 `yaml:"schedule"`：

```yaml
schedule:
  shutdown_timeout: 30s # 停止时等待执行中的任务的最长时间，超时后取消它们的 ctx；为 0 时立即取消；默认 30s
```
