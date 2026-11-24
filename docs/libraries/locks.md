# 原子锁

带过期时间的原子锁，让同一段逻辑在同一时刻只在一个实例上执行，例如多个实例都会触发的报表生成。持有者崩溃时锁到期自动失效。

包：`lock`、`lock/pglock`、`lock/locktest`。

- 锁只决定谁来执行。并发修改同一行数据用带条件的更新或 `SELECT ... FOR UPDATE`，由数据库保证一致。
- 所有进程必须共用同一个锁存储，单机部署也一样：[不停机升级](../installation/operations.md#upgrade-overlap)时新旧进程同时运行。

<a id="example-usage"></a>
## 示例

```go
locker := lock.New(pglock.New(db))

err := locker.Do(ctx, "reports:daily", time.Minute, func(ctx context.Context) error {
    return buildDailyReport(ctx) // 同一时刻只有一个实例在执行
})
if errors.Is(err, lock.ErrNotAcquired) {
    return nil // 其他实例正在执行
}
```

`db` 是入口打开的 `*bun.DB`。

<a id="assembling-locks"></a>
## 装配

1. 把 `pglock.Migrations()` 加入[发布命令](../database/migrations.md#publishing-framework-migrations)，发布后执行迁移，建出 `locks` 表。
2. 在入口创建一个 `*lock.Locker`，传给需要它的业务动作：

```go
locker := lock.New(pglock.New(db))
publishScheduled := article.NewPublishScheduled(db, locker)
```

<a id="running-once"></a>
## 只让一个实例执行：`Do`

`Do` 取得锁后执行函数，结束后释放锁；锁被占用时不执行函数，返回 `lock.ErrNotAcquired`。下面是发布到期文章的业务动作，每个实例都定时调用它，`a.db`、`a.locker` 由构造函数传入：

```go
func (a *PublishScheduled) Handle(ctx context.Context) error {
    err := a.locker.Do(ctx, "articles:publish-scheduled", time.Minute, func(ctx context.Context) error {
        _, err := a.db.NewUpdate().Model((*Article)(nil)).
            Set("status = ?", StatusPublished).
            Set("updated_at = ?", time.Now()).
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

- 锁名用 `领域:动作`，按对象加锁时带上 ID，例如 `articles:42:render`。
- 释放锁独立于 `ctx`：请求或命令被取消后 `Do` 仍会释放锁。
- 交给[调度器](../background/scheduling.md)的定时任务不需要锁，调度器保证每次触发只在一个实例上执行。

<a id="waiting-for-locks"></a>
## 等待锁：`Acquire`、`Wait`

`Acquire` 只尝试一次，锁被占用时立即返回 `lock.ErrNotAcquired`；`Wait` 在 `timeout` 内按固定间隔重试，`ctx` 取消时返回 `ctx` 的错误。两者返回 `*lock.Lock`，用完调用 `Release`。

站点地图过期时，多个请求只让一个重新生成，其他请求等它完成后直接用结果。`locker` 是入口创建的 `*lock.Locker`：

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

取得锁之后再检查一次：等待的请求拿到锁时，前一个请求通常已经生成好了。缓存的并发加载由 [`cache`](cache.md#hot-keys) 合并，不需要锁。

<a id="lock-ttl"></a>
## 锁的有效期与续期：`Refresh`

`ttl` 参数是锁的有效期，过期的锁可以被其他持有者取得，所以 `ttl` 要明显长于任务的执行时间。执行时间难以预估的长任务定期续期，`lk` 是 `Acquire` 或 `Wait` 返回的锁：

```go
if err := lk.Refresh(ctx, time.Minute); err != nil {
    return err // lock.ErrNotHeld：锁已过期并被他人取得，停止执行
}
```

- 锁过期并被他人取得后，原持有者的 `Refresh`、`Release` 返回 `lock.ErrNotHeld`。`Do` 的函数超过 `ttl` 时返回值同样包含 `ErrNotHeld`，这时调大 `ttl` 或在函数中续期。
- `locker.Held(ctx, name)` 只读取锁此刻是否被持有，用于后台页面展示状态。判断能否执行用 `Acquire`。

<a id="postgres-store"></a>
## PostgreSQL 存储：`pglock`

`pglock` 把每把锁保存为 `locks` 表的一行。每个操作是一条语句，并发取得同一把锁时只有一方成功。

- 过期按应用服务器的时钟计算，各实例的时钟偏差要远小于锁的有效期，否则两个实例可能同时持有。
- 过期的行等同于不存在，下一次取得时直接接管。锁名不重复使用时（例如按对象或日期动态生成的锁名），过期的行留在表中，用 `Cleanup` 定时删除：

```go
locks := pglock.New(db)
sched.Add("locks:cleanup", "@hourly", schedule.Cleanup(locks.Cleanup, 1000),
    schedule.Describe("删除过期的锁"))
```

`db` 是入口打开的 `*bun.DB`，`sched` 是入口创建的[调度器](../background/scheduling.md)。

<a id="other-stores"></a>
## 实现其他锁存储

实现 `lock.Store` 接口。每个方法必须是原子的：`Refresh` 和 `Release` 比较 `owner` 和修改在一个原子操作中完成。用 `locktest.TestStore(t, newStore)` 检查实现符合约定。

<a id="testing"></a>
## 测试

`locktest.NewStore()` 是内存锁存储，只在同一个进程内互斥，只用于测试：

```go
locker := lock.New(locktest.NewStore())
```
