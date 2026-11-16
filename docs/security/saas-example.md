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
| 凭据 | 各自一套 JWT + refresh cookie | 同左；代登时签发带 `Impersonator` 的成员令牌 |

单账号流程（邮箱密码、JWT cookie、`RequireBearer`）与[单账号后台](admin-example.md)相同，本页只写多出来的部分。租户确定、数据隔离、客户账号见[多租户](tenancy.md)；代登录约定见[认证：代为登录](authentication-advanced.md#impersonation)。

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
    Name      string    `bun:"name,notnull"`
    Disabled  bool      `bun:"disabled,notnull"`
    CreatedAt time.Time `bun:"created_at,notnull"`
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
    Name      string    `bun:"name,notnull"`
    Disabled  bool      `bun:"disabled,notnull"`
    CreatedAt time.Time `bun:"created_at,notnull"`
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

填充数据时创建全局角色，例如平台 `support`（含 `users:impersonate`、只读权限），租户模板 `owner` / `member`，见[授权](authorization.md#creating-roles)和[运营后台](tenancy.md#platform)。

<a id="assembling"></a>
## 装配

两套 `login.Realm`、两套 `apitoken.Store` / `JWT`、共用一个 `authz.Store`。cookie 的 `Path` 分别限制在各自的 auth 前缀下。

```go
type Config struct {
    StaffToken  apitoken.Config `yaml:"staff_token"`
    MemberToken apitoken.Config `yaml:"member_token"`
}

type Accounts struct {
    Staff        *login.Realm
    StaffTokens  *apitoken.Store
    StaffJWT     *apitoken.JWT
    StaffCookie  apitoken.RefreshCookie
    Members      *login.Realm
    MemberTokens *apitoken.Store
    MemberJWT    *apitoken.JWT
    MemberCookie apitoken.RefreshCookie
    Roles        *authz.Store
}

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

    staffTokens := apitoken.NewStore(db, cfg.StaffToken)
    memberTokens := apitoken.NewStore(db, cfg.MemberToken)
    return &Accounts{
        Staff: staffRealm, StaffTokens: staffTokens,
        StaffJWT:    apitoken.NewJWT(staffTokens, apitoken.JWTConfig{Keys: apitoken.HMACKeys(c)}),
        StaffCookie: apitoken.RefreshCookie{Name: "ops_refresh", Path: "/ops/api/auth"},
        Members: memberRealm, MemberTokens: memberTokens,
        MemberJWT:    apitoken.NewJWT(memberTokens, apitoken.JWTConfig{Keys: apitoken.HMACKeys(c)}),
        MemberCookie: apitoken.RefreshCookie{Name: "console_refresh", Path: "/t"},
        Roles:        roles,
    }
}
```

`MemberCookie.Path` 设为 `/t`，这样任意 `/t/{tenant}/api/auth` 都能带上 refresh cookie；若希望更窄，可按租户拆 cookie（实现更复杂，一般不必要）。

需要发布的迁移：`login`、`ratelimit`、`apitoken`、`authz`，以及 `staff` / `members` / `tenants` 等业务表。

<a id="ops-api"></a>
## 运营后台

登录 / 刷新 / 登出与[单账号后台](admin-example.md#auth-api)相同，只是换用 `Staff` 的 Realm、JWT 和 cookie 名 `ops_refresh`。额外提供「以此成员登录」：

```go
type ImpersonateInput struct {
    UserAgent string `header:"User-Agent"`
    Body      struct {
        TenantID string `json:"tenant_id" minLength:"1"`
        MemberID string `json:"member_id" minLength:"1"`
    }
}

type ImpersonateOutput struct {
    Body struct {
        apitoken.Session        // refresh_token 交给租户后台前端设 cookie，不要放进 URL
        TenantID         string `json:"tenant_id"`
        ConsolePath      string `json:"console_path"` // 例如 /t/acme/app
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
    pair, err := h.memberJWT.Login(ctx, target, apitoken.Device{UserAgent: in.UserAgent})
    if err != nil {
        return nil, err
    }
    out := &ImpersonateOutput{}
    out.Body.Session = apitoken.NewSession(pair)
    out.Body.TenantID = in.Body.TenantID
    out.Body.ConsolePath = "/t/" + in.Body.TenantID + "/app"
    return out, nil
}

// 注册时：
rest.Register(api, op("ops-impersonate", http.MethodPost, "/members/impersonate",
    rest.RequireBearer(api, acc.StaffJWT.Validate, "users:impersonate")), h.impersonate)
```

运营前端拿到 `refresh_token` 后，打开租户后台并调用一次租户侧的交接接口（见下），由租户后台写入自己的 cookie。**不要**把 token 放进查询参数。

<a id="console-api"></a>
## 租户后台

`Tenant` 从路径取出；`Accounts` 只接受 `member`。成员自己的登录 / 刷新 / 登出同样用 JWT + cookie，cookie 名 `console_refresh`。

代登交接：公开接口接收运营传来的 refresh，校验后写成租户后台的 cookie，并返回 access——之后与普通成员会话相同。

```go
type HandoffInput struct {
    Body struct {
        RefreshToken string `json:"refresh_token" minLength:"1"`
    }
}

func (h *handlers) handoff(ctx context.Context, in *HandoffInput) (*SessionOutput, error) {
    pair, err := h.jwt.Refresh(ctx, in.Body.RefreshToken)
    if err != nil {
        return nil, err
    }
    // Refresh 已轮换；用新的 Pair 写 cookie。也可 Login 后只接受尚未用过的 refresh，按产品选择。
    return h.session(pair), nil
}
```

租户后台 UI：若 `sub.Impersonator` 非零，显示「正在代 {成员} 操作」和退出；退出时 `Revoke` 当前 credential，清 cookie，回到运营后台。

业务路由照常 `RequireBearer(api, memberJWT.Validate, "articles:write")`；列表与读写必须带 `subject.Tenant` 条件，见[多租户：数据隔离](tenancy.md#data-isolation)。

<a id="impersonation"></a>
## 代为登录

端到端流程：

```text
运营已登录（staff JWT）
  │ POST /ops/api/members/impersonate  （需要 users:impersonate）
  ▼
得到成员的 Pair（Subject=member，Impersonator=staff）
  │ 打开 /t/{tenant}/app，POST refresh_token 到交接接口
  ▼
租户后台写入 console_refresh cookie，之后用成员 access 调 /t/{tenant}/api
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
        BasePath: "/ops/api",
        Accounts: identity.Accounts{staff.Type: staff.Resolve(a.db, acc.Roles)},
    })
    opsapi.Register(ops, acc)
})
router.Handle("/ops/*", opsSPA)

router.Route("/t/{tenant}", func(r chi.Router) {
    r.Route("/api", func(r chi.Router) {
        console := rest.New(r, rest.Options{
            BasePath: "/t/{tenant}/api",
            Tenant:   func(ctx huma.Context) string { return ctx.Param("tenant") },
            Accounts: identity.Accounts{member.Type: member.Resolve(a.db, acc.Roles)},
        })
        consoleapi.Register(console, acc)
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

运营前端拿到 refresh token 后，要调用租户侧的交接接口，由租户后台写入自己的 cookie。不要把 token 放进查询参数。

**成员的个人访问令牌访问其他租户，得到 403**

令牌绑定了租户，见[令牌与租户](tenancy.md#tokens)。
