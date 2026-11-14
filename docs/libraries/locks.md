# 原子锁

原子锁保证同一段逻辑在同一时刻只有一个持有者在执行。

只部署一个实例时，你很少需要考虑这个问题。但部署多个实例之后，同一段逻辑可能在几个实例上同时执行：比如每个实例都会触发“生成站点地图”，结果同一份工作做了好几遍，甚至互相覆盖。原子锁让其中一个实例取得锁并执行，其他实例发现锁已被占用，直接跳过。

`lock` 包提供的锁都有过期时间：取得锁之后，在有效期内独占它；如果持有者在执行中崩溃，锁到期后会自动失效。

> [!NOTE]
> 锁只负责“谁来执行”。如果你要解决的是并发修改同一行数据，请优先使用带条件的更新或 `SELECT ... FOR UPDATE`，由数据库保证一致。

- [示例用法](#example-usage)
- [装配锁](#assembling-locks)
- [只让一个实例执行](#running-once)
- [等待锁](#waiting-for-locks)
- [锁的有效期](#lock-ttl)
- [Redis 存储](#redis-store)
- [其他存储](#other-stores)
- [测试](#testing)
- [API 参考](#api-reference)

<a id="example-usage"></a>
## 示例用法

下面的代码保证每天的报表只由一个实例生成：

```go
locker := lock.New(redislock.New(rdb))

err := locker.Do(ctx, "reports:daily", time.Minute, func(ctx context.Context) error {
    return buildDailyReport(ctx) // 同一时刻只有一个实例在执行
})
if errors.Is(err, lock.ErrNotAcquired) {
    return nil // 其他实例正在执行
}
```

`Do` 先尝试取得名为 `reports:daily` 的锁，有效期一分钟。取得了，就执行传入的函数，结束后释放锁；锁已经被别的实例占用时，它不执行函数，而是返回 `lock.ErrNotAcquired`。

<a id="assembling-locks"></a>
## 装配锁

在入口创建 `Locker`，传给需要它的业务动作：

```go
import (
    "github.com/go-tenon/tenon/lock"
    "github.com/go-tenon/tenon/lock/redislock"
)

locker := lock.New(redislock.New(rdb))
publishScheduled := article.NewPublishScheduled(db, locker)
```

`lock.New` 接收锁存储，生产环境使用 [Redis 存储](#redis-store)，`rdb` 是[入口中创建的客户端](redis.md#connecting)。所有进程共用同一个存储才能互斥，单机部署也是如此：[不停机升级](../installation/deployment.md#upgrade-overlap)时新旧进程会同时运行。

<a id="running-once"></a>
## 只让一个实例执行

`Do` 取得锁后执行函数，结束后释放锁。锁已被其他持有者占用时不执行函数，返回 `lock.ErrNotAcquired`。例如发布到期文章的 `PublishScheduled`：

```go
func (a *PublishScheduled) Handle(ctx context.Context) error {
    err := a.locker.Do(ctx, "articles:publish-scheduled", time.Minute, func(ctx context.Context) error {
        _, err := a.db.NewUpdate().Model((*Article)(nil)).
            Set("status = ?", StatusPublished).
            Where("status = ?", StatusScheduled).
            Where("publish_at <= ?", time.Now()).
            Exec(ctx)
        return err
    })
    if errors.Is(err, lock.ErrNotAcquired) {
        return nil // 其他实例正在执行
    }
    return err
}
```

锁名是任意字符串，建议用 `领域:动作` 的形式，需要按对象加锁时带上 ID，例如 `articles:42:render`。

释放锁独立于 `ctx`：请求或命令被取消后，`Do` 仍会释放锁。

> [!TIP]
> 按时触发的任务交给[调度器](scheduling.md)执行时，调度器已经保证每次触发只在一个实例上执行，任务里直接执行即可。锁适合调度器之外的场景，例如下面的重新生成站点地图。

<a id="waiting-for-locks"></a>
## 等待锁

`Acquire` 尝试取得锁，失败时立即返回 `lock.ErrNotAcquired`；`Wait` 在锁被占用时最多等待一段时间。两者都返回 `*lock.Lock`，用完后调用 `Release`。

例如站点地图生成得很慢，多个请求同时发现它过期时，只让一个请求重新生成，其他请求等它完成后直接使用结果：

```go
lk, err := locker.Wait(ctx, "sitemap:rebuild", time.Minute, 3*time.Second)
if err != nil {
    return err // 3 秒内没等到锁时是 lock.ErrNotAcquired
}
defer lk.Release(ctx)
if fresh, err := sitemapFresh(ctx); err != nil || fresh {
    return err // 等待期间已由其他请求生成
}
return generateSitemap(ctx)
```

取得锁之后要再检查一次：等待的请求拿到锁时，前一个请求通常已经生成好了。读缓存时同一个进程内的并发加载由 [cachex](cache.md#hot-keys) 合并。

`Wait` 每 100 毫秒重试一次，`ctx` 取消时立即返回 `ctx` 的错误。

<a id="lock-ttl"></a>
## 锁的有效期

`Do`、`Acquire` 和 `Wait` 的 `ttl` 参数是锁的有效期。超过有效期的锁可以被其他持有者取得，因此 `ttl` 应明显长于任务的执行时间。

执行时间难以预估的长任务可以定期续期，把有效期重设为从现在起的 `ttl`：

```go
if err := lk.Refresh(ctx, time.Minute); err != nil {
    return err // lock.ErrNotHeld：锁已过期并被他人取得，应停止执行
}
```

锁过期并被他人取得后，原持有者的 `Refresh` 和 `Release` 都返回 `lock.ErrNotHeld`，新的持有者照常持有。`Do` 在函数返回后释放锁，此时得到 `ErrNotHeld` 也会一并返回，提示任务超过了有效期。

`locker.Held(ctx, name)` 只读取锁此刻是否被持有，适合在后台页面展示状态，例如[定时任务是否正在执行](scheduling.md#listing-tasks)。它的结果只反映查询的那一刻，判断能否执行仍用 `Acquire`。

<a id="redis-store"></a>
## Redis 存储

每把锁是一个 Redis 键：键名就是锁名，值是持有者标识，键的过期时间就是锁的有效期。过期由 Redis 计算，与各实例的时钟无关；过期的键由 Redis 自动删除。多个应用共用一个 Redis 时，在锁名前加上应用的命名空间，例如 `app:articles:publish-scheduled`。Redis 必须配置 `noeviction`，锁才能在内存紧张时保留下来，见 [Redis](redis.md)。

锁只写在一个 Redis 主节点上。使用哨兵或集群时，主节点故障切换可能丢失刚写入的锁，短时间内出现两个持有者；对互斥要求严格的操作，请在数据库事务中用 `SELECT ... FOR UPDATE` 锁住相关的行。

<a id="other-stores"></a>
## 其他存储

锁存储是一个接口，每个方法都必须是原子的：

```go
type Store interface {
    Acquire(ctx context.Context, name, owner string, ttl time.Duration) (bool, error)
    Refresh(ctx context.Context, name, owner string, ttl time.Duration) (bool, error)
    Release(ctx context.Context, name, owner string) (bool, error)
    Held(ctx context.Context, name string) (bool, error) // 是否被持有且未过期
}
```

实现时可以参考 [`lock/redislock`](../../lock/redislock/redislock.go)：`Refresh` 和 `Release` 需要先比较 `owner` 再修改，两步必须在一个原子操作中完成。`locktest.TestStore(t, newStore)` 检查实现是否符合这些约定，框架自己的存储也用它测试。

<a id="testing"></a>
## 测试

`locktest.NewStore()` 是测试用的内存存储，只在同一个进程内互斥：

```go
import "github.com/go-tenon/tenon/lock/locktest"

locker := lock.New(locktest.NewStore())
```

它只用于测试。生产环境中同时运行的进程，包括不停机升级时的新旧进程，必须共用 Redis 存储。

<a id="api-reference"></a>
## API 参考

包 `github.com/go-tenon/tenon/lock`。

### New

```go
func New(store Store) *Locker
```

### Locker.Do

```go
func (l *Locker) Do(ctx context.Context, name string, ttl time.Duration, fn func(ctx context.Context) error) error
```

- **参数**
    - `name`：锁名，例如 `articles:publish-scheduled`
    - `ttl`：锁的有效期，应当比 `fn` 预计的执行时间长
    - `fn`：取得锁之后执行的函数
- **返回**：`fn` 返回的错误；锁被占用时返回 `ErrNotAcquired`；`fn` 执行超过 `ttl`、锁已被别人取得时，返回值包含 `ErrNotHeld`

释放锁独立于 `ctx`。

### Locker

```go
func (l *Locker) Do(ctx context.Context, name string, ttl time.Duration, fn func(ctx context.Context) error) error
func (l *Locker) Acquire(ctx context.Context, name string, ttl time.Duration) (*Lock, error)
func (l *Locker) Wait(ctx context.Context, name string, ttl, timeout time.Duration) (*Lock, error)
func (l *Locker) Held(ctx context.Context, name string) (bool, error)
```

`Do` 取得锁、执行 `fn`、释放锁；锁被占用时返回 `ErrNotAcquired`，不执行 `fn`；`fn` 超过 `ttl` 后锁可能已被别人取得，返回值包含 `ErrNotHeld`。`Acquire` 只尝试一次，`Wait` 在 `timeout` 内反复尝试。`Held` 报告锁当前是否被持有。

### Lock

```go
func (lk *Lock) Refresh(ctx context.Context, ttl time.Duration) error
func (lk *Lock) Release(ctx context.Context) error
func (lk *Lock) Name() string
```

锁已经过期并被别人取得时，`Refresh` 和 `Release` 返回 `ErrNotHeld`。

### 错误

| 错误 | 含义 |
| --- | --- |
| `ErrNotAcquired` | 锁正被其他持有者占用 |
| `ErrNotHeld` | 锁已过期，被释放或被其他持有者取得 |

### Store

```go
type Store interface {
    Acquire(ctx context.Context, name, owner string, ttl time.Duration) (bool, error)
    Refresh(ctx context.Context, name, owner string, ttl time.Duration) (bool, error)
    Release(ctx context.Context, name, owner string) (bool, error)
    Held(ctx context.Context, name string) (bool, error)
}
```

每个方法都必须是原子的。`redislock.New(rdb rueidis.Client)` 是 Redis 实现，`locktest` 提供测试用的内存存储和契约测试，见[其他存储](#other-stores)。
