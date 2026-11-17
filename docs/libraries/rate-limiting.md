# 限流

限流用来防止某个客户端或某项操作过于频繁，例如每个 IP 每秒最多 10 个请求，或者同一个邮箱每分钟最多尝试登录 5 次。没有限流的登录接口，可以被脚本以每秒上千次的速度猜测密码；没有限流的公开接口，一个失控的客户端就能拖垮整个服务。

`ratelimit` 包与传输方式无关：同一个限流器，既可以作为 HTTP 中间件按请求限流，也可以在处理器、命令或任务中直接调用。

阅读前需要了解：[中间件](../http/middleware.md)；多个实例共享计数时还需要[迁移](../database/migrations.md)。

- [示例用法](#example-usage)
- [限流器](#limiters)
- [限制请求频率](#http-middleware)
    - [限流的 key](#rate-limit-key)
- [在处理器中限流](#in-handlers)
- [PostgreSQL 限流器](#postgres-limiter)
- [常见问题](#troubleshooting)
- [API 参考](#api-reference)

<a id="example-usage"></a>
## 示例用法

下面限制同一个邮箱每分钟最多尝试登录 5 次：

```go
logins := pglimit.New(db, "login", ratelimit.Config{Rate: 5, Interval: time.Minute, Burst: 5})

if err := logins.Allow(ctx, email); err != nil {
    return err // ratelimit.ErrExceeded：响应 429，带 Retry-After
}
```

超出限制时，`Allow` 返回 `ratelimit.ErrExceeded`，接口照常返回它，客户端收到 `429` 和建议的等待时间。

也可以把限流器作为中间件，按客户端地址限制全部请求。每个请求都要经过它，用进程内的限流器：

```go
router.Use(httpx.RateLimit(ratelimit.NewMemory(cfg.HTTP.RateLimit), httpx.KeyByIP))
```

<a id="limiters"></a>
## 限流器

限流器只有一个方法：

```go
type Limiter interface {
    Allow(ctx context.Context, key string) error
}
```

每次调用为 `key` 消耗一次配额。配额用完时返回 `ratelimit.ErrExceeded`，错误中带有下一次调用可以成功的最短等待时间；存储出错时返回其他错误。

配额由 `ratelimit.Config` 描述：

```go
ratelimit.Config{
    Rate:     10,          // 每个 Interval 补充 10 次
    Interval: time.Second,
    Burst:    20,          // 最多连续 20 次
}
```

上面的配置表示：每个 key 平均每秒最多 10 次，短时间内最多连续 20 次。被拒绝的调用不消耗配额，按返回的等待时间重试即可。

框架提供两种实现：

| 实现 | 计数保存在 | 适合 |
| --- | --- | --- |
| `ratelimit.NewMemory(cfg)` | 进程内存，每个实例分别计数 | 每个请求都要经过的全局限流 |
| `pglimit.New(db, name, cfg)` | PostgreSQL，多个实例共享 | 登录、验证码、发邮件等需要多实例共享计数的低频操作 |

它们的算法相同，换实现时限流结果一致。

<a id="http-middleware"></a>
## 限制请求频率

`httpx.RateLimit` 把限流器安装为中间件。配额用完时返回 `429 Too Many Requests`，并通过 `Retry-After` 响应头告诉客户端多少秒后重试。限流不在默认中间件中，需要时在 `httpx.Defaults` 之后安装：

```go
router.Use(httpx.Defaults(cfg.HTTP)...)
router.Use(httpx.RateLimit(ratelimit.NewMemory(cfg.HTTP.RateLimit), httpx.KeyByIP))
```

`cfg.HTTP.RateLimit` 来自 `http.rate_limit` 配置段，下面是它的默认值：

```yaml
http:
  rate_limit:
    rate: 10
    interval: 1s
    burst: 20
```

内存限流器会自动删除已经回满的 key，内存占用只与最近活跃的客户端数量有关。

限流器返回存储错误时，中间件返回 `503`，并把错误写入访问日志的 `error` 字段。

<a id="rate-limit-key"></a>
### 限流的 key

`RateLimit` 的第二个参数决定按什么维度计数。`httpx.KeyByIP` 按客户端地址计数，它使用 `ClientIP` 解析后的地址，所以部署在反向代理之后时，请同时配置[可信代理](../http/middleware.md#trusted-proxies)，否则所有请求都会被算到代理头上。

你也可以传入自己的函数。函数返回空字符串时，这个请求直接放行：

```go
skipHealth := func(r *http.Request) string {
    if r.URL.Path == "/livez" || r.URL.Path == "/readyz" {
        return ""
    }
    return httpx.KeyByIP(r)
}
router.Use(httpx.RateLimit(ratelimit.NewMemory(cfg.HTTP.RateLimit), skipHealth))
```

中间件运行在路由匹配和认证之前，只能使用请求本身携带的信息。key 要取客户端改不了的值，例如 IP：用 `Authorization` 等未经校验的请求头做 key 的话，客户端每次换一个值就能得到一份新配额。需要按用户限流时，请[在处理器中限流](#in-handlers)。

<a id="in-handlers"></a>
## 在处理器中限流

例如文章可以设置[访问口令](../security/hashing.md#hashing-passwords)。为了防止口令被暴力猜测，同一篇文章每分钟最多尝试 5 次。只需在校验口令的处理器开头调用限流器：

```go
func (h *Handlers) Unlock(w http.ResponseWriter, r *http.Request) error {
    if err := h.attempts.Allow(r.Context(), chi.URLParam(r, "id")); err != nil {
        return err
    }
    // ……校验口令
}
```

只想限制连续的失败时，在成功后调用 `Reset(ctx, key)`，让这个 key 回到配额全满的状态。[登录与注册](../security/login.md#limits)的密码登录就是这样做的：每次尝试前 `Allow`，登录成功后 `Reset`，所以已经内置了按账号的限流。

`ErrExceeded` 是分类为 `errorx.ResourceExhausted` 的业务错误，处理器直接返回即可：页面显示 `429` 错误页，带 `Retry-After` 响应头，提示文案按请求语言翻译。JSON API 的处理器同样直接返回它，响应 `429` 和错误码 `rate_limit.exceeded`。

入口中创建限流器并传给登录处理器：

```go
attempts := pglimit.New(db, "passcode", ratelimit.Config{Rate: 5, Interval: time.Minute, Burst: 5})
```

校验口令需要多个实例共享计数，否则攻击者把请求分散到各个实例，就能得到成倍的尝试次数，所以这里使用 [PostgreSQL 限流器](#postgres-limiter)。

<a id="postgres-limiter"></a>
## PostgreSQL 限流器

多个实例共享配额时使用 PostgreSQL 限流器。计数保存在 `rate_limits` 表中，使用前先[发布](../database/migrations.md#publishing-framework-migrations)它的迁移：

```go
pglimit.Migrations()
```

`pglimit.New` 接收应用的数据库连接池，第二个参数 `name` 区分不同的限流器：同名的限流器共享配额，所以每个 `name` 应该只对应一种配额。

```go
import "github.com/go-tenon/tenon/ratelimit/pglimit"

attempts := pglimit.New(db, "passcode", ratelimit.Config{Rate: 5, Interval: time.Minute, Burst: 5})
```

每个 key 是表中的一行，主键是 `(name, key)`，只保存一个时间（理论到达时间）。每次 `Allow` 是一条语句：同一个 key 的并发调用由行锁排队，不会多放行；时间按应用服务器的时钟计算，各实例的时钟需要同步。

每次 `Allow` 都写一次数据库，所以只用于登录、验证码、发邮件这类低频操作：每次登录尝试本来就要查询账号、校验密码，多一次写入可以忽略。每个请求都要经过的全局限流用内存限流器。

配额回满的 key 与不存在的 key 等价，它们的行用 `Cleanup` 定时删除。`Cleanup` 删除的是表中所有限流器的行：

```go
sched.Add("rate-limits:cleanup", "@hourly", schedule.Cleanup(attempts.Cleanup, 1000),
    schedule.Describe("删除配额已回满的限流计数"))
```

<a id="troubleshooting"></a>
## 常见问题

**所有用户共用了一份配额**

部署在反向代理之后，但没有配置可信代理，所有请求的地址都是代理的地址。配置 `client_ip.trusted_proxies`，见[限流的 key](#rate-limit-key)。

**部署了几个实例后，限流变宽松了**

内存限流器的计数只在本进程中，请求分散到几个实例，配额也成倍增加。需要共享计数时用 [PostgreSQL 限流器](#postgres-limiter)。

**数据库不可用时所有请求都返回 503**

全局限流用了 PostgreSQL 限流器，限流器出错时中间件返回 `503`。全局限流请用内存限流器，PostgreSQL 限流器只用于低频操作。

**登录成功后，用户仍然被限流**

只想限制连续的失败时，在成功后调用 `Reset`，见[在处理器中限流](#in-handlers)。

<a id="api-reference"></a>
## API 参考

### ratelimit.Limiter

```go
type Limiter interface {
    Allow(ctx context.Context, key string) error
}
```

- **参数**（`Allow`）
    - `key`：限流的对象，例如客户端地址或邮箱
- **返回**：配额用完时返回 `ErrExceeded`（分类 `ResourceExhausted`，带建议的等待时间，HTTP 响应 `429`）；存储出错时返回其他错误

每次调用为 `key` 消耗一次配额。

### ratelimit.Config

| 字段 | 说明 |
| --- | --- |
| `Rate int` | 每个 `Interval` 补充的配额 |
| `Interval time.Duration` | 补充 `Rate` 个配额的周期 |
| `Burst int` | 允许连续使用的最大配额，即令牌桶容量 |

### ratelimit.NewMemory

```go
func NewMemory(cfg Config) *Memory
```

进程内的限流器，计数不在实例之间共享。

### pglimit.New

```go
func New(db bun.IDB, name string, cfg ratelimit.Config) *Limiter
func Migrations() fs.FS
func (l *Limiter) Cleanup(ctx context.Context, limit int) (int, error)
```

- **参数**
    - `db`：数据库连接池，必须已执行 `Migrations` 中的迁移
    - `name`：限流器的名称，同名的限流器共享配额，例如 `login`
    - `cfg`：配额，见上面的 `Config`
- **返回**：限流器

包 `github.com/go-tenon/tenon/ratelimit/pglimit`。计数保存在 PostgreSQL 中，所有实例共享。`Cleanup` 删除最多 `limit` 个配额已经回满的 key（所有限流器的），返回删除的个数。

### ratelimit.Observe

```go
func Observe(mp metric.MeterProvider, name string, l Limiter) Limiter
```

包装限流器，报告 `ratelimit.requests` 指标，见[可观测性](../general/telemetry.md#ratelimit)。

### httpx.RateLimit

```go
func RateLimit(limiter ratelimit.Limiter, key KeyFunc) func(http.Handler) http.Handler
```

按 `key(r)` 限制请求的中间件，`httpx.KeyByIP` 按客户端地址。
