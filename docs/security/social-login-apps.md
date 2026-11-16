# 在 App 中使用第三方登录

[第三方登录](social-login.md)一页介绍了 Google、GitHub 等提供方的配置，以及在网页和单页应用中登录。移动 App 和桌面 App 登录时情况不同：App 不能安全地保存提供方的密钥，回调地址也不是网页。这一页介绍 App 中的写法，以及上架 App Store 时通常必须提供的 Sign in with Apple。

阅读前需要了解：[第三方登录](social-login.md)；[API 令牌](api-tokens.md)。

- [在 App 中登录](#api-login)
    - [App 原生登录](#native-sign-in)
- [Sign in with Apple](#sign-in-with-apple)
- [常见问题](#troubleshooting)

<a id="api-login"></a>
## 在 App 中登录

App 自己完成跳转：

1. 客户端生成 state、nonce 和 PKCE verifier，自己保存，然后打开第三方的授权地址，`redirect_uri` 是它登记的回调地址；
2. 第三方带着授权码跳回客户端，客户端比较 state；
3. 客户端把授权码、verifier、nonce 和回调地址交给 API；
4. API 用 client secret 换取 token，读取账号，签发 [API 令牌](api-tokens.md)。

读者 App 可以用第三方账号注册：还没有绑定时，API 返回注册凭证和第三方提供的名称，App 进入注册页预填。例如 `social.go`：

```go
type SocialTokenInput struct {
    Provider  string `path:"provider"`
    UserAgent string `header:"User-Agent"`
    Body      struct {
        Code         string `json:"code,omitempty"`
        CodeVerifier string `json:"code_verifier,omitempty"`
        RedirectURI  string `json:"redirect_uri,omitempty"`
        IDToken      string `json:"id_token,omitempty" doc:"App 原生登录时 SDK 返回的 ID token，代替 code"`
        Nonce        string `json:"nonce,omitempty"`
    }
    ip string
}

// Resolve 记录客户端地址，见 Huma 的 Resolver。
func (in *SocialTokenInput) Resolve(ctx huma.Context) []error {
    in.ip, _, _ = net.SplitHostPort(ctx.RemoteAddr())
    return nil
}

type SocialTokenOutput struct {
    Body struct {
        Token string `json:"token,omitempty"`
        Proof string `json:"proof,omitempty" doc:"还没有账号：带着它调用注册接口"`
        Name  string `json:"name,omitempty"`
    }
}

// RegisterSocial 注册用第三方账号换取 API 令牌的接口。
func RegisterSocial(api *rest.API, providers map[string]*oauth.Provider, users *login.Realm, tokens *apitoken.Store) {
    op := huma.Operation{OperationID: "auth-social-token", Method: http.MethodPost, Path: "/auth/{provider}/tokens"}
    rest.Public()(&op)
    rest.Register(api, op, func(ctx context.Context, in *SocialTokenInput) (*SocialTokenOutput, error) {
        p, ok := providers[in.Provider]
        if !ok {
            return nil, social.ErrUnknownProvider.New()
        }
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
        out, err := users.External(ctx, ext)
        if err != nil {
            return nil, err
        }
        resp := &SocialTokenOutput{}
        if out.Proof != "" {
            resp.Body.Proof, resp.Body.Name = out.Proof, ext.Name
            return resp, nil
        }
        resp.Body.Token, err = tokens.Login(ctx, out.Subject, apitoken.Device{IP: in.ip, UserAgent: in.UserAgent})
        return resp, err
    })
}
```

App 带着 `proof` 调用[注册接口](login.md#codes)，与验证码注册是同一个接口。

`Exchange` 检查两件事：`redirect_uri` 必须在 `client_redirect_urls` 中，否则攻击者可以用发往其他地址的授权码换取 token；客户端没有 client secret，PKCE 是它唯一的保护，所以 `code_verifier` 必填。client secret 始终留在服务器上。

<a id="native-sign-in"></a>
### App 原生登录

App 也可以使用系统或第三方 SDK 提供的原生登录，例如 Google 登录和 Sign in with Apple。SDK 直接返回 ID token，App 把它放在 `id_token` 中交给同一个接口，由 `IDToken` 校验。

SDK 签发的 ID token 的受众是 App 自己的 client ID，它与网页使用的不同，所以要列在 `oauth.Config.Audiences` 中，见上面的 `GoogleAppClientIDs`。

nonce 用来确认 ID token 属于这一次登录：截获的 ID token 在有效期内可以被重放，只有服务器下发 nonce、App 把它放进登录请求，服务器再比较保存的那个值，才能防住。有的 SDK 要求在请求中放 nonce 的 SHA-256，这时比较哈希值。

<a id="sign-in-with-apple"></a>
## Sign in with Apple

Apple 也是 OpenID Connect 提供方，签发者是 `https://appleid.apple.com`，与 Google 的写法基本相同，但有几点差别：

```go
apple, err := oauth.Discover(ctx, oauth.Config{
    Provider:     "apple",
    Issuer:       "https://appleid.apple.com",
    ClientID:     cfg.Apple.ServiceID,
    ClientSecret: cfg.Apple.ClientSecret,
    RedirectURL:  "https://example.com/auth/apple/callback", // 服务器上的转发地址，见下文
    Scopes:       []string{"openid", "email", "name"},
    NoPKCE:       true,
    FormPost:     true,
})
```

- **client secret 是你自己签名的 JWT**：用 Apple 下发的私钥（`.p8`）按 ES256 签名，有效期最长 6 个月，需要定期重新生成，见 [Apple 的文档](https://developer.apple.com/documentation/accountorganizationaldatasharing/creating-a-client-secret)。
- **不使用 PKCE**：Apple 的授权和 token 接口没有 PKCE 参数，依靠 state、nonce 和 client secret，所以设置 `NoPKCE`。
- **用户姓名只在第一次授权时返回**：它不在 ID token 中，而是回调表单中的 `user` 字段，第一次登录时就要保存下来。`email_verified` 是字符串，`oauth` 已经处理。
- **申请了姓名或邮箱时，回调是跨站 POST**：Apple 要求 `response_mode=form_post`（`FormPost`），浏览器从 `appleid.apple.com` 把授权码提交到回调地址。

最后一点影响[单页应用中登录](social-login.md#spa-login)的写法：回调是 POST，单页应用接收不了。在服务器上注册这个回调地址，把表单原样转成查询参数，跳到单页应用的回调页，之后的步骤与 Google 相同：

```go
router.Post("/auth/apple/callback", func(w http.ResponseWriter, r *http.Request) {
    if err := r.ParseForm(); err != nil {
        http.Error(w, "bad request", http.StatusBadRequest)
        return
    }
    http.Redirect(w, r, "/admin/auth/apple/callback?"+r.PostForm.Encode(), http.StatusSeeOther)
})
```

`RedirectURL` 是这个服务器地址。授权码只能使用一次，有效期很短，与第三方用查询参数直接跳回时一样出现在地址中；`user` 字段（第一次授权时的姓名）随之带到回调页，单页应用把它一起交给注册接口。

<a id="troubleshooting"></a>
## 常见问题

**App 原生登录时，ID token 校验失败，提示受众不对**

SDK 签发的 ID token 的受众是 App 自己的 client ID，与网页使用的不同。把它加入 `oauth.Config.Audiences`，见 [App 原生登录](#native-sign-in)。

**交换授权码时报错 `redirect_uri` 不允许**

App 使用的回调地址要列在 `client_redirect_urls` 中，见[在 App 中登录](#api-login)。

**Sign in with Apple 一段时间后突然全部失败**

Apple 的 client secret 是你自己签名的 JWT，有效期最长 6 个月，到期后要重新生成，见 [Sign in with Apple](#sign-in-with-apple)。

**第二次用 Apple 登录时拿不到用户的姓名**

Apple 只在第一次授权时返回姓名，第一次登录时就要保存下来。
