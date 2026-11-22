# 操作日志

操作日志记录谁（actor）在哪个租户中对什么（target）做了什么（event），每次操作是 `activity_log` 表中的一行，供产品界面按对象或按操作人查看历史。它是业务数据，在业务事务中写入，与业务修改一起提交或回滚；给运维看的日志见[日志](../general/logging.md)。

包：`activity`。

<a id="example-usage"></a>
## 示例

```go
err := database.RunInTx(ctx, db, nil, func(ctx context.Context, tx database.Tx) error {
    // ... 发布文章
    return activity.Record(ctx, tx, sub, activity.Activity{
        Event:      "article.published",
        TargetType: "article",
        TargetID:   item.ID.String(),
        Properties: map[string]any{"title": item.Title},
    })
})

// 一篇文章的历史，从新到旧（默认第 1 页、每页 20 条）
res, err := activity.List(ctx, db, activity.Query{Tenant: sub.Tenant, TargetType: "article", TargetID: id.String()})
```

`db` 是入口打开的 `*bun.DB`，`sub` 是执行操作的主体（`identity.Subject`），`item` 是刚发布的文章。

<a id="installation"></a>
## 装配

把 `activity.Migrations()` 加入[发布命令](../database/migrations.md#publishing-framework-migrations)，发布后执行 `migrate up`，建出 `activity_log` 表。`activity` 没有需要创建的对象，函数直接接收数据库连接或事务。

<a id="recording"></a>
## 用 `Record` 记录操作

在[业务动作](../concepts/actions.md#writing-actions)修改数据的事务中调用 `activity.Record`，第三个参数是 `Handle` 收到的主体：

```go
func (a *Publish) Handle(ctx context.Context, sub identity.Subject, id uuid.UUID) (*Article, error) {
    item := new(Article)
    err := database.RunInTx(ctx, a.db, nil, func(ctx context.Context, tx database.Tx) error {
        // ……读取、检查并更新文章
        return activity.Record(ctx, tx, sub, activity.Activity{
            Event:      "article.published",
            TargetType: "article",
            TargetID:   item.ID.String(),
            Properties: map[string]any{"title": item.Title},
        })
    })
    // ……
}
```

`Record` 必须用事务 `tx`：用 `a.db` 时事务回滚后记录仍然留下。`activity.Activity` 只填 `Event`、`TargetType`、`TargetID` 和 `Properties`，其余字段由 `Record` 填写：

| 字段 | 来源 |
| --- | --- |
| `ID` | 新的 UUIDv7，按时间递增 |
| `Tenant` | 主体的 `Tenant`，`""` 为平台本身，见[多租户](../security/tenancy.md) |
| `ActorType`、`ActorID` | 主体的 `Type` 和 `ID`，`a.Actor()` 返回两者组成的 `identity.Ref` |
| `ImpersonatorType`、`ImpersonatorID` | [代为登录](../security/authentication.md#impersonation)时实际操作的人，`a.Impersonator()` 返回它 |
| `RequestID` | 本次请求的[请求 ID](../http/middleware.md#request-id)，用它在日志中找到这次请求 |
| `CreatedAt` | 当前时间 |

<a id="events-and-properties"></a>
### 事件与属性

- `Event` 写成 `对象.动作`，例如 `article.published`。它也是显示时的[翻译](#displaying)消息 ID，所以是固定的字符串，变量放进属性。`Event` 为空时 `Record` 返回错误。
- `TargetType`、`TargetID` 是被操作的对象，没有具体对象的操作（例如修改租户设置）可以不填。`TargetID` 需要同时设置 `TargetType`。
- `Properties` 以 JSON 保存显示需要的数据。对象之后可能被修改或删除，显示的内容在记录时写进来，例如文章当时的标题。记录修改前后的值时只写需要的字段：

```go
Properties: map[string]any{
    "title": map[string]any{"old": before.Title, "new": item.Title},
},
```

- 属性只写可以展示的数据。手机号、邮箱这类个人信息不写：注销账号后操作日志仍然保留，见[保留的数据](privacy.md#retained-data)。
- 读回时 JSON 中的数字是 `float64`，较大的整数 ID 保存为字符串。

<a id="jobs"></a>
### 队列与定时任务

队列和定时任务传入代表系统的主体，例如[定时发布](../concepts/actions.md#as-jobs)的 `Job` 调用 `Handle(ctx, system, id)`。多租户应用中系统主体要带上任务所属的租户，否则记录落到平台本身，租户的历史中查不到，见[后台任务](../security/tenancy.md#tenant-jobs)。

由 HTTP 请求发布的队列任务带着原请求的请求 ID，定时任务每次执行也有自己的请求 ID，任务中写入的记录可以与发布它的请求关联起来。

<a id="listing"></a>
## 用 `List` 查看操作日志

`activity.List` 按时间从新到旧返回一页记录（`page.Offset[activity.Activity]`）。`Tenant` 总是作为条件，其余条件为空时不限制。`a.db` 是入口打开的 `*bun.DB`，`sub` 是当前请求的主体，`in.OffsetParams` 是[固定分页](../http/responses.md#offset-pagination)参数：

```go
res, err := activity.List(ctx, a.db, activity.Query{
    Tenant:     sub.Tenant,
    TargetType: "article",
    TargetID:   id.String(),
    Page:       in.OffsetParams,
})
```

| 条件 | 含义 |
| --- | --- |
| `TargetType`、`TargetID` | 某类对象或某个对象的历史；`TargetID` 需要同时设置 `TargetType` |
| `Actor` | 某个主体（`identity.Ref`）做过的操作 |
| `Event` | 某种事件 |
| `Page` | `page.OffsetParams`，零值为第 1 页、每页 20 条（`activity.DefaultPerPage`） |

查看历史也要授权，通常能查看文章的人才能查看它的历史，见[授权](../security/authorization.md)。其他查询（例如按时间范围导出）直接用 Bun，`activity.Activity` 就是这张表的模型。表上有 `(tenant, id)`、`(tenant, target_type, target_id, id)` 和 `(tenant, actor_id, id)` 三个索引，条件以 `tenant` 开头：

```go
var list []activity.Activity
err := a.db.NewSelect().Model(&list).
    Where("tenant = ?", sub.Tenant).
    Where("created_at >= ?", since).
    OrderExpr("id").
    Scan(ctx)
```

<a id="displaying"></a>
### 翻译事件

显示时用[本地化](../general/localization.md)把事件和属性翻译成当前语言，修改文案、增加语言时已有的记录不用改：

```yaml
# locales/zh-CN.yaml
article.published: "发布了文章《{{.title}}》"
```

```go
text := i18n.T(ctx, a.Event, a.Properties) // a 是一条 activity.Activity
```

操作人的名字同样在显示时按 `a.Actor()` 查询，用户改名后历史中显示新名字。

<a id="history-page"></a>
### 在页面中用 `ActivityLog` 显示

`@go-tenon/ui` 的 `ActivityLog` 从新到旧以固定分页表格显示记录，读取失败时显示“重试”。它读取应用自己的固定分页接口（返回 `page.Offset[T]`），每一条的字段：

| 字段 | 内容 |
| --- | --- |
| `id` | 记录的 ID |
| `text` | 事件的译文，即 `i18n.T(ctx, a.Event, a.Properties)` |
| `actor` | 操作人的名字；为空时显示“系统” |
| `impersonator` | 代为登录时实际操作的人的名字，可以为空 |
| `created_at` | 时间 |
| `href` | 点击后打开的应用内地址，可以为空 |

接口把 `page.OffsetParams` 嵌入输入类型，用 `List` 查询，再用 `page.NewOffset` 组装响应；`res` 是 `List` 的结果，`names` 是应用按 `identity.Ref` 查出的操作人名字：

```go
type HistoryItem struct {
    ID           string    `json:"id"`
    Text         string    `json:"text"`
    Actor        string    `json:"actor,omitempty"`
    Impersonator string    `json:"impersonator,omitempty"`
    CreatedAt    time.Time `json:"created_at"`
}

items := make([]HistoryItem, 0, len(res.Items))
for _, a := range res.Items {
    items = append(items, HistoryItem{
        ID:           a.ID.String(),
        Text:         i18n.T(ctx, a.Event, a.Properties),
        Actor:        names[a.Actor()],
        Impersonator: names[a.Impersonator()],
        CreatedAt:    a.CreatedAt,
    })
}
body := page.NewOffset(items, in.OffsetParams, res.Total)
```

前端把生成的客户端方法交给 `ActivityLog`，`key` 区分同一页面上的几份记录或不同的筛选条件（变化时自动回到第一页），`toolbar` 放筛选控件：

```tsx
<ActivityLog
  source={{ key: ["article", id], list: (p) => api.article.history(id, p) }}
/>
```

<a id="retention"></a>
## 用 `Cleanup` 删除过期的记录

`activity.Cleanup` 返回删除保留期以前的记录的清理函数，交给[定时任务](../background/scheduling.md#framework-cleanups)。`db` 是入口打开的 `*bun.DB`，`sched` 是调度器：

```go
sched.Add("activity:cleanup", "0 4 * * *", schedule.Cleanup(activity.Cleanup(db, 180*24*time.Hour), 1000))
```

法律要求网络日志保留至少 6 个月。需要更久时，删除之前把旧记录导出到对象存储或数据仓库。

`activity.Export` 返回一个人在所有租户中作为操作人的记录，登记到[导出个人数据](privacy.md#sources)。
