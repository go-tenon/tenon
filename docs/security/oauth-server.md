# OAuth 授权服务器

`oauthserver` 是 OAuth 2.1 授权服务器，用授权码 + PKCE 做两件事：claude.ai、ChatGPT 这类第三方应用在用户同意后代表用户调用 [MCP 服务](../ai/mcp.md)；应用自己的桌面端、App 在系统浏览器中登录。

包：`oauthserver`。

- 遵循 OAuth 2.1 和 [MCP 授权规范](https://modelcontextprotocol.io/specification/2026-07-28/basic/authorization)：PKCE S256、授权服务器元数据（RFC 8414）、授权响应带 `iss`（RFC 9207）、令牌绑定资源（RFC 8707）。只支持公开客户端，不发刷新令牌。
- 同意页是应用前端中的一个页面，以当前登录的身份调用 `Register` 注册的接口，所以能授权的是这些接口接受的账号体系。

能填写请求头的客户端（Claude Code、Cursor）也可以用[个人访问令牌](api-tokens.md#personal-access-tokens)连接 MCP 服务。

<a id="example-usage"></a>
## 示例

```go
oauth := oauthserver.New(a.db, tokens, oauthserver.Options{
    Issuer:    "https://example.com",
    Resources: []string{"https://example.com/mcp"},
    Scopes: []oauthserver.Scope{
        {Name: "articles:read", Description: "查看文章"},
        {Name: "articles:write", Description: "撰写和发布文章"},
    },
    ConsentURL: "/oauth/consent", // 前端中的同意页
})
router.Handle(oauth.MetadataPath(), oauth.Metadata())       // /.well-known/oauth-authorization-server
oauth.Routes(router)                                         // 授权、令牌、撤销端点
oauth.Register(api, rest.RequireToken(api, tokens.Validate)) // 同意页调用的接口
```

`a.db` 是入口打开的 `*bun.DB`，`tokens` 是 [`*apitoken.Store`](api-tokens.md#assembling)，`router` 是应用的 chi 路由，`api` 是 `rest.New` 创建的 API。MCP 服务一侧见[挂到 MCP 服务](#mcp)。

<a id="installation"></a>
## 安装

授权码和动态注册的客户端在 `oauthserver` 的表中，令牌在 `apitoken` 的表中。把两个包的迁移加入[发布命令](../database/migrations.md#publishing-framework-migrations)，发布后执行迁移：

```go
migratecmd.Publish(apitoken.Migrations(), oauthserver.Migrations())
```

<a id="assembling"></a>
## 装配：`Options` 与端点

| 选项 | 说明 |
| --- | --- |
| `Issuer` | 授权服务器的地址，也是各端点的前缀。https 地址，本机开发可以用 `http://localhost:8080`；部署在路径之下时带上路径，见[部署在路径之下](#path-prefix) |
| `Resources` | 令牌可以用于的资源，与用户在客户端中填写的 MCP 服务地址完全相同 |
| `Scopes` | 客户端可以申请的范围，见[范围](#scopes)；与 `Resources` 同时设置或同时为空 |
| `Clients`、`DynamicRegistration` | 见[客户端](#clients)、[动态客户端注册](#dynamic-registration) |
| `ConsentURL` | 同意页的地址，必填：本站的路径，或者完整的 https 地址 |
| `TokenLifetime`、`TokenIdleTimeout` | OAuth 令牌的最长有效期和最长空闲时间，默认 1 年和 30 天 |

选项不合法时 `New` panic。`Routes` 注册 `GET /oauth/authorize`、`POST /oauth/token`、`POST /oauth/revoke`（RFC 7009），开启动态注册时还有 `POST /oauth/register`，路径相对于 `Issuer`。`Register(api, auth)` 注册同意页调用的 `GET /oauth/consent`（`oauth-consent-get`，读取授权请求）和 `POST /oauth/consent`（`oauth-consent-decide`，允许或拒绝），`auth` 与同一个前端的其他接口相同。

授权码 10 分钟有效，用 `oauth.Cleanup` 每小时删除过期的，见[清理框架数据](../background/scheduling.md#framework-cleanups)。包级函数 `oauthserver.Erase` 登记到[注销账号](../features/privacy.md#framework-sources)。

<a id="scopes"></a>
### 范围

范围是客户端申请的权限，同意页逐项列出 `Description`。名称用[权限目录](authorization.md#permission-catalog)中的权限：令牌的 `Scopes` 是用户同意的范围，`sub.Can` 同时检查用户本人的权限和它，所以只有查看权限的用户授权了 `articles:write`，令牌也发布不了文章。

客户端省略 `scope` 时申请全部范围。只放适合交给 AI 的权限，删除数据、管理成员这类操作留在应用内。

<a id="mcp"></a>
### 挂到 MCP 服务

`oauth` 是授权服务器，`tools` 是 MCP 工具集，`accounts` 与 REST API 的 `rest.Options.Accounts` 相同：

```go
res := oauth.Resource("https://example.com/mcp") // 不在 Options.Resources 中时 panic
router.Handle(res.MetadataPath(), mcpx.ResourceMetadata(res)) // /.well-known/oauth-protected-resource/mcp
router.Handle("/mcp", mcpx.Handler(tools, mcpx.HandlerOptions{
    Validate: tokens.ValidateFor(res.URL),
    Accounts: accounts,
    Resource: &res,
}))
```

- 设置了 `Resource` 后，未认证的请求得到 `401` 和 `WWW-Authenticate: Bearer resource_metadata="…"`，客户端由此找到授权服务器。不设置时客户端不会开始授权。
- `tokens.ValidateFor(res.URL)` 只接受发给这个地址的 [OAuth 令牌](api-tokens.md#oauth-tokens)，登录 token 和个人访问令牌照常可用。JSON API 用的 `tokens.Validate` 拒绝 OAuth 令牌。

<a id="flow"></a>
## 授权流程

1. 客户端访问 MCP 服务得到 `401`，从两份元数据中找到各端点。
2. 客户端在浏览器中打开 `/oauth/authorize`，授权端点校验后跳到 `ConsentURL?request=…`；用户登录后点“允许”。
3. 浏览器带着授权码回到客户端，客户端用授权码和 PKCE 的 verifier 换取令牌，以这个用户的身份调用工具。

用户点“拒绝”时客户端收到 `access_denied`。client_id 或回调地址不对时，授权端点跳到 `ConsentURL?error=…`，值是 `oauth.invalid_client` 或 `oauth.invalid_redirect_uri`；其他问题（缺少 PKCE、未知的范围）带着错误跳回客户端。

<a id="clients"></a>
## 客户端

- **https 地址形式的 client_id**（Client ID Metadata Document）：claude.ai、ChatGPT、Claude Code 用这种方式，即来即用。授权时用[出站请求](../libraries/outbound.md)取回这个地址上的 JSON，检查 `client_id` 与地址相同、`redirect_uris` 包含本次的回调地址；拒绝内网地址。
- **预先登记的客户端**：在 `Options.Clients` 中登记 `ID`（不能是 https 地址）、`Name` 和 `RedirectURIs`。应用自己的桌面端和 App 另外设置 `FirstParty`，见[应用自己的客户端](#first-party-clients)。

回调地址按完整地址匹配；本机地址（`localhost`、`127.0.0.1`、`[::1]`）忽略端口。

<a id="dynamic-registration"></a>
### 动态客户端注册

只会先注册自己（RFC 7591）的客户端需要动态注册。MCP 规范已把它标为过时，所以默认关闭；开启时设置 `DynamicRegistration: true`，同时要设置 `Resources` 和 `Scopes`。客户端 `POST /oauth/register` 提交名称和 1 到 10 个回调地址（https 或本机的 http），得到 `dcr_` 开头的 client_id。

> [!WARNING]
> 注册接口是公开的。用[限流](../libraries/rate-limiting.md)按 IP 限制它，并定时删除长期没有用于授权的注册。

`sched` 是入口中的 `*schedule.Scheduler`。删除注册后已签发的令牌继续有效，客户端下次连接时重新注册：

```go
sched.Add("oauth:cleanup-clients", "@daily", schedule.Cleanup(oauth.CleanupClients(90*24*time.Hour), 1000))
```

<a id="tokens"></a>
## 令牌

第三方客户端换到的是 [`apitoken` 的 OAuth 令牌](api-tokens.md#oauth-tokens)，权限是用户当前的权限与同意的范围的交集。它按使用滑动过期：`TokenIdleTimeout` 内用过就继续有效，最长 `TokenLifetime`，到期后客户端让用户重新授权。它出现在用户的[令牌列表](api-tokens.md#listing-tokens)中，`ClientName` 用来做“已连接的应用”页面，`Revoke` 断开连接。

<a id="first-party-clients"></a>
## 应用自己的客户端

应用自己的桌面端、App 在系统浏览器中登录（RFC 8252），密码、第三方登录、二次验证都在网页上完成。在 `Clients` 中登记并设置 `FirstParty`：

```go
Clients: []oauthserver.Client{{
    ID:           "admin-desktop",
    Name:         "运营后台",
    RedirectURIs: []string{"http://127.0.0.1/callback"}, // 桌面端：本机地址，端口每次不同
    FirstParty:   true,
}},
```

- 授权请求不带 `scope` 和 `resource`，带了时以 `invalid_request` 跳回客户端。
- 同意页只确认登录：`oauth-consent-get` 返回 `first_party: true`。仍然要用户点一下：本机上任何程序都能打开授权地址，没有这一步它就能悄悄拿到登录 token。
- 换到的是登录 token，与密码登录得到的相同，可以访问整个 API，“退出其他设备”会撤销它。令牌端点的响应没有 `expires_in`，按 [apitoken 的配置](api-tokens.md#configuration)滑动过期。
- 只给自己的客户端登录时 `Resources` 和 `Scopes` 都不设置，这时 https 地址形式的 client_id 和动态注册都不可用。

回调地址的选择和客户端一侧的流程见[原生客户端：登记客户端](../clients/native.md#registering)和[客户端协议：原生客户端](../http/protocol.md#native-clients)。

<a id="consent-page"></a>
## 同意页

同意页是前端中的一个路由，路径与 `ConsentURL` 相同。`tenon add client` 会在客户端显示的前端中生成它（`src/routes/oauth/consent.tsx`）；只对第三方开放时按下面的规则写：

1. 页面要求登录，登录后回到原地址（包括 `request` 参数）。地址中有 `error` 时只显示错误码。
2. `GET /oauth/consent?request=…` 返回 `client_name`、`redirect_host`、`first_party`、`scopes`（每项有 `name`、`description`）。
3. `first_party` 为 true 时显示“在 `client_name` 上登录”和“继续”；否则列出各范围的 `description` 和 `redirect_host`，以及“允许”“拒绝”。
4. 点击后 `POST /oauth/consent`，请求体 `{"request": …, "approve": true}`，用返回的 `url` 整页跳转（`location.assign(url)`）。

- 两个接口只接受用户本人的登录 token：个人访问令牌、OAuth 令牌和[代为登录](authentication.md#impersonation)的 token 返回 `403 oauth.restricted_credential`。
- 两个接口每次都重新校验 `request`，被改过的请求返回 `422`。

> [!WARNING]
> 同意页不能被嵌入框架，否则可以被诱导点击。`frontend` 默认的内容安全策略带 `frame-ancestors 'none'`，见[托管前端：安全](../frontend/hosting.md#security)；用 `Options.CSP` 给某些页面放开它时，按请求的路径只放开那些页面，同意页所在的路由保持 `frame-ancestors 'none'`。

<a id="deployment"></a>
## 部署

- 授权服务器和 MCP 服务都要能从公网访问，Claude 的请求来自 `160.79.104.0/21`；取回客户端元数据要能访问外网。
- `Issuer` 和 `Resources` 写用户看到的对外 https 地址，在反向代理之后也一样；claude.ai 一直提示授权失败时先检查这一条。

<a id="path-prefix"></a>
### 部署在路径之下

应用只分到 `/blog` 这样的路径时，`Issuer` 和 `Resources` 都带上路径，`Routes` 挂在这个路径上：

```go
oauth := oauthserver.New(a.db, tokens, oauthserver.Options{
    Issuer:     "https://example.com/blog",
    Resources:  []string{"https://example.com/blog/mcp"},
    ConsentURL: "/blog/oauth/consent",
    // ……
})
router.Handle(oauth.MetadataPath(), oauth.Metadata()) // /.well-known/oauth-authorization-server/blog
router.Route("/blog", func(r chi.Router) {
    oauth.Routes(r) // /blog/oauth/authorize……
})

res := oauth.Resource("https://example.com/blog/mcp")
router.Handle(res.MetadataPath(), mcpx.ResourceMetadata(res)) // /.well-known/oauth-protected-resource/blog/mcp
```

两份元数据在域名的根上，路径插在 `.well-known` 之后（RFC 8414、RFC 9728），反向代理要把它们也转发给应用并保留完整路径：

```nginx
location /blog/ { proxy_pass http://blog:8080; }
location = /.well-known/oauth-authorization-server/blog { proxy_pass http://blog:8080; }
location = /.well-known/oauth-protected-resource/blog/mcp { proxy_pass http://blog:8080; }
```

<a id="testing"></a>
## 测试

测试中代替浏览器：`GET /oauth/authorize` 的 `Location` 中取出 `request`；用 `tokens.Login` 签发的 token 调用 `POST /oauth/consent` 得到授权码；用授权码和 verifier 调用 `POST /oauth/token` 换令牌，再调用 MCP 工具。
