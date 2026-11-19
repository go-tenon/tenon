# 完整示例：运营后台与多租户

在[单账号后台](admin-example.md)之上，本页给出一套 **SaaS** 常见结构：平台运营人员、租户成员两套账号，租户后台按路径区分租户，运营可**代登录**进某个成员的租户后台排查问题。

阅读前需要了解：[单账号后台](admin-example.md)；[认证进阶](authentication-advanced.md)；[多租户](tenancy.md)。

| | 运营后台 | 租户后台 |
| --- | --- | --- |
| 账号 | `staff`，`staff` 表 | `member`，`members` 表 |
| 入口 | 单页应用 → `/ops/api`（租户为 `""`） | 单页应用 → `/t/{tenant}/api` |
| 账号怎么来 | 管理员邀请 / 创建 | 租户内邀请或管理员创建 |
| 怎么登录 | 邮箱 + 密码 | 邮箱 + 密码 |
| 权限 | 平台域 `""` 上的 `authz` 角色 | 该租户上的 `authz` 角色 |
| 凭据 | 会话 cookie `__Host-ops` | 会话 cookie `__Host-session`；代登时签发带 `Impersonator` 的成员令牌 |

单账号流程（邮箱密码、会话 cookie、`RequireToken`）与[单账号后台](admin-example.md)相同，本页只写多出来的部分。租户确定、数据隔离、客户账号见[多租户](tenancy.md)；代登录约定见[认证：代为登录](authentication-advanced.md#impersonation)。

- [账号表](#account-tables)
- [装配](#assembling)
- [运营后台](#ops-api)
- [租户后台](#console-api)
- [代为登录](#impersonation)
- [接入入口](#wiring)
- [常见问题](#troubleshooting)

<a id="account-tables"></a>
## 账号表

两套账号各自一张表、各自一个 `Type`。权限交给 `authz`，表上不必再存权限数组；`Resolve` 负责停用检查，再交给 `roles.Resolve`。

```go
// internal/staff/staff.go
const Type identity.SubjectType = "staff"

type Staff struct {
    bun.BaseModel `bun:"table:staff"`
    ID        string    `bun:"id,pk"`
    CreatedAt time.Time `bun:"created_at,notnull"`
    UpdatedAt time.Time `bun:"updated_at,notnull"`
    Name      string    `bun:"name,notnull"`
    Disabled  bool      `bun:"disabled,notnull"`
}

func (s *Staff) Ref() identity.Ref { return identity.Ref{Type: Type, ID: s.ID} }

func Resolve(db bun.IDB, roles *authz.Store) identity.Resolver {
    return func(ctx context.Context, sub identity.Subject) (identity.Subject, error) {
        var s Staff
        err := db.NewSelect().Model(&s).Where("id = ?", sub.ID).Scan(ctx)
        if errors.Is(err, sql.ErrNoRows) || err == nil && s.Disabled {
            return identity.Subject{}, identity.ErrUnauthenticated.New()
        }
        if err != nil {
            return identity.Subject{}, err
        }
        return roles.Resolve(ctx, sub) // 平台域 "" 上的角色
    }
}
```

```go
// internal/member/member.go
const Type identity.SubjectType = "member"

type Member struct {
    bun.BaseModel `bun:"table:members"`
    ID        string    `bun:"id,pk"`
    CreatedAt time.Time `bun:"created_at,notnull"`
    UpdatedAt time.Time `bun:"updated_at,notnull"`
    Name      string    `bun:"name,notnull"`
    Disabled  bool      `bun:"disabled,notnull"`
}

func (m *Member) Ref() identity.Ref { return identity.Ref{Type: Type, ID: m.ID} }

func Resolve(db bun.IDB, roles *authz.Store) identity.Resolver {
    return func(ctx context.Context, sub identity.Subject) (identity.Subject, error) {
        // 代登：确认操作者在平台仍有 users:impersonate
        if !sub.Impersonator.IsZero() {
            perms, _, err := roles.Permissions(ctx, sub.Impersonator, "")
            if err != nil {
                return identity.Subject{}, err
            }
            if !identity.Grants(perms, "users:impersonate") {
                return identity.Subject{}, identity.ErrUnauthenticated.New()
            }
        }
        var m Member
        err := db.NewSelect().Model(&m).Where("id = ?", sub.ID).Scan(ctx)
        if errors.Is(err, sql.ErrNoRows) || err == nil && m.Disabled {
            return identity.Subject{}, identity.ErrUnauthenticated.New()
        }
        if err != nil {
            return identity.Subject{}, err
        }
        return roles.Resolve(ctx, sub) // 按 context 中的租户加载权限
    }
}
```

全局角色在代码中声明，应用启动时用 `EnsureRole` 确保存在，例如平台 `support`（含 `users:impersonate`、只读权限），租户模板 `owner` / `member`，见[预置角色](authorization.md#preset-roles)和[运营后台](tenancy.md#platform)。

<a id="assembling"></a>
## 装配

两套 `login.Realm`、两套 `apitoken.Store`、共用一个 `authz.Store`。两个前端在同一个源下，两套账号各用一个[会话 cookie](api-tokens.md#browser-sessions)：运营是 `__Host-ops`，成员是默认的 `__Host-session`。浏览器同时保存两个，互不覆盖，每个 API 只读自己的那一个。

```go
type Config struct {
    StaffToken  apitoken.Config `yaml:"staff_token"`
    MemberToken apitoken.Config `yaml:"member_token"`
}

type Accounts struct {
    Staff        *login.Realm
    StaffTokens  *apitoken.Store
    Members      *login.Realm
    MemberTokens *apitoken.Store
    Roles        *authz.Store
}

// StaffCookie 是运营的会话 cookie；成员用默认的 httpx.SessionCookie{}。
var StaffCookie = httpx.SessionCookie{Name: "__Host-ops"}

func New(db *bun.DB, cfg Config, hasher *hashing.Hasher, c *crypt.Crypt) *Accounts {
    every15 := ratelimit.Config{Rate: 5, Interval: 15 * time.Minute, Burst: 5}
    roles := authz.NewStore(db, auth.Permissions)

    staffRealm := login.New(db, login.Options{
        Type:     staff.Type,
        Hasher:   hasher,
        Crypt:    c,
        Policy:   login.Policy{PasswordKinds: []string{login.KindEmail}},
        Resolve:  staff.Resolve(db, roles),
        Attempts: pglimit.New(db, "login:staff:attempts", every15),
        Sends:    pglimit.New(db, "login:staff:sends", every15),
        // Send: 邀请设密邮件，见登录与注册
    })
    memberRealm := login.New(db, login.Options{
        Type:     member.Type,
        Hasher:   hasher,
        Crypt:    c,
        Policy:   login.Policy{PasswordKinds: []string{login.KindEmail}},
        Resolve:  member.Resolve(db, roles),
        Attempts: pglimit.New(db, "login:member:attempts", every15),
        Sends:    pglimit.New(db, "login:member:sends", every15),
    })

    return &Accounts{
        Staff: staffRealm, StaffTokens: apitoken.NewStore(db, cfg.StaffToken),
        Members: memberRealm, MemberTokens: apitoken.NewStore(db, cfg.MemberToken),
        Roles: roles,
    }
}
```

成员的 cookie 不区分租户：登录 token 通常不绑租户，同一个成员带着同一个会话进入他所属的各个租户，权限由各租户上的角色决定。

需要发布的迁移：`login`、`ratelimit`、`apitoken`、`authz`，以及 `staff` / `members` / `tenants` 等业务表。

<a id="ops-api"></a>
## 运营后台

登录 / 登出与[单账号后台](admin-example.md#auth-api)相同，只是换用 `Staff` 的 Realm 和 `StaffCookie`（`StaffCookie.Set`、`StaffCookie.Clear`）。额外提供「以此成员登录」：

```go
type ImpersonateInput struct {
    UserAgent string `header:"User-Agent"`
    Body      struct {
        TenantID string `json:"tenant_id" minLength:"1"`
        MemberID string `json:"member_id" minLength:"1"`
    }
}

// ImpersonateOutput 直接写入成员的会话 cookie：两个后台同源，运营的 cookie 不受影响。
type ImpersonateOutput struct {
    SetCookie *http.Cookie `header:"Set-Cookie"`
    Body      struct {
        ConsolePath string `json:"console_path"` // 例如 /t/acme/app，运营前端随后打开它
    }
}

func (h *handlers) impersonate(ctx context.Context, in *ImpersonateInput) (*ImpersonateOutput, error) {
    staff, _ := identity.FromContext(ctx)
    if !staff.Impersonator.IsZero() {
        return nil, identity.ErrForbidden.New() // 代登只有一层
    }
    targetRef := identity.Ref{Type: member.Type, ID: in.Body.MemberID}
    if _, isMember, err := h.roles.Permissions(ctx, targetRef, in.Body.TenantID); err != nil {
        return nil, err
    } else if !isMember {
        return nil, ErrMemberNotFound.New() // 包内 errorx.Define(errorx.NotFound, "member.not_found")
    }

    target := identity.Subject{
        Type:         member.Type,
        ID:           in.Body.MemberID,
        Impersonator: staff.Ref(),
    }
    token, err := h.memberTokens.Login(ctx, target, apitoken.Device{UserAgent: in.UserAgent})
    if err != nil {
        return nil, err
    }
    out := &ImpersonateOutput{SetCookie: httpx.SessionCookie{}.Set(token, h.memberLifetime)}
    out.Body.ConsolePath = "/t/" + in.Body.TenantID + "/app"
    return out, nil
}

// 注册时：
rest.Register(api, op("ops-impersonate", http.MethodPost, "/members/impersonate",
    rest.RequireToken(api, acc.StaffTokens.Validate, "users:impersonate")), h.impersonate)
```

成员的 token 不经过前端代码：响应直接写入成员的会话 cookie，运营前端随后打开 `console_path`，租户后台就以这个成员的身份工作。运营自己的 `__Host-ops` 不受影响，排查结束后回到运营后台仍然是登录状态。

<a id="console-api"></a>
## 租户后台

`Tenant` 从路径取出；`Accounts` 只接受 `member`。成员自己的登录 / 登出同样用会话 cookie，名称是默认的 `__Host-session`。

租户后台 UI：若 `sub.Impersonator` 非零，显示「正在代 {成员} 操作」和退出；退出时 `Revoke` 当前 credential，清 cookie，回到运营后台。

业务路由照常 `RequireToken(api, acc.MemberTokens.Validate, "articles:write")`；列表与读写必须带 `subject.Tenant` 条件，见[多租户：数据隔离](tenancy.md#data-isolation)。

<a id="impersonation"></a>
## 代为登录

端到端流程：

```text
运营已登录（__Host-ops）
  │ POST /ops/api/members/impersonate  （需要 users:impersonate）
  ▼
签发成员的 token（Subject=member，Impersonator=staff），写入 __Host-session
  │ 运营前端打开 /t/{tenant}/app
  ▼
租户后台带着成员的 cookie 调 /t/{tenant}/api
  │ 每个请求：member.Resolve 校验 Impersonator 仍有权限，再 roles.Resolve
  ▼
排查结束：撤销该 credential，清 cookie，回到 /ops
```

应用还需自行约定：

- 代登期间禁止改密码、管令牌、付款等——在对应 `Handle` 里拒绝 `!subject.Impersonator.IsZero()`
- [操作日志](../features/activity.md)会记下 `Impersonator`；自写日志时同时写下 `subject` 与 `impersonator`

<a id="wiring"></a>
## 接入入口

```go
acc := accounts.New(a.db, a.cfg.Accounts, hashing.New(a.cfg.Hashing), a.crypt)

router.Route("/ops/api", func(r chi.Router) {
    ops := rest.New(r, rest.Options{
        BasePath:      "/ops/api",
        Accounts:      identity.Accounts{staff.Type: staff.Resolve(a.db, acc.Roles)},
        SessionCookie: accounts.StaffCookie, // 运营只读 __Host-ops
    })
    opsapi.RegisterRoutes(ops, acc)
})
router.Handle("/ops/*", opsSPA)

router.Route("/t/{tenant}", func(r chi.Router) {
    r.Route("/api", func(r chi.Router) {
        console := rest.New(r, rest.Options{
            BasePath: "/t/{tenant}/api",
            Tenant:   func(ctx huma.Context) string { return ctx.Param("tenant") },
            Accounts: identity.Accounts{member.Type: member.Resolve(a.db, acc.Roles)},
        })
        consoleapi.RegisterRoutes(console, acc)
    })
    r.Handle("/app/*", consoleSPA)
})
```

- 运营 token 打到租户 API、成员 token 打到运营 API → `401`（`Accounts` 不接受）
- 成员令牌绑了租户时（例如个人访问令牌），访问其他租户 → `403`，见[令牌与租户](tenancy.md#tokens)
- 登录令牌通常不绑租户：同一成员可带同一会话进入他所属的多个租户；权限由各租户上的角色决定

第三方登录、短信注册的读者 App 等仍见[第三方登录](social-login.md)与[登录与注册](login.md)；本页专注运营 + 租户 + 代登录这一条主线。

<a id="troubleshooting"></a>
## 常见问题

**运营的 token 调用租户后台的接口，得到 401**

两套账号各自的 API 只接受自己的账号，这是预期的行为。

**代登录后，租户后台仍然显示未登录**

两个后台必须同源，代登录的响应写入的成员 cookie 才能被租户后台读到；`Secure` 的 cookie 只在 HTTPS 和 `localhost` 上保存。

**运营登录后，租户后台的成员被退出了（或者反过来）**

两套账号用了同一个 cookie 名称。运营的 API 设置 `SessionCookie: httpx.SessionCookie{Name: "__Host-ops"}`，见[装配](#assembling)。

**成员的个人访问令牌访问其他租户，得到 403**

令牌绑定了租户，见[令牌与租户](tenancy.md#tokens)。
