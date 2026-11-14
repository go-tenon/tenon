# 功能开关

功能开关让新功能先在一小部分人中上线：开发时只给员工开启，然后开放给几个试用的租户，再按比例放量，最后全量上线。出了问题可以立即全部关闭，代码保持原样。

`feature` 包分两部分：

- **开关在代码中声明**，带有一个默认规则，例如“只给员工开启”；
- **设置保存在数据库中**，由运营人员修改：全员开启、关闭或按比例放量，单独开启或关闭某个租户、某个用户。

检查开关时只读不写，也不缓存：修改设置后，所有实例的下一次检查立即生效。按比例放量用开关名和用户（或租户）ID 的哈希决定，同一个人每次的结果相同，把比例从 10% 提高到 50% 时，原来的 10% 仍然开启。

- [示例用法](#example-usage)
- [安装](#installation)
- [声明开关](#defining-flags)
- [检查开关](#checking-flags)
    - [一次检查多个开关](#checking-in-pages)
    - [后台任务](#background-jobs)
- [修改设置](#changing-settings)
    - [判断顺序](#evaluation-order)
    - [后台页面](#admin-pages)
- [删除开关](#removing-flags)
- [测试](#testing)
- [API 参考](#api-reference)

<a id="example-usage"></a>
## 示例用法

下面声明一个开关，在代码中检查它，然后在后台把它开放给一个租户：

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

没有任何设置时，`Active` 使用开关在代码中声明的默认规则；有了设置，就按[判断顺序](#evaluation-order)决定结果。所以你可以先只给员工开启，再给几个租户试用，然后按比例放量，最后全量上线。出了问题时，把全员设为 `Off`，所有人立即关闭。

<a id="installation"></a>
## 安装

把 `feature` 的迁移加入[发布命令](../database/migrations.md#publishing-framework-migrations)，发布后执行 `migrate up`：

```go
migratecmd.Publish(
    migrate.Source{Name: "feature", FS: feature.Migrations()},
)
```

迁移创建 `feature_settings` 表，保存全部设置。

<a id="defining-flags"></a>
## 声明开关

开关和使用它的业务放在一起。例如 `features.go`：

```go
var NewEditor = feature.Flag{
    Name:        "articles.new-editor",
    Description: "新版文章编辑器",
    ByTenant:    true,
    Default: func(ctx context.Context, sub identity.Subject) bool {
        return sub.Can("platform:staff")
    },
}
```

| 字段 | 说明 |
| --- | --- |
| `Name` | 保存在设置中的名称，只用非空白字符，发布后保持固定 |
| `Description` | 在后台页面中显示 |
| `ByTenant` | 按比例放量时按租户计算，同一租户的人看到的结果相同；为 `false` 时按用户计算 |
| `Default` | 没有任何设置命中时的结果。上面只给有 `platform:staff` 权限的员工开启；为 `nil` 时关闭 |

`Default` 可能在每次检查时调用，要足够快。它可以读取主体的权限，也可以按租户的套餐等条件判断。

在[入口](../concepts/dependencies.md#assembling-the-application)的 `open` 中用全部开关创建 `Store`，保存在 `App` 中：

```go
features := feature.NewStore(db, feature.Catalog{
    article.NewEditor,
})
```

目录中的开关名重复或不合法时 `NewStore` 会 panic，启动时就能发现。

<a id="checking-flags"></a>
## 检查开关

在业务动作中用 `Active` 检查，它返回 `bool`：

```go
func (a *Edit) Handle(ctx context.Context, subject identity.Subject, id uuid.UUID) (*EditView, error) {
    if !a.features.Active(ctx, subject, NewEditor) {
        return nil, ErrNotFound.New() // 没有开启时，当作这个功能不存在
    }
    // ...
}
```

每次 `Active` 是一次带索引的查询。读取设置出错时，它记录一条 Error 日志并返回 `Default` 的结果，请求照常处理。

<a id="checking-in-pages"></a>
### 一次检查多个开关

一个界面往往要检查多个开关，例如编辑页上的多个按钮。`For` 用一次查询读出这个主体的全部设置，“当前用户”接口把单页应用需要的开关一起返回：

```go
flags := h.features.For(ctx, subject)
out.Body.Features = map[string]bool{
    "new_editor": flags.Active(article.NewEditor),
}
```

`flags` 只在本次请求中使用。前端只拿到开关的结果，判断仍在服务端：接口和业务动作照常用 `Active` 检查，不信任前端传来的开关状态。

<a id="background-jobs"></a>
### 后台任务

[队列任务](../security/tenancy.md#tenant-jobs)使用 `system(tenant)` 这样类型为 `identity.System` 的主体。它代表系统，所以只有租户的设置和按租户放量对它有效。

<a id="changing-settings"></a>
## 修改设置

`Set` 修改一条设置，`Clear` 删除它：

```go
// 1. 给两个试用的租户开启
store.Set(ctx, article.NewEditor, feature.Tenant("acme"), feature.On)
store.Set(ctx, article.NewEditor, feature.Tenant("globex"), feature.On)

// 2. 某个用户反馈问题，单独给他关闭
store.Set(ctx, article.NewEditor, feature.Subject(identity.Ref{Type: user.Type, ID: "u_42"}), feature.Off)

// 3. 按比例放量
store.Set(ctx, article.NewEditor, feature.Everyone, feature.Percent(10))
store.Set(ctx, article.NewEditor, feature.Everyone, feature.Percent(50))

// 4. 发现严重问题，全部关闭
store.Set(ctx, article.NewEditor, feature.Everyone, feature.Off)

// 5. 修复后恢复放量，最终全量上线
store.Set(ctx, article.NewEditor, feature.Everyone, feature.On)
```

比例只能设给全员。开关不在目录中、租户或用户 ID 为空、比例不在 0 到 100 之间时，`Set` 返回 `feature.ErrInvalid`（`errorx.InvalidArgument`）。

修改设置的人和时间可以记录到[操作日志](activity.md)中。`WithDB` 让设置和日志在同一个事务中提交：

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

- 全员的 `On` 和 `Off` 优先于一切，所以 `Off` 能真正地紧急关闭：之前单独开启过的租户也会关闭。清除这条设置后，单独设置重新生效。
- `Percent(0)` 和 `Off` 不同：`Percent(0)` 时单独开启的租户仍然开启，`Default` 则被忽略。
- 没有用户 ID 的主体（例如未登录的访客）不在 100 以下的比例中；`ByTenant` 的开关同理看租户 ID。
- 用户的设置在他所属的每个租户中都生效。

<a id="admin-pages"></a>
### 后台页面

框架不提供管理页面。`Catalog` 列出全部开关，`Settings` 列出一个开关的全部设置（全员在前，然后是各租户、各用户），用它们在[运营后台](../security/tenancy.md#platform)中做一个页面：

```go
for _, flag := range store.Catalog() {
    settings, err := store.Settings(ctx, flag)
    if err != nil {
        return err
    }
    for _, s := range settings {
        // s.Scope.Tenant() 为空、s.Scope.Subject() 为零值时是全员；
        // s.State.Mode 为 on、off 或 percent，比例在 s.State.Percent 中
    }
}
```

修改设置的路由用权限保护，例如只允许有 `platform:features` 权限的运营人员访问。想让租户管理员自己开启某些试用功能时，可以提供一个只能修改本租户设置（`feature.Tenant(sub.Tenant)`）的页面。

<a id="removing-flags"></a>
## 删除开关

功能全量上线一段时间后，删掉代码中的判断和开关声明。新版本部署之后，用 `Prune` 删除数据库中残留的设置，它删除不在目录中的开关的全部设置，可以写成一个[命令](../general/commands.md#custom-commands)在发布后执行：

```go
n, err := store.Prune(ctx)
```

新旧版本同时运行时（例如[不停机升级](../installation/deployment.md#zero-downtime-upgrades)），等旧版本退出后再执行，否则旧版本中的开关会暂时退回 `Default`。

<a id="testing"></a>
## 测试

在[测试数据库](../testing/database.md#database-tests)上使用真实的 `Store`，需要开启时调用 `Set`：

```go
func TestEditWithNewEditor(t *testing.T) {
    db := newTestDB(t)
    features := feature.NewStore(db, feature.Catalog{article.NewEditor})
    require.NoError(t, features.Set(ctx, article.NewEditor, feature.Tenant("acme"), feature.On))

    edit := article.NewEdit(db, features)
    // ...
}
```

<a id="api-reference"></a>
## API 参考

包 `github.com/go-tenon/tenon/feature`。

### Flag、Catalog

| `Flag` 字段 | 说明 |
| --- | --- |
| `Name string` | 开关名，例如 `articles.new-editor` |
| `Description string` | 在后台页面中显示 |
| `ByTenant bool` | 按租户放量，同一租户的人结果一致；否则按主体放量 |
| `Default func(ctx, sub identity.Subject) bool` | 没有任何设置命中时的结果，`nil` 表示关闭 |

`Catalog` 是应用声明的全部开关。

### Store

```go
func NewStore(db bun.IDB, catalog Catalog) *Store
func (s *Store) Active(ctx context.Context, sub identity.Subject, f Flag) bool
func (s *Store) For(ctx context.Context, sub identity.Subject) Flags
func (s *Store) Set(ctx context.Context, f Flag, scope Scope, state State) error
func (s *Store) Clear(ctx context.Context, f Flag, scope Scope) error
func (s *Store) Settings(ctx context.Context, f Flag) ([]Setting, error)
func (s *Store) Prune(ctx context.Context) (int, error)
func (s *Store) WithDB(db bun.IDB) *Store
```

`Active` 检查一个开关，`For` 一次读出主体的全部开关（之后的 `Flags.Active` 在内存中判断）。`Set`、`Clear` 修改一个范围上的设置，`Prune` 删除已不在 `Catalog` 中的开关的设置。

### Scope、State

```go
var Everyone Scope
func Tenant(id string) Scope
func Subject(ref identity.Ref) Scope

var On, Off State
func Percent(n int) State
```

全员的 `On`、`Off` 优先于一切，其次是用户、租户的单独设置，然后是全员的 `Percent(n)`，最后是默认规则，见[判断顺序](#evaluation-order)。
