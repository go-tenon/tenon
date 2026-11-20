# 多租户

单数据库多租户：所有租户的数据在同一组表中，每行用 `tenant_id` 区分。入口从请求中取出租户，`authz` 按用户在这个租户中的角色加载权限，业务代码只读写这个租户的数据。运营后台与租户后台两套账号的装配也在这一页。

包：`identity`、`authz`、`httpx/rest`。

- 业务代码中的租户只取自主体的 `Tenant`（`subject.Tenant`）：取自请求体时，一个租户的管理员能操作其他租户。
- 租户 `""` 是平台本身，运营后台、个人中心都在这里。

<a id="identifying-tenants"></a>
## 确定租户：`Tenant` 选项

`rest.Options.Tenant` 从请求中取出租户。`roles` 是应用的 `*authz.Store`：

```go
// 路径：/t/{tenant}/articles
api := rest.New(router, rest.Options{
    Tenant:   func(ctx huma.Context) string { return ctx.Param("tenant") },
    Accounts: identity.Accounts{user.Type: roles.Resolve},
})

// 子域名：acme.example.com；主域名 example.com 是平台本身
Tenant: func(ctx huma.Context) string {
    if tenant, ok := strings.CutSuffix(ctx.Host(), ".example.com"); ok {
        return tenant
    }
    return ""
},

// 请求头：App 和小程序常用
Tenant: func(ctx huma.Context) string { return ctx.Header("X-Tenant") },
```

- 租户在每个请求开始时写入 context，公开的路由也一样，用 `identity.TenantFromContext(ctx)` 读取。
- `roles.Resolve` 加载用户在这个租户中的权限并写入 `sub.Tenant`；用户在这个租户中没有角色时返回 `404`，租户是否存在只告诉成员。
- 自定义域名要查表才能确定租户时，在 chi 中间件中查找并拒绝未知的域名，`Tenant` 只读取结果。
- `realtime.Options`、`natsrpc.Options`、`mcpx.HandlerOptions` 的 `Tenant` 作用相同。

<a id="members"></a>
## 成员与角色

用户在租户中有角色就是成员，撤销全部角色就移出了租户。`authz.Role` 的 `Tenant` 为 `""` 是全局角色（`owner`、`member` 这类模板，所有租户都能分配，修改它影响所有租户），为租户 ID 是这个租户自己创建的角色。

