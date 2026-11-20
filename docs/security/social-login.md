# 第三方登录

用 Google、GitHub、微信、Sign in with Apple 等第三方账号登录和注册：`login/oauth` 与第三方完成授权，得到第三方确认过的账号 `login.External`；[`login.Realm`](login.md) 的 `External` 把它当作一种[登录标识](login.md#identifiers)，已经绑定就登录，还没有就返回注册凭证。

包：`login/oauth`、`login`。

| 客户端 | 与第三方交互的一方 | 方法 |
| --- | --- | --- |
| [单页应用](#spa-login) | API 生成跳转地址，单页应用跳转并把回调参数交回 API | `Start`、`Finish` |
| [App](#api-login) | App 自己跳转，把授权码交给 API | `Exchange` |
| [App 原生登录](#native-sign-in) | 系统或第三方 SDK 直接返回 ID token | `IDToken` |

client secret 只放在服务端。

<a id="example-usage"></a>
## 示例

Google 登录。`cfg` 是应用的配置，`users` 是账号体系的 `*login.Realm`，`callbackParams` 是回调地址的查询参数（`url.Values`）。

```go
google, err := oauth.Discover(ctx, oauth.Config{
    Provider:     "google",
    Issuer:       "https://accounts.google.com",
    ClientID:     cfg.Google.ClientID,
    ClientSecret: cfg.Google.ClientSecret,
    RedirectURL:  "https://example.com/auth/google/callback",
})

authURL, flow := google.Start() // 跳到 authURL，flow 保存在客户端

ext, err := google.Finish(ctx, flow, callbackParams) // 回调：比较 state，换取 token，读出账号
out, err := users.External(ctx, ext)                 // 已绑定：out.Subject；未绑定：out.Proof
```

<a id="providers"></a>
## 创建提供方：`oauth.Discover`、`oauth.New`

支持 OpenID Connect 的提供方（Google、Apple、Microsoft、企业 IdP）用 `oauth.Discover` 创建，账号来自 ID token；只支持 OAuth 2.0 的（GitHub、微信）用 `oauth.New`，账号由应用的函数读取，见[接入 GitHub](#oauth2-providers)。`cfg` 是[配置](#configuration)中的 `Config`：

```go
// Providers 在入口中调用一次，按名称返回全部提供方。
func Providers(ctx context.Context, cfg Config) (map[string]*oauth.Provider, error) {
    redirect := func(name string) string { return strings.ReplaceAll(cfg.RedirectURL, "{provider}", name) }
    google, err := oauth.Discover(ctx, oauth.Config{
        Provider: "google", Issuer: "https://accounts.google.com",
        ClientID: cfg.Google.ClientID, ClientSecret: cfg.Google.ClientSecret,
        RedirectURL: redirect("google"), ClientRedirectURLs: cfg.ClientRedirectURLs,
        Audiences: append([]string{cfg.Google.ClientID}, cfg.GoogleAppClientIDs...),
    })
    if err != nil {
        return nil, err
    }
    gh, err := oauth.New(oauth.Config{
        Provider: "github", ClientID: cfg.GitHub.ClientID, ClientSecret: cfg.GitHub.ClientSecret,
        RedirectURL: redirect("github"), ClientRedirectURLs: cfg.ClientRedirectURLs,
        Scopes: []string{"read:user", "user:email"},
    }, github.Endpoint, githubAccount)
    if err != nil {
        return nil, err
    }
    return map[string]*oauth.Provider{"google": google, "github": gh}, nil
}
```

- `Discover` 请求发现文档，所以提供方在入口中创建一次，之后一直复用。
- `Discover` 校验 ID token 的签名、签发者、受众（`Audiences`，默认只有 `ClientID`）、有效期和 nonce，读取 `sub`、`email`、`email_verified`、`name`，全部声明放在 `ext.Claims` 中。接入 Microsoft 或企业 IdP 时换掉 `Issuer`。
- `Provider` 是登录标识的 `Kind`：小写字母开头，只含小写字母、数字和下划线，不能是 `email`、`phone`、`username`。
- 全部出站请求使用 `oauth.Config.Client`，默认 `http.DefaultClient`。`Issuer` 由租户填写时（[企业单点登录](enterprise-sso.md)）必须传入 [`outbound`](../libraries/outbound.md) 的客户端，拒绝访问内网地址。

<a id="oauth2-providers"></a>
## 接入 GitHub、微信：`oauth.Account`

`oauth.New` 的最后一个参数是 `oauth.Account`：用换到的 token 读取账号，`client` 在请求中带上 token。`Subject` 只用第三方的用户 ID（GitHub 的数字 `id`、微信的 `unionid`）：用户名和邮箱都会变。微信不支持 PKCE，设置 `NoPKCE`。

```go
// githubAccount 读取 GitHub 账号：ID 来自 /user，主邮箱和它是否已验证来自 /user/emails。
func githubAccount(ctx context.Context, client *http.Client, _ *oauth2.Token) (login.External, error) {
    var u struct {
        ID          int64
        Login, Name string
    }
    var emails []struct {
        Email             string
        Primary, Verified bool
    }
    if err := getJSON(ctx, client, "https://api.github.com/user", &u); err != nil {
        return login.External{}, err
    }
    if err := getJSON(ctx, client, "https://api.github.com/user/emails", &emails); err != nil {
        return login.External{}, err
    }
    ext := login.External{Subject: strconv.FormatInt(u.ID, 10), Name: cmp.Or(u.Name, u.Login)}
    for _, e := range emails {
        if e.Primary {
            ext.Email, ext.EmailVerified = e.Email, e.Verified
        }
    }
    return ext, nil
}
```

`getJSON` 是应用的小函数：用 `http.NewRequestWithContext` 发出 GET，状态码不是 200 时返回错误，否则把响应解码到 `v`。

<a id="configuration"></a>
## 配置

`Providers` 读取的 `Config` 加入应用的根配置结构体，配置段为 `social`：

```go
type Client struct {
    ClientID     string `yaml:"client_id"`
    ClientSecret string `yaml:"client_secret"`
}

type Config struct {
    Google             Client   `yaml:"google"`
    GitHub             Client   `yaml:"github"`
    RedirectURL        string   `yaml:"redirect_url"`
    ClientRedirectURLs []string `yaml:"client_redirect_urls"`
    GoogleAppClientIDs []string `yaml:"google_app_client_ids"`
}
```

```yaml
social:
  google:
    client_id: ${GOOGLE_CLIENT_ID}
    client_secret: ${GOOGLE_CLIENT_SECRET}
  github:
    client_id: ${GITHUB_CLIENT_ID}
    client_secret: ${GITHUB_CLIENT_SECRET}
  redirect_url: https://example.com/admin/auth/{provider}/callback # 单页应用的回调页
  client_redirect_urls:  # App 登记的回调地址，Exchange 只接受这些地址
    - com.example.app:/oauth/callback
  google_app_client_ids: # App 原生登录的 Google client ID，例如 iOS、Android 各一个
    - 1234-ios.apps.googleusercontent.com
```

给 `Config` 加上 `Validate() error`，空的 `client_id` 在启动时就报错，见[默认值与校验](../general/configuration.md#defaults-and-validation)。

<a id="signing-in"></a>
## 登录与注册：`Realm.External`

`ext` 是 `Finish`、`Exchange` 或 `IDToken` 返回的 `login.External`，`users` 是账号体系的 `*login.Realm`：

```go
out, err := users.External(ctx, ext)
```

| 结果 | 含义 | 应用要做的 |
| --- | --- | --- |
| `out.Subject` | 第三方账号已经绑定 | 签发 token |
| `out.Proof` | 还没有绑定 | 注册新账号，或者让用户登录已有账号后[绑定](#linking-accounts) |
| `out.Pending`、`out.Enroll` | 账号体系[要求二次验证](login.md#requiring-two-factor) | 与密码登录相同，先完成第二步或先启用 |

- **注册**与[验证码注册](login.md#codes)相同：注册业务动作在事务中创建账号行，把 `out.Proof` 交给 `Register`，提交之后调用 `CompleteRegister`。第三方确认过的邮箱（`EmailVerified`）同时写入为登录标识。
- **二次验证**：第三方账号登录跳过二次验证，`RequireTwoFactor` 时除外。应用按 `ext.Claims`（例如 `amr`）确认提供方做过多因素认证后设置 `ext.TwoFactor = true`，这次登录记为 `[提供方, idp_mfa]`，计入二次验证。只对信任其 MFA 的提供方设置，例如租户的企业 IdP。
- `ext.Claims` 是 ID token 的全部声明，例如企业 IdP 的 `groups`，可以在每次登录时同步为角色。

> [!WARNING]
> 框架不按邮箱把第三方账号关联到已有账号：任何人都能在不验证邮箱的平台上填别人的邮箱，借此登录别人的账号。注册时邮箱已属于其他账号，`Register` 返回 `login.ErrTaken`，提示用户先用原来的方式登录，再绑定。设置 `ext.TrustEmail = true` 时，`External` 把第三方账号直接绑定给这个邮箱已有的账号并登录：只对租户验证过邮箱域名的企业 IdP 设置，见[企业单点登录](enterprise-sso.md#signing-in)。

<a id="spa-login"></a>
## 在单页应用中登录：`Start`、`Finish`

1. 登录页调用 `POST /auth/{provider}/start`，`Start` 返回跳转地址和 `oauth.Flow`（state、nonce、PKCE verifier）。单页应用把 `flow` 存进 `sessionStorage`，整页跳转到第三方；
2. 第三方带着授权码和 state 跳回单页应用的回调页（`RedirectURL`）；
3. 回调页把查询参数和 `flow` 交给 `POST /auth/{provider}/finish`，然后删除 `flow`；
4. API 签发登录 token，写入[会话 cookie](api-tokens.md#browser-sessions)，或者返回 `pending`、`enroll`，单页应用接着走与密码登录相同的第二步。

两个接口都用 `rest.Public()`，`finish` 写入会话 cookie，另加 `rest.SameOrigin(api)`。处理器中 `p` 是 `providers[in.Provider]`（不存在时返回 404），`ErrNotLinked` 是应用用 `errorx.Define` 定义的错误，`editors` 是编辑的 `*login.Realm`，`tokens` 是 `*apitoken.Store`，`lifetime` 是会话的有效期：

```go
// start
authURL, flow := p.Start()
out.Body.URL, out.Body.Flow = authURL, flow.String()

// finish：in.Body.Flow 是保存的 flow，in.Body.Callback 是回调页地址中的查询参数
flow, err := oauth.ParseFlow(in.Body.Flow)
if err != nil {
    return nil, err
}
params, err := url.ParseQuery(in.Body.Callback)
if err != nil {
    return nil, oauth.ErrInvalid.Wrap(err)
}
ext, err := p.Finish(ctx, flow, params)
if err != nil {
    return nil, err
}
res, err := editors.External(ctx, ext)
if err != nil {
    return nil, err
}
switch {
case res.Proof != "":
    return nil, ErrNotLinked.New() // 编辑后台不开放注册：先用密码登录，再绑定
case res.Pending != "" || res.Enroll != "":
    out.Body.Pending, out.Body.Enroll = res.Pending, res.Enroll
    return out, nil
}
token, err := tokens.Login(ctx, res.Subject, apitoken.Device{UserAgent: in.UserAgent})
if err != nil {
    return nil, err
}
out.SetCookie = httpx.SessionCookie{}.Set(token, lifetime)
return out, nil
```

`Finish` 的错误：state 与 `flow` 不一致（例如攻击者诱导用户打开带着自己授权码的回调地址）、授权码已用过、ID token 无效时返回 `oauth.ErrInvalid`（401）；用户取消授权时返回 `oauth.ErrDenied`（422）。

<a id="linking-accounts"></a>
## 绑定第三方账号：`Link`

账号设置中的“绑定 GitHub”与[单页应用中登录](#spa-login)的跳转相同，回调交给一个要求登录的接口，用 `Link` 绑定到当前账号。`p`、`flow`、`params` 与登录时相同，`users` 是 `*login.Realm`，`db` 是 `*bun.DB`：

```go
ext, err := p.Finish(ctx, flow, params)
if err != nil {
    return err
}
out, err := users.External(ctx, ext)
if err != nil {
    return err
}
if out.Proof == "" {
    return ErrAlreadyLinked.New() // 应用定义的错误：这个第三方账号已经绑定了某个账号
}
sub, _ := identity.FromContext(ctx)
return users.Link(ctx, db, sub.Ref(), out.Proof)
```

- 一个账号可以绑定多个第三方账号。`Identifiers` 列出全部登录标识，第三方账号的 `Kind` 是提供方的名称；`Detach` 解除绑定，账号始终保留至少一种登录方式，见[管理登录方式](login.md#managing)。
- 设置了 [`PerTenant`](login.md#identifiers) 的账号体系，第三方账号按租户隔离：同一个 Google 账号可以在两个租户中各绑定一个账号。

<a id="api-login"></a>
## 在 App 中登录：`Exchange`

移动 App 和桌面 App 自己生成 state、nonce 和 PKCE verifier，打开第三方的授权地址，`redirect_uri` 是它登记的回调地址；授权码跳回 App 后，App 比较 state，把授权码、verifier、nonce 和回调地址交给 `POST /auth/{provider}/tokens`。原生登录的 ID token 也交给这个接口。`p`、`users`、`tokens` 的来源同[单页应用中登录](#spa-login)：

```go
var ext login.External
var err error
if in.Body.IDToken != "" {
    ext, err = p.IDToken(ctx, in.Body.IDToken, in.Body.Nonce)
} else {
    ext, err = p.Exchange(ctx, oauth.Grant{
        Code: in.Body.Code, Verifier: in.Body.CodeVerifier, RedirectURI: in.Body.RedirectURI, Nonce: in.Body.Nonce,
    })
}
if err != nil {
    return nil, err
}
res, err := users.External(ctx, ext)
if err != nil {
    return nil, err
}
if res.Proof != "" { // 还没有账号：App 进入注册页，用 name 预填，带着 proof 调用注册接口
    out.Body.Proof, out.Body.Name = res.Proof, ext.Name
    return out, nil
}
out.Body.Token, err = tokens.Login(ctx, res.Subject, apitoken.Device{UserAgent: in.UserAgent})
return out, err
```

- 注册接口与[验证码注册](login.md#codes)是同一个。
- `Exchange` 在这些情况返回 `oauth.ErrInvalid`：`redirect_uri` 不在 `client_redirect_urls` 中，否则攻击者能用发往其他地址的授权码换取 token；缺少 `code_verifier`，App 没有 client secret，PKCE 是它唯一的保护（`NoPKCE` 时除外）；OpenID Connect 提供方缺少 `nonce`。

<a id="native-sign-in"></a>
### App 原生登录：`IDToken`

Google 登录 SDK、Sign in with Apple 等原生登录直接返回 ID token，App 把它放在 `id_token` 中交给[同一个接口](#api-login)，由 `IDToken` 校验。`IDToken` 只用于 `Discover` 创建的提供方。

- SDK 签发的 ID token 的受众是 App 自己的 client ID，要列在 `oauth.Config.Audiences` 中，否则返回 `oauth.ErrInvalid`，见[配置](#configuration)中的 `google_app_client_ids`。
- nonce 由服务器下发、App 放进登录请求，服务器把保存的值传给 `IDToken` 比较，防止截获的 ID token 被重放。有的 SDK 在请求中放 nonce 的 SHA-256，这时传入哈希值。`nonce` 为空时不比较。

<a id="sign-in-with-apple"></a>
## Sign in with Apple

上架 App Store 的 App 提供第三方登录时，通常必须同时提供 Sign in with Apple。Apple 用 `Discover` 创建，`cfg.Apple` 是应用配置中的一段：

```go
apple, err := oauth.Discover(ctx, oauth.Config{
    Provider:     "apple",
    Issuer:       "https://appleid.apple.com",
    ClientID:     cfg.Apple.ServiceID,
    ClientSecret: cfg.Apple.ClientSecret, // 自己签名的 JWT
    RedirectURL:  "https://example.com/auth/apple/callback", // 服务端的转发地址
    Scopes:       []string{"openid", "email", "name"},
    NoPKCE:       true,
    FormPost:     true,
})
```

- **client secret** 是用 Apple 下发的私钥（`.p8`）按 ES256 签名的 JWT，有效期最长 6 个月，到期前要重新生成，否则全部登录失败，见 [Apple 的文档](https://developer.apple.com/documentation/accountorganizationaldatasharing/creating-a-client-secret)。
- **`NoPKCE`**：Apple 不支持 PKCE。
- **姓名**只在第一次授权时出现在回调表单的 `user` 字段中，第一次登录时就要保存。`email_verified` 是字符串，`oauth` 已经处理。
- **`FormPost`**：申请了姓名或邮箱时，Apple 要求 `response_mode=form_post`，回调是从 `appleid.apple.com` 发来的跨站 POST。

单页应用接收不了 POST 回调。在服务端注册 `RedirectURL`，把表单转成查询参数跳到单页应用的回调页，之后与[单页应用中登录](#spa-login)相同；`user` 字段随之带到回调页，交给注册接口。`api` 是 `*rest.API`：

```go
api.Router().Post("/auth/apple/callback", func(w http.ResponseWriter, r *http.Request) {
    if err := r.ParseForm(); err != nil {
        http.Error(w, "bad request", http.StatusBadRequest)
        return
    }
    http.Redirect(w, r, "/admin/auth/apple/callback?"+r.PostForm.Encode(), http.StatusSeeOther)
})
```
