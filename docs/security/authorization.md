# 授权：角色与权限

授权判断已认证的调用者能不能执行当前的操作：路由要求权限，业务动作检查数据的归属，列表查询限定可见的范围。`authz` 把角色和权限保存在数据库中，按租户分配。

包：`identity`、`authz`。

| 层次 | 回答的问题 | 在哪里检查 |
| --- | --- | --- |
| 路由 | 能不能使用这一类功能，例如“编辑文章” | 框架按路由声明的权限，在调用处理器之前 |
| 资源 | 能不能操作这一条数据，例如“只能修改自己的文章” | [业务动作](../concepts/actions.md)的 `Handle`，在读写数据的事务中 |
| 数据范围 | 列表中出现哪些数据 | 业务动作的 `Handle`，在构造查询时 |

三层都要写：路由只检查权限，看不到具体的数据。授权写在服务端，前端按权限隐藏按钮只影响显示，任何客户端都能直接调用接口。

<a id="example-usage"></a>
## 示例

```go
// 路由要求权限
rest.RequireToken(api, tokens.Validate, "articles:write")(&op)

// 业务动作中检查数据的归属
if !subject.Can("articles:admin") && item.AuthorID != subject.ID {
    return ErrForbidden.New()
}

// 角色和权限保存在数据库中，每个请求按角色加载权限
roles := authz.NewStore(db, auth.Permissions)
err := roles.Assign(ctx, sub.Ref(), tenant, editor.ID)
api := rest.New(router, rest.Options{Accounts: identity.Accounts{user.Type: roles.Resolve}})
```

