# 数据库进阶

[快速入门](quick-start.md)介绍了日常需要的连接、模型、查询和事务。这一页介绍几种不是每个应用都需要的用法：

- **慢查询日志**：找出执行得慢的语句；
- **软删除**：删除时只做一个标记，数据仍然留在表中，可以恢复；
- **隔离级别与死锁**：多个事务同时修改同一批数据时会发生什么；
- **只读副本**：把只读的查询分给从库，减轻主库的压力；
- **多个数据库**：一个应用连接两个或更多数据库。

用到哪个读哪一节即可。

阅读前需要了解：[数据库：快速入门](quick-start.md)。

- [慢查询](#slow-queries)
- [软删除](#soft-deletes)
- [隔离级别与死锁](#isolation-and-deadlocks)
- [只读副本](#read-replicas)
- [多个数据库](#multiple-databases)

<a id="slow-queries"></a>
## 慢查询

执行时间达到 `slow_query`（默认 1 秒）的语句记录一条 `Warn` 日志：

```text
WARN slow query component=database duration=1.284s db.operation.name=SELECT db.collection.name=articles query="SELECT \"a\".\"id\", … FROM \"articles\" AS \"a\" WHERE (\"a\".\"author_id\" = ?) ORDER BY \"a\".\"id\" DESC LIMIT ?"
```

`query` 中的字符串、数字等字面量都换成了 `?`，日志只记录语句的形状，同一条语句不同参数的执行看起来一样，便于汇总。日志在执行语句的 `ctx` 中记录，带着请求 ID 和 `trace_id`，可以找到是哪个请求或任务执行的。日志器默认是 `slog.Default()`，用 `database.WithLogger` 指定其他的。

<a id="soft-deletes"></a>
## 软删除

如果删除后需要保留记录，可以使用 Bun 原生的[模型软删除](https://bun.uptrace.dev/guide/soft-deletes.html#using-bun-models)。上面 `DeletedAt` 的 `soft_delete` 标签启用该功能，`nullzero` 将 Go 的零时间映射为 SQL `NULL`；迁移中对应的 `deleted_at` 列应允许为空。

对于配置了该标签的模型，`NewDelete()` 会更新 `deleted_at`，普通模型查询会自动排除已软删除的记录。下面的 `id` 是目标文章的 UUID，`ctx` 是请求上下文，`db` 是打开的连接池：

```go
// 标记为已删除，保留记录。
_, err := db.NewDelete().
    Model((*Article)(nil)).
    Where("id = ?", id).
    Exec(ctx)

// 仅查询已软删除的文章。
var deleted []Article
err = db.NewSelect().Model(&deleted).WhereDeleted().Scan(ctx)

// 查询全部文章，包括已软删除的记录。
var all []Article
err = db.NewSelect().Model(&all).WhereAllWithDeleted().Scan(ctx)

// 物理删除目标文章，无论它是否已软删除。
_, err = db.NewDelete().
    Model((*Article)(nil)).
    Where("id = ?", id).
    ForceDelete().
    Exec(ctx)
```

软删除由 Bun 根据模型标签处理，直接执行原生 SQL 时需要自行处理 `deleted_at` 条件。上述时间戳钩子只处理插入和更新查询，`NewDelete()` 的软删除保持 `updated_at` 原值。

软删除的记录仍然参与数据库唯一约束。本例的 `articles_slug_key` 会继续保留已删除文章的 slug；如果业务允许复用，可以在迁移中把它改为只约束未删除记录的部分唯一索引：

```sql
CREATE UNIQUE INDEX articles_slug_key ON articles (slug) WHERE deleted_at IS NULL;
```

索引沿用原来的名字，[处理约束错误](quick-start.md#constraint-errors)的代码保持原样。

<a id="isolation-and-deadlocks"></a>
## 隔离级别与死锁

PostgreSQL 的默认隔离级别是 READ COMMITTED。“先读取、检查、再修改”的逻辑应当放在一个事务中，并用 `FOR UPDATE` 锁住要修改的行，见[在事务中检查](../security/authorization.md#checking-in-transactions)。

数据库检测到死锁时会中止其中一个事务，`database.IsDeadlock` 可以识别这个错误。被中止的事务已经整体回滚，登记的提交后回调也一并丢弃，可以直接重试：

```go
for attempt := 1; ; attempt++ {
    err := database.RunInTx(ctx, a.db, nil, publish)
    if database.IsDeadlock(err) && attempt < 3 {
        continue
    }
    return err
}
```

<a id="read-replicas"></a>
## 只读副本

读多写少的应用可以把一部分读取交给只读副本，减轻主库的压力。副本是主库之外的另一个连接池，用 `database.ReadOnly` 打开，在入口和主库一起创建：

```go
replica, err := database.Open(ctx, cfg.Replica, database.Name("replica"), database.ReadOnly())
```

`ReadOnly` 让副本的会话默认只读：误把写操作交给它时，立即得到 `database.IsReadOnly` 能识别的错误。`Name` 是连接池的名字，区分主库和副本。有多个副本时，在它们前面放一个负载均衡（云数据库的只读地址、HAProxy 等），`replica.dsn` 指向它。

哪些读取交给副本，由业务动作在构造函数中声明。文章列表能容忍几百毫秒的复制延迟，交给副本；发布文章要在事务中读取并修改，用主库：

```go
list := article.NewList(a.replica)
publish := article.NewPublish(a.db, a.emails)
```

副本上的数据比主库晚一点，选择时注意：

- **刚写入的数据可能还读不到。** 写完立即要读的地方用主库，例如修改后返回最新内容、标记已读后刷新未读数。
- **授权检查和事务中的读取用主库**，见[在事务中检查](../security/authorization.md#checking-in-transactions)。
- **框架的组件都用主库**：权限、token、功能开关、队列。`search.Search` 这类只读取的函数接收 `db` 参数，可以传入副本。
- 副本上运行很久的查询可能因为与复制冲突被取消，报表类的长查询可以在副本上开启 `hot_standby_feedback`。监控副本的复制延迟（`pg_stat_replication.replay_lag`）。

测试中用 `dbtest.Replica(t, db)` 代替副本：它连接同一个测试库，没有延迟，写入时失败，能发现误把写操作交给副本的代码。

<a id="multiple-databases"></a>
## 多个数据库

按模块拆分的数据库，例如把埋点事件放进单独的分析库，同样是一个连接池。在配置中加一段，在入口打开，传给这个模块的业务动作：

```yaml
analytics:
  dsn: ${ANALYTICS_DATABASE_URL}
```

```go
analytics, err := database.Open(ctx, cfg.Analytics, database.Name("analytics"))
track := events.NewTrack(analytics)
```

每个数据库有自己的迁移文件。`migratecmd.Name` 构造另一个数据库的迁移命令，挂在 `migrate` 下：

```go
mig := migratecmd.New(migrations.FS, func(c Config) database.Config { return c.Database })
mig.AddCommand(migratecmd.New(analyticsmigrations.FS, func(c Config) database.Config { return c.Analytics },
    migratecmd.Name("analytics")))
```

`migrate analytics up` 迁移分析库，`migrate analytics make add_index` 在 `migrations/analytics` 中新建文件。部署时两个数据库的迁移都要执行。

拆库之后要注意：

- **没有跨库的事务。** 两个库的修改需要一起成功时，在一个库的事务中发布[任务](../libraries/queue.md)，由任务修改另一个库，任务至少执行一次。
- **任务在哪个库发布，就要有一个用这个库的 worker 转发**：队列把任务写进发布它的事务所在数据库的 WAL。通常只在主库发布任务；某个模块必须在自己的库中发布时，`work` 进程为这个库再创建一个 `queue.Worker`。
- 框架包的迁移（队列、权限等）发布到使用它们的那个库，通常是主库。
