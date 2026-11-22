# 已保存视图

保存各业务对象的命名筛选与排序条件，分为租户内全体成员可见的共享视图，以及仅归属本人可见与管理的个人视图。

包：`savedview`。

- 每个业务对象用泛型 `savedview.New[T]` 创建自己的 `*savedview.Group[T]`，共用一张 `saved_views` 表，按 `tenant`、`target` 和作用域隔离。
- 框架管理视图的持久化、作用域归属、同作用域重名校验、数量上限、排序与隐私注销；业务包自行校验 `Config` 中的业务引用，并在执行查询时叠加自身的数据权限。

<a id="example"></a>
## 示例

```go
type FilterConfig struct {
    Statuses []string `json:"statuses,omitempty"`
    Priority string   `json:"priority,omitempty"`
    Sort     string   `json:"sort,omitempty"`
}

views := savedview.New[FilterConfig](db, "conversation", savedview.Options{
    ManageShared: func(sub identity.Subject) bool { return sub.Can("views:manage") },
})

v, err := views.Create(ctx, sub, savedview.CreateInput[FilterConfig]{
    Scope:  savedview.ScopeShared,
    Name:   "高优待回复",
    Config: FilterConfig{Statuses: []string{"open"}, Priority: "high", Sort: "waiting"},
})
list, err := views.List(ctx, sub, "") // 共享在前、个人在后
```

`db` 是入口打开的 `*bun.DB`，`sub` 是当前请求的主体（`identity.Subject`）。

<a id="installation"></a>
## 装配

1. 把 `savedview.Migrations()` 加入[发布命令](../database/migrations.md#publishing-framework-migrations)，发布后执行 `migrate up`，建出 `saved_views` 表。
2. 在[入口](../concepts/dependencies.md#assembling-the-application)的 `Open` 中为每个业务对象调用 `savedview.New[T]`，传给对应的业务动作。
3. 把 `savedview.Erase`、`savedview.Export` 登记到[注销账号](privacy.md#framework-sources)；多租户应用在移出成员的同一事务中调用 `savedview.DeleteByOwner`。

<a id="scopes-and-permissions"></a>
## 作用域与权限

视图分为 `savedview.ScopeShared`（共享）和 `savedview.ScopePersonal`（个人），作用域在创建后保持不变：

- **共享视图**：租户（或单租户应用）内全部主体可见；创建、修改、删除和排序时检查 `Options.ManageShared`（`identity.System` 始终允许，未设置 `ManageShared` 时只有 `identity.System` 可以管理）。
- **个人视图**：仅归属主体可见与管理；`identity.System` 不能创建个人视图。其他主体通过 ID 访问他人的个人视图时返回 `savedview.ErrNotFound`。
- 用 `views.CanManage(sub, v)` 计算当前主体是否可以编辑或删除视图 `v`，填入响应 DTO 的 `editable` 字段。

> [!WARNING]
> 共享视图对租户内全部成员可见，执行视图查询或统计数量时必须叠加当前成员的数据可见范围，避免通过共享视图越权读取数据。

<a id="crud-and-transactions"></a>
## 增删改查、排序与事务

`views` 是 `*savedview.Group[T]`，`db` 是 `*bun.DB`，`sub` 是当前请求的主体：

- `List(ctx, sub, scope)`：`scope` 为空串返回可见的全部视图（共享在前、个人在后），传入 `ScopeShared` 或 `ScopePersonal` 只返回对应作用域，同作用域按 `position`、`created_at`、`id` 升序排列。
- `Get(ctx, sub, id)`：读取一条可见视图；ID 无效、属于其他租户或对象，或属于他人个人视图时返回 `ErrNotFound`。
- `Create` / `Update` / `Delete`：自动去除名称首尾空白并校验长度（默认最多 50 字符）、同作用域重名（忽略大小写，返回 `ErrNameTaken`）和同作用域数量上限（默认各 50 条，返回 `ErrLimitExceeded`）。
- `Reorder(ctx, sub, scope, ids)`：按 `ids` 顺序重排该作用域下的 `position`，未列出的视图顺延在后。
- `WithDB(tx)` 返回使用事务 `tx` 的副本，让视图写入与[操作日志](activity.md)或实时推送在同一个事务中提交：

```go
err := database.RunInTx(ctx, db, nil, func(ctx context.Context, tx database.Tx) error {
    v, err := views.WithDB(tx).Create(ctx, sub, input)
    if err != nil {
        return err
    }
    if v.Scope == savedview.ScopeShared {
        return activity.Record(ctx, tx, sub, activity.Activity{
            Event: "view.created", TargetType: "saved_view", TargetID: v.ID,
            Properties: map[string]any{"name": v.Name, "target": v.Target},
        })
    }
    return nil
})
```

<a id="cleanup"></a>
## 移出成员与注销账号

- 成员被移出某个租户时，在移出事务中调用 `savedview.DeleteByOwner(ctx, tx, tenantID, ref)`，删除该成员在该租户下全部业务对象的个人视图，共享视图保持不变。
- 账号注销与数据导出时，在 `privacy.Source` 中登记 `Erase: savedview.Erase` 与 `Export: savedview.Export`。
