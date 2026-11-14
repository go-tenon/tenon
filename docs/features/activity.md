# 操作日志

正式的产品几乎都需要回答“谁在什么时候改了这篇文章”。`activity` 包把每一次操作保存为 `activity_log` 表中的一行：谁（actor）在哪个租户中对什么（target）做了什么（event），供产品界面按对象或按操作人查看历史。

操作日志和[日志](../general/logging.md)解决的是不同的问题：日志是给运维看的，写到标准输出或日志平台，产品界面查不到；操作日志是业务数据，在业务事务中写入，与业务修改一起提交或回滚，记录总是与业务数据一致。

每条记录都由业务动作显式写入：业务真正关心的是“文章已发布”这样的事件，批量更新也同样能记录。

- [示例用法](#example-usage)
- [安装](#installation)
- [记录操作](#recording)
    - [事件与属性](#events-and-properties)
    - [队列与定时任务](#jobs)
- [查看操作日志](#listing)
    - [显示](#displaying)
- [保留期限](#retention)
- [API 参考](#api-reference)

<a id="example-usage"></a>
## 示例用法

下面在发布文章的事务中记下一条操作日志，然后查询这篇文章的历史：

```go
err := database.RunInTx(ctx, db, nil, func(ctx context.Context, tx database.Tx) error {
    // ... 发布文章
    return activity.Record(ctx, tx, subject, activity.Activity{
        Event:      "article.published",
        TargetType: "article",
        TargetID:   item.ID.String(),
        Properties: map[string]any{"title": item.Title},
    })
})

// 一篇文章的历史
list, err := activity.List(ctx, db, activity.Query{TargetType: "article", TargetID: id.String()})
```

`Record` 写入的记录与文章的修改在同一个事务中，一起提交，一起回滚。操作人、租户和请求 ID 自动取自 `subject` 和 `ctx`，你只需要说明发生了什么、对象是谁。

<a id="installation"></a>
## 安装

把 `activity` 的迁移加入[发布命令](../database/migrations.md#publishing-framework-migrations)，发布后执行 `migrate up`：

```go
migratecmd.Publish(
    migrate.Source{Name: "activity", FS: activity.Migrations()},
)
```

`activity` 包没有需要装配的对象，所有函数都直接接收数据库连接或事务。

<a id="recording"></a>
## 记录操作

在业务动作修改数据的事务中调用 `activity.Record`。下面是[业务动作](../concepts/actions.md#writing-actions)一章中发布文章的 `Handle`，事务的最后加上一条记录：

```go
func (a *Publish) Handle(ctx context.Context, subject identity.Subject, id uuid.UUID) (*Article, error) {
    item := new(Article)
    err := database.RunInTx(ctx, a.db, nil, func(ctx context.Context, tx database.Tx) error {
        // ……读取、检查并更新文章，与原来相同
        _, err = tx.NewUpdate().Model(item).Column("status", "published_at", "updated_at").WherePK().Exec(ctx)
        if err != nil {
            return errors.Wrap(err, "publish article")
        }
        return activity.Record(ctx, tx, subject, activity.Activity{
            Event:      "article.published",
            TargetType: "article",
            TargetID:   item.ID.String(),
            Properties: map[string]any{"title": item.Title},
        })
    })
    // ……
}
```

`Record` 的第三个参数是执行操作的主体，与 `Handle` 收到的 `subject` 相同。`activity.Activity` 中只需要填写四个字段，其余字段由 `Record` 填写：

| 字段 | 来源 |
| --- | --- |
| `ID` | 新的 UUIDv7，按时间递增 |
| `Tenant` | 主体的 `Tenant`，`""` 为平台本身，见[多租户](../security/tenancy.md) |
| `ActorType`、`ActorID` | 主体的 `Type` 和 `ID`，`a.Actor()` 返回两者组成的 `identity.Ref` |
| `ImpersonatorType`、`ImpersonatorID` | 主体的 `Impersonator`：[代为登录](../security/authentication.md#impersonation)时实际操作的人，例如客服；平时为空，`a.Impersonator()` 返回它 |
| `RequestID` | 本次请求的[请求 ID](../http/middleware.md#request-id)，可以用它在日志中找到这次请求的全部日志 |
| `CreatedAt` | 当前时间 |

`Record` 必须使用事务 `tx`，记录才会随事务一起回滚，见[事务](../database/quick-start.md#transactions)。

<a id="events-and-properties"></a>
### 事件与属性

`Event` 是发生了什么，建议写成 `对象.动作` 的形式，例如 `article.published`、`member.invited`。它同时也是显示时的[翻译](#displaying)消息 ID，因此它是固定的字符串，变量放进属性。

`TargetType` 和 `TargetID` 是被操作的对象，查看一篇文章的历史就是按它们查询。没有具体对象的操作（例如修改租户设置）可以不填。

`Properties` 保存显示这条记录需要的其他数据，以 JSON 存储。对象之后可能被修改或删除，需要显示的内容应当在记录时写进来，例如文章当时的标题。需要记录修改前后的值时，只写需要的字段：

```go
Properties: map[string]any{
    "title": map[string]any{"old": before.Title, "new": item.Title},
},
```

属性只写可以展示的数据，密码哈希、令牌这类敏感数据留在原处。读取时，JSON 中的数字是 `float64`，较大的整数 ID 请保存为字符串。

<a id="jobs"></a>
### 队列与定时任务

队列和定时任务没有登录的用户，传入代表系统的主体即可。[定时发布](../concepts/actions.md#as-jobs)的 `Job` 调用 `Handle(ctx, system, id)`，记录的操作人就是 `system`。多租户应用中，系统主体还要带上任务所属的租户，否则记录会落到平台本身，见[后台任务](../security/tenancy.md#tenant-jobs)。

由 HTTP 请求发布的队列任务，处理时的 context 中带有原请求的请求 ID，任务中写入的记录与发布它的请求可以关联起来。

<a id="listing"></a>
## 查看操作日志

`activity.List` 按时间从新到旧返回记录。`Tenant` 总是作为条件，其余条件为空时不限制：

```go
list, err := activity.List(ctx, a.db, activity.Query{
    Tenant:     subject.Tenant,
    TargetType: "article",
    TargetID:   id.String(),
})
```

| 条件 | 含义 |
| --- | --- |
| `TargetType`、`TargetID` | 某类对象或某个对象的历史；`TargetID` 需要同时设置 `TargetType` |
| `Actor` | 某个主体（`identity.Ref`）做过的操作 |
| `Event` | 某种事件 |
| `Limit` | 返回的条数，默认 50 |
| `Before` | 翻页：传入上一页最后一条记录的 `ID`，返回比它更早的记录 |

查看历史本身也需要授权，通常是能查看文章的人才能查看它的历史，见[授权](../security/authorization.md)。`List` 之外的查询，例如按时间范围导出，可以直接用 Bun 查询，`activity.Activity` 就是这张表的模型：

```go
var list []activity.Activity
err := a.db.NewSelect().Model(&list).
    Where("tenant = ?", subject.Tenant).
    Where("created_at >= ?", since).
    OrderExpr("id").
    Scan(ctx)
```

表上有 `(tenant, id)`、`(tenant, target_type, target_id, id)` 和 `(tenant, actor_id, id)` 三个索引，查询请以 `tenant` 开头。

<a id="displaying"></a>
### 显示

记录中保存的是事件和属性，显示时再用[本地化](../general/localization.md)翻译成当前语言：

```yaml
# locales/zh-CN.yaml
article.published: "发布了文章《{{.title}}》"
```

```go
text := i18n.T(ctx, a.Event, a.Properties)
```

这样修改文案、增加语言时，已有的记录保持原样。操作人的名字同样在显示时按 `a.Actor()` 查询，用户改名后历史中显示的是新名字。

<a id="retention"></a>
## 保留期限

操作日志会一直增长。`activity.Cleanup` 返回删除保留期以前的记录的清理函数，交给[定时任务](../libraries/scheduling.md#framework-cleanups)：

```go
sched.Add("activity:cleanup", "0 4 * * *", schedule.Cleanup(activity.Cleanup(a.db, 180*24*time.Hour), 1000))
```

保留多久取决于产品和合规要求。需要长期保存的场景，可以在删除之前把旧记录导出到对象存储或数据仓库。

<a id="api-reference"></a>
## API 参考

包 `github.com/go-tenon/tenon/activity`。

### Record

```go
func Record(ctx context.Context, db bun.IDB, actor identity.Subject, a Activity) error
```

- **参数**
    - `db`：业务事务，必须与业务修改是同一个事务
    - `actor`：操作人，租户和代为登录的人也取自它
    - `a`：记录的内容，填写 `Event`、`TargetType`、`TargetID` 和 `Properties`
- **返回**：写入失败时的错误

请求 ID 和创建时间自动填写。

### 其他函数

```go
func List(ctx context.Context, db bun.IDB, q Query) ([]Activity, error)
func Cleanup(db bun.IDB, keep time.Duration) func(ctx context.Context, limit int) (int, error)
func Migrations() fs.FS
```

`List` 按对象或操作人查询，按时间倒序；`Cleanup` 删除早于 `keep` 的记录，交给定时任务。

`Activity` 是 `activity_log` 表的 Bun 模型：

| 字段 | 说明 |
| --- | --- |
| `Event` | 发生了什么，例如 `article.published` |
| `TargetType`、`TargetID` | 被操作的对象，可以为空 |
| `Properties` | 展示这条记录需要的其他数据，保存为 JSON |
| `Actor()`、`Impersonator()` | 操作人；代为登录时实际操作的人 |
| `Tenant`、`RequestID`、`CreatedAt` | 自动填写 |
