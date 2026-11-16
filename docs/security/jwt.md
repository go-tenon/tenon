# JWT 与单页应用的登录

[API 令牌](api-tokens.md)一页介绍的是**不透明令牌**：一串随机字符，每次请求都要查数据库才知道它属于谁。这一页介绍另一种方式 **JWT**（JSON Web Token）：令牌中直接写着用户和过期时间，并带有服务端的签名，服务端验证签名即可，不用每次查数据库。

JWT 一旦签发就无法单独作废，所以 Tenon 让它的有效期很短（access token，几分钟），再配一个保存在数据库中、可以撤销的 refresh token 用来换新的 access token。浏览器中的单页应用通常这样登录：access token 放在页面内存中，refresh token 放在 HttpOnly cookie 中。[入门教程第 6 章](../tutorial/06-login.md#how-it-works)就是这样做的。

阅读前需要了解：[API 令牌](api-tokens.md)；[认证](authentication.md)。

- [JWT 模式](#jwt)
    - [轮换与被盗识别](#rotation)
    - [access token 的内容](#claims)
    - [密钥](#keys)
- [单页应用](#single-page-applications)
- [常见问题](#troubleshooting)

<a id="jwt"></a>
## JWT 模式

不透明的登录 token 每个请求都要查一次表。JWT 模式改为签发一对凭据：请求带短期的 access token（JWT），校验只验签名和有效期，在内存中完成；access token 过期后，用 refresh token 换一对新的。适合请求量大、或者有其他服务也要校验 token 的场景。权限仍由 Resolver 在每个请求中[加载](api-tokens.md#loading-permissions)，使用 `authz` 时可以[缓存权限](authorization.md#caching-permissions)，请求就完全在内存中完成。

每次登录仍然是 `api_tokens` 中的一行，[设备列表](api-tokens.md#listing-tokens)、[撤销](api-tokens.md#revoking-tokens)、[过期清理](api-tokens.md#cleanup)都照常使用。区别在于撤销的效果：refresh token 立即失效，已经签发的 access token 在有效期（默认 10 分钟）内仍然可以发起请求。撤销需要立即生效的账号体系继续用不透明 token。两种方式可以在同一个应用中并存，例如运营后台和商家后台用不透明 token，客户 App 用 JWT。

[SSE](../responses/sse.md#revoked-credentials) 和[实时连接](../libraries/realtime.md#revocation)这类长连接另有机制：复查凭据时 `JWT.Validate` 按登录 ID 查一次表，撤销后连接和不透明 token 一样，最多 30 秒内结束，调用 `Recheck*` 时立即结束。复查只看登录是否还在，所以已经建立的连接可以一直保持，跨过 access token 的有效期。断线后重连时 access token 可能已经过期，实时连接的客户端要设置 [`getToken`](../libraries/realtime.md#tokens) 换新的。

用 `Store` 创建 `apitoken.JWT`。`HMACKeys` 从[应用密钥](encryption.md#configuration)派生签名密钥：

```go
customerTokens := apitoken.NewStore(a.db, a.cfg.CustomerToken)
customerJWT := apitoken.NewJWT(customerTokens, apitoken.JWTConfig{Keys: apitoken.HMACKeys(a.crypt)})
requireCustomer := rest.RequireBearer(api, customerJWT.Validate)
```

`JWT.Validate` 也是 `identity.TokenValidator`。不是 JWT 的 token 交给 `Store.Validate`，所以同一个 API 仍然接受[个人访问令牌](api-tokens.md#personal-access-tokens)。

登录时用 `customerJWT.Login` 代替 `Store.Login`，返回 `Pair`；刷新接口是公开的，客户端提交 refresh token。浏览器中的单页应用把 refresh token 放在 cookie 中，见[单页应用](#single-page-applications)：

```go
pair, err := customerJWT.Login(ctx, sub, device)
// 返回 pair.Access、pair.Refresh 和 pair.ExpiresAt（access token 的过期时间）

pair, err := customerJWT.Refresh(ctx, in.Body.RefreshToken)
```

客户端的规则（完整的约定见[客户端协议](../http/protocol.md#sessions)）：

- 请求带 `Authorization: Bearer <access token>`；access token 过期或收到 `401` 时，用 refresh token 刷新，再重试请求。
- 刷新返回新的 refresh token 时替换保存的那一个；返回的 refresh token 为空时，继续使用已保存的。
- 刷新返回 `401` 时让用户重新登录。

登录的有效期按[配置](api-tokens.md#configuration)计算，只在刷新时延长，所以 `idle_timeout` 必须长于 access token 的有效期，否则 `NewJWT` 会 panic。

<a id="rotation"></a>
### 轮换与被盗识别

refresh token 每次刷新都会更换。上一个 token 在轮换后的 30 秒内（`JWTConfig.Grace`）仍然可以刷新，但只得到新的 access token，这样客户端同时发起的几次刷新都能成功。超过宽限期还有人用上一个 token 刷新，说明它被另一方持有：框架撤销这次登录，双方都需要重新登录，然后调用 `OnReuse`：

```go
customerJWT := apitoken.NewJWT(customerTokens, apitoken.JWTConfig{
    Keys: apitoken.HMACKeys(a.crypt),
    OnReuse: func(ctx context.Context, t apitoken.Token) {
        slog.WarnContext(ctx, "refresh token reused", "token", t.ID, "ip", t.IP)
    },
})
```

<a id="claims"></a>
### access token 的内容

access token 只包含签发时确定的身份信息，**权限另行加载**；权限仍由 `rest.Options.Accounts` 中的 Resolver 在每个请求中[加载](api-tokens.md#loading-permissions)。Resolver 查库时，请求仍会访问数据库；只认身份的入口可以把 Resolver 设为 `nil`。

| claim | 内容 | 校验后的主体 |
| --- | --- | --- |
| `sub` | 主体，如 `customer:42`（`identity.Ref` 的字符串形式） | `Type`、`ID` |
| `sid` | 登录的 ID，即 `api_tokens` 的行 ID | `Credential` |
| `tid` | 绑定的租户 | `Tenant` |
| `act` | [代为登录](authentication-advanced.md#impersonation)的实际操作者（RFC 8693） | `Impersonator` |
| `amr`、`auth_time` | 登录通过的验证方式和最近一次验证身份的时间（RFC 8176、OIDC） | `Auth` |
| `iat`、`exp` | 签发与过期时间，过期时间不晚于登录本身 | — |
| `iss`、`aud` | `JWTConfig.Issuer`、`Audience`，设置时才写入并校验 | — |

`Credential` 与不透明 token 的含义相同，所以登出仍然是 `Store.Revoke(ctx, sub, sub.Credential)`。

<a id="keys"></a>
### 密钥

`HMACKeys` 签名和校验用同一把密钥，只有持有应用密钥的服务能校验。[轮换应用密钥](encryption.md#rotating-keys)后，旧密钥签发的 access token 在过期前仍然有效。

其他服务（网关、另一个语言写的服务）也要校验 access token 时，改用 Ed25519：私钥签名，公钥通过 JWKS 公开。轮换时把旧公钥放在后面，保留到旧 access token 全部过期：

```go
keys := apitoken.Ed25519Keys(a.cfg.JWTKey, a.cfg.PreviousJWTKey)
customerJWT := apitoken.NewJWT(customerTokens, apitoken.JWTConfig{Keys: keys, Issuer: "https://shop.example.com"})
router.Method(http.MethodGet, "/.well-known/jwks.json", customerJWT.JWKS())
```

`a.cfg.JWTKey` 是应用配置中的 `ed25519.PrivateKey`，用 `ed25519.GenerateKey` 生成，按应用保管密钥的方式存放。

<a id="single-page-applications"></a>
## 单页应用

浏览器中的单页应用（例如 React）使用 [JWT 模式](#jwt)，并把 refresh token 放进 cookie：

- access token 只保存在内存中，请求照常带 `Authorization: Bearer`；
- refresh token 保存在 `apitoken.RefreshCookie` 描述的 cookie 中：`HttpOnly`，页面上的脚本读不到；`SameSite=Strict`，只随本站发起的请求发送；`Path` 只包含刷新和登出接口，只随这两个请求发送；
- 打开页面（包括刷新页面、新标签页）时先调用一次刷新接口拿到 access token，失败就显示登录页。

这样 XSS 最多在页面打开期间借用用户的权限，长期有效的凭据始终在 cookie 中。API 本身仍然只认 Bearer，cookie 只用于刷新，所以 API 天然免疫跨站请求伪造。

不透明的登录 token 没有 refresh token，放进 cookie 就成了 API 的凭据，所以浏览器中的单页应用不使用它。App、桌面端、小程序和脚本照常在请求体中传递 refresh token，或者使用不透明 token。同一组接口可以同时服务两种客户端：登录时请求体带 `cookie: true` 的写 cookie，否则把 refresh token 放在响应体中；刷新时请求体中有 refresh token 就用它，没有就读 cookie：

```go
var refreshCookie = apitoken.RefreshCookie{Name: "blog_refresh", Path: "/api/auth"}

type LoginInput struct {
    UserAgent string `header:"User-Agent"`
    Body      struct {
        Email    string `json:"email" format:"email"`
        Password string `json:"password" minLength:"1"`
        Cookie   bool   `json:"cookie,omitempty" doc:"浏览器中的单页应用传 true：refresh token 写入 HttpOnly cookie"`
    }
}

type RefreshInput struct {
    Cookie string `cookie:"blog_refresh"`
    Body   apitoken.RefreshRequest
}

type SessionOutput struct {
    SetCookie *http.Cookie `header:"Set-Cookie"`
    Body      apitoken.Session
}

// session 把一对凭据交给客户端：浏览器的 refresh token 写入 cookie，其他客户端的放在响应体中。
func session(pair apitoken.Pair, cookie bool) *SessionOutput {
    out := &SessionOutput{}
    if cookie {
        out.Body, out.SetCookie = refreshCookie.Session(pair)
    } else {
        out.Body = apitoken.NewSession(pair)
    }
    return out
}

func (h *handlers) refresh(ctx context.Context, in *RefreshInput) (*SessionOutput, error) {
    token, cookie := in.Body.RefreshToken, false
    if token == "" {
        token, cookie = in.Cookie, true
    }
    pair, err := h.jwt.Refresh(ctx, token)
    if err != nil {
        return nil, err // 401：重新登录
    }
    return session(pair, cookie), nil
}

// logout 撤销这次登录，并清除浏览器中的 cookie。
func (h *handlers) logout(ctx context.Context, _ *struct{}) (*LogoutOutput, error) {
    sub, _ := identity.FromContext(ctx)
    if err := h.tokens.Revoke(ctx, sub, sub.Credential); err != nil {
        return nil, err
    }
    return &LogoutOutput{SetCookie: refreshCookie.Clear()}, nil
}
```

`LogoutOutput` 只有 `SetCookie` 一个字段。登录处理器在 `Password` 成功后调用 `h.jwt.Login`，再 `return session(pair, in.Body.Cookie), nil`。`apitoken.Session` 和 `apitoken.RefreshRequest` 是框架固定的请求体和响应体格式，客户端都按它们读写，见[客户端协议](../http/protocol.md#sessions)。`RefreshCookie.Session` 在宽限期内的重复刷新（`pair.Refresh` 为空）时不返回 cookie，浏览器中保留的就是最新的那个。前端的写法见 [API 客户端](../responses/clients.md#creating-the-client)，完整的例子见[单账号后台](admin-example.md#auth-api)。

部署方式决定还要做什么：

| 单页应用与 API | 需要的设置 |
| --- | --- |
| 同源，例如由 [`httpx.SPA`](../responses/spa.md) 提供 | 保持默认 |
| 同一站点的不同子域名，例如 `app.example.com` 与 `api.example.com` | API 的 [CORS](../http/optional-middleware.md#cors) 设置 `credentials: true`，逐个列出来源；`RefreshCookie.Domain` 设为 `example.com`；前端刷新时 `credentials: "include"` |
| 不同站点，例如 `example.com` 与 `example-api.io` | 浏览器拦截第三方 cookie，用不了 cookie。退回请求体方式，refresh token 只能放在 `localStorage` 中，页面上的脚本都能读到，要格外防范 XSS |

<a id="troubleshooting"></a>
## 常见问题

**撤销了登录，用户仍然能调用接口几分钟**

已经签发的 access token 在有效期（默认 10 分钟）内仍然可以使用，撤销只让 refresh token 立即失效。需要立即生效时，缩短 access token 的有效期，或者使用不透明令牌。

**启动时 panic，提示 `idle_timeout` 太短**

登录的有效期只在刷新时延长，`idle_timeout` 必须长于 access token 的有效期。

**同时打开几个标签页后，用户被退出了**

几个标签页同时用同一个 refresh token 刷新时，30 秒的宽限期内都能成功；超过宽限期再用旧的 refresh token 刷新，会被当作被盗，整个登录被撤销。客户端要在刷新后保存新的 refresh token，见[轮换与被盗识别](#rotation)。

**刷新页面后变成未登录**

refresh token 的 cookie 没有带上。cookie 的 `Path` 只包含刷新和登出接口，`SameSite=Strict` 只随本站发起的请求发送；检查前端是否通过同一个站点访问接口，见[单页应用](#single-page-applications)。

**其他服务校验不了 access token**

`HMACKeys` 签名和校验用同一把密钥，只有持有应用密钥的服务能校验。需要其他服务校验时改用 Ed25519，通过 JWKS 公开公钥，见[密钥](#keys)。
