# API 令牌

JSON API 使用 token [认证](authentication.md)。`apitoken` 包把 token 存在数据库中，提供签发、校验、滑动过期和撤销，校验函数可以直接交给 `rest.RequireToken`。

同一个 token 有两种携带方式：浏览器把它放在 HttpOnly 的[会话 cookie](#browser-sessions) 中，页面上的脚本读不到它；App、脚本和第三方放在 `Authorization: Bearer` 请求头中。

阅读前需要了解：[认证](authentication.md)；[入门教程第 6 章](../tutorial/06-login.md)。

token 分为两类：

| 类别 | 签发方式 | 有效期 | 用途 |
| --- | --- | --- | --- |
| 登录 token | `Login`，用户在单页应用或 App 中登录时 | 按[配置](#configuration)滑动：经常使用就一直有效，空闲太久或超过最长有效期后失效 | 代表一台设备 |
| 个人访问令牌 | `Create`，用户在“API 令牌”页面手动创建 | 由创建者指定，可以不过期 | 脚本、CI 和第三方集成 |

表中只保存 token 的 SHA-256，明文只在签发时返回一次。

token 是不透明的随机字符串：每个请求都按 token 查一次表，撤销立即生效。

- [示例用法](#example-usage)
- [配置](#configuration)
- [装配](#assembling)
- [登录与登出](#login-and-logout)
- [浏览器的会话 cookie](#browser-sessions)
- [个人访问令牌](#personal-access-tokens)
- [设备与令牌列表](#listing-tokens)
- [撤销令牌](#revoking-tokens)
- [加载权限](#loading-permissions)
- [OAuth 令牌](#oauth-tokens)
- [过期清理](#cleanup)
- [常见问题](#troubleshooting)
- [API 参考](#api-reference)

<a id="example-usage"></a>
## 示例用法

下面在登录成功后签发令牌，用它保护路由，并在退出登录时撤销它：

```go
tokens := apitoken.NewStore(db, cfg.APIToken)

// 登录成功后签发
plain, err := tokens.Login(ctx, sub, apitoken.Device{IP: ip, UserAgent: ua})

// 保护路由：浏览器的会话 cookie，或者 Authorization: Bearer <plain>
rest.RequireToken(api, tokens.Validate)(&op)

// 退出登录
err = tokens.Revoke(ctx, sub, sub.Credential)
```

浏览器的登录接口把 `plain` 写进[会话 cookie](#browser-sessions)，其他客户端把它放在 `Authorization: Bearer` 请求头中。数据库只保存令牌的哈希，所以 `plain` 只在签发时出现一次。用户每次使用令牌，它的有效期就向后顺延；空闲超过时限，令牌自动过期。

<a id="configuration"></a>
## 配置

把 `apitoken.Config` 放进应用的根配置结构体，配置段为 `api_token`：

```go
type Config struct {
    // ...
    APIToken apitoken.Config `yaml:"api_token"`
}
```

```yaml
api_token:
  lifetime: 720h              # 登录 token 的最长有效期，到期后必须重新登录
  idle_timeout: 24h           # 超过这么久没有使用即失效，0 表示不限制
  remember_idle_timeout: 168h # 登录时选择“记住我”的空闲时长
```

上面是默认值。登录 token 的截止时间是「签发时间 + `lifetime`」与「最近使用时间 + 空闲时长」中较早的一个。登录 token 每次使用都会延长它的有效期，收到 `401` 时让用户重新登录即可。

有效期在签发时写入 token：修改配置只影响之后签发的 token，已经签发的 token 仍然按签发时的有效期滑动。

`apitoken.Config` 自己实现了 `Validate() error`。挂进根配置后，[`config.Load`](../general/configuration.md#loading-configuration) 会在启动时自动调用它——检查的是「有效期等配置项合不合法」，不是请求里的 Bearer token。请求时验 token 的是下一节的 `Store.Validate`，二者不要混：

| | `apitoken.Config.Validate` | `Store.Validate` |
| --- | --- | --- |
| 时机 | **启动时**，加载配置之后 | **每次请求**，`RequireToken` 调校验器时 |
| 干什么 | 配置写得对不对 | 这串 token 对应谁、是否仍有效 |
| 失败后果 | 应用起不来 | 返回 `401` |

<a id="assembling"></a>
## 装配

串起来看：

1. 发布迁移，建好 `api_tokens` 表
2. 启动时 `config.Load` 读出 `api_token` 段，并跑 `Config.Validate`
3. 在入口调用 `apitoken.NewStore` 一次，把 `Store` 交给登录接口和路由保护
4. 请求到来时，`RequireToken` 从会话 cookie 或 `Authorization` 中取出 token，调用 `tokens.Validate`

`apitoken.NewStore` 接收应用的 `*bun.DB` 和配置。使用前先发布 `api_tokens` 表的迁移，见[发布框架迁移](../database/migrations.md#publishing-framework-migrations)：

```go
migrationCmd.AddCommand(migratecmd.Publish(
    migrate.Source{Name: "apitoken", FS: apitoken.Migrations()},
))
```

`Store.Validate` 的签名就是 `identity.TokenValidator`，可以直接用来保护路由：

```go
tokens := apitoken.NewStore(a.db, a.cfg.APIToken)
requireArticles := rest.RequireToken(api, tokens.Validate, "articles")
```

产品有多套账号、需要不同的有效期时，例如商家员工空闲 1 天失效、移动端的客户 90 天才过期，每套账号用自己的配置创建一个 `Store` 签发。它们共用 `api_tokens` 表，`Validate` 只读表，用其中任何一个校验都可以：

```go
merchantTokens := apitoken.NewStore(a.db, a.cfg.MerchantToken)
customerTokens := apitoken.NewStore(a.db, a.cfg.CustomerToken)
plain, err := customerTokens.Login(ctx, sub, device)
```

每个 API 接受哪些账号的 token 由 [`rest.Options.Accounts`](authentication.md#protecting-routes) 决定，与签发它的 `Store` 无关。

`Validate` 返回的主体 `Method` 为 `bearer`，`Credential` 是 token 的 ID。token 只说明“这是谁”，不携带用户的权限：权限在每个请求中[加载](#loading-permissions)。它每分钟最多为一个 token 写一次库，记录最近使用时间并延长登录 token 的有效期，因此空闲时长的精度约为一分钟。

<a id="login-and-logout"></a>
## 登录与登出

App、脚本等非浏览器客户端用账号密码换取登录 token，token 在响应体中；浏览器的登录把它写进 cookie，见[浏览器的会话 cookie](#browser-sessions)。例如 `routes.go` 中的登录和登出接口，`users` 是用户的 [`login.Realm`](login.md)，它校验邮箱和密码：

```go
type TokenInput struct {
    UserAgent string `header:"User-Agent"`
    Body      struct {
        Email    string `json:"email" format:"email"`
        Password string `json:"password" minLength:"1"`
        Remember bool   `json:"remember,omitempty"`
    }
    ip string
}

// Resolve 记录客户端地址，见 Huma 的 Resolver。
func (in *TokenInput) Resolve(ctx huma.Context) []error {
    in.ip, _, _ = net.SplitHostPort(ctx.RemoteAddr())
    return nil
}

type TokenOutput struct {
    Body struct {
        Token string `json:"token"`
    }
}

func issueToken(users *login.Realm, tokens *apitoken.Store) func(context.Context, *TokenInput) (*TokenOutput, error) {
    return func(ctx context.Context, in *TokenInput) (*TokenOutput, error) {
        out, err := users.Password(ctx, login.Email(in.Body.Email), in.Body.Password)
        if err != nil {
            return nil, err
        }
        plain, err := tokens.Login(ctx, out.Subject, apitoken.Device{
            Remember:  in.Body.Remember,
            IP:        in.ip,
            UserAgent: in.UserAgent,
        })
        if err != nil {
            return nil, err
        }
        resp := &TokenOutput{}
        resp.Body.Token = plain
        return resp, nil
    }
}

func revokeToken(tokens *apitoken.Store) func(context.Context, *struct{}) (*struct{}, error) {
    return func(ctx context.Context, _ *struct{}) (*struct{}, error) {
        sub, _ := identity.FromContext(ctx)
        return nil, tokens.Revoke(ctx, sub, sub.Credential)
    }
}
```

登录接口是公开的，登出接口用 `RequireToken` 保护：

```go
op := huma.Operation{OperationID: "auth-token-create", Method: http.MethodPost, Path: "/auth/tokens"}
rest.Public()(&op)
rest.Register(api, op, issueToken(users, tokens))

op = huma.Operation{OperationID: "auth-token-delete", Method: http.MethodDelete, Path: "/auth/tokens/current"}
rest.RequireToken(api, tokens.Validate)(&op)
rest.Register(api, op, revokeToken(tokens))
```

`Login` 记录 IP 和 User-Agent，用于[设备列表](#listing-tokens)，并保存 `out.Subject.Auth`（这次登录通过的验证方式和时间），之后校验得到的主体带着它。敏感操作前用户再次输入密码时，`Confirm(ctx, sub, methods...)` 更新它，见[确认身份](login-security.md#confirming)。登出时把主体的 `Credential` 交给 `Revoke`，撤销的就是本次请求所用的 token。验证码登录、注册和找回密码见[登录与注册](login.md)。

<a id="browser-sessions"></a>
## 浏览器的会话 cookie

浏览器中的页面不保管 token。登录接口把 token 写进**会话 cookie**，之后浏览器随每个同源请求自动带上它：

- `httpx.SessionCookie{}.Set(token, maxAge)` 返回这个 cookie：名称是 `__Host-session`，`HttpOnly`（页面上的脚本读不到它，即使页面被注入了恶意脚本，也拿不走登录凭据）、`Secure`、`SameSite=Lax`、`Path=/`。`maxAge` 是浏览器保存它的时长，通常是配置中的 `lifetime`；token 是否有效仍由服务端判断。
- `httpx.SessionCookie{}.Clear()` 返回删除它的 `Set-Cookie`，登出时使用。
- `rest.RequireToken` 同时接受这个 cookie 和 `Authorization: Bearer`，两者都有时以请求头为准。
- 同一个源下有多套账号体系时（例如运营后台和租户控制台），各用一个名称，例如 `httpx.SessionCookie{Name: "__Host-ops"}`，交给这套 API 的 `rest.Options.SessionCookie`（和 `realtime.Options.SessionCookie`）。两个 cookie 同时存在、互不覆盖，每个 API 只读自己的那一个。

```go
type SessionOutput struct {
    SetCookie *http.Cookie `header:"Set-Cookie"`
}

func createSession(users *login.Realm, tokens *apitoken.Store, lifetime time.Duration) func(context.Context, *TokenInput) (*SessionOutput, error) {
    return func(ctx context.Context, in *TokenInput) (*SessionOutput, error) {
        out, err := users.Password(ctx, login.Email(in.Body.Email), in.Body.Password)
        if err != nil {
            return nil, err
        }
        plain, err := tokens.Login(ctx, out.Subject, apitoken.Device{Remember: in.Body.Remember, IP: in.ip, UserAgent: in.UserAgent})
        if err != nil {
            return nil, err
        }
        return &SessionOutput{SetCookie: httpx.SessionCookie{}.Set(plain, lifetime)}, nil
    }
}

op := huma.Operation{OperationID: "auth-session-create", Method: http.MethodPost, Path: "/auth/session", DefaultStatus: http.StatusNoContent}
rest.Public()(&op)
rest.SameOrigin(api)(&op)
rest.Register(api, op, createSession(users, tokens, cfg.APIToken.Lifetime))
```

cookie 由浏览器自动附加，其他网站的页面向你的 API 发请求时也可能带上它，冒充用户操作（跨站请求伪造，CSRF）。框架做了三层防护，应用不需要 CSRF token：

- `SameSite=Lax`：其他网站的页面发出的请求不带这个 cookie，只有顶层的页面跳转例外；
- `rest.RequireToken`：用 cookie 认证的写请求（GET、HEAD、OPTIONS 以外）必须来自同源的页面，按浏览器发送的 `Sec-Fetch-Site` 判断，没有时比较 `Origin`，否则返回 `403 http.cross_site`。Bearer 请求不受影响；
- `rest.SameOrigin`：给会设置 cookie 的公开接口（浏览器的登录）用，防止其他网站让用户在不知情时登录攻击者的账号。

前端用 `@go-tenon/client` 的会话记录当前是谁，见[客户端代码](../http/clients.md#typescript)。会话 cookie 只在同源请求中发送：页面和 API 必须由同一个源提供，跨域的调用方（例如另一个域名上的前端）用 Bearer。`Secure` 的 cookie 只在 HTTPS 和 `http://localhost` 上保存，开发时用 `localhost` 访问。

<a id="personal-access-tokens"></a>
## 个人访问令牌

用户可以为脚本或第三方集成创建令牌，并把它限制在部分权限内。下面是单页应用“API 令牌”页面调用的创建接口，与[登录与登出](#login-and-logout)的接口放在同一个文件中，令牌明文只在响应中出现这一次：

```go
type CreateTokenInput struct {
    Body struct {
        Name string `json:"name" minLength:"1" maxLength:"100"`
    }
}

type CreateTokenOutput struct {
    Body struct {
        Token string `json:"token" doc:"令牌明文，只返回这一次"`
    }
}

func createToken(tokens *apitoken.Store) func(context.Context, *CreateTokenInput) (*CreateTokenOutput, error) {
    return func(ctx context.Context, in *CreateTokenInput) (*CreateTokenOutput, error) {
        sub, _ := identity.FromContext(ctx)
        plain, err := tokens.Create(ctx, sub, apitoken.Personal{
            Name:   in.Body.Name,
            Scopes: []string{"articles:read"},
        })
        if err != nil {
            return nil, err
        }
        out := &CreateTokenOutput{}
        out.Body.Token = plain
        return out, nil
    }
}
```

```go
op = huma.Operation{OperationID: "auth-personal-token-create", Method: http.MethodPost, Path: "/auth/personal-tokens"}
rest.RequireToken(api, tokens.Validate)(&op)
rest.Register(api, op, createToken(tokens))
```

`apitoken.Personal` 的字段：

| 字段 | 说明 |
| --- | --- |
| `Name` | 在令牌列表中显示的名称，1 到 255 个字符 |
| `Scopes` | 令牌被限制在内的权限，至少一个，支持通配符，例如 `articles:*` |
| `Tenant` | 不为空时，令牌只能在这个租户中使用 |
| `ExpiresAt` | 过期时间，零值表示不过期 |

令牌能做的是**用户当前的权限与 `Scopes` 的交集**：`Scopes` 写了 `*`，也以用户本人的权限为上限；用户被收回权限后，令牌随之收窄。个人访问令牌按 `ExpiresAt` 过期，`RevokeOthers` 保留它。

<a id="listing-tokens"></a>
## 设备与令牌列表

`List` 返回用户全部未过期的 token，最近使用的在前：

```go
list, err := tokens.List(ctx, sub)
for _, t := range list {
    // t.Kind、t.Name、t.IP、t.UserAgent、t.LastUsedAt、t.ExpiresAt
    // t.Current：是否为本次请求所用的 token
    // t.Impersonator：代为登录时签发 token 的操作者，见代为登录
}
```

`Current` 根据 `sub.Credential` 判断，所以在 API 中传入 `identity.FromContext` 读到的主体即可。`Impersonator` 不为零值的 token 是客服等人[代为登录](authentication-advanced.md#impersonation)时签发的，在设备列表中标出来，用户可以看到并撤销它。

<a id="revoking-tokens"></a>
## 撤销令牌

| 方法 | 撤销范围 | 常见用途 |
| --- | --- | --- |
| `Revoke(ctx, sub, id)` | `sub` 的一个 token；`id` 为 `sub.Credential` 时即登出 | 登出、在列表中移除某台设备或某个令牌 |
| `RevokeOthers(ctx, sub)` | `sub` 除本次请求所用 token 以外的全部**登录 token** | “退出其他设备” |
| `RevokeSubject(ctx, ref)` | 主体 `ref`（`identity.Ref`）的全部 token，包括个人访问令牌 | 封禁账号、修改密码 |

撤销立即生效，被撤销的 token 在下一次请求时返回 `401`；用它建立的 SSE 长连接最多 30 秒内结束，见[凭据失效](../http/sse.md#revoked-credentials)。撤销后调用 `RecheckCredential` 或 `RecheckSubject`，连接会立即结束，见[立即断开](../http/sse.md#immediate-revocation)。

<a id="loading-permissions"></a>
## 加载权限

token 只标识用户，权限在每个请求中重新加载，所以权限变化、账号停用都能立即生效。权限由 `rest.Options.Accounts` 中这套账号的 Resolver 在每个请求中按用户当前的状态加载，见[每次请求加载权限](authentication.md#resolving-subjects)：

```go
api := rest.New(router, rest.Options{
    Accounts: identity.Accounts{
        user.Type: func(ctx context.Context, sub identity.Subject) (identity.Subject, error) {
            u, err := user.Find(ctx, a.db, sub.ID)
            if err != nil {
                return identity.Subject{}, err
            }
            if u.Disabled {
                return identity.Subject{}, identity.ErrUnauthenticated.New()
            }
            sub.Permissions = u.Permissions
            return sub, nil
        },
    },
    // ...
})
```

个人访问令牌的 `Scopes` 由 `Validate` 填入，`sub.Can` 同时检查权限和 `Scopes`。

<a id="oauth-tokens"></a>
## OAuth 令牌

用户在 claude.ai 这类客户端中授权访问 [MCP 服务](../ai/mcp.md)后，[OAuth 授权服务器](oauth-server.md)用 `Authorize` 签发 `Kind` 为 `oauth` 的令牌。它和个人访问令牌一样带有 `Scopes`，另外记录 `ClientID`、`ClientName` 和 `Resource`，按使用滑动过期。

OAuth 令牌只能用于授权时的资源：`Validate` 不接受它，`ValidateFor(resource)` 只接受发给 `resource` 的 OAuth 令牌，同时照常接受登录 token 和个人访问令牌：

```go
mcpx.HandlerOptions{Validate: tokens.ValidateFor("https://example.com/mcp") /* … */}
```

它出现在 `List` 中，`ClientName` 用于“已连接的应用”页面，`Revoke` 断开连接；`RevokeOthers` 保留它，`RevokeSubject` 一并撤销。客户端交回令牌时，授权服务器调用 `RevokeToken`。

<a id="cleanup"></a>
## 过期清理

过期的 token 校验失败，但行仍然留在表中。`Store` 提供 `Cleanup` 来清理，每次最多删除 `limit` 个过期 token，可以注册为[定时任务](../background/scheduling.md)：

```go
n, err := tokens.Cleanup(ctx, 1000)
```

<a id="troubleshooting"></a>
## 常见问题

**用户一段时间不操作后被退出了**

登录 token 空闲超过 `idle_timeout` 后过期，每次使用会顺延。需要保持更久时调大它，见[配置](#configuration)。

**创建个人访问令牌后，忘了保存明文**

数据库只保存令牌的哈希，明文只在签发时出现一次。撤销它，再创建一个新的。

**个人访问令牌的权限比 `Scopes` 写的少**

令牌能做的是用户当前的权限与 `Scopes` 的交集，用户被收回权限后令牌随之收窄，见[个人访问令牌](#personal-access-tokens)。

**`api_tokens` 表越来越大**

过期的 token 仍然留在表中。用 `Cleanup` 注册定时任务清理，见[过期清理](#cleanup)。

<a id="api-reference"></a>
## API 参考

包 `github.com/go-tenon/tenon/apitoken`。

### Store

```go
func NewStore(db bun.IDB, cfg Config) *Store
func (s *Store) Login(ctx context.Context, sub identity.Subject, d Device) (string, error)
func (s *Store) Create(ctx context.Context, sub identity.Subject, p Personal) (string, error)
func (s *Store) Validate(ctx context.Context, token string) (identity.Subject, error)
func (s *Store) ValidateFor(resource string) func(ctx context.Context, token string) (identity.Subject, error)
func (s *Store) Confirm(ctx context.Context, sub identity.Subject, methods ...string) error
func (s *Store) List(ctx context.Context, sub identity.Subject) ([]Token, error)
func (s *Store) Revoke(ctx context.Context, sub identity.Subject, id string) error
func (s *Store) RevokeOthers(ctx context.Context, sub identity.Subject) (int, error)
func (s *Store) RevokeSubject(ctx context.Context, ref identity.Ref) (int, error)
func (s *Store) RevokeToken(ctx context.Context, token string) error
func (s *Store) Cleanup(ctx context.Context, limit int) (int, error)
func Erase(ctx context.Context, db bun.IDB, ref identity.Ref) error
func Export(ctx context.Context, db bun.IDB, ref identity.Ref) (any, error)
```

| 方法 | 说明 |
| --- | --- |
| `Login` | 登录令牌，空闲超时后失效；`Device` 记下“记住我”、IP 和 User-Agent |
| `Create` | 个人访问令牌，`Personal` 指定名称、`Scopes` 和可选的租户 |
| `Validate` | 校验令牌，交给 `rest.RequireToken` |
| `ValidateFor` | 只接受签发给 `resource` 的 OAuth 令牌，见 [OAuth 授权服务器](oauth-server.md) |
| `Confirm` | 记下主体刚刚确认过身份，供 `rest.Confirmed` 检查 |
| `List`、`Revoke`、`RevokeOthers` | 设备列表、退出某台设备、退出其他设备 |
| `RevokeSubject` | 停用账号时撤销全部令牌 |
| `Erase` | 包级函数，在注销的事务中删除全部令牌，登记到[注销账号](../features/privacy.md#framework-sources) |
| `Export` | 包级函数，列出全部令牌（不含令牌本身），登记到[导出个人数据](../features/data-export.md#sources) |
| `Cleanup` | 删除过期的令牌，交给定时任务 |
