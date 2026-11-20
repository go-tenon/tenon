# 数据库

Tenon 只支持 PostgreSQL。`database.Open` 打开 pgx 连接池，返回 [Bun](https://bun.uptrace.dev/guide/) 的 `*bun.DB`，查询和事务直接用 Bun 的 API。`database` 另外提供事务提交后的回调、约束错误的判断、慢查询日志和只读副本。

包：`database`、`acquire`。

<a id="introduction"></a>
## 示例

```go
db, err := database.Open(ctx, cfg.Database)
if err != nil {
    return err
}
defer db.Close()

var items []Article
err = db.NewSelect().Model(&items).
    Where("status = ?", StatusPublished).
    OrderExpr("published_at DESC").
    Limit(20).
    Scan(ctx)

_, err = db.NewInsert().Model(item).Exec(ctx)
_, err = db.NewUpdate().Model(item).WherePK().Exec(ctx)
_, err = db.NewDelete().Model((*Article)(nil)).Where("id = ?", id).Exec(ctx)
```

`cfg.Database` 是应用配置中的 [`database.Config`](#configuration)，`Article` 是[模型](#defining-models)，`item` 是一篇文章，`id` 是它的 ID。

- SQL 中的值一律用 `?` 占位符传入。
- 把请求的 `ctx` 传给 `Scan`、`Exec`：请求取消或超时时查询随之停止。

<a id="connecting"></a>
## 用 `database.Open` 连接数据库

- 连接池在入口打开一次，传给需要它的[业务动作](../concepts/actions.md)，退出时关闭，见[依赖装配](../concepts/dependencies.md)。
- `Open` 立即 Ping 数据库，连不上时返回 `database.ErrUnavailable`，命令以 `ExitUnavailable` 退出，见[退出码](../general/commands.md#exit-codes)。
- 选项：`database.WithTelemetry(mp, tp)` 报告每条语句的指标和链路，见[可观测性](../general/telemetry.md#database)；`database.WithLogger` 指定慢查询的日志器；`database.Name` 是连接池的名字，默认 `primary`。
- 测试中用 `dbtest.Open` 为每个测试建临时库，见[数据库测试](../testing/testing.md#database-tests)。

<a id="configuration"></a>
## 配置

应用的根配置中放一个 `database.Config` 字段（`` Database database.Config `yaml:"database"` ``），`config.Load` 填充默认值并校验：

```yaml
database:
  dsn: ${DATABASE_URL}     # 必填，例如 postgres://user:pass@localhost:5432/app?sslmode=disable
  max_conns: 20            # 最大连接数，默认 4 × GOMAXPROCS
  min_conns: 0             # 空闲时也保持的连接数，默认 0
  max_conn_lifetime: 1h    # 连接的最长存活时间，默认 1h
  max_conn_idle_time: 30m  # 超过 min_conns 的空闲连接的保留时间，默认 30m
  slow_query: 1s           # 慢查询日志的阈值，0 表示不记录，默认 1s
  replicas: []             # 只读副本的连接串，默认为空
```

所有实例的 `max_conns` 之和要小于数据库（或 PgBouncer）的最大连接数。

<a id="pool-exhaustion"></a>
### 连接用满时返回 503：`acquire.WithTimeout`

连接都在使用中时，新的语句排队等待。`acquire.WithTimeout(ctx, time.Second)` 给取连接设时限，超时返回 `database.ErrBusy`，HTTP 响应 `503` 并带 `Retry-After`。[默认中间件](../http/middleware.md#default-middleware)按 `http.acquire_timeout`（默认 1 秒）为每个请求设置它；队列任务和命令不设时限。频繁出现 `database.busy` 时，先找占着连接的慢查询，再考虑调大 `max_conns`。

<a id="defining-models"></a>
## 定义模型

```go
package article

import (
    "time"

    "github.com/google/uuid"

    "github.com/go-tenon/tenon/database"
)

const (
    StatusDraft     = "draft"
    StatusPublished = "published"
)

type Article struct {
    ID uuid.UUID `bun:"id,pk"`
    database.Timestamps
    DeletedAt   time.Time  `bun:"deleted_at,soft_delete,nullzero"`
    AuthorID    string     `bun:"author_id,notnull"`
    Title       string     `bun:"title,notnull"`
    Slug        string     `bun:"slug,notnull"`
    Content     string     `bun:"content,notnull"`
    Status      string     `bun:"status,notnull"`
    PublishedAt *time.Time `bun:"published_at"`
}
```

- 表结构只由[迁移](migrations.md#migration-structure)修改，模型的字段按表中列的顺序写。
- `ID` 之后嵌入 `database.Timestamps`，见[自动维护时间戳](#timestamps)；`DeletedAt` 用于[软删除](#soft-deletes)。

<a id="primary-keys"></a>
### 主键用 UUIDv7

主键由应用在插入前生成，迁移中声明为 `id uuid PRIMARY KEY`。`subject` 是当前的主体：

```go
item := &Article{ID: uuid.Must(uuid.NewV7()), AuthorID: subject.ID}
```

插入前就有 ID，同一事务中的关联记录、对象存储的键都能直接用它；UUIDv7 按时间递增，写入性能接近自增 ID。

<a id="timestamps"></a>
### 自动维护时间戳：`database.Timestamps`

嵌入 `database.Timestamps` 的模型由 Bun 的钩子写 `created_at`、`updated_at`：插入时填零值的 `CreatedAt` 和 `UpdatedAt`，已给出的 `CreatedAt` 保留。更新时，`db` 是连接池，`item` 是读出并改过的文章：

```go
// 钩子刷新 updated_at
_, err := db.NewUpdate().Model(item).WherePK().Exec(ctx)
_, err = db.NewUpdate().Model((*Article)(nil)).
    Set("status = ?", StatusPublished).Where("id = ?", id).Exec(ctx)

// 自己带上 updated_at：钩子看不到语句中写了哪些列
_, err = db.NewUpdate().Model(item).Column("title", "updated_at").WherePK().Exec(ctx)
_, err = db.NewUpdate().Model(item).
    Set("title = ?", item.Title).Set("updated_at = ?", time.Now().UTC()).
    WherePK().Exec(ctx)
```

> [!WARNING]
> `Model((*Article)(nil)).Set(...)` 中不写 `Set("updated_at = ?", ...)`：钩子已经加了一个，PostgreSQL 报 `multiple assignments to same column "updated_at"`。

- 原生 SQL 自己写两列；upsert 的冲突更新写 `Set("updated_at = EXCLUDED.updated_at")`。
- SQL 中的时间由 Go 传入，不用 `NOW()`，时间列不设默认值：测试的假时钟只对 Go 有效，见[控制时间](../testing/testing.md#controlling-time)。
- 读出的时间在 UTC 中。PostgreSQL 只保存到微秒，自己写入、之后要与读出的值比较的时间用 `time.Now().UTC().Truncate(time.Microsecond)`。

<a id="handling-not-found"></a>
## 处理“未找到”：`sql.ErrNoRows`

查一行没有找到时 Bun 返回 `sql.ErrNoRows`，转换成模块的业务错误；其他错误用 `errors.Wrap` 补充上下文后返回，响应 `500`，见[错误处理](../general/errors.md#wrapping-errors)：

```go
if errors.Is(err, sql.ErrNoRows) {
    return nil, ErrNotFound.New()
}
if err != nil {
    return nil, errors.Wrap(err, "load article")
}
```

<a id="constraint-errors"></a>
## 处理约束错误：`IsUniqueViolation`

`articles_slug_key` 是[迁移](migrations.md#migration-structure)中的唯一索引，`a.db` 由构造函数传入：

```go
_, err := a.db.NewInsert().Model(item).Exec(ctx)
if database.IsUniqueViolation(err, "articles_slug_key") {
    return nil, ErrSlugTaken.Wrap(err, errorx.Public("slug", c.Slug))
}
if err != nil {
    return nil, errors.Wrap(err, "insert article")
}
```

| 函数 | 判断的错误 |
| --- | --- |
| `IsUniqueViolation(err, names...)` | 唯一约束与主键；传入约束名时只匹配这些约束 |
| `IsForeignKeyViolation(err)` | 外键约束，包括 `ON DELETE RESTRICT` |
| `IsCheckViolation(err)` | 检查约束 |
| `IsDeadlock(err)` | 死锁，见[隔离级别与死锁](#isolation-and-deadlocks) |
| `IsReadOnly(err)` | 在只读会话中写入，见[只读副本](#read-replicas) |

`IsUniqueViolation` 总是传约束名，不同的约束返回不同的业务错误。

<a id="transactions"></a>
## 事务：`database.RunInTx`

回调返回错误时回滚，返回 `nil` 时提交。`db` 是连接池，`item`、`audit` 是要插入的模型：

```go
err := database.RunInTx(ctx, db, nil, func(ctx context.Context, tx database.Tx) error {
    if _, err := tx.NewInsert().Model(item).Exec(ctx); err != nil {
        return err
    }
    _, err := tx.NewInsert().Model(audit).Exec(ctx)
    return err
})
```

> [!WARNING]
> 回调中的语句都用参数 `tx` 执行。用外层的 `db` 执行的语句不在事务中，回滚时会留下来，还可能等待事务持有的锁而卡住。

- 第三个参数是 `*sql.TxOptions`，`nil` 表示默认。
- `database.Tx` 嵌入 `bun.Tx`，可以传给接收 `bun.IDB` 的函数，例如 `activity.Record` 和队列的 `Topic.Publish`，写入随事务一起提交。
- 把 `tx` 传给内层的 `database.RunInTx` 时以保存点执行，内层出错只撤销内层；此时 `opts` 必须为 `nil`。

<a id="after-commit"></a>
### 提交后删除缓存、广播：`tx.AfterCommit`

撤销不了的操作（删除缓存、广播、调用外部服务）用 `tx.AfterCommit` 登记，提交之后执行：提前广播，客户端可能收到随后被回滚的通知；提前删除缓存，并发的读请求会把旧数据写回缓存。`a.db`、`a.hub` 由构造函数传入，`item` 是事务中更新的文章：

```go
err := database.RunInTx(ctx, a.db, nil, func(ctx context.Context, tx database.Tx) error {
    // ……加锁读取、检查权限并更新文章
    tx.AfterCommit(func(ctx context.Context) error {
        return a.hub.PublishJSON(ctx, "articles", ArticlePublished{ID: item.ID, Title: item.Title})
    })
    return nil
})
```

- 回调在提交后、`RunInTx` 返回前按登记顺序同步执行，耗时的操作交给队列；回滚时全部丢弃。回调的 `ctx` 去掉了取消信号。
- 保存点中登记的回调等最外层提交，保存点回滚时只丢弃它自己的。
- 回调返回错误或 panic 时记录 Error 日志，`RunInTx` 仍返回 `nil`。返回错误前用 `errors.Wrap` 写明是哪个操作。

> [!WARNING]
> 回调只在内存中，提交后、执行前进程退出就丢失。必须完成的工作（例如发通知邮件）在事务中发布任务，见[队列](../background/queue.md#publishing-jobs)。

<a id="slow-queries"></a>
## 慢查询日志

执行时间达到 `slow_query`（默认 1 秒）的语句记录一条 `Warn` 日志：

```text
WARN slow query component=database duration=1.284s db.operation.name=SELECT db.collection.name=articles query="SELECT … FROM \"articles\" AS \"a\" WHERE (\"a\".\"author_id\" = ?) LIMIT ?"
```

`query` 中的字面量换成了 `?`。日志带着请求 ID 和 `trace_id`，日志器默认是 `slog.Default()`，用 `database.WithLogger` 指定其他的。

<a id="soft-deletes"></a>
## 软删除

用 Bun 的[模型软删除](https://bun.uptrace.dev/guide/soft-deletes.html#using-bun-models)：模型的 `DeletedAt` 带 `soft_delete,nullzero` 标签，迁移中的 `deleted_at` 是第四列，见[时间列](migrations.md#timestamp-columns)。`db` 是连接池，`id` 是文章的 ID：

```go
// 写入 deleted_at，updated_at 不变；之后的模型查询自动排除这一行
_, err := db.NewDelete().Model((*Article)(nil)).Where("id = ?", id).Exec(ctx)

var deleted, all []Article
err = db.NewSelect().Model(&deleted).WhereDeleted().Scan(ctx)    // 只查已删除的
err = db.NewSelect().Model(&all).WhereAllWithDeleted().Scan(ctx) // 查全部

_, err = db.NewDelete().Model((*Article)(nil)).Where("id = ?", id).ForceDelete().Exec(ctx) // 物理删除
```

- **唯一约束**：已删除的行仍占着唯一值。用部分唯一索引代替唯一约束，`database.IsUniqueViolation(err, "articles_slug_key")` 照常识别：

  ```sql
  CREATE UNIQUE INDEX articles_slug_key ON articles (slug) WHERE deleted_at IS NULL;
  ```

- **外键**：软删除是 `UPDATE`，`ON DELETE CASCADE` 不触发。子表的数据在同一个[事务](#transactions)中一起软删除。
- **原生 SQL**：自己写 `deleted_at IS NULL`。

<a id="isolation-and-deadlocks"></a>
## 隔离级别与死锁：`IsDeadlock`

默认隔离级别是 READ COMMITTED。先读、检查、再修改的逻辑放在一个事务中，用 `For("UPDATE")` 锁住要修改的行，见[在事务中检查](../security/authorization.md#checking-in-transactions)。所有事务按相同的顺序加锁（例如按 ID 排序）。

死锁时被中止的事务整体回滚，`AfterCommit` 回调一并丢弃，可以直接重试。`a.db` 是连接池，`publish` 是事务的回调：

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
## 只读副本：`replicas`

在 `database.replicas` 中列出副本的连接串，业务代码不用改。`database.Open` 返回的 `*bun.DB` 按语句选择在哪个库执行：

| 语句 | 在哪里执行 |
| --- | --- |
| 事务之外的 `NewSelect()`，包括 `Count`、`Exists` | 副本，多个副本轮流 |
| 写操作、事务中的全部语句 | 主库 |
| 原生 SQL：`NewRaw`、`ExecContext`、`QueryContext` | 主库 |
| `database.Primary` 返回的 `ctx` 中的语句 | 主库 |

- 每个副本有自己的连接池，参数与主库相同。
- 副本的会话只读。报 `cannot execute ... in a read-only transaction`（`database.IsReadOnly`）时，通常是事务之外写了 `For("UPDATE")`，或者 `NewSelect` 调用了 `nextval` 这类写数据的函数：放进事务，或者用 `database.Primary(ctx)`。
- 队列任务中的查询、token 的校验、权限缓存的填充都在主库执行。

<a id="replication-lag"></a>
### 复制延迟：`database.Primary`

副本比主库晚几毫秒到几百毫秒。事务之外要读到刚写入的数据时，用 `database.Primary` 包装 `ctx`。`db` 是连接池，`order`、`id` 是要读的订单和它的 ID：

```go
ctx = database.Primary(ctx)
err := db.NewSelect().Model(&order).Where("id = ?", id).Scan(ctx) // 在主库执行
```

- **写完立即读**：提交订单后马上请求详情，读副本可能得到 `NotFound`。让详情接口读主库，或者让提交接口直接返回订单。
- **填充缓存**：从副本读到的旧数据会一直留在缓存中直到过期。

<a id="explicit-replica"></a>
### 单独的副本连接池：`database.ReadOnly`

报表、导出这类只在副本上执行的读取，另开一个只读连接池。`cfg.Reports` 是配置中另一段 `database.Config`，`dsn` 指向副本：

```go
reports, err := database.Open(ctx, cfg.Reports, database.Name("reports"), database.ReadOnly())
summary := report.NewSummary(reports)
```

它的全部语句都在这个副本上，包括原生 SQL。测试中用 `dbtest.Replica(t, db)` 代替它。

<a id="multiple-databases"></a>
## 多个数据库

另一个数据库（例如分析库）是另一个连接池：配置中加一段 `database.Config`，在入口打开，传给这个模块的业务动作。`cfg.Analytics` 是这段配置：

```go
analytics, err := database.Open(ctx, cfg.Analytics, database.Name("analytics"))
track := events.NewTrack(analytics)
```

每个库有自己的迁移，用 `migratecmd.Name` 构造迁移命令，挂在 `migrate` 下。`analyticsmigrations.FS` 是嵌入分析库迁移的包：

```go
mig := migratecmd.New(migrations.FS, func(c Config) database.Config { return c.Database })
mig.AddCommand(migratecmd.New(analyticsmigrations.FS, func(c Config) database.Config { return c.Analytics },
    migratecmd.Name("analytics")))
```

`migrate analytics up` 迁移分析库，`migrate analytics make add_index` 在 `migrations/analytics` 中新建文件。

- 没有跨库的事务。两个库的修改要一起成功时，在一个库的事务中发布[任务](../background/queue.md)，由任务修改另一个库。
- 队列从发布任务的那个库的 WAL 中读取任务。在别的库中发布任务时，为这个库另建一个 `queue.Worker`，否则任务一直不执行。
- 框架包的迁移发布到使用它们的那个库，通常是主库。
