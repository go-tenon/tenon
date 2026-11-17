# 数据库：快速入门

几乎每个 API 都需要与数据库交互。Tenon 只支持 PostgreSQL，并使用社区中成熟的组件：[pgx](https://github.com/jackc/pgx) 是驱动，[Bun](https://bun.uptrace.dev) 负责构建查询和映射模型，[goose](https://github.com/pressly/goose) 管理迁移（见[迁移](migrations.md)）。

几个术语：

- **连接池**：程序和数据库之间预先建立的一组连接。每次查询从池中借一个连接，用完还回去，不必每次重新连接；池中最多有多少个连接，就最多有多少条语句同时执行。
- **模型**：一个对应数据表的 Go 结构体，字段对应表的列。Bun 按模型生成 SQL，并把查询结果填进结构体。
- **事务**：把几条语句变成一个整体，要么全部生效，要么全部不生效。

`database` 包只负责打开一个配置好的连接池（[pgxpool](https://pkg.go.dev/github.com/jackc/pgx/v5/pgxpool)），返回原生的 `*bun.DB`。之后的查询、事务都直接使用 Bun 的 API，你随时可以参考 [Bun 的文档](https://bun.uptrace.dev/guide/)。在此之上，`database` 另外提供几样 Bun 没有的东西：事务提交后的回调、约束错误的判断，以及慢查询日志。

阅读前需要了解：SQL 的基本语句；[配置](../general/configuration.md)；[入门教程第 3、4 章](../tutorial/03-database.md)。慢查询日志、软删除、隔离级别、只读副本和多个数据库见[数据库进阶](advanced.md)。

- [示例用法](#introduction)
- [连接数据库](#connecting)
- [配置](#configuration)
    - [连接用满时](#pool-exhaustion)
- [定义模型](#defining-models)
    - [主键](#primary-keys)
    - [自动维护时间戳](#timestamps)
- [执行查询](#running-queries)
    - [处理“未找到”](#handling-not-found)
- [处理约束错误](#constraint-errors)
- [事务](#transactions)
    - [提交后执行](#after-commit)
- [测试](#testing)
- [常见问题](#troubleshooting)
- [API 参考](#api-reference)

<a id="introduction"></a>
## 示例用法

打开连接池，然后用 Bun 查询：

```go
db, err := database.Open(ctx, cfg.Database)
if err != nil {
    return err
}
defer db.Close()

var items []Article
err = db.NewSelect().Model(&items).
    Where("status = ?", "published").
    OrderExpr("published_at DESC").
    Limit(20).
    Scan(ctx)
```

`database.Open` 返回的 `db` 是 Bun 的 `*bun.DB`，`NewSelect` 构建一条 `SELECT` 语句，`Scan` 执行它，并把结果填进 `items`。下面几节依次介绍连接、配置、模型、查询和事务。

<a id="connecting"></a>
## 连接数据库

使用 `database.Open` 打开连接池。它会立即 Ping 数据库确认连通，失败时自动释放已打开的资源，并返回 `database.ErrUnavailable`。这个错误属于 `errorx.Unavailable` 分类，命令因此以 `ExitUnavailable` 退出，部署脚本可以据此重试，见[退出码](../general/commands.md#exit-codes)。下面是一个完整的连接检查函数：

```go
package storage

import (
    "context"

    "github.com/go-tenon/tenon/database"
)

func Check(ctx context.Context, dsn string) error {
    cfg := database.DefaultConfig()
    cfg.DSN = dsn
    db, err := database.Open(ctx, cfg)
    if err != nil {
        return err
    }
    defer db.Close()
    return db.PingContext(ctx)
}
```

连接池参数为 `0` 时使用默认值，所以 `database.Config{DSN: dsn}` 也能直接打开。为 `ctx` 设置一个截止时间，数据库不可达时启动过程会按时结束。

在应用中，连接池在入口打开**一次**，传给所有需要它的[业务动作](../concepts/actions.md)，在应用退出时关闭，见[依赖装配](../concepts/dependencies.md)。

<a id="configuration"></a>
## 配置

数据库配置通常放在配置文件的 `database` 段中：

```yaml
database:
  dsn: ${DATABASE_URL}
  max_conns: 20
  min_conns: 0
  max_conn_lifetime: 1h
  max_conn_idle_time: 30m
  slow_query: 1s
```

| 字段 | 说明 | `DefaultConfig` 中的默认值 |
| --- | --- | --- |
| `dsn` | PostgreSQL 连接串，例如 `postgres://user:pass@localhost:5432/app?sslmode=disable` | 无，必填 |
| `max_conns` | 最大连接数 | 4 × `GOMAXPROCS` |
| `min_conns` | 空闲时也保持的连接数 | `0` |
| `max_conn_lifetime` | 连接的最长存活时间，到期后在空闲时关闭 | `1h` |
| `max_conn_idle_time` | 超过 `min_conns` 的空闲连接的最长保留时间 | `30m` |
| `slow_query` | 执行时间达到它的语句记为[慢查询](advanced.md#slow-queries)；`0` 表示不记录 | `1s` |

这些字段对应 `database.Config` 结构体。把它放进应用的根配置结构体，`config.Load` 会自动填充上表中的默认值：

```go
type Config struct {
    Database database.Config `yaml:"database"`
}

cfg, err := config.Load[Config](path)
if err != nil {
    return err
}
```

`database.Config` 实现了 `Validate` 方法，`config.Load` 会自动检查 DSN 是否为空、连接池参数是否合法；DSN 的格式在打开连接时检查。

> [!TIP]
> 设置 `max_conns` 时，请考虑应用的部署副本数：所有副本的连接数之和要控制在数据库的最大连接数以内。

<a id="pool-exhaustion"></a>
### 连接用满时

连接都在使用中时，新的语句排队等待空闲连接。HTTP 请求的排队时间要有上限：过载时一直排队只会让所有请求一起变慢、一起超时。`acquire.WithTimeout` 给取连接这一步设一个时限，超过时返回 `database.ErrBusy`，响应为 `503` 并带 `Retry-After`，客户端稍后重试：

```go
ctx = acquire.WithTimeout(ctx, time.Second)
```

[默认中间件](../http/middleware.md#default-middleware)为每个请求按 `http.acquire_timeout`（默认 1 秒）设置它。`acquire` 包只在 context 中传递“最多愿意等多久去拿一个共享资源”，数据库连接池读取它，HTTP 中间件与数据库各自独立。时限只作用于取连接，语句本身的执行时间由请求超时控制。队列任务和命令排队等待连接是正常情况，所以只有 HTTP 请求设置它。

<a id="defining-models"></a>
## 定义模型

模型是一个带有 `bun` 标签的普通 Go 结构体，描述数据表的列与字段之间的映射，例如文章：

```go
package article

import (
    "time"

    "github.com/google/uuid"
)

const (
    StatusDraft     = "draft"
    StatusPublished = "published"
)

type Article struct {
    ID          uuid.UUID  `bun:"id,pk"`
    AuthorID    string     `bun:"author_id,notnull"`
    Title       string     `bun:"title,notnull"`
    Slug        string     `bun:"slug,notnull"`
    Content     string     `bun:"content,notnull"`
    Status      string     `bun:"status,notnull"`
    PublishedAt *time.Time `bun:"published_at"`
    CreatedAt   time.Time  `bun:"created_at,notnull,skipupdate"`
    UpdatedAt   time.Time  `bun:"updated_at,notnull"`
    DeletedAt   time.Time  `bun:"deleted_at,soft_delete,nullzero"`
}
```

`Slug` 是文章在网址中的标识，例如 `/blog/hello-world` 中的 `hello-world`，它在所有文章中必须唯一。文章创建时是草稿（`draft`），发布后变为 `published`，并记录发布时间。

模型声明字段映射和下面介绍的 Bun 行为，表结构本身由[迁移](migrations.md#migration-structure)管理。`created_at`、`updated_at`、`deleted_at` 由你按需在模型和迁移中声明。本文的 `Article` 示例同时使用时间戳和软删除。

<a id="primary-keys"></a>
### 主键

Tenon 约定主键使用 UUIDv7，由应用在插入前生成，迁移中声明为 `id uuid PRIMARY KEY`：

```go
item := &Article{ID: uuid.Must(uuid.NewV7()), AuthorID: subject.ID}
```

在插入之前就有 ID，同一事务中的关联记录、对象存储的键或者幂等重试都可以直接使用它。UUIDv7 以毫秒时间戳开头，新记录总是追加在索引末尾，写入性能接近自增 ID；对外暴露时也隐藏了记录数量、无法逐个遍历；它包含创建时间，访问控制仍由[授权](../security/authorization.md)检查负责。只在内部使用、不出现在接口中的大表，可以在迁移中改用自增主键 `bigint GENERATED ALWAYS AS IDENTITY`。

<a id="timestamps"></a>
### 自动维护时间戳

如果你希望创建、修改记录时自动维护时间戳，可以在模型上实现 Bun 的 [`BeforeAppendModel`](https://bun.uptrace.dev/guide/hooks.html#beforeappendmodel) 钩子。Bun 会在构建插入或更新语句前自动调用它。

为上面的 `Article` 实现钩子：

```go
var _ bun.BeforeAppendModelHook = (*Article)(nil)

func (a *Article) BeforeAppendModel(ctx context.Context, query bun.Query) error {
    now := time.Now().UTC()
    switch query.(type) {
    case *bun.InsertQuery:
        if a.CreatedAt.IsZero() {
            a.CreatedAt = now
        }
        a.UpdatedAt = now
    case *bun.UpdateQuery:
        a.UpdatedAt = now
    }
    return nil
}
```

这个实现会在插入时填充空的 `CreatedAt` 并设置 `UpdatedAt`，在更新时刷新 `UpdatedAt`。模型中 `CreatedAt` 的 `skipupdate` 标签会让 Bun 在根据模型生成更新字段时跳过创建时间。

钩子修改的是模型字段。使用 `Column(...)` 限定更新列时，仍需把 `updated_at` 包含进去。下面的 `item` 是已加载并修改标题的 `Article`，`db` 是打开的连接池：

```go
_, err := db.NewUpdate().
    Model(&item).
    Column("title", "updated_at").
    WherePK().
    Exec(ctx)
```

如果使用 `Set(...)` 显式指定更新表达式，需要同时设置 `Set("updated_at = ?", time.Now().UTC())`。`Model((*Article)(nil))` 和原生 SQL 绕过模型钩子，这些写入路径需要自行维护时间戳。迁移中的时间列不设默认值，时间总是由应用写入，这样测试才能用假时钟控制时间，见[控制时间](../testing/overview.md#controlling-time)。

<a id="running-queries"></a>
## 执行查询

在业务动作中，用构造函数传入的 `db` 构建查询：

```go
var item Article
err := db.NewSelect().
    Model(&item).
    Where("id = ?", id).
    Scan(ctx)
```

查询参数通过 `?` 占位符绑定，Bun 会负责转义，SQL 中的值一律用占位符传入。始终把请求的 `ctx` 传给 `Scan` 或 `Exec`，这样请求取消或超时时，查询也会随之停止。

插入、更新和删除同样使用 Bun 的原生 API：

```go
_, err := db.NewInsert().Model(&item).Exec(ctx)
_, err = db.NewUpdate().Model(&item).WherePK().Exec(ctx)
_, err = db.NewDelete().Model((*Article)(nil)).Where("id = ?", id).Exec(ctx)
```

<a id="handling-not-found"></a>
### 处理“未找到”

查询单条记录时，如果没有匹配的行，Bun 会返回 `sql.ErrNoRows`。请把它转换为业务的“未找到”错误；其他数据库错误用 `cockroachdb/errors` 的 `errors.Wrap` 补充上下文后返回：

```go
if errors.Is(err, sql.ErrNoRows) {
    return nil, ErrNotFound.New()
}
if err != nil {
    return nil, errors.Wrap(err, "load article")
}
```

其他数据库错误会被当作内部错误，返回 `500`，完整的错误链和堆栈会记录到日志中，见[错误处理](../general/errors.md#wrapping-errors)。

<a id="constraint-errors"></a>
## 处理约束错误

数据库约束是保证数据一致性的最后一道防线。当插入的数据违反唯一约束时，例如两篇文章使用了相同的 slug，你通常希望返回一个 `409 Conflict`。下面是“创建文章”的 `Handle`，`articles_slug_key` 是[迁移](migrations.md#migration-structure)中定义的唯一约束：

```go
func (a *Create) Handle(ctx context.Context, subject identity.Subject, c Changes) (*Article, error) {
    if subject.ID == "" {
        return nil, ErrForbidden.New()
    }
    item := &Article{
        ID: uuid.Must(uuid.NewV7()), AuthorID: subject.ID, Status: StatusDraft,
        Title: c.Title, Slug: c.Slug, Content: c.Content,
    }
    _, err := a.db.NewInsert().Model(item).Exec(ctx)
    if database.IsUniqueViolation(err, "articles_slug_key") {
        return nil, ErrSlugTaken.Wrap(err, errorx.Public("slug", c.Slug))
    }
    if err != nil {
        return nil, errors.Wrap(err, "insert article")
    }
    return item, nil
}
```

`database` 包提供以下判断函数：

| 函数 | 判断的约束 | SQLSTATE |
| --- | --- | --- |
| `IsUniqueViolation(err, names...)` | 唯一约束与主键 | 23505 |
| `IsForeignKeyViolation(err)` | 外键约束 | 23503 |
| `IsCheckViolation(err)` | 检查约束 | 23514 |
| `IsDeadlock(err)` | 死锁 | 40P01 |

`IsUniqueViolation` 可以传入约束名，只在指定的约束冲突时返回 `true`。推荐总是指定约束名，为不同的约束返回不同的业务错误，例如“slug 已被使用”和“邮箱已注册”；其他错误则保留原因继续返回。

<a id="transactions"></a>
## 事务

使用 `database.RunInTx` 在事务中执行一组操作。回调返回错误时事务回滚，返回 `nil` 时提交：

```go
err := database.RunInTx(ctx, db, nil, func(ctx context.Context, tx database.Tx) error {
    if _, err := tx.NewInsert().Model(item).Exec(ctx); err != nil {
        return err
    }
    _, err := tx.NewInsert().Model(audit).Exec(ctx)
    return err
})
```

参数与 Bun 的 `db.RunInTx` 相同，只是数据库作为第二个参数传入；第三个参数是 `*sql.TxOptions`，`nil` 表示默认选项。`database.Tx` 嵌入了 `bun.Tx`，查询的写法完全一样，也可以传给接收 `bun.IDB` 的函数，例如 `activity.Record` 和队列的 `Topic.Publish`。它比 `bun.Tx` 多了一个 [AfterCommit](#after-commit)。

> [!WARNING]
> 在回调内部，所有操作都必须使用传入的 `tx`。外层的 `db` 在这个事务之外，它的写入在回滚时会留下来。

把 `tx` 作为第二个参数再调用 `database.RunInTx`，内层会以保存点（savepoint）执行：内层返回错误时只撤销内层的修改，外层可以继续。

<a id="after-commit"></a>
### 提交后执行

广播事件、删除缓存、调用外部服务这类操作撤销不了，应当在事务提交之后执行：提前广播，浏览器可能收到一条随后被回滚的通知；提前删除缓存，并发的读请求会把还没提交的旧数据重新写进缓存。在事务中用 `tx.AfterCommit` 登记它们。下面在[发布文章](../concepts/actions.md#writing-actions)的事务中登记一次[广播](../libraries/broadcasting.md#publishing-events)，`a.hub` 由构造函数传入：

```go
err := database.RunInTx(ctx, a.db, nil, func(ctx context.Context, tx database.Tx) error {
    // ……加锁读取、检查权限并更新文章
    tx.AfterCommit(func(ctx context.Context) error {
        return a.hub.PublishJSON(ctx, "articles", ArticlePublished{ID: item.ID, Title: item.Title})
    })
    return nil
})
```

- **提交成功后才执行。** 回调在 `RunInTx` 返回之前按登记顺序执行；事务回滚时全部丢弃。
- **保存点中登记的回调同样等最外层提交。** 保存点回滚时只丢弃它自己登记的回调。因此接收 `tx database.Tx` 的辅助函数在任何一层都可以直接登记。
- **回调独立于请求。** 回调收到的 `ctx` 去掉了取消信号，提交之后客户端断开，回调照常执行。
- **失败只记录日志。** 回调返回错误或 panic 时记录一条 Error 日志，其余回调继续执行，`RunInTx` 仍然返回 `nil`：数据已经提交，操作是成功的。返回错误前用 `errors.Wrap` 写明是哪个操作，日志中才看得出来。

> [!WARNING]
> 回调只保存在内存中，事务提交之后、回调执行之前进程退出，回调随之丢失。它适合尽力而为的操作；必须完成的工作，例如发送通知邮件，请在事务中发布任务，任务与业务数据一起提交，见[队列](../background/queue.md#publishing-jobs)。

回调是同步执行的，会推迟响应，耗时的操作交给队列。

<a id="testing"></a>
## 测试

`database/dbtest` 包可以为每个测试创建一个独立的真实数据库，执行迁移后交给测试使用，测试结束时自动删除。详见[测试](../testing/database.md#database-tests)。

<a id="troubleshooting"></a>
## 常见问题

**启动时报错连不上数据库，命令以退出码 4 结束**

`database.Open` 打开时会立即检查连通性。检查 DSN 中的地址、端口、用户名和密码，以及数据库是否在运行。

**查询返回 `sql: no rows in result set`**

`Scan` 查一行时没有找到数据，返回 `sql.ErrNoRows`。把它转换为业务错误，见[处理“未找到”](#handling-not-found)。

**请求返回 503，错误码是 `database.busy`**

连接池的连接都在使用中，请求等不到空闲连接。检查是否有慢查询长时间占着连接；需要时调大 `max_conns`，但所有实例加起来不能超过数据库的最大连接数，见[连接用满时](#pool-exhaustion)。

**事务中的查询没有生效，或者出现死锁**

在 `RunInTx` 的函数中，要用参数 `tx` 执行语句：用外面的 `db` 执行的语句不在事务中，还可能因为等待事务释放锁而卡住。

**在事务中发送了邮件，但事务回滚了，邮件却已经发出**

事务之外的副作用要放在 `tx.AfterCommit` 中，提交成功后才执行，见[提交后执行](#after-commit)。

<a id="api-reference"></a>
## API 参考

包 `github.com/go-tenon/tenon/database`。

### Open

```go
func Open(ctx context.Context, cfg Config, opts ...Option) (*bun.DB, error)
```

- **参数**
    - `cfg`：数据库配置，见[配置](#configuration)
    - `opts`：可选的设置，见下面的 `Option`
- **返回**：Bun 的数据库对象；连不上数据库时返回 `ErrUnavailable`

创建连接池并立即 Ping 数据库，失败时关闭已经打开的资源。

### Option

```go
func WithTelemetry(mp metric.MeterProvider, tp trace.TracerProvider) Option
func WithLogger(l *slog.Logger) Option
func Name(name string) Option
func ReadOnly() Option
```

`WithTelemetry` 报告每条语句的指标和链路，见[可观测性](../general/telemetry.md#database)；`WithLogger` 指定慢查询的日志器；`Name` 是连接池的名称，默认 `primary`；`ReadOnly` 让会话默认只读，用于[只读副本](advanced.md#read-replicas)。

### Config、DefaultConfig

```go
func DefaultConfig() Config
```

字段见[配置](#configuration)。

### RunInTx

```go
func RunInTx(ctx context.Context, db bun.IDB, opts *sql.TxOptions, fn func(ctx context.Context, tx Tx) error) error
```

- **参数**
    - `db`：连接池，或者外层 `RunInTx` 的 `Tx`
    - `opts`：隔离级别等事务选项，使用默认值时传 `nil`
    - `fn`：在事务中执行的函数
- **返回**：`fn` 返回的错误，或者提交失败的错误

`fn` 返回错误时回滚，返回 `nil` 时提交，然后执行 `AfterCommit` 登记的回调。`db` 是外层 `RunInTx` 的 `Tx` 时，以保存点执行，登记的回调等外层事务提交之后才执行，此时 `opts` 必须为 `nil`。

### Tx.AfterCommit

```go
func (tx Tx) AfterCommit(fn func(ctx context.Context) error)
```

登记提交后执行的回调。事务回滚时丢弃；回调返回的错误记录为 Error 日志，已经提交的事务保持有效。

### 约束错误

```go
func IsUniqueViolation(err error, constraints ...string) bool
func IsForeignKeyViolation(err error) bool
func IsCheckViolation(err error) bool
func IsDeadlock(err error) bool
func IsReadOnly(err error) bool
```

判断 PostgreSQL 返回的错误。`IsUniqueViolation` 传入约束名时只匹配这些约束。

### 错误

| 错误 | 分类 | 何时返回 |
| --- | --- | --- |
| `ErrUnavailable` | `Unavailable` | `Open` 时连不上数据库 |
| `ErrBusy` | `Unavailable` | 取连接超过 [`acquire` 的时限](#pool-exhaustion) |
