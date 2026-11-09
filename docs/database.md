# 数据库

[文档首页](README.md) / 数据库

当前数据库组合是 PostgreSQL、pgx 和 Bun。`database.Open` 完成连接池装配和连通性检查，之后直接使用返回的 `*bun.DB` 查询和事务能力。

本章内容：

- [打开连接](#connection)
- [连接配置](#configuration)
- [定义模型与查询](#models)
- [事务](#transactions)
- [识别约束错误](#constraints)
- [使用真实数据库测试](#tests)

<a id="connection"></a>

## 打开连接

下面是完整的连接探测函数：

```go
package storage

import (
    "context"

    "github.com/ryan053857/mygoravel/config"
    "github.com/ryan053857/mygoravel/database"
)

func Check(ctx context.Context, dsn config.Secret) error {
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

应用入口通常只打开一次，然后把 `db` 传给业务服务，退出时再关闭。启动探测通过 context deadline 限定等待时间。

<a id="configuration"></a>

## 连接配置

```yaml
database:
  dsn: ${DATABASE_URL}
  max_open_conns: 20
  max_idle_conns: 10
  conn_max_lifetime: 1h
  conn_max_idle_time: 30m
```

| 字段 | DefaultConfig 中的值 | 说明 |
| --- | --- | --- |
| dsn | 空，必填 | pgx 接受的 PostgreSQL 连接串 |
| max_open_conns | 4 × GOMAXPROCS(0) | 最大连接数；0 沿用 database/sql 的不设上限语义 |
| max_idle_conns | 4 × GOMAXPROCS(0) | 最大空闲连接数；0 不保留空闲连接 |
| conn_max_lifetime | 1h | 连接最长存活时间 |
| conn_max_idle_time | 30m | 空闲连接最长保留时间 |

根据数据库容量和部署副本数分配连接预算。`DefaultConfig` 提供默认值，`Open` 使用调用方传入的值，包括显式零值。

<a id="models"></a>

## 定义模型与查询

使用 Bun 的结构体标签。示例：

```go
type Note struct {
    ID        uuid.UUID `bun:"id,pk"`
    OwnerID   string    `bun:"owner_id,notnull"`
    Title     string    `bun:"title,notnull"`
    CreatedAt time.Time `bun:"created_at,notnull"`
}
```

这里需要 `github.com/google/uuid` 和标准库 `time`。模型描述查询映射，表结构由[迁移](migrations.md)管理。

用已经注入服务的 `db` 查询笔记：

```go
var item Note
err := db.NewSelect().
    Model(&item).
    Where("id = ?", id).
    Scan(ctx)
```

查询参数通过 Bun 的占位符绑定。区分 `sql.ErrNoRows` 与其他数据库错误，前者可以映射为业务 NotFound，后者保留错误原因返回给边界。

<a id="transactions"></a>

## 事务

事务直接使用 Bun 的 `RunInTx`。下面是业务服务中的片段，`item` 是准备插入的模型：

```go
err := db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
    _, err := tx.NewInsert().Model(item).Exec(ctx)
    return err
})
```

回调返回错误时回滚，成功时提交。参与同一事务的操作统一使用传入的 `tx`。需要嵌套事务或 savepoint 时，应明确使用 Bun 的原生能力与语义。

网络请求和缓存失效由业务单独编排。查询缓存通常在提交成功后失效，见[缓存失效](cache.md#invalidation)。

<a id="constraints"></a>

## 识别约束错误

已知的唯一约束可以映射为稳定业务错误：

```go
if database.IsUniqueViolation(err, "notes_owner_title_key") {
    return nil, ErrConflict.Wrap(err)
}
```

`ErrConflict` 是应用的 fault 模板。还可以使用 `IsForeignKeyViolation` 和 `IsCheckViolation` 判断对应 SQLSTATE。

指定已知约束名，分别映射对应业务冲突；其他错误保留原因返回。传给数据库的 context 应来自当前用例，以便请求取消和截止时间生效。

<a id="tests"></a>

## 使用真实数据库测试

`database/dbtest` 为每次测试创建隔离数据库，可以先执行嵌入的迁移，并在结束时删除数据库。使用方法见[测试](testing.md#database-tests)。

---

[上一篇：认证与授权](authentication.md) · [下一篇：数据库迁移](migrations.md)