全局角色写在代码中，启动时用 [`EnsureRole`](authorization.md#preset-roles) 确保存在。

<a id="creating-tenants"></a>
### 创建租户

租户是应用自己的表（例如 `tenants`）。创建租户的业务动作在一个 `database.RunInTx` 中插入这一行，再用 `roles.Assign(ctx, subject.Ref(), t.ID, ownerRoleID)` 把创建者设为 `owner`。`roles` 用这个事务的 `tx` 构造（`authz.NewStore(tx, catalog, ...)`），两步一起提交：租户不会没有 owner。

<a id="managing-members"></a>
### 成员管理：`Members`、`Assign`、`RemoveMember`

成员管理的接口用 `members:manage` 保护。`roles` 是 `*authz.Store`，`sub` 是本次请求的主体：

| 操作 | 调用 |
| --- | --- |
| 列出成员和角色 | `roles.Members(ctx, sub.Tenant)` |
| 可以分配的角色 | `roles.Roles(ctx, sub.Tenant)`：全局角色加本租户的角色 |
| 邀请成员、调整角色 | `roles.Assign`、`roles.Unassign`，域传 `sub.Tenant` |
| 移出成员 | `roles.RemoveMember(ctx, member, sub.Tenant)` |
| 自定义角色 | `roles.CreateRole(ctx, authz.Role{Tenant: sub.Tenant, ...})` |
| 读取、修改角色 | 先用 `roles.Role(ctx, id, sub.Tenant)` 确认它属于本租户或是全局角色 |

- `Assign` 拒绝把其他租户的角色分配到本租户。
- 调整角色或移出成员后调用 [`RecheckSubject`](../http/sse.md#immediate-revocation)，这个人已经打开的长连接立即按新的权限校验。
- 只给运营用的全局角色在展示和分配前按名称过滤。

<a id="switching-tenants"></a>
### 切换租户：`Tenants`

`roles.Tenants(ctx, sub.Ref())` 返回用户所属的全部租户。切换就是跳转到另一个租户的地址（`/t/acme` → `/t/globex`），登录令牌不变。前端见[租户切换](../frontend/layout.md#tenants)。

<a id="data-isolation"></a>
## 租户数据隔离：`tenant_id`

属于租户的每张表有 `tenant_id text NOT NULL` 列，唯一约束和常用索引以它开头，例如 `UNIQUE (tenant_id, slug)`、`(tenant_id, created_at)`。业务动作的每个查询都带上 `subject.Tenant`，写入时也从主体取租户：

```go
func find(ctx context.Context, db bun.IDB, subject identity.Subject, id uuid.UUID) (*Article, error) {
    item := new(Article)
    err := db.NewSelect().Model(item).
        Where("tenant_id = ?", subject.Tenant).Where("id = ?", id).Scan(ctx)
    if errors.Is(err, sql.ErrNoRows) {
        return nil, ErrNotFound.New() // 其他租户的文章与不存在的一样返回 404
    }
    return item, err
}

item := &Article{TenantID: subject.Tenant, AuthorID: subject.ID} // 写入
```

> [!WARNING]
> 漏掉一个 `tenant_id` 条件，就会把一个租户的数据暴露给另一个租户。

- `Handle` 和模块内的查询函数接收整个 `identity.Subject`，不单独接收租户 ID；按 ID 查询也带上租户。
- 每个业务动作写一个“其他租户访问不到”的测试：用租户 B 的主体读租户 A 的数据，期望 `404`，见[测试业务动作](../testing/testing.md#testing-actions)。
- 要再加一层保护时用 PostgreSQL 的[行级安全策略](https://www.postgresql.org/docs/current/ddl-rowsecurity.html)：事务开始时 `SET LOCAL app.tenant_id`。

<a id="background-work"></a>
## 后台任务中的租户

队列和定时任务没有请求，租户由任务自己带着，再构造带租户的主体交给 `Handle`。

<a id="tenant-jobs"></a>
### 队列任务：payload 带上租户

只带 ID 的任务以平台本身（`""`）的身份查询，`find` 永远返回 `ErrNotFound`。payload 带上租户，系统主体按租户构造：

```go
type PublishJob struct {
    Tenant string    `json:"tenant"`
    ID     uuid.UUID `json:"id"`
}

var PublishAt = queue.Topic[PublishJob]("articles.publish")

// system 是队列任务在 tenant 中的调用者。
func system(tenant string) identity.Subject {
    return identity.Subject{Type: identity.System, ID: "system", Tenant: tenant, Permissions: []string{"articles:admin"}}
}

func (a *Publish) Job(ctx context.Context, job PublishJob) error {
    _, err := a.Handle(ctx, system(job.Tenant), job.ID)
    return err
}

// 发布：tx 是业务事务，租户取自主体
err := PublishAt.Publish(ctx, tx, PublishJob{Tenant: subject.Tenant, ID: item.ID}, queue.At(publishAt))
```

- 系统主体只在 `job.Tenant` 中有权限：payload 中的 ID 属于其他租户时 `find` 找不到它。
- [操作日志](../features/activity.md)按主体的租户记录，命令行入口同样用 `--tenant` 参数构造 `system(tenant)`。

<a id="tenant-scheduled-tasks"></a>
### 定时任务：每个租户一个队列任务

一条 SQL 处理所有租户的定时任务（例如[发布到期文章](../background/scheduling.md#defining-tasks)）保持原样。要逐个租户执行时（例如每天的摘要），定时任务在一个事务中查出全部租户 ID，用 `queue.Topic[string]` 的 `PublishAll(ctx, tx, ids)` 为每个租户发布一个任务：一个租户出错只重试它自己，执行时间与租户数量无关。

<a id="other-resources"></a>
## 缓存、广播与其他资源中的租户

数据库之外按名称共享的资源，名称带上租户，租户取自 `sub.Tenant`：

| 资源 | 做法 |
| --- | --- |
| [缓存](../libraries/cache.md) | 键带上租户：`app:article:v1:<tenant>:<id>`。命中缓存会跳过 `find` 的租户条件，所以读缓存的函数也接收主体 |
| [广播](../libraries/broadcasting.md) | 主题带上租户：`t:<tenant>:articles`；[实时频道](../libraries/realtime.md#private-channels)的授权函数把频道名中的租户与 `sub.Tenant` 比较 |
| [限流](../libraries/rate-limiting.md) | 按租户限额时 key 用 `tenant:<id>` |
| [锁](../libraries/locks.md) | 按 slug 等租户内唯一的值加锁时带上租户；UUID 不用 |
| [Webhook](../features/webhooks.md) | 不用改：`Event.Send` 只发给 `subject.Tenant` 的端点 |
| [文件](../libraries/filesystem.md) | 不用改：引用文件的业务行有 `tenant_id`，读写经过它的租户检查 |

<a id="platform"></a>
## 运营后台：平台角色与 `AllTenants`

运营后台的接口不带租户，租户为 `""`。运营人员的角色分配在平台中，只在平台生效。`roles` 是 `*authz.Store`，`staff` 是运营人员的 `identity.Ref`：

```go
admin, err := roles.CreateRole(ctx, authz.Role{Name: "platform-admin", Permissions: []string{"platform:*"}})
err = roles.Assign(ctx, staff, "", admin.ID)

// 客服在每个租户中都有只读权限
support, err := roles.CreateRole(ctx, authz.Role{Name: "support", Permissions: []string{"articles:read"}})
err = roles.Assign(ctx, staff, authz.AllTenants, support.ID)
```

- `AllTenants` 中只能分配全局角色，否则返回 `authz.ErrInvalid`。它对所有租户生效，只给只读权限。
- 以某个成员的身份排查问题用[代为登录](#impersonation)。

<a id="two-account-systems"></a>
## 运营后台与租户后台：两套账号

运营人员和租户成员是两套[账号体系](authentication.md#account-systems)，各有一个 `login.Realm` 和 `apitoken.Store`，共用一个 `authz.Store`：

| | 运营后台 | 租户后台 |
| --- | --- | --- |
| 主体类型、表 | `staff`，`staff` 表 | `member`，`members` 表 |
| 接口 | `/ops/api`，租户为 `""` | `/t/{tenant}/api` |
| 会话 cookie | `__Host-ops` | 默认的 `__Host-session` |

每套账号的 Resolver 读账号表，不存在或已停用时返回 `identity.ErrUnauthenticated`，然后 `return roles.Resolve(ctx, sub)`。成员的 Resolver 还要在 `sub.Impersonator` 不为零值时检查操作者在平台中仍有 `users:impersonate`，写法见[代为登录的限制](authentication.md#impersonation-rules)。需要发布 `login`、`ratelimit`、`apitoken`、`authz` 的迁移。

<a id="assembling"></a>
### 装配：两个 `Realm`、两个会话 cookie

`internal/accounts` 创建两套账号。`db` 是 `*bun.DB`，`hasher`、`c` 见[哈希与加密](encryption.md)，`perms` 是[权限目录](authorization.md#permission-catalog)，`cfg` 有两个 `apitoken.Config`：

```go
// StaffCookie 是运营的会话 cookie；成员用默认的 httpx.SessionCookie{}。
var StaffCookie = httpx.SessionCookie{Name: "__Host-ops"}

func New(db *bun.DB, cfg Config, hasher *hashing.Hasher, c *crypt.Crypt) *Accounts {
    every15 := ratelimit.Config{Rate: 5, Interval: 15 * time.Minute, Burst: 5}
    roles := authz.NewStore(db, perms)
    realm := func(t identity.SubjectType, resolve identity.Resolver) *login.Realm {
        return login.New(db, login.Options{
            Type: t, Hasher: hasher, Crypt: c, Resolve: resolve,
            Policy:   login.Policy{PasswordKinds: []string{login.KindEmail}},
            Attempts: pglimit.New(db, "login:"+string(t)+":attempts", every15),
            Sends:    pglimit.New(db, "login:"+string(t)+":sends", every15),
        })
    }
    return &Accounts{
        Staff: realm(staff.Type, staff.Resolve(db, roles)), StaffTokens: apitoken.NewStore(db, cfg.StaffToken),
        Members: realm(member.Type, member.Resolve(db, roles)), MemberTokens: apitoken.NewStore(db, cfg.MemberToken),
        Roles: roles,
    }
}
```

两套账号的 cookie 名称必须不同：相同时运营登录会把租户后台的成员挤掉。浏览器同时保存两个 cookie，每个 API 只读 `rest.Options.SessionCookie` 指定的那一个。

<a id="wiring"></a>
### 接入入口：`/ops/api` 与 `/t/{tenant}/api`

`acc` 是 `accounts.New` 的结果，`a.db` 是 `*bun.DB`：

```go
router.Route("/ops/api", func(r chi.Router) {
    ops := rest.New(r, rest.Options{
        BasePath:      "/ops/api",
        Accounts:      identity.Accounts{staff.Type: staff.Resolve(a.db, acc.Roles)},
        SessionCookie: accounts.StaffCookie,
    })
    opsapi.RegisterRoutes(ops, acc)
})
router.Route("/t/{tenant}/api", func(r chi.Router) {
    console := rest.New(r, rest.Options{
        BasePath: "/t/{tenant}/api",
        Tenant:   func(ctx huma.Context) string { return ctx.Param("tenant") },
        Accounts: identity.Accounts{member.Type: member.Resolve(a.db, acc.Roles)},
    })
    consoleapi.RegisterRoutes(console, acc)
})
```

- 运营的令牌调用租户后台、成员的令牌调用运营后台返回 `401`。
- 成员的登录令牌不绑定租户：同一个会话进入他所属的各个租户，权限按各租户的角色。
- 运营后台的前端用 `go tool tenon add frontend ops --api /ops/api` 生成，见[再加一个前端](../installation/tooling.md#add-frontend)。

<a id="impersonation"></a>
### 运营代为登录租户成员

“以此成员登录”的接口要求 `users:impersonate`，为成员签发带 `Impersonator` 的登录令牌，直接写入成员的会话 cookie。`h.roles` 是 `*authz.Store`，`h.memberTokens` 是成员的 `*apitoken.Store`：

```go
func (h *handlers) impersonate(ctx context.Context, in *ImpersonateInput) (*ImpersonateOutput, error) {
    staff, _ := identity.FromContext(ctx)
    if !staff.Impersonator.IsZero() {
        return nil, identity.ErrForbidden.New() // 代为登录只有一层
    }
    target := identity.Subject{Type: member.Type, ID: in.Body.MemberID, Impersonator: staff.Ref()}
    if _, isMember, err := h.roles.Permissions(ctx, target.Ref(), in.Body.TenantID); err != nil {
        return nil, err
    } else if !isMember {
        return nil, ErrMemberNotFound.New() // 应用定义的 NotFound 错误
    }
    token, err := h.memberTokens.Login(ctx, target, apitoken.Device{UserAgent: in.UserAgent})
    if err != nil {
        return nil, err
    }
    out := &ImpersonateOutput{SetCookie: httpx.SessionCookie{}.Set(token, h.memberLifetime)}
    out.Body.ConsolePath = "/t/" + in.Body.TenantID + "/app" // 运营前端随后打开它
    return out, nil
}
```

- 两个后台同源，租户后台才读得到写入的 `__Host-session`；`Secure` 的 cookie 只在 HTTPS 和 `localhost` 上保存。
- 令牌不经过前端代码，运营自己的 `__Host-ops` 不受影响。
- 租户后台在 `sub.Impersonator` 不为零值时显示“正在代某人操作”和退出按钮；退出时 `Revoke` 当前凭据、清除 cookie、回到运营后台。

<a id="customers"></a>
## 租户的客户账号：`PerTenant`

租户的终端客户有两种设计：

- **平台统一账号**：客户可以属于多个租户，在租户中分配只有客户权限的角色，与成员相同。
- **租户内账号**：同一个邮箱在两个租户各注册一次。它是单独的[账号体系](authentication.md#account-systems)（例如 `customer.Type`），[`login.Realm`](login.md#identifiers) 设置 `PerTenant: true`。

`PerTenant` 时邮箱和手机号在租户内唯一，登录得到的主体带有请求所在的租户，签发的令牌只能在这个租户使用，访问其他租户返回 `403`。`db` 是 `*bun.DB`，`customerTokens` 是客户的 `*apitoken.Store`，`device` 是登录设备（`apitoken.Device`）：

```go
customers := login.New(db, login.Options{Type: customer.Type, PerTenant: true /* 其余字段见登录与注册 */})

out, err := customers.Password(ctx, login.Email(in.Body.Email), in.Body.Password)
plain, err := customerTokens.Login(ctx, out.Subject, device) // out.Subject.Tenant 是请求所在的租户
```

客户表的主键是 `(tenant_id, id)` 时把租户编进主体的 ID；[第三方账号](social-login.md#linking-accounts)同样按租户隔离。

<a id="tokens"></a>
## 令牌与租户

- 登录令牌不绑定租户（`PerTenant` 的账号除外）。
- [个人访问令牌](api-tokens.md#personal-access-tokens)绑定创建时请求所在的租户：`sub.Tenant` 是 `"acme"` 时，`tokens.Create(ctx, sub, apitoken.Personal{...})` 创建的令牌只能访问 acme，访问其他租户返回 `403`；在平台（`""`）创建的不绑定。令牌能做的是用户在这个租户中的权限与 `Scopes` 的交集。
