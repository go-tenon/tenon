# 功能开关

功能开关让新功能逐步上线：开关和默认规则在代码中声明，运营人员在数据库中为全员、租户或用户修改设置，例如给几个租户试用、按比例放量、紧急关闭。

包：`feature`。

- 检查开关只读数据库、不缓存，修改设置后所有实例的下一次检查立即生效。
- 按比例放量用开关名和用户（或租户）ID 的哈希决定：同一个人每次结果相同，比例从 10% 提高到 50% 时原来的 10% 仍然开启。

<a id="example-usage"></a>
## 示例

```go
var NewEditor = feature.Flag{Name: "articles.new-editor", Description: "新版文章编辑器"}

flags := feature.NewStore(db, feature.Catalog{NewEditor})

if flags.Active(ctx, subject, NewEditor) {
    // 新版编辑器
}

// 后台：给一个租户开启，或者按比例放量
err := flags.Set(ctx, NewEditor, feature.Tenant("acme"), feature.On)
err = flags.Set(ctx, NewEditor, feature.Everyone, feature.Percent(10))
```

`db` 是入口打开的 `*bun.DB`，`subject` 是当前请求的 `identity.Subject`。结果按[判断顺序](#evaluation-order)决定。

<a id="installation"></a>
## 安装

1. 把 `feature.Migrations()` 加入[发布命令](../database/migrations.md#publishing-framework-migrations)，发布后执行 `migrate up`，建出 `feature_settings` 表。
2. 在[入口](../concepts/dependencies.md#assembling-the-application)的 `Open` 中用全部开关创建一个 `Store`，保存在 `App` 中，交给需要它的业务动作：

```go
features := feature.NewStore(a.db, feature.Catalog{
    article.NewEditor,
})
```

3. 把 `feature.Erase` 登记到[注销账号](privacy.md#framework-sources)，删除为这个用户单独做的设置。

开关名为空、重复或包含空白时 `NewStore` panic，启动时就能发现。

<a id="defining-flags"></a>
## 声明开关 `feature.Flag`

开关和使用它的业务放在同一个包中，例如 `article/features.go`：

```go
var NewEditor = feature.Flag{
    Name:        "articles.new-editor", // 保存在设置中，发布后不再修改
    Description: "新版文章编辑器",       // 在后台页面中显示
    ByTenant:    true,                  // 按租户放量，同一租户的人结果相同；false 时按用户
    Default: func(ctx context.Context, sub identity.Subject) bool {
        return sub.Can("platform:staff") // 没有设置命中时只给员工开启；nil 表示关闭
    },
}
```

`Default` 可能在每次检查时调用，要足够快。它可以读取主体的权限，也可以按租户的套餐等条件判断。

<a id="checking-flags"></a>
## 用 `Active` 检查开关

`a.features` 是入口创建的 `*feature.Store`，`subject` 是当前请求的主体：

```go
func (a *Edit) Handle(ctx context.Context, subject identity.Subject, id uuid.UUID) (*EditView, error) {
    if !a.features.Active(ctx, subject, NewEditor) {
        return nil, ErrNotFound.New() // 没有开启时，当作这个功能不存在
    }
    // ...
}
```

每次 `Active` 是一次带索引的查询。读取设置出错时它记录一条 Error 日志，返回 `Default` 的结果，请求照常处理。

<a id="checking-in-pages"></a>
### 用 `For` 一次检查多个开关

`For` 用一次查询读出这个主体的全部设置，适合“当前用户”接口把前端需要的开关一起返回。`h.features` 是 `*feature.Store`：

```go
flags := h.features.For(ctx, subject)
out.Body.Features = map[string]bool{
    "new_editor": flags.Active(article.NewEditor),
}
```

`flags` 只在本次请求中使用。前端只用结果决定显示什么；接口和业务动作照常在服务端用 `Active` 检查，前端传来的开关状态不可信。

<a id="background-jobs"></a>
### 后台任务中的开关

[租户的队列任务](../security/tenancy.md#tenant-jobs)使用 `identity.System` 类型的主体。用户的单独设置和按用户放量对它无效；全员的 `On`、`Off`、`Percent(100)`，租户的设置和 `ByTenant` 开关的放量照常生效。

<a id="changing-settings"></a>
## 用 `Set`、`Clear` 修改设置

`store` 是 `*feature.Store`。`Set` 写入或覆盖一条设置，`Clear` 删除它：

```go
store.Set(ctx, article.NewEditor, feature.Tenant("acme"), feature.On)  // 给试用的租户开启
store.Set(ctx, article.NewEditor, feature.Subject(identity.Ref{Type: user.Type, ID: "u_42"}), feature.Off) // 单独给某个用户关闭
store.Set(ctx, article.NewEditor, feature.Everyone, feature.Percent(10)) // 按比例放量
store.Set(ctx, article.NewEditor, feature.Everyone, feature.On)          // 全量上线
store.Set(ctx, article.NewEditor, feature.Everyone, feature.Off)         // 紧急关闭
store.Clear(ctx, article.NewEditor, feature.Everyone)                    // 删除全员的设置，单独设置和 Default 重新生效
```

开关不在目录中、租户 ID 为空、用户不合法、比例设给了租户或用户、比例不在 0 到 100 之间时，`Set` 返回 `feature.ErrInvalid`（`errorx.InvalidArgument`）。

`WithDB` 让设置和[操作日志](activity.md)在同一个事务中提交，`a.db` 是 `*bun.DB`：

```go
return database.RunInTx(ctx, a.db, nil, func(ctx context.Context, tx database.Tx) error {
    if err := a.features.WithDB(tx).Set(ctx, flag, scope, state); err != nil {
        return err
    }
    return activity.Record(ctx, tx, subject, activity.Activity{
        Event: "feature.changed", TargetType: "feature", TargetID: flag.Name,
    })
})
```

<a id="evaluation-order"></a>
### 判断顺序

检查时从上到下，第一条命中的规则决定结果：

| 顺序 | 规则 | 结果 |
| --- | --- | --- |
| 1 | 全员设为 `On` 或 `Off` | 开启或关闭，**忽略**租户和用户的单独设置 |
| 2 | 用户的单独设置 | 开启或关闭 |
| 3 | 租户的单独设置 | 开启或关闭 |
| 4 | 全员设为 `Percent(n)` | 哈希落在 n% 内时开启 |
| 5 | 都没有 | `Default` 的结果 |

- 全员的 `Off` 是紧急关闭：单独开启过的租户也会关闭。
- `Percent(0)` 时单独开启的租户仍然开启，`Default` 被忽略。
- 没有用户 ID 的主体（例如未登录的访客）只在 `Percent(100)` 中开启；`ByTenant` 的开关同理看租户 ID。
- 用户的设置在他所属的每个租户中都生效。

<a id="admin-pages"></a>
### 后台页面

框架不提供管理页面。`store.Catalog()` 列出全部开关，`store.Settings` 列出一个开关的全部设置（全员在前，然后是各租户、各用户），用它们在[运营后台](../security/tenancy.md#platform)中做页面：

```go
for _, flag := range store.Catalog() {
    settings, err := store.Settings(ctx, flag)
    if err != nil {
        return err
    }
    for _, s := range settings {
        // s.Scope.Tenant() 为空且 s.Scope.Subject() 为零值时是全员；
        // s.State.Mode 为 on、off 或 percent，比例在 s.State.Percent 中
    }
}
```

修改设置的接口用权限保护，例如只允许有 `platform:features` 权限的运营人员。让租户管理员自己开启试用功能时，接口只接受 `feature.Tenant(sub.Tenant)` 这一个范围。

<a id="removing-flags"></a>
## 用 `Prune` 删除开关

功能全量上线一段时间后，删掉代码中的判断和开关声明。新版本部署之后用 `Prune` 删除不在目录中的开关的全部设置，可以写成一个[命令](../general/commands.md#custom-commands)在发布后执行：

```go
n, err := store.Prune(ctx) // n 是删除的设置条数
```

新旧版本同时运行时（例如[不停机升级](../installation/operations.md#zero-downtime-upgrades)），等旧版本退出后再执行：否则旧版本中的开关会退回 `Default`。

<a id="testing"></a>
## 测试

在[测试数据库](../testing/testing.md#database-tests)上使用真实的 `Store`，需要开启时调用 `Set`：

```go
func TestEditWithNewEditor(t *testing.T) {
    t.Parallel()
    ctx := t.Context()
    db := dbtest.Open(t, migrations.FS) // 应用的迁移，包括发布的 feature 迁移
    features := feature.NewStore(db, feature.Catalog{article.NewEditor})
    require.NoError(t, features.Set(ctx, article.NewEditor, feature.Tenant("acme"), feature.On))

    edit := article.NewEdit(db, features)
    // ...
}
```
