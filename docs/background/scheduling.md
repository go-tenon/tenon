# 定时任务

应用中总有一些需要按时执行的工作：过期的令牌、验证码和失败任务会在数据库中留下过期的行，需要定期清理；应用自己也常有定时的工作，例如每天凌晨生成报表。

过去，你可能会为每一项工作在服务器上写一条 crontab。但这样一来，定时任务散落在代码之外，部署多台服务器时还要小心别让它们重复执行。`schedule` 包让你在代码中声明定时任务，按 cron 表达式执行，精度到秒。

调度器只负责“按时触发”：任务就是一个 `func(ctx context.Context) error`，在调度器所在的进程中执行。部署多个实例时，同一个任务的同一次触发只会在一个实例上执行。

**cron 表达式**是描述“什么时候执行”的一串字段，例如 `0 0 2 * * *` 表示每天 02:00:00。写 6 段时第一段是秒；也可以写常见的 5 段（分、时、日、月、星期），在那一分钟的第 0 秒触发。

阅读前需要了解：[原子锁](../libraries/locks.md)；[依赖装配](../concepts/dependencies.md#running-parts)。

- [示例用法](#example-usage)
- [定义任务](#defining-tasks)
    - [表达式](#expressions)
    - [时区](#timezones)
- [运行调度器](#running-the-scheduler)
- [多个实例](#multiple-instances)
- [防止重叠](#preventing-overlaps)
- [失败与停止](#failures-and-shutdown)
- [查看任务](#listing-tasks)
    - [列出任务的命令](#schedules-command)
    - [执行记录](#run-history)
    - [后台页面](#admin-pages)
    - [立即执行](#running-manually)
- [清理框架数据](#framework-cleanups)
- [测试](#testing)
- [常见问题](#troubleshooting)
- [API 参考](#api-reference)

<a id="example-usage"></a>
## 示例用法

下面注册了两个定时任务，然后运行调度器：

```go
sched := schedule.New(locker, cfg.Schedule, nil)
sched.Add("reports:daily", "0 0 2 * * *", buildDailyReport, // 每天 02:00:00
    schedule.Describe("生成前一天的报表"))
sched.Add("articles:publish-scheduled", "@every 1m", publishScheduled.Handle)

err := sched.Run(ctx) // 一直运行到 ctx 被取消
```

`Add` 注册任务：任务名、cron 表达式和要执行的函数。`0 0 2 * * *` 表示每天 02:00:00 执行（第一段是秒），`@every 1m` 表示每分钟一次。部署多个实例时，`locker` 保证每一次触发只有一个实例真正执行。

<a id="defining-tasks"></a>
## 定义任务

`schedule.New` 创建调度器，`Add` 注册任务。名称在调度器中唯一，也是锁名的一部分：

```go
sched := schedule.New(locker, cfg.Schedule, nil)
sched.Add("articles:publish-scheduled", "* * * * *", article.NewPublishScheduled(db).Handle)
```

任务通常是[业务动作](../concepts/actions.md#as-scheduled-tasks)的 `Handle` 方法：发布到期文章的 `PublishScheduled` 只接收 `ctx`，它的 `Handle` 正好是任务的签名。`schedule.Describe` 为任务加一句说明，在[查看任务](#listing-tasks)时展示。

`locker` 是[原子锁](../libraries/locks.md)，调度器用它在多个实例之间去重、防止任务重叠，见[多个实例](#multiple-instances)。第二个参数是配置段，见[失败与停止](#failures-and-shutdown)；第三个参数是日志器，`nil` 时使用 `slog.Default()`。

[原子锁](../libraries/locks.md#running-once)一章中，每个实例都要自己定时调用 `PublishScheduled` 并用锁去重；交给调度器之后，`PublishScheduled` 里的 `locker.Do` 和 `locker` 依赖都可以去掉，调度器已经保证每次只在一个实例上执行。

<a id="expressions"></a>
### 表达式

| 写法 | 含义 |
| --- | --- |
| `*/5 * * * *` | 标准的 5 段 cron 表达式，分钟精度，在第 0 秒触发 |
| `*/10 * * * * *` | 6 段，第一段是秒：每 10 秒 |
| `@hourly`、`@daily`、`@weekly`、`@monthly`、`@everysecond` | 预定义写法，`@daily` 即 `0 0 * * *` |
| `@every 15s`、`@every 90s` | 固定间隔，至少 1 秒，必须是整秒 |

cron 表达式的完整语法（包括 `L`、`W`、`#` 等）见 [gronx](https://github.com/adhocore/gronx#cron-expression)。`@every` 按 Unix 时间对齐，例如 `@every 15s` 总是在第 0、15、30、45 秒触发，与进程何时启动无关，因此所有实例算出的触发时刻都一样。

表达式不合法、名称重复或过长时，`Add` 直接 panic，应用在启动时就会发现。

<a id="timezones"></a>
### 时区

cron 表达式按 `time.Local` 计算，Go 程序从环境变量 `TZ` 读取它，例如 `TZ=Asia/Shanghai`。容器镜像通常是 UTC，单个任务需要按其他时区计算时，使用 `schedule.In`：

```go
shanghai, err := time.LoadLocation("Asia/Shanghai")
// ...
sched.Add("reports:daily", "0 3 * * *", reports.Generate, schedule.In(shanghai))
```

<a id="running-the-scheduler"></a>
## 运行调度器

调度器是进程的一个运行部分（见[多个运行部分](../concepts/dependencies.md#running-parts)）。在入口的 `bootstrap` 包中，`a.locker` 是入口创建的[原子锁](../libraries/locks.md#postgres-store)。注册任务单独写在 `tasks` 中，调度器运行它，[查看任务](#listing-tasks)的命令和后台页面也用它，任务只定义一次：

```go
// tasks 创建调度器并注册全部定时任务。
func (a *App) tasks() *schedule.Scheduler {
    sched := schedule.New(a.locker, a.cfg.Schedule, nil)
    article.RegisterTasks(sched, a.db)
    sched.Add("queue:prune-failed", "@daily", schedule.Cleanup(a.failed.Cleanup(30*24*time.Hour), 1000),
        schedule.Describe("删除 30 天前的失败任务"))
    return sched
}

// Scheduler 装配调度器，返回的函数按时执行定时任务，直到 ctx 被取消。
func (a *App) Scheduler(*command.Daemon) (func(context.Context) error, error) {
    return a.tasks().Run, nil
}
```

`Run` 每秒检查一次到期的任务，每个任务在自己的 goroutine 中执行，一个任务执行很久时，其他任务照常按时触发。

业务模块的定时任务由模块的 `RegisterTasks` 登记，见[定时任务](../concepts/actions.md#as-scheduled-tasks)；框架数据的清理这类不属于某个模块的任务直接写在 `tasks` 中。最后把调度器登记为 `Parts` 中的一个部分，`serve` 就会运行它，`serve scheduler` 只运行它，见[serve：运行常驻进程](../general/commands.md#serve-command)：

```go
{Name: "scheduler", Short: "执行定时任务", Start: a.Scheduler},
```

单独部署调度器时不需要健康检查，见[部署](../installation/deployment.md#health-checks)。

<a id="multiple-instances"></a>
## 多个实例

部署多个实例时，每个实例上的调度器都会在同一时刻触发同一个任务。调度器在执行之前取得一把锁，锁名包含任务名和触发时刻，例如 `schedule:queue:prune-failed:1794366000`，只有取得锁的实例执行这一次触发。锁一分钟后自然过期，所以各实例的时钟偏差小于一分钟时，每次触发只执行一次。

所有进程共用同一个[锁存储](../libraries/locks.md#postgres-store)，去重就覆盖全部实例。单机部署也一样：[不停机升级](../installation/operations.md#upgrade-overlap)时新旧两个调度器会同时运行。每次触发留下一行过期的锁，用 `pglock` 的 `Cleanup` 定时删除，见 [PostgreSQL 存储](../libraries/locks.md#postgres-store)。

只想在每个实例上各执行一次的任务，例如清理本机的临时目录，使用 `schedule.EveryInstance()`：

```go
sched.Add("tmp:cleanup", "@hourly", cleanTmpDir, schedule.EveryInstance())
```

<a id="preventing-overlaps"></a>
## 防止重叠

任务上一次还没结束时，下一次触发会被跳过并记录一条 `info` 日志，不论上一次在哪个实例上执行。调度器在任务执行期间持有锁 `schedule:<任务名>:running`，每 20 秒续期一次，持有它的实例崩溃时，锁在一分钟内过期。

确实需要并行执行的任务，使用 `schedule.AllowOverlap()`。

一个任务卡住（例如一直等待一个外部请求）时，它会一直持有这把锁，之后的触发都被跳过。`schedule.Timeout` 限制每次执行的时长：

```go
sched.Add("reports:daily", "0 3 * * *", reports.Generate, schedule.Timeout(30*time.Minute))
```

超过时长时，任务的 `ctx` 以 `schedule.ErrTimeout` 取消，这次执行记为失败，锁随之释放，下一次触发照常执行。任务要把 `ctx` 传给数据库查询、HTTP 请求等调用，才能及时结束；不理会 `ctx` 的任务会一直运行到自己返回。

<a id="failures-and-shutdown"></a>
## 失败与停止

任务返回错误或 panic 时，调度器记录一条 `error` 日志，等下一次照常触发。需要重试的工作，在任务中把它[发布到队列](queue.md#publishing-jobs)：

```go
sched.Add("reports:daily", "0 3 * * *", func(ctx context.Context) error {
    return report.Generate.Publish(ctx, a.db, time.Now().AddDate(0, 0, -1)) // queue.Topic[time.Time]
})
```

队列负责重试、记录失败，耗时的工作也在 worker 中执行。

调度器的日志带有 `component=schedule` 字段，任务的 `ctx` 中带有 `task` 字段，用 `slog.InfoContext(ctx, ...)` 记录的日志会自动带上它。

调度器只补一分钟以内的触发：进程暂停或调度落后不到一分钟时，错过的触发会补上，同一个任务只补一次；部署、重启期间错过的触发直接跳过。收到停止信号后，调度器停止触发新任务，等待执行中的任务结束，最多等待 `shutdown_timeout`，超时后取消它们的 `ctx`。这个时间在应用配置的调度段中设置：

```go
type Config struct {
    // ...
    Schedule schedule.Config `yaml:"schedule"`
}
```

```yaml
schedule:
  shutdown_timeout: 30s # 默认值；为 0 时立即取消执行中的任务
```

<a id="listing-tasks"></a>
## 查看任务

`Tasks` 返回注册的全部任务：名称、说明、表达式、时区和选项（包括 `Timeout`）。它只读取任务的定义，所以在任何进程中都可以调用：

```go
for _, t := range a.tasks().Tasks() {
    t.Name, t.Description, t.Expr  // "reports:daily"、"生成前一天的报表"、"0 3 * * *"
    t.Next(time.Now())             // 下一次执行的时间
    t.Prev(time.Now())             // 上一次应当执行的时间
}
```

`Next` 和 `Prev` 按表达式计算，结果与调度器实际触发的时刻一致；`@every` 按 Unix 时间对齐，与调度器何时启动无关。

`Running` 报告每个任务此刻是否正在执行，在任何一个实例上都算。它查询任务执行期间持有的锁 `schedule:<任务名>:running`，所以同样可以在任何进程中调用，只要和调度器共用锁存储：

```go
running, err := sched.Running(ctx) // 任务名 → 是否正在执行
running["reports:daily"]
```

使用 `EveryInstance` 或 `AllowOverlap` 的任务不在锁存储中登记执行状态，不出现在结果中。

<a id="schedules-command"></a>
### 列出任务的命令

`schedulecmd.New` 返回 `schedules` 命令，列出全部任务。它和其他需要数据库的命令一样在 [bootstrap.Commands](../concepts/actions.md#as-commands) 中声明，传入的是注册任务的方法 `a.tasks`：声明命令时 `App` 还没有装配，执行时才调用它：

```go
schedulecmd.New(a.tasks),
```

执行 `./bin/blog schedules`：

```text
TASK                        EXPRESSION    NEXT RUN             IN        RUNNING  DESCRIPTION
articles:publish-scheduled  * * * * *     2026-11-11 17:01:00  42s       no       发布到了预定时间的文章
locks:cleanup               @hourly       2026-11-11 18:00:00  59m42s    yes      删除过期的锁
queue:prune-failed          @daily        2026-11-12 00:00:00  6h59m42s  no       删除 30 天前的失败任务
```

`RUNNING` 是任务此刻是否在任何一个实例上执行；使用 `EveryInstance` 或 `AllowOverlap` 的任务不登记执行状态，显示为 `-`。

<a id="run-history"></a>
### 执行记录

上一次什么时候执行、结果如何，是运行时的数据：部署多个实例时，每次触发由先取得锁的实例执行。`schedule/scheduleapi` 把它们写进 `schedule_runs` 表：调度器加上 `scheduleapi.Observer`，每次执行结束、被跳过时写一行，由其他实例执行的触发不报告，所以每次执行只记一行。表的迁移由 `scheduleapi.Migrations()` [发布](../database/migrations.md)：

```go
scheduleapi.Migrations()
```

```go
func (a *App) tasks() *schedule.Scheduler {
    sched := schedule.New(a.locker, a.cfg.Schedule, nil, schedule.WithObserver(scheduleapi.Observer(a.db)))
    // ……
    sched.Add("schedule:prune-runs", "@daily", schedule.Cleanup(scheduleapi.Cleanup(a.db, 7*24*time.Hour), 1000),
        schedule.Describe("删除 7 天前的执行记录"))
    return sched
}
```

执行记录要定时清理，`scheduleapi.Cleanup(db, 保留期)` 见[清理框架数据](#framework-cleanups)。

想把执行事件送到别处（例如指标、告警）时，自己写一个 `schedule.Observer`，`WithObserver` 可以用多次：

| 事件 | 时机 |
| --- | --- |
| `TaskStarted` | 本实例开始执行 |
| `TaskSucceeded` | 执行完成 |
| `TaskFailed` | 返回错误或 panic，`Err` 是原因 |
| `TaskSkipped` | 应当在本实例执行却被跳过，`SkipReason` 是 `overlap`（上一次还没结束）或 `lock_failed`（锁存储出错） |

`Tick` 是这次触发的时刻，即任务应当执行的时间；`Started` 是实际开始的时间，两者之差是调度延迟。[立即执行](#running-manually)的事件 `Manual` 为 `true`，`Tick` 为零值。结束事件在释放执行锁之后发出，Observer 收到它时 `Running` 已经返回 `false`。Observer 在执行任务的 goroutine 中同步调用，写一行数据库很快，更重的工作请缓冲后批量处理。

<a id="admin-pages"></a>
### 后台页面

`scheduleapi.Register` 在 HTTP 进程中注册定时任务的接口，`@go-tenon/ui` 的 `SchedulePanel` 是现成的页面。`protect` 保护全部接口，通常要求一个运维权限：

```go
scheduleapi.Register(api, rest.RequireToken(api, tokens.Validate, "jobs:manage"), a.db, a.tasks)
```

| 接口 | 用途 |
| --- | --- |
| `GET /schedules`（`schedule-list`） | 全部任务：说明、表达式、下一次执行、是否正在执行、上一次的结果和耗时、是否错过了触发 |
| `POST /schedules/{name}/run`（`schedule-run`） | [立即执行](#running-manually)，返回 202 |
| `GET /schedules/runs`（`schedule-runs`） | 执行记录，按任务（`task`）、结果（`status`）筛选，分页 |

`a.tasks` 只用来读取任务的定义和执行状态，HTTP 进程不运行调度器。应当执行的时间过去了一分钟，这次触发却没有执行记录，也没有正在执行，任务标为错过（`missed`），例如调度器停止或部署期间；从来没有按表达式执行过的任务不算。

页面接收生成的客户端分组 `api.schedule`：

```tsx
export const Route = createFileRoute("/_app/schedules")({
  staticData: { title: "定时任务", icon: "clock", permission: "jobs:manage" },
  component: () => <SchedulePanel api={api.schedule} />,
})
```

它分成“任务”和“执行记录”两个页内标签：任务列表上可以立即执行，点任务名打开它的执行记录；执行记录按任务、结果筛选，点失败的错误看完整的错误。有任务错过了触发时，标签上面显示提示。

<a id="running-manually"></a>
### 立即执行

页面上的“立即执行”把任务名[发布到队列](queue.md#publishing-jobs)（`scheduleapi.RunNow`），由 worker 执行：耗时的任务不占用请求，HTTP 实例也可能有很多个。worker 进程注册它的处理函数：

```go
scheduleapi.HandleRunNow(w, a.tasks)
```

处理函数调用 `Scheduler.RunNow`：不按表达式，也不参与同一次触发的去重，执行的事件照常交给 Observer，执行记录中标为手动。没有 `AllowOverlap` 的任务照样[防止重叠](#preventing-overlaps)：正在任何一个实例上执行时这次不执行，记一行跳过。任务失败不重试，和按表达式触发一样等下一次。

<a id="framework-cleanups"></a>
## 清理框架数据

框架中保存在数据库里的数据都提供 `func(ctx, limit)` 形式的清理函数，每次最多删除 `limit` 条过期的行：有固定过期时间的是 `Cleanup` 方法本身，按保留期删除的由 `Cleanup(保留期)` 返回。`schedule.Cleanup` 把它包装成任务：反复调用，直到一次删除的数量少于 `limit`。

| 数据 | 清理方法 | 建议频率 |
| --- | --- | --- |
| [API 令牌](../security/api-tokens.md#cleanup) | `apitoken.Store` 的 `Cleanup` | 每小时 |
| [登录验证码](../security/login.md#cleanup) | 每套账号的 `login.Realm` 的 `Cleanup` | 每小时 |
| [原子锁](../libraries/locks.md#postgres-store) | `pglock.New(db)` 的 `Cleanup` | 每小时 |
| [限流计数](../libraries/rate-limiting.md#postgres-limiter) | 任意一个 `pglimit.New(…)` 的 `Cleanup`（删除所有限流器的） | 每小时 |
| [未绑定的 passkey](../security/login-security.md#passkeys) | `passkey.Store` 的 `Cleanup` | 每天 |
| [文件](../libraries/filesystem.md#cleanup) | 文件服务的 `Cleanup` | 每 5 分钟 |
| [操作日志](../features/activity.md#retention) | `activity.Cleanup(db, 保留期)` | 每天 |
| [通知](../features/notifications.md#retention) | `notification.Cleanup(db, 保留期)` | 每天 |
| [推送设备](../features/push.md#cleanup) | `push.Cleanup(db, 保留期)` | 每天 |
| [后台操作](../features/operations.md#cleanup)（包括导入和导出） | `operation.New(db)` 的 `Cleanup(保留期)` | 每天 |
| [OAuth 授权码](../security/oauth-server.md#assembling) | `oauthserver.New(…)` 的 `Cleanup` | 每小时 |
| [OAuth 动态注册的客户端](../security/oauth-server.md#dynamic-registration) | `oauthserver.New(…)` 的 `CleanupClients(保留期)` | 每天 |
| [Webhook 投递记录](../features/webhooks.md#retention) | `webhook.Cleanup(db, 保留期)` | 每天 |
| [失败任务](queue-monitoring.md#failed-jobs) | `queue.NewFailedJobs(db).Cleanup(保留期)` | 每天 |
| [定时任务的执行记录](#run-history) | `scheduleapi.Cleanup(db, 保留期)` | 每天 |
| [冷静期已过的注销账号](../features/privacy.md#erasing) | 每套账号体系的 `privacy.New(…)` 的 `EraseDue` | 每 10 分钟 |

在线成员保存在 NATS 中，过期的记录由 NATS 自动删除。

<a id="testing"></a>
## 测试

`RunDue` 执行在指定时刻到期的任务，等它们结束后返回合并的错误，用来确定性地验证调度配置。[`locktest.NewStore()`](../libraries/locks.md#testing) 是测试用的内存锁存储：

```go
func TestDailyReport(t *testing.T) {
    sched := schedule.New(lock.New(locktest.NewStore()), schedule.DefaultConfig(), nil)
    var ran bool
    sched.Add("reports:daily", "0 3 * * *", func(context.Context) error {
        ran = true
        return nil
    }, schedule.In(time.UTC))

    sched.RunDue(ctx, time.Date(2026, 11, 11, 2, 59, 0, 0, time.UTC))
    if ran {
        t.Fatal("ran before 03:00")
    }
    if err := sched.RunDue(ctx, time.Date(2026, 11, 11, 3, 0, 0, 0, time.UTC)); err != nil || !ran {
        t.Fatalf("ran=%v err=%v", ran, err)
    }
}
```

任务本身是普通的函数，直接调用它测试即可。

<a id="troubleshooting"></a>
## 常见问题

**任务没有按时执行**

检查调度器是否在运行（`serve` 或 `serve scheduler`）。再看时区：表达式按 `TZ` 环境变量计算，容器中通常是 UTC，见[时区](#timezones)。

**任务每次都被跳过，日志中提示上一次还没结束**

上一次执行卡住了，一直持有锁。用 `schedule.Timeout` 限制每次执行的时长，见[防止重叠](#preventing-overlaps)。

**部署了几个实例，同一次触发执行了多次**

各实例没有共用同一个锁存储，或者时钟偏差超过了一分钟，见[多个实例](#multiple-instances)。

**启动时 panic：`schedule: invalid cron expression`**

表达式写错了，例如字段的个数不对、取值超出范围。5 段或 6 段的写法都可以，6 段时第一段是秒；`@every` 的间隔必须是整秒，见[表达式](#expressions)。

<a id="api-reference"></a>
## API 参考

包 `github.com/go-tenon/tenon/schedule`。

### New

```go
func New(locker *lock.Locker, cfg Config, logger *slog.Logger, opts ...SchedulerOption) *Scheduler
func WithTelemetry(mp metric.MeterProvider, tp trace.TracerProvider) SchedulerOption
func WithObserver(o Observer) SchedulerOption
```

`locker` 用于在多个实例之间去重。`WithTelemetry` 见[可观测性](../general/telemetry.md#schedule)；`WithObserver` 接收每次执行的事件，见[执行记录](#run-history)。

### Scheduler

```go
func (s *Scheduler) Add(name, expr string, run func(ctx context.Context) error, opts ...TaskOption)
func (s *Scheduler) Run(ctx context.Context) error
func (s *Scheduler) RunDue(ctx context.Context, t time.Time) error
func (s *Scheduler) Tasks() []Task
func (s *Scheduler) Running(ctx context.Context) (map[string]bool, error)
```

`Add` 注册任务，名称为空或重复、表达式无效，或在 `Run` 之后调用时 panic。`Run` 每秒检查到期的任务。`RunDue` 执行在时刻 `t` 到期的任务，用于测试。`Tasks` 和 `Running` 供[查看任务](#listing-tasks)使用。

### Scheduler.Add

```go
func (s *Scheduler) Add(name, expr string, run func(ctx context.Context) error, opts ...TaskOption)
```

- **参数**
    - `name`：任务名，在调度器中唯一，也用作锁名的一部分
    - `expr`：cron 表达式，见[表达式](#expressions)
    - `run`：要执行的函数
    - `opts`：见下面的 `TaskOption`

名称为空或重复、表达式无效，或者在 `Run` 之后调用时 panic。

### TaskOption

| 选项 | 说明 |
| --- | --- |
| `Describe(description string)` | 任务的说明 |
| `In(loc *time.Location)` | 按这个时区解释表达式，见[时区](#timezones) |
| `Timeout(d time.Duration)` | 执行超时，超过后取消任务的 `ctx`，记为 `ErrTimeout` |
| `AllowOverlap()` | 上一次还没结束时也触发，见[防止重叠](#preventing-overlaps) |
| `EveryInstance()` | 每个实例都执行，跳过锁 |

### Cleanup

```go
func Cleanup(fn func(ctx context.Context, limit int) (int, error), limit int) func(ctx context.Context) error
```

把分批删除的函数（例如 `FailedJobs.Cleanup`）包装成任务：每批最多 `limit` 行，删到没有为止，见[清理框架数据](#framework-cleanups)。
