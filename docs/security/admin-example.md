# 完整示例：单账号后台

本页给出一套**数据库驱动的单账号后台**最短闭环：一张用户表、邮箱密码登录、会话 cookie、用 `RequireToken` 保护业务接口。读完就能搭起可用的后台认证；细节由后面各章拆开讲，多租户与代登录见[运营后台与多租户](saas-example.md)。

阅读前需要了解：[入门教程第 6 章](../tutorial/06-login.md)；[依赖装配](../concepts/dependencies.md)。

| | 本示例 |
| --- | --- |
| 账号 | 一套 `user`，`users` 表 |
| 入口 | 单页应用调用 `/admin/api` |
| 账号怎么来 | 管理员用命令创建（不开放注册） |
| 怎么登录 | 邮箱 + 密码 |
| 凭据 | 登录 token 放在 HttpOnly 的会话 cookie 中，页面上的脚本读不到它 |

- [怎么运转](#how-it-works)
- [用户表](#users)
- [装配](#assembling)
- [登录接口](#auth-api)
- [保护业务路由](#protecting-routes)
- [接入入口](#wiring)
- [接下来读什么](#next)
- [常见问题](#troubleshooting)

<a id="how-it-works"></a>
## 怎么运转

```text
登录 ──► login.Realm 校验邮箱密码
     ──► apitoken.Store 签发登录 token
     ──► 写入会话 cookie（__Host-session，HttpOnly）

请求 ──► 浏览器自动带上 cookie（写请求必须来自同源的页面）
     ──► Store.Validate 认出是谁
     ──► Resolver 从库加载权限 / 检查停用
     ──► RequireToken 检查权限后进入处理器
```

生命周期与其他模块相同：启动时 `Load` 校验配置 → 入口创建 `Realm` / `Store` 一次 → 请求时复用。

<a id="users"></a>
## 用户表

应用的 `users` 表只放业务字段；邮箱、密码哈希在框架的 `auth_*` 表里，用 `identity.Ref` 引用。

```go
package user

import (
    "context"
    "database/sql"
    "errors"
    "time"

    "github.com/uptrace/bun"

    "github.com/go-tenon/tenon/errorx"
    "github.com/go-tenon/tenon/identity"
)

const Type identity.SubjectType = "user"

var ErrDisabled = errorx.Define(errorx.Unauthenticated, "user.disabled")

type User struct {
    bun.BaseModel `bun:"table:users"`

    ID          string    `bun:"id,pk"`
    Name        string    `bun:"name,notnull"`
    Disabled    bool      `bun:"disabled,notnull"`
    Permissions []string  `bun:"permissions,array,notnull"`
    CreatedAt   time.Time `bun:"created_at,notnull"`
}

func (u *User) Ref() identity.Ref { return identity.Ref{Type: Type, ID: u.ID} }

// Resolve 每个请求重新读库：停用立即失效，权限随库中字段变化。
func Resolve(db bun.IDB) identity.Resolver {
    return func(ctx context.Context, sub identity.Subject) (identity.Subject, error) {
        var u User
        err := db.NewSelect().Model(&u).Where("id = ?", sub.ID).Scan(ctx)
        if errors.Is(err, sql.ErrNoRows) || err == nil && u.Disabled {
            return identity.Subject{}, ErrDisabled.New()
        }
        if err != nil {
            return identity.Subject{}, err
        }
        sub.Permissions = u.Permissions
        return sub, nil
    }
}
```

创建账号时在事务里插入 `users` 行，再用 `login.Attach` / `SetPassword` 写入邮箱和密码（与[教程](../tutorial/06-login.md)相同）。需要发布的迁移：`login`、`ratelimit`、`apitoken`，以及 `users` 表自己的迁移，见[发布框架迁移](../database/migrations.md#publishing-framework-migrations)。

<a id="assembling"></a>
## 装配

在入口的 `Open` 中创建 `Realm` 和 `Store`。`Hashing` / `Crypt` / 限流的配置挂法见[哈希](hashing.md)、[加密](encryption.md)、[登录装配](login.md#assembling)。

```go
every15 := ratelimit.Config{Rate: 5, Interval: 15 * time.Minute, Burst: 5}
users := login.New(db, login.Options{
    Type:     user.Type,
    Hasher:   hashing.New(cfg.Hashing),
    Crypt:    crypt.New(cfg.Crypt),
    Policy:   login.Policy{PasswordKinds: []string{login.KindEmail}},
    Resolve:  user.Resolve(db), // 登录成功签发前也走一遍：停用账号拿不到 token
    Attempts: pglimit.New(db, "login:attempts", every15),
    Sends:    pglimit.New(db, "login:sends", every15),
})
tokens := apitoken.NewStore(db, cfg.APIToken)
```

根配置里需要 `crypt`、`hashing`、`api_token`；限流计数保存在数据库中，记得发布 `pglimit` 的迁移。

<a id="auth-api"></a>
## 登录接口

登录成功后把 token 写进[会话 cookie](api-tokens.md#browser-sessions)，之后浏览器自动带上它，页面不保管 token。

```go
package adminauth

import (
    "context"
    "net/http"
    "time"

    "github.com/danielgtaylor/huma/v2"

    "github.com/go-tenon/tenon/apitoken"
    "github.com/go-tenon/tenon/httpx"
    "github.com/go-tenon/tenon/httpx/rest"
    "github.com/go-tenon/tenon/identity"
    "github.com/go-tenon/tenon/login"
)

type LoginInput struct {
    UserAgent string `header:"User-Agent"`
    Body      struct {
        Email    string `json:"email" format:"email"`
        Password string `json:"password" minLength:"1"`
    }
}

// SessionOutput 写入或清除会话 cookie。
type SessionOutput struct {
    SetCookie *http.Cookie `header:"Set-Cookie"`
}

type handlers struct {
    users    *login.Realm
    tokens   *apitoken.Store
    lifetime time.Duration
}

func (h *handlers) login(ctx context.Context, in *LoginInput) (*SessionOutput, error) {
    out, err := h.users.Password(ctx, login.Email(in.Body.Email), in.Body.Password)
    if err != nil {
        return nil, err // 401 / 429
    }
    token, err := h.tokens.Login(ctx, out.Subject, apitoken.Device{UserAgent: in.UserAgent})
    if err != nil {
        return nil, err
    }
    return &SessionOutput{SetCookie: httpx.SessionCookie{}.Set(token, h.lifetime)}, nil
}

func (h *handlers) logout(ctx context.Context, _ *struct{}) (*SessionOutput, error) {
    sub, _ := identity.FromContext(ctx)
    if err := h.tokens.Revoke(ctx, sub, sub.Credential); err != nil {
        return nil, err
    }
    return &SessionOutput{SetCookie: httpx.SessionCookie{}.Clear()}, nil
}

func RegisterRoutes(api *rest.API, requireUser func(*huma.Operation), users *login.Realm, tokens *apitoken.Store, lifetime time.Duration) {
    h := &handlers{users: users, tokens: tokens, lifetime: lifetime}

    op := huma.Operation{OperationID: "auth-login", Method: http.MethodPost, Path: "/auth/login", DefaultStatus: http.StatusNoContent}
    rest.Public()(&op)
    rest.SameOrigin(api)(&op) // 只接受本站页面的登录，防止登录 CSRF
    rest.Register(api, op, h.login)

    op = huma.Operation{OperationID: "auth-logout", Method: http.MethodPost, Path: "/auth/logout", DefaultStatus: http.StatusNoContent}
    requireUser(&op)
    rest.Register(api, op, h.logout)
}
```

打开页面时先读一次当前用户（例如 `GET /admin/api/me`）：cookie 有效就是已登录，`401` 就进登录页。前端写法见 [API 客户端](../http/clients.md#creating-the-client)。

<a id="protecting-routes"></a>
## 保护业务路由

```go
requireUser := rest.RequireToken(api, tokens.Validate) // 只要登录
requireWrite := rest.RequireToken(api, tokens.Validate, "articles:write")

op := huma.Operation{OperationID: "article-create", Method: http.MethodPost, Path: "/articles"}
requireWrite(&op)
rest.Register(api, op, NewCreate(db).HTTP)
```

处理器里用 `identity.FromContext` 取出主体，再交给业务动作。权限名、通配符和资源级检查见[授权](authorization.md)。

<a id="wiring"></a>
## 接入入口

```go
router.Route("/admin/api", func(r chi.Router) {
    api := rest.New(r, rest.Options{
        Title:    "Admin API",
        BasePath: "/admin/api",
        Accounts: identity.Accounts{user.Type: user.Resolve(a.db)},
    })
    requireUser := rest.RequireToken(api, a.tokens.Validate)
    adminauth.RegisterRoutes(api, requireUser, a.users, a.tokens, a.cfg.APIToken.Lifetime)
    article.RegisterRoutes(api, requireUser, a.db) // 业务路由按需加 RequireToken 的权限参数
})
router.Handle("/admin/*", adminSPA)
```

`Accounts` 只接受 `user`：别的账号体系的 token 打过来与无效 token 一样是 `401`。

<a id="next"></a>
## 接下来读什么

| 想弄清 | 去读 |
| --- | --- |
| 凭据、主体、`Accounts`、校验器 | [认证](authentication.md) |
| 邮箱密码、邀请、二次验证、确认身份 | [登录与注册](login.md) |
| 会话 cookie、撤销、设备列表 | [API 令牌](api-tokens.md) |
| 路由权限、角色、`authz` | [授权](authorization.md) |
| 运营后台 + 租户后台 + 代登录 | [运营后台与多租户](saas-example.md) |

动手跟着改博客项目时，仍以[入门教程：作者登录](../tutorial/06-login.md)为准；本页是参考向的完整切片。

<a id="troubleshooting"></a>
## 常见问题

**登录成功，刷新页面后又要登录**

会话 cookie 没有保存或没有带上：页面和接口要同源；`Secure` 的 cookie 只在 HTTPS 和 `localhost` 上保存，见[浏览器的会话 cookie](api-tokens.md#browser-sessions)。

**修改数据时返回 `403 http.cross_site`**

用 cookie 认证的写请求不是从本站的页面发出的。浏览器中的页面不会遇到；脚本和工具改用 `Authorization: Bearer`。

**其他账号体系的 token 调用后台接口，得到 401**

`Accounts` 只接受 `user`，这是预期的行为，见[接入入口](#wiring)。
