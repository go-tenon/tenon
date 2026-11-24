# 限流

按 key 限制操作的频率，例如同一个邮箱每分钟最多尝试登录 5 次。同一个限流器可以装成 HTTP 中间件，也可以在业务动作、命令和任务中直接调用。

包：`ratelimit`、`ratelimit/pglimit`。

<a id="example-usage"></a>
## 示例

```go
logins := pglimit.New(db, "login", ratelimit.Config{Rate: 5, Interval: time.Minute, Burst: 5})

if err := logins.Allow(ctx, email); err != nil {
    return err // ratelimit.ErrExceeded：响应 429，带 Retry-After
}
```

`db` 是入口打开的 `*bun.DB`。

<a id="limiters"></a>
## 选择限流器：内存或 PostgreSQL

`ratelimit.Limiter` 有两个方法：`Allow` 为 key 消耗一次配额，用完时返回 `ratelimit.ErrExceeded`，存储出错时返回其他错误；`Reset` 让 key 回到配额全满。配额由 `ratelimit.Config` 描述：

```go
ratelimit.Config{
    Rate:     10, // 每个 Interval 补充 10 次
    Interval: time.Second,
    Burst:    20, // 最多连续 20 次
}
```

被拒绝的调用不消耗配额。两种实现的算法相同：

| 实现 | 计数保存在 | 用于 |
| --- | --- | --- |
| `ratelimit.NewMemory(cfg)` | 进程内存，每个实例分别计数 | 每个请求都经过的全局限流 |
| `pglimit.New(db, name, cfg)` | PostgreSQL，所有实例共享 | 登录、验证码、发邮件这类低频操作 |

- 内存限流器的计数只在本进程中，部署几个实例配额就成倍放宽。要求严格的限制（例如猜口令）用 PostgreSQL 限流器，否则攻击者把请求分散到各个实例就能多试几倍。
- `cfg` 不合法时 `NewMemory` 和 `pglimit.New` panic。
- `ratelimit.Observe(meters, "login", logins)` 包装限流器，报告 `ratelimit.requests` 指标，见[可观测性](../general/telemetry.md#ratelimit)。

<a id="http-middleware"></a>
## 按 IP 或自定义 key 限制请求：`httpx.RateLimit`

安装位置和默认的 `http.rate_limit` 配置见[中间件：限流](../http/middleware.md#rate-limiting)。第二个参数从请求中取 key，`httpx.KeyByIP` 按客户端地址。函数返回空字符串时请求直接放行，例如跳过健康检查：

```go
skipHealth := func(r *http.Request) string {
    if r.URL.Path == "/livez" || r.URL.Path == "/readyz" {
        return ""
    }
    return httpx.KeyByIP(r)
}
router.Use(httpx.RateLimit(ratelimit.NewMemory(cfg.HTTP.RateLimit), skipHealth))
```

- 部署在反向代理之后时配置[可信代理](../http/middleware.md#trusted-proxies)，否则所有请求都按代理的地址计数，所有用户共用一份配额。
- 中间件在路由匹配和认证之前运行，key 取客户端改不了的值：用 `Authorization` 这类未经校验的请求头做 key，客户端每次换一个值就得到一份新配额。按用户限流时[在业务动作中限流](#in-handlers)。
- 全局限流用内存限流器：限流器出错时中间件返回 `503`，用 PostgreSQL 限流器时数据库不可用会让所有请求失败。

<a id="in-handlers"></a>
## 在业务动作中限流：`Allow`、`Reset`

在要保护的操作开头调用 `Allow`。例如文章的[访问口令](../security/encryption.md#hashing-passwords)，同一篇文章每分钟最多尝试 5 次，`a.attempts` 是构造函数传入的限流器：

```go
func (a *Unlock) Handle(ctx context.Context, id uuid.UUID, passcode string) error {
    if err := a.attempts.Allow(ctx, id.String()); err != nil {
        return err
    }
    // ……校验口令
}
```

入口中创建它，所有实例共享计数：

```go
attempts := pglimit.New(db, "passcode", ratelimit.Config{Rate: 5, Interval: time.Minute, Burst: 5})
```

- `ErrExceeded` 的分类是 `errorx.ResourceExhausted`，直接返回：JSON API 响应 `429`、错误码 `rate_limit.exceeded` 和 `Retry-After`，页面显示 `429` 错误页，提示按请求语言翻译。
- 只限制连续的失败时，成功后调用 `Reset(ctx, key)`。[登录](../security/login.md#limits)的密码登录就是这样做的，已经内置了按账号的限流。

<a id="postgres-limiter"></a>
## PostgreSQL 限流器：`pglimit`

1. 把 `pglimit.Migrations()` 加入[发布命令](../database/migrations.md#publishing-framework-migrations)，发布后执行迁移，建出 `rate_limits` 表。
2. 每种配额用一个 `name` 创建：同名的限流器共享配额，所以 `name` 与配额一一对应。

```go
attempts := pglimit.New(db, "passcode", ratelimit.Config{Rate: 5, Interval: time.Minute, Burst: 5})
```

- 每次 `Allow` 写一次数据库，只用于低频操作；每个请求都经过的限流用内存限流器。
- 同一个 key 的并发调用由行锁排队，放行的次数准确。时间按应用服务器的时钟计算，各实例的时钟需要同步。
- 配额回满的 key 等同于不存在，用 `pglimit.Cleanup(db)` 定时删除（覆盖表中全部限流器，只需登记一次）：

```go
sched.Add("rate-limits:cleanup", "@hourly", schedule.Cleanup(pglimit.Cleanup(db), 1000),
    schedule.Describe("删除配额已回满的限流计数"))
```

`sched` 是入口创建的[调度器](../background/scheduling.md)。
