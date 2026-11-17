# 第三方登录

用户可以用 Google、GitHub 等账号登录，分两步：

1. `login/oauth` 与第三方交互，得到第三方确认过的账号 `login.External`：OpenID Connect 提供方（Google、Apple、Microsoft、企业的身份系统）用 `oauth.Discover`，只支持 OAuth 2.0 的提供方（GitHub、微信）用 `oauth.New`；
2. [`login.Realm`](login.md) 的 `External` 把第三方账号当作一种[登录标识](login.md#identifiers)：已经绑定就登录，还没有就返回注册凭证。

三种入口共用同一套提供方，只有与第三方交互的方式不同：

| 入口 | 谁与第三方交互 | 方法 |
| --- | --- | --- |
| [单页应用](#spa-login) | API 生成跳转地址和 Flow，单页应用保存 Flow，回调后把它和回调参数交给 API | `Start`、`Finish` |
| [App](social-login-apps.md#api-login) | App 自己跳转到第三方，把授权码交给 API | `Exchange` |
| [App 原生登录](social-login-apps.md#native-sign-in) | SDK 直接返回 ID token | `IDToken` |

几个术语：**OAuth 2.0** 是让用户把第三方账号的访问权授予你的应用的协议，**OpenID Connect** 在它之上增加了“这个人是谁”的 ID token；**state** 和 **PKCE** 是登录过程中的两个随机值，防止攻击者把自己的授权码塞给用户。

阅读前需要了解：[登录与注册](login.md)；[API 令牌](api-tokens.md)。App 中的写法和 Sign in with Apple 见[在 App 中使用第三方登录](social-login-apps.md)。

- [示例用法](#example-usage)
- [提供方](#providers)
- [登录与注册](#signing-in)
- [在单页应用中登录](#spa-login)
- [账号关联](#linking-accounts)
- [常见问题](#troubleshooting)
- [API 参考](#api-reference)

<a id="example-usage"></a>
## 示例用法

下面创建 Google 登录的提供方，开始登录，在回调中取得 Google 确认过的账号：

```go
google, err := oauth.Discover(ctx, oauth.Config{
    Provider:     "google",
    Issuer:       "https://accounts.google.com",
    ClientID:     cfg.Google.ClientID,
    ClientSecret: cfg.Google.ClientSecret,
    RedirectURL:  "https://example.com/auth/google/callback",
})

// 开始：跳到 Google，flow 保存在客户端（例如 cookie）
authURL, flow := google.Start()

// 回调：换取第三方账号，交给 Realm 登录或注册
ext, err := google.Finish(ctx, flow, r.URL.Query())
out, err := users.External(ctx, ext)
```

`Start` 返回跳转到 Google 的地址；用户在 Google 登录并同意之后，浏览器回到你的回调地址，`Finish` 校验回调并取得账号。`users.External` 把这个账号当作一种登录标识：已经绑定过就直接登录，还没有就返回注册凭证。

<a id="providers"></a>
## 提供方

先在第三方开放平台登记应用，得到 client ID 和 client secret，并登记回调地址。例如 `social.go`：

```go
package social

import (
    "cmp"
    "context"
    "encoding/json"
    "fmt"
    "net/http"
    "strconv"
    "strings"

    "golang.org/x/oauth2"
    "golang.org/x/oauth2/github"

    "github.com/go-tenon/tenon/errorx"
    "github.com/go-tenon/tenon/login"
    "github.com/go-tenon/tenon/login/oauth"
)

var (
    ErrUnknownProvider = errorx.Define(errorx.NotFound, "social.unknown_provider")
    ErrNotLinked       = errorx.Define(errorx.PermissionDenied, "social.not_linked")
    ErrAlreadyLinked   = errorx.Define(errorx.Conflict, "social.already_linked")
)

// Client 是在第三方开放平台登记的应用。
type Client struct {
    ClientID     string `yaml:"client_id"`
    ClientSecret string `yaml:"client_secret"`
}

type Config struct {
    Google Client `yaml:"google"`
    GitHub Client `yaml:"github"`
    // RedirectURL 是单页应用中回调页的地址，{provider} 换成提供方的名称。
    RedirectURL string `yaml:"redirect_url"`
    // ClientRedirectURLs 是 App 登记的回调地址，Exchange 只接受这些地址。
    ClientRedirectURLs []string `yaml:"client_redirect_urls"`
    // GoogleAppClientIDs 是 App 原生登录使用的 Google client ID，例如 iOS 和 Android 各一个。
    GoogleAppClientIDs []string `yaml:"google_app_client_ids"`
}

// Providers 按名称创建全部提供方。Google 在这里读取发现文档，所以应当在启动时调用一次。
func Providers(ctx context.Context, cfg Config) (map[string]*oauth.Provider, error) {
    google, err := oauth.Discover(ctx, oauth.Config{
        Provider:           "google",
        Issuer:             "https://accounts.google.com",
        ClientID:           cfg.Google.ClientID,
        ClientSecret:       cfg.Google.ClientSecret,
        RedirectURL:        strings.ReplaceAll(cfg.RedirectURL, "{provider}", "google"),
        ClientRedirectURLs: cfg.ClientRedirectURLs,
        Audiences:          append([]string{cfg.Google.ClientID}, cfg.GoogleAppClientIDs...),
    })
    if err != nil {
        return nil, err
    }
    gh, err := oauth.New(oauth.Config{
        Provider:           "github",
        ClientID:           cfg.GitHub.ClientID,
        ClientSecret:       cfg.GitHub.ClientSecret,
        RedirectURL:        strings.ReplaceAll(cfg.RedirectURL, "{provider}", "github"),
        ClientRedirectURLs: cfg.ClientRedirectURLs,
        Scopes:             []string{"read:user", "user:email"},
    }, github.Endpoint, githubAccount)
    if err != nil {
        return nil, err
    }
    return map[string]*oauth.Provider{"google": google, "github": gh}, nil
}

// githubAccount 用 GitHub API 读取账号：用户 ID 来自 /user，邮箱和它是否已验证来自 /user/emails。
func githubAccount(ctx context.Context, client *http.Client, _ *oauth2.Token) (login.External, error) {
    var u struct {
        ID    int64  `json:"id"`
        Login string `json:"login"`
        Name  string `json:"name"`
    }
    if err := getJSON(ctx, client, "https://api.github.com/user", &u); err != nil {
        return login.External{}, err
    }
    var emails []struct {
        Email    string `json:"email"`
        Primary  bool   `json:"primary"`
        Verified bool   `json:"verified"`
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

func getJSON(ctx context.Context, client *http.Client, url string, v any) error {
    req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
    if err != nil {
        return err
    }
    resp, err := client.Do(req)
    if err != nil {
        return err
    }
    defer resp.Body.Close()
    if resp.StatusCode != http.StatusOK {
        return fmt.Errorf("GET %s: %s", url, resp.Status)
    }
    return json.NewDecoder(resp.Body).Decode(v)
}
```

把上面的 `Config`（或等价字段）加入应用的根配置结构体，配置段为 `social`：

```yaml
social:
  google:
    client_id: ${GOOGLE_CLIENT_ID}
    client_secret: ${GOOGLE_CLIENT_SECRET}
  github:
    client_id: ${GITHUB_CLIENT_ID}
    client_secret: ${GITHUB_CLIENT_SECRET}
  redirect_url: https://example.com/admin/auth/{provider}/callback
  client_redirect_urls:
    - https://app.example.com/auth/callback
```

生命周期是：

1. 启动时 [`config.Load`](../general/configuration.md#loading-configuration) 读出 `social` 段
2. 在入口调用一次 `Providers(ctx, cfg.Social)`，得到 `map[string]*oauth.Provider`（Google 的发现文档也在这里拉）
3. 登录 / 回调接口按名称取出提供方使用——不要每个请求都 `Discover` 一遍

需要在启动时就拦住空 `client_id` 之类的配置错误时，给 `Config` 加上 `Validate() error`，`Load` 会自动调用，见[配置校验](../general/configuration.md#loading-configuration)。

几点说明：

- OpenID Connect 提供方的账号来自 ID token：`oauth.Discover` 校验它的签名、签发者、受众、有效期和 nonce，读取 `sub`、`email`、`email_verified` 和 `name`。接入 Microsoft 或企业的身份系统时只需要换掉 `Issuer`。
- 只支持 OAuth 2.0 的提供方，由应用写一个函数用换到的 token 读取账号，例如上面的 `githubAccount`；`client` 会在请求中带上 token。
- 第三方返回的用户 ID 才是账号的唯一标识：GitHub 是数字 `id`，OpenID Connect 是 `sub`，微信是 `unionid`。用户名和邮箱都可能变化，所以只用 ID 作为 `Subject`。
- `oauth.Discover` 会请求发现文档，校验 ID token 的公钥按需下载并缓存，所以提供方在启动时创建一次，之后一直复用。
- 全部出站请求使用 `Config.Client`，默认是 `http.DefaultClient`。Issuer 来自租户后台填写的企业 SSO 时，要传入 [`outbound`](../libraries/outbound.md) 的客户端，拒绝访问内网地址。

<a id="signing-in"></a>
## 登录与注册

得到 `login.External` 之后交给 `Realm.External`：

```go
out, err := users.External(ctx, ext)
```

| 结果 | 含义 | 应用要做的 |
| --- | --- | --- |
| `out.Subject` | 这个第三方账号已经绑定 | 签发 token |
| `out.Proof` | 还没有绑定 | 注册新账号，或者让用户登录已有账号后[绑定](#linking-accounts) |
| `out.Pending`、`out.Enroll` | 账号体系要求[二次验证](login-security.md#requiring-two-factor) | 与密码登录相同，先完成第二步或先启用 |

注册与[验证码注册](login.md#codes)相同：应用的注册业务动作在事务中创建账号行，再把 `out.Proof` 交给 `Register`，提交之后调用 `CompleteRegister`。第三方账号作为登录标识写入，第三方确认过（`EmailVerified`）的邮箱也一并写入，之后用户也可以用这个邮箱收验证码登录。第三方账号登录时跳过[二次验证](login-security.md#two-factor)这一步，由提供方负责验证身份；账号体系[强制二次验证](login-security.md#requiring-two-factor)时除外。

`ext.Claims` 是 ID token 的全部声明（`Account` 读取的提供方由应用自己填写），例如企业 IdP 的 `groups`，应用可以在每次登录时据此同步角色。提供方在这次登录中做过多因素认证时（各家写在 `amr` 等声明中，写法不同），应用确认后设置 `ext.TwoFactor = true`，这次登录记为 `[提供方, idp_mfa]`，计入二次验证。只对信任其 MFA 的提供方这样做，例如租户自己的企业 IdP。

**框架不按邮箱把第三方账号自动关联到已有账号。** 否则任何人都可以在一个不验证邮箱的第三方平台上填上别人的邮箱，借此登录别人的账号。注册时邮箱已属于其他账号，`Register` 返回 `login.ErrTaken`：提示用户先用原来的方式登录，再绑定这个第三方账号。

例外是租户验证过邮箱域名的企业 IdP：应用确认这个连接属于当前租户、租户验证过 `ext.Email` 的域名之后，设置 `ext.TrustEmail = true`，`External` 就把第三方账号直接绑定给这个邮箱已有的账号并登录。公司改用 SSO 后，员工原来的账号因此自动关联。只对验证过域名的企业 IdP 设置它，Google、GitHub 等公共提供方保持默认，见[企业单点登录](enterprise-sso.md)。

<a id="spa-login"></a>
## 在单页应用中登录

client secret 和各家的授权地址都留在服务端，由 API 为单页应用生成跳转地址：

1. 登录页上的“使用 GitHub 登录”调用 `POST /auth/github/start`，`Start` 生成跳转地址和 `oauth.Flow`（state、nonce 和 PKCE verifier）。单页应用把 `flow` 存进 `sessionStorage`，然后整页跳转到第三方；
2. 用户同意授权后，第三方带着授权码和 state 跳回单页应用的回调页 `/admin/auth/github/callback`，也就是 `RedirectURL`；
3. 回调页把地址中的查询参数和保存的 `flow` 交给 `POST /auth/github/finish`，`Finish` 比较 state，用授权码和 verifier 换取 token，读取账号；
4. API 签发登录 token，写入[会话 cookie](api-tokens.md#browser-sessions)。编辑必须使用[二次验证](login-security.md#requiring-two-factor)，所以多数时候 `finish` 返回的是 `pending`（或者还没有启用时的 `enroll`），单页应用接着走与密码登录相同的第二步。

编辑后台不开放注册，第三方账号还没有绑定时返回 `social.ErrNotLinked`，编辑先用密码登录，再在账号设置中[绑定](#linking-accounts)。例如 `web_social.go`：

```go
type StartInput struct {
    Provider string `path:"provider"`
}

type StartOutput struct {
    Body struct {
        URL  string `json:"url" doc:"跳转到第三方的地址"`
        Flow string `json:"flow" doc:"保存到回调之后，交给 finish"`
    }
}

type FinishInput struct {
    Provider  string `path:"provider"`
    UserAgent string `header:"User-Agent"`
    Body      struct {
        Flow     string `json:"flow" minLength:"1"`
        Callback string `json:"callback" doc:"回调页地址中的查询参数，原样交回，例如 code=…&state=…"`
    }
}

// FinishOutput 与密码登录的结果相同：登录成功时写入会话 cookie，否则 Pending、Enroll 中的一个不为空。
type FinishOutput struct {
    SetCookie *http.Cookie `header:"Set-Cookie"`
    Body      struct {
        Pending string `json:"pending,omitempty" doc:"带着它和验证器上的数字调用 /auth/two-factor"`
        Enroll  string `json:"enroll,omitempty" doc:"还没有启用二次验证：带着它调用 /auth/two-factor/enroll"`
    }
}

// RegisterWebSocial 注册编辑后台单页应用的第三方登录接口。
func RegisterWebSocial(api *rest.API, providers map[string]*oauth.Provider, editors *login.Realm, tokens *apitoken.Store, lifetime time.Duration) {
    start := huma.Operation{OperationID: "editor-social-start", Method: http.MethodPost, Path: "/auth/{provider}/start"}
    rest.Public()(&start)
    rest.Register(api, start, func(_ context.Context, in *StartInput) (*StartOutput, error) {
        p, ok := providers[in.Provider]
        if !ok {
            return nil, social.ErrUnknownProvider.New()
        }
        out := &StartOutput{}
        var flow oauth.Flow
        out.Body.URL, flow = p.Start()
        out.Body.Flow = flow.String()
        return out, nil
    })

    finish := huma.Operation{OperationID: "editor-social-finish", Method: http.MethodPost, Path: "/auth/{provider}/finish"}
    rest.Public()(&finish)
    rest.SameOrigin(api)(&finish) // 会写入会话 cookie：只接受本站页面的请求
    rest.Register(api, finish, func(ctx context.Context, in *FinishInput) (*FinishOutput, error) {
        p, ok := providers[in.Provider]
        if !ok {
            return nil, social.ErrUnknownProvider.New()
        }
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
            return nil, err // 用户取消了授权：oauth.ErrDenied
        }
        res, err := editors.External(ctx, ext)
        if err != nil {
            return nil, err
        }
        out := &FinishOutput{}
        switch {
        case res.Proof != "":
            return nil, social.ErrNotLinked.New()
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
    })
}
```

在编辑后台的 API 上注册：

```go
providers, err := social.Providers(ctx, a.cfg.Social)
if err != nil {
    return err
}
authhttp.RegisterWebSocial(admin, providers, acc.Editors, acc.EditorTokens, a.cfg.EditorToken.Lifetime)
```

`flow` 中有 state 和 PKCE verifier，回调页用完就从 `sessionStorage` 中删除。state 来自回调地址，`flow` 来自发起登录的那个标签页，两者对不上时（例如攻击者诱导用户打开带着自己授权码的回调地址）返回 `oauth.ErrInvalid`，响应 `401`；授权码已用过、ID token 无效时也是如此。

<a id="linking-accounts"></a>
## 账号关联

一个账号可以绑定多个第三方账号。账号设置页面中的“绑定 GitHub”与[单页应用中登录](#spa-login)的跳转相同，回调页把参数交给一个要求登录的接口，用 `Link` 绑定到当前登录的账号：

```go
ext, err := p.Finish(ctx, flow, params)
if err != nil {
    return err
}
out, err := h.users.External(ctx, ext)
if err != nil {
    return err
}
if out.Proof == "" {
    return social.ErrAlreadyLinked.New() // 这个第三方账号已经绑定了某个账号
}
sub, _ := identity.FromContext(ctx)
if err := h.users.Link(ctx, h.db, sub.Ref(), out.Proof); err != nil {
    return err
}
```

`Identifiers` 列出账号的全部登录标识，第三方账号的 `Kind` 是提供方的名称；`Detach` 解除绑定，账号始终保留至少一种登录方式，见[管理登录方式](login.md#managing)。设置了 [`PerTenant`](login.md#identifiers) 的账号体系，第三方账号同样按租户隔离：同一个 Google 账号可以在两个租户中各绑定一个账号。

<a id="troubleshooting"></a>
## 常见问题

**回调时返回 401，`oauth.ErrInvalid`**

state 与发起登录时保存的不一致（例如在另一个标签页中完成了登录），或者授权码已经用过、ID token 无效。让用户重新发起登录，见[在单页应用中登录](#spa-login)。

**用第三方账号注册时返回 `login.ErrTaken`**

这个邮箱已经属于其他账号。框架不按邮箱自动关联，用户要先用原来的方式登录，再绑定第三方账号，见[账号关联](#linking-accounts)。

**后台用第三方账号登录时返回 `social.ErrNotLinked`**

不开放注册的后台中，第三方账号要先绑定到已有账号上才能登录。

**启动时没有发现配置错误，登录时才报错**

给配置结构体加上 `Validate`，空的 `client_id` 这类错误在启动时就会被拦住。

<a id="api-reference"></a>
## API 参考

包 `github.com/go-tenon/tenon/login/oauth`。

### Discover、New

```go
func Discover(ctx context.Context, cfg Config) (*Provider, error)
func New(cfg Config, endpoint oauth2.Endpoint, account Account) (*Provider, error)
```

`Discover` 用于支持 OpenID Connect 的提供方（Google、Apple、企业 IdP），从 `Issuer` 读取配置；`New` 用于只支持 OAuth 2.0 的提供方（GitHub），`account` 用访问令牌读取账号信息。

### Provider

```go
func (p *Provider) Start() (string, Flow)
func (p *Provider) Finish(ctx context.Context, f Flow, params url.Values) (login.External, error)
func (p *Provider) Exchange(ctx context.Context, g Grant) (login.External, error)
func (p *Provider) IDToken(ctx context.Context, raw, nonce string) (login.External, error)
func (p *Provider) Name() string
func ParseFlow(s string) (Flow, error)
```

`Start` 返回跳转地址和这次流程的状态；`Finish` 校验回调并取得账号；`Exchange` 用于 App 自己完成授权后交来的授权码；`IDToken` 用于 App 原生登录得到的 ID token。结果交给 [`login.Realm.External`](login.md#api-reference)。
