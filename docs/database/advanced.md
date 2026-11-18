# 数据库进阶

[快速入门](quick-start.md)介绍了日常需要的连接、模型、查询和事务。这一页介绍几种不是每个应用都需要的用法：

- **慢查询日志**：找出执行得慢的语句；
- **软删除**：删除时只做一个标记，数据仍然留在表中，可以恢复；
- **隔离级别与死锁**：多个事务同时修改同一批数据时会发生什么；
- **只读副本**：把事务之外的查询自动分给从库，减轻主库的压力；
- **多个数据库**：一个应用连接两个或更多数据库。

用到哪个读哪一节即可。

阅读前需要了解：[数据库：快速入门](quick-start.md)。

- [慢查询](#slow-queries)
- [软删除](#soft-deletes)
- [隔离级别与死锁](#isolation-and-deadlocks)
- [只读副本](#read-replicas)
- [多个数据库](#multiple-databases)
- [常见问题](#troubleshooting)

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

读多写少的应用可以把读取分给只读副本（从库），减轻主库的压力。副本由 PostgreSQL 的流复制从主库同步数据，只能读，不能写。在 `database` 配置中列出副本的连接串：

```yaml
database:
  dsn: ${DATABASE_URL}                  # 主库
  replicas:
    - ${DATABASE_REPLICA_1_URL}
    - ${DATABASE_REPLICA_2_URL}
```

业务代码不用改。`database.Open` 返回的还是一个 `*bun.DB`，它按每条语句选择在哪个库执行：

| 语句 | 在哪里执行 |
| --- | --- |
| 事务之外的 `NewSelect()` 查询，包括 `Count`、`Exists` | 副本，多个副本轮流 |
| 写操作：`NewInsert`、`NewUpdate`、`NewDelete` 等 | 主库 |
| 事务中的全部语句，包括查询 | 主库 |
| 原始 SQL：`NewRaw`、`ExecContext`、`QueryContext` | 主库 |
| `database.Primary` 返回的 `ctx` 中的语句 | 主库 |

每个副本有自己的连接池，参数与主库相同（`max_conns` 等），会话默认只读：误把写操作交给副本时，立即得到 `database.IsReadOnly` 能识别的错误。连接池的名字是 `primary-replica-1`、`primary-replica-2`……，链路和指标的 `server.address` 是语句实际执行的那个库，见[可观测性](../general/telemetry.md#database)。`Open` 时每个副本都要能连上，否则返回 `database.ErrUnavailable`。

<a id="replication-lag"></a>
### 复制延迟

副本上的数据比主库晚一点，通常是几毫秒到几百毫秒。刚写入的数据，紧接着在副本上可能还读不到。在一个事务中先写后读不受影响，因为事务中的语句都在主库执行。需要在事务之外读到最新数据时，用 `database.Primary` 包装 `ctx`：

```go
ctx = database.Primary(ctx)
err := db.NewSelect().Model(&order).Where("id = ?", id).Scan(ctx) // 在主库执行
```

`ctx` 是请求的上下文，`db` 是打开的连接池，`order` 和 `id` 是要读取的订单和它的 ID。需要读主库的典型场景：

- **客户端写完立即读。** 例如提交订单后马上请求订单详情：这是两个请求，第二个请求读副本时可能得到 `NotFound`。让详情接口读主库，或者让提交接口直接返回订单的内容，客户端不必再查一次。
- **填充缓存。** 缓存在修改提交后删除，接下来的读取把数据重新写进缓存。从副本读到旧数据时，旧数据会一直留在缓存中，直到过期。
- **读取之后要做判断再修改。** 这类逻辑放进事务，并用 `FOR UPDATE` 锁住要修改的行，见[隔离级别与死锁](#isolation-and-deadlocks)。事务之外的 `FOR UPDATE` 会分到副本，副本拒绝它。

框架已经处理了自己的组件：[队列](../background/queue.md)任务中的查询都在主库执行，因为任务在发布它的事务提交后立即送达；token 的校验、权限缓存的填充、[操作](../features/operations.md)进度的查询也在主库执行。

监控副本的复制延迟（主库上 `pg_stat_replication` 的 `replay_lag`）。副本上运行很久的查询可能因为与复制冲突被取消，报表类的长查询可以在副本上开启 `hot_standby_feedback`。

<a id="explicit-replica"></a>
### 单独的副本连接池

报表、导出这类需要明确指定、只在副本上执行的读取，可以另开一个只读连接池，把它交给对应的业务动作：

```go
reports, err := database.Open(ctx, cfg.Reports, database.Name("reports"), database.ReadOnly())
summary := report.NewSummary(reports)
```

`cfg.Reports` 是配置中另一段 `database.Config`，`dsn` 指向副本。`ReadOnly` 让会话默认只读，`Name` 是连接池的名字。通过它执行的全部语句都在这个副本上，包括原始 SQL 和队列任务中的查询。

测试中用 `dbtest.Replica(t, db)` 代替这样的连接池：它连接同一个测试库，没有延迟，写入时失败，能发现误把写操作交给副本的代码。

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

- **没有跨库的事务。** 两个库的修改需要一起成功时，在一个库的事务中发布[任务](../background/queue.md)，由任务修改另一个库，任务至少执行一次。
- **任务在哪个库发布，就要有一个用这个库的 worker 转发**：队列把任务写进发布它的事务所在数据库的 WAL。通常只在主库发布任务；某个模块必须在自己的库中发布时，worker 部分为这个库再创建一个 `queue.Worker`。
- 框架包的迁移（队列、权限等）发布到使用它们的那个库，通常是主库。

<a id="troubleshooting"></a>
## 常见问题

**日志中出现大量 `slow query`**

执行时间达到 `slow_query`（默认 1 秒）的语句都会记录。按日志中的 `query` 找到语句，用 `EXPLAIN ANALYZE` 查看执行计划，通常是缺少索引；按 `request_id` 找到是哪个请求执行的，见[慢查询](#slow-queries)。

**事务报错 `deadlock detected`**

两个事务按不同的顺序锁住了同一批行，互相等待。让所有事务按相同的顺序加锁（例如按 ID 排序），并在出现死锁时重试，见[隔离级别与死锁](#isolation-and-deadlocks)。

**刚写入的数据，偶尔查不到**

配置了副本时，事务之外的查询在副本上执行，副本上的数据比主库晚一点。写入之后马上要读的地方用 `database.Primary(ctx)` 读主库，见[复制延迟](#replication-lag)。

**查询报错只读（`cannot execute ... in a read-only transaction`）**

语句被交给了副本，副本的会话是只读的。常见原因是在事务之外写了 `FOR UPDATE`，或者 `NewSelect` 中调用了会写数据的函数（例如 `nextval`）：把它放进事务，或者用 `database.Primary(ctx)`。用 `database.IsReadOnly` 可以识别这个错误。

**拆库后，某个库中发布的任务一直没有执行**

任务在哪个库发布，就要有一个用这个库的 worker 转发，见[多个数据库](#multiple-databases)。