变量的来源见各节：[路由](#route-permissions)、[资源](#ownership)、[角色](#roles)。大多数接口只需要第一种。

<a id="permissions"></a>
## 权限名与 `Subject.Can`

权限是以冒号分隔的字符串，写成“资源:操作”，例如 `articles:read`、`articles:write`、`members:manage`。

- `sub.Can(perm)` 在主体的 `Permissions` 授予了 `perm`、并且凭据的 `Scopes` 为 `nil` 或也允许它时返回 `true`。只读的个人访问令牌用 `Scopes` 限制，见[主体](authentication.md#key-concepts)。
- 授予时最后一段可以是 `*`：`articles:*` 包含 `articles:read` 和 `articles:comments:delete`，单独的 `*` 包含全部权限。检查时传入具体的权限名。
- `identity.Grants(perms, perm)` 用同样的规则检查一组权限。
- 简单的应用由令牌校验器或 Resolver 直接给出权限；需要角色时用 [`authz`](#roles)。

<a id="route-permissions"></a>
### 在路由上要求权限

`rest.RequireToken(api, tokens.Validate, "articles:write")` 在令牌校验器之后列出路由要求的权限，注册写法见[访问策略](../http/routing.md#access-policies)。

- 列出多个权限时调用者必须全部具备，缺少任何一个返回 `403` `auth.forbidden`。
- 路由要求的权限写入 OpenAPI 的 `x-required-permissions`，[`routes` 命令](../general/commands.md#routes-command)也列出它们。
- 路由上检查权限，不检查角色名：租户可以自己创建和命名角色，写死的角色名在那时失效。

<a id="checking-permissions"></a>
### 在业务动作中检查：`subject.Can`

同一个接口按权限返回不同的结果时，在 `Handle` 中调用 `Can`：

```go
if subject.Can("articles:read-drafts") {
    // 编辑可以看到所有作者的草稿
}
```

`subject` 是 `Handle` 收到的主体，见[获取已认证的主体](authentication.md#retrieving-the-subject)。

<a id="roles"></a>
## 角色：`authz.Store`

`authz` 提供基于角色的权限：权限组成角色，角色在某个域中分配给主体。修改角色和分配在下一个请求生效，用户的登录保持有效。先用 `migratecmd.Publish(authz.Migrations())` 发布它的迁移，见[发布框架迁移](../database/migrations.md#publishing-framework-migrations)，再用[权限目录](#permission-catalog)创建 `Store`：

```go
roles := authz.NewStore(db, auth.Permissions)
```

`db` 是入口打开的 `*bun.DB`，也可以是 `database.Tx`。

- 分配按主体的类型和 ID 保存，一个 `Store` 可以服务[多套账号体系](authentication.md#account-systems)。
- `authz` 保存主体的数据：把 `roles.Erase` 登记到[注销账号](../features/privacy.md#framework-sources)，`roles.Export` 登记到[导出个人数据](../features/privacy.md#sources)。

<a id="permission-catalog"></a>
### 声明权限目录：`authz.Catalog`

应用的全部权限在代码中声明一次，例如 `internal/auth/permissions.go`：

```go
package auth

import "github.com/go-tenon/tenon/authz"

const (
    ArticlesRead   = "articles:read"
    ArticlesWrite  = "articles:write"
    ArticlesAdmin  = "articles:admin"
    CommentsDelete = "comments:delete"
)

var Permissions = authz.Catalog{
    {Name: ArticlesRead, Description: "查看文章"},
    {Name: ArticlesWrite, Description: "撰写和修改自己的文章"},
    {Name: ArticlesAdmin, Description: "修改和发布任何人的文章"},
    {Name: CommentsDelete, Description: "删除评论"},
}
```

- 角色只能包含目录中的权限，以及能匹配目录中权限的通配符；写错权限名时保存角色返回 `authz.invalid`。目录为空、或权限名中有 `*` 时 `NewStore` panic。
- `roles.Catalog()` 返回全部权限和说明，是角色编辑界面的数据来源。
- 目录同时交给 `rest.Options.Permissions`：它写入 OpenAPI 的 `x-tenon-permissions`，生成的客户端据此得到权限常量和说明，见[客户端的权限](../frontend/client-react.md#ts-permissions)。

<a id="creating-roles"></a>
### 创建和分配角色

```go
editor, err := roles.CreateRole(ctx, authz.Role{
    Name:        "editor",
    Description: "编辑",
    Permissions: []string{"articles:*", auth.CommentsDelete},
})

err = roles.Assign(ctx, identity.Ref{Type: user.Type, ID: userID}, "", editor.ID)
```

`roles` 是 `authz.Store`，`userID` 是用户的 ID。

`Assign` 的第三个参数是域。一个主体可以有多个角色，权限是它们的合集：

| 域 | 含义 |
| --- | --- |
| `""` | 平台本身；单租户的应用都用它 |
| 租户 ID | 只在这个租户中，见[多租户](tenancy.md) |
| `authz.AllTenants`（`"*"`） | 所有租户，例如运营人员在每个租户中的只读权限；只能分配全局角色 |

- `Role.Tenant` 为空的是全局角色，可以在任何域中分配；不为空的是租户自定义的角色，只能在这个租户中分配。
- 修改角色立即影响拥有它的全部主体。`DeleteRole` 默认同时撤销它的分配；传入 `authz.RestrictInUse()` 时，还有主体使用该角色则拒绝删除并返回 `authz.ErrRoleInUse`（公开参数 `count` 是使用它的分配数）。`Assign` 与 `DeleteRole` 在数据库事务中对角色行加锁互斥，并发分配和删除不会留下孤儿分配。
- `UpdateRole`、`DeleteRole`、`Role` 的最后一个参数是租户：租户管理员的界面传当前租户，其他租户的角色和全局角色返回 `authz.role_not_found`，所以可以直接传入请求中的角色 ID；平台管理员传 `""`。

<a id="preset-roles"></a>
### 预置角色：`EnsureRole`

平台管理员、租户的所有者这类每个环境都要有的角色声明在代码中，应用启动时用 `EnsureRole` 确保存在：不存在时创建，已存在时把说明和权限改成声明的样子，返回带 ID 的角色。

```go
var Owner = authz.Role{Name: "owner", Description: "所有者", Permissions: []string{"*"}}

owner, err := roles.EnsureRole(ctx, Owner)
```

`roles` 是 `authz.Store`，这段代码在入口打开依赖的函数中执行一次。

- 重复调用、多个实例同时启动都是安全的。预置角色以代码为准，在界面中的修改在下一次启动时被改回，界面中把它显示为只读。
- 不在迁移中写固定的角色 ID：`owner.ID` 交给需要它的业务动作，或者用到时调用 `roles.RoleByName(ctx, "owner", "")`。
- 只在开发和演示时需要的角色和分配写在[填充数据](../database/migrations.md#seeding)的命令中。

<a id="resolving-permissions"></a>
### 每次请求加载角色的权限：`roles.Resolve`

`roles.Resolve` 是一个 `identity.Resolver`，直接放进 API、[MCP 服务](../ai/mcp.md)和[实时频道](../libraries/realtime.md#tokens)的 `Accounts`。它按本次请求的租户加载主体的权限，写入 `sub.Permissions` 和 `sub.Tenant`：

```go
api := rest.New(router, rest.Options{Accounts: identity.Accounts{user.Type: roles.Resolve}})
```

`router` 是应用的 `chi.Router`。

- 主体在租户中没有角色时返回 `authz.not_member`（`404`），不暴露租户是否存在；在平台（`""`）中没有角色是正常的，权限为空。
- 加载是一次带索引的查询，修改在下一个请求生效；请求量大时[缓存权限](#caching-permissions)。
- SSE 这类长连接每 30 秒按新的权限重新检查；需要立即生效时，修改分配后调用 `RecheckSubject`，修改角色后调用 `RecheckTenant`，见[立即断开](../http/sse.md#immediate-revocation)。
- 还要检查账号是否停用时，自己写一个 Resolver：读用户，停用时返回 `identity.ErrUnauthenticated`（`401`），否则 `return roles.Resolve(ctx, sub)`，写法见[每次请求加载权限](authentication.md#resolving-subjects)。

<a id="caching-permissions"></a>
### 缓存权限：`authz.Cache`

请求量大时，用 `authz.Cache` 把每个主体的权限缓存在 [Redis](../libraries/cache.md) 中：

```go
roles := authz.NewStore(db, auth.Permissions, authz.Cache(rdb, "blog:prod:", time.Minute))
```

`rdb` 是应用共用的 Redis 客户端。第二个参数是命名空间，共用一个 Redis 的应用和环境各不相同，否则会读到别的数据库中的权限；第三个参数是过期时间。`Resolve` 和 `Permissions` 先读缓存。

- 缓存不改变立即生效：`Assign`、`Unassign`、`RemoveMember` 使这个主体的缓存失效，`UpdateRole`、`DeleteRole` 使全部缓存失效，方法返回之后所有实例读到新的权限。修改后照常调用 `RecheckSubject`、`RecheckTenant`。
- 修改角色和分配的每个 `Store` 都要带上同样的 `authz.Cache`，否则缓存不会随修改失效。
- `Store` 建在 [`database.Tx`](../database/database.md#after-commit) 上时，缓存在事务提交之后失效；建在 `bun.Tx` 上时 `NewStore` panic，因为无从得知何时提交。
- 绕过 `Store` 直接改表、或者失效失败（记录 Error 日志）时，旧的权限最多保留一个过期时间。Redis 不可用时直接查询数据库。

<a id="super-admins"></a>
### 超级管理员：`*`

权限为 `[]string{"*"}` 的角色拥有全部权限。资源级的规则要让管理员也能修改别人的数据时，检查一个具体的权限，例如[`articles:admin`](#authorization-functions)，`*` 包含它。

<a id="ownership"></a>
## 资源级授权：检查数据的归属

`articles:read` 只说明调用者能用文章接口，能读哪一篇由文章本身决定。“获取文章”中，已发布的文章所有人都能看，草稿只有作者能看：

```go
func (a *Get) Handle(ctx context.Context, subject identity.Subject, id uuid.UUID) (*Article, error) {
    if subject.ID == "" {
        return nil, ErrForbidden.New()
    }
    item, err := find(ctx, a.db, id)
    if err != nil {
        return nil, err
    }
    if item.Status == StatusDraft && item.AuthorID != subject.ID {
        return nil, ErrNotFound.New() // 别人的草稿，当作不存在
    }
    return item, nil
}
```

`a.db` 是 `*bun.DB`；`find`、`ErrNotFound`（`errorx.NotFound`）、`ErrForbidden`（`errorx.PermissionDenied`）是文章模块中共享的代码，见[模块内共享的代码](../concepts/actions.md#shared-code)。

- 主体作为参数传入 `Handle`：权限要求写在签名中，测试中容易构造不同身份的调用者。
- `Handle` 开头拒绝 `ID` 为空的主体，路由被误改成公开、或命令和其他代码直接调用时检查同样有效。
- 数据的存在本身需要保密时返回 `errorx.NotFound` 类错误，例如别人的草稿；数据公开但不能修改时返回 `errorx.PermissionDenied` 类错误，例如别人已发布的文章。
- 从缓存读到的数据同样要检查归属，见[缓存](../libraries/cache.md#cache-aside)。

<a id="authorization-functions"></a>
### 编写授权函数

规则在几个业务动作和[给界面的 DTO](#authorization-in-views)中都要用时，写成业务包 `article.go` 中导出的函数，规则只有一份：

```go
// CanEdit 报告 subject 能否修改 item：作者本人，或者能修改任何文章的编辑。
func CanEdit(subject identity.Subject, item *Article) bool {
    return subject.Can("articles:admin") || item.AuthorID == subject.ID
}
```

<a id="checking-in-transactions"></a>
### 在事务中检查

写操作在读写数据的同一个事务中检查归属，并用 `FOR UPDATE` 锁住这一行：在事务外检查时，两步之间数据可能已被其他请求改变。“发布文章”的 `Handle`：

```go
err := database.RunInTx(ctx, a.db, nil, func(ctx context.Context, tx database.Tx) error {
    err := tx.NewSelect().Model(item).Where("id = ?", id).For("UPDATE").Scan(ctx)
    if errors.Is(err, sql.ErrNoRows) {
        return ErrNotFound.New()
    }
    if err != nil {
        return errors.Wrap(err, "load article")
    }
    if !CanEdit(subject, item) {
        return ErrForbidden.New()
    }
    // 检查状态，更新文章
    return nil
})
```

`a.db` 是 `*bun.DB`，`subject` 和 `id` 是 `Handle` 的参数，`item` 是 `new(Article)`，`CanEdit` 见[编写授权函数](#authorization-functions)。完整的 `publish.go` 见[编写业务动作](../concepts/actions.md#writing-actions)。

<a id="scoping-queries"></a>
## 列表查询中的授权

列表、搜索和导出在查询条件中限定范围：查出来再过滤会让分页和计数出错。编辑看到全部文章，其他人只看到已发布的和自己的草稿：

```go
q := a.db.NewSelect().Model(&items).OrderExpr("created_at DESC").Limit(limit)
if !subject.Can("articles:read-drafts") {
    q = q.WhereGroup(" AND ", func(q *bun.SelectQuery) *bun.SelectQuery {
        return q.Where("status = ?", StatusPublished).WhereOr("author_id = ?", subject.ID)
    })
}
```

`a.db` 是 `*bun.DB`，`items` 是 `[]Article`，`subject` 和 `limit` 是 `Handle` 的参数。多租户应用的每个查询还要限定在 `subject.Tenant` 中，见[租户数据隔离](tenancy.md#data-isolation)。

<a id="authorization-in-views"></a>
## 在界面中按权限显示

规则只在服务端实现，结论交给前端：全局权限由“当前用户”接口返回 `sub.Permissions`；针对某一条数据的权限，DTO 中带上[授权函数](#authorization-functions)算出的结果。

```go
type ArticleDTO struct {
    // ……
    CanEdit bool `json:"can_edit" doc:"当前用户能否编辑这篇文章"`
}

func toDTO(sub identity.Subject, a *Article) ArticleDTO {
    return ArticleDTO{ /* …… */ CanEdit: CanEdit(sub, a)}
}
```

```tsx
{article.can_edit && <Link to="/articles/$id/edit" params={{ id: article.id }}>编辑</Link>}
{can("comments:delete") && <button onClick={clearComments}>清空评论</button>}
```

- `can` 是 `@go-tenon/ui` 的 [`useCan()`](../frontend/app.md#permissions)，检查 `createApp` 的 `permissions` 读到的权限，通配符的规则与 `Subject.Can` 相同。不用 React 时用 `@go-tenon/client` 的 `grants(permissions, "comments:delete")`。
- 隐藏按钮只影响显示，业务动作仍然按上面的方式授权。

<a id="authorization-failures"></a>
## 授权失败的响应

| 情况 | 状态码 | 错误码 |
| --- | --- | --- |
| 缺少路由要求的权限 | 403 | `auth.forbidden`，公开参数 `permission` 是缺少的权限 |
| `roles.Resolve`：主体在请求的租户中没有角色 | 404 | `authz.not_member` |
| 业务动作返回 `errorx.PermissionDenied` 类错误 | 403 | 你定义的错误码，例如 `article.forbidden` |
| 业务动作返回 `errorx.NotFound` 类错误 | 404 | 你定义的错误码，例如 `article.not_found` |

响应格式见[错误处理](../general/errors.md#response-format)，认证失败的响应见[认证](authentication.md#failed-authentication)。
