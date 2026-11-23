# API 令牌

用户登录后由 `apitoken` 签发 token，JSON API 用它[认证](authentication.md)：浏览器把它放在会话 cookie 中，App、脚本和第三方放在 `Authorization: Bearer` 中，服务端校验的是同一个 token。

包：`apitoken`。

- 表中只保存 token 的 SHA-256，明文只在签发时返回一次；用户忘了保存时撤销它，再签发一个。
- 每个请求按 token 查一次表，撤销在下一个请求生效。
- token 只标识用户，不携带权限：权限在每个请求中[加载](#loading-permissions)。

| 种类（`Kind`） | 签发 | 有效期 | 用途 |
| --- | --- | --- | --- |
| `login` | `Login`，用户登录时 | 按[配置](#configuration)滑动过期 | 代表一台设备 |
| `personal` | `Create`，用户手动创建 | 创建者指定，可以不过期 | 脚本、CI、第三方集成 |
| `oauth` | `Authorize`，[OAuth 授权服务器](oauth-server.md)签发 | 按使用滑动过期 | MCP 客户端 |
| `impersonation` | `Impersonate` 或消费交换票据 | 短时滑动过期（默认 1 小时，最长 4 小时） | 管理员代为登录排查问题 |

<a id="example-usage"></a>
## 示例

```go
tokens := apitoken.NewStore(db, cfg.APIToken)

// 登录成功后签发，sub 是 login.Realm 返回的主体
plain, err := tokens.Login(ctx, sub, apitoken.Device{IP: ip, UserAgent: ua})

// 保护路由：会话 cookie 或 Authorization: Bearer <plain>
rest.RequireToken(api, tokens.Validate)(&op)

// 退出登录：撤销本次请求所用的 token
err = tokens.Revoke(ctx, sub, sub.Credential)
```

`db` 是入口打开的 `*bun.DB`，`cfg.APIToken` 是配置中的 `api_token` 段，`api` 是 `rest.New` 创建的 API。

<a id="assembling"></a>
## 装配

1. 把 `apitoken.Migrations()` 加入[发布命令](../database/migrations.md#publishing-framework-migrations)，发布后执行迁移，建出 `api_tokens` 表。
2. 把 `apitoken.Config` 放进根配置，见[配置](#configuration)。
3. 在入口用 `apitoken.NewStore(a.db, a.cfg.APIToken)` 创建一次 `Store`，交给登录接口和 `rest.RequireToken`。配置不合法时 `NewStore` panic。

`Store.Validate` 就是 `identity.TokenValidator`，例如 `rest.RequireToken(api, tokens.Validate, "articles")`。它返回的主体 `Method` 为 `bearer`，`Credential` 是 token 的 ID，`Tenant` 是 token 绑定的租户。它每分钟最多为一个 token 写一次库，记录最近使用时间并延长登录 token 的有效期，所以空闲时长的精度约为一分钟。

多套账号需要不同的有效期时（例如商家员工空闲 1 天失效，App 中的客户 90 天），每套账号用自己的配置创建一个 `Store` 签发，例如 `apitoken.NewStore(a.db, a.cfg.CustomerToken)`。它们共用 `api_tokens` 表，校验用其中任何一个都可以；API 接受哪些账号由 [`rest.Options.Accounts`](authentication.md#protecting-routes) 决定。

<a id="login-and-logout"></a>
## 登录与登出

登录接口校验账号后用 `Login` 签发，App、脚本等客户端从响应体中取 token；浏览器的登录把它写进[会话 cookie](#browser-sessions)。`users` 是用户的 [`login.Realm`](login.md)，`tokens` 是 `*apitoken.Store`，`in.ip` 是输入类型在 Huma 的 `Resolve` 中记下的客户端地址：

```go
// 登录接口：rest.Public()
out, err := users.Password(ctx, login.Email(in.Body.Email), in.Body.Password)
if err != nil {
    return nil, err
}
plain, err := tokens.Login(ctx, out.Subject, apitoken.Device{
    Remember:  in.Body.Remember, // 空闲时长用 remember_idle_timeout
    IP:        in.ip,
    UserAgent: in.UserAgent,
})
resp.Body.Token = plain

// 登出接口：rest.RequireToken(api, tokens.Validate)
sub, _ := identity.FromContext(ctx)
err = tokens.Revoke(ctx, sub, sub.Credential)
```

- 账号启用了二次验证时 `out.Pending` 不为空、`out.Subject` 为零值，先完成[二次验证](login.md#two-factor)再签发。
- IP 和 User-Agent 显示在[设备列表](#listing-tokens)中。
- `Login` 保存 `out.Subject.Auth`（这次登录的验证方式和时间），校验得到的主体带着它。敏感操作前再次验证身份后用 `tokens.Confirm(ctx, sub, methods...)` 更新它，见[确认身份](login.md#confirming)。

<a id="browser-sessions"></a>
## 浏览器的会话 cookie

浏览器的登录接口把 token 写进 `httpx.SessionCookie` 返回的 HttpOnly cookie，页面上的脚本拿不到 token。`rest.RequireToken` 从 cookie 或 `Authorization: Bearer` 中取 token，两者都有时以请求头为准。cookie 的属性和浏览器一侧的规则见[客户端协议：会话](../http/protocol.md#sessions)。

```go
type SessionOutput struct {
    SetCookie *http.Cookie `header:"Set-Cookie"`
}

// 登录：签发 plain 之后
return &SessionOutput{SetCookie: httpx.SessionCookie{}.Set(plain, cfg.APIToken.Lifetime)}, nil

// 登出：Revoke 之后
return &SessionOutput{SetCookie: httpx.SessionCookie{}.Clear()}, nil
```

浏览器的登录接口这样注册，`createSession` 是返回 `*SessionOutput` 的登录处理器：

```go
op := huma.Operation{OperationID: "auth-session-create", Method: http.MethodPost, Path: "/auth/session", DefaultStatus: http.StatusNoContent}
rest.Public()(&op)
rest.SameOrigin(api)(&op)
rest.Register(api, op, createSession)
```

- `Set` 的第二个参数是浏览器保存 cookie 的时长，通常是 `lifetime`；token 空闲超时后 cookie 还在，请求照样返回 `401`。
- 设置 cookie 的公开接口加 `rest.SameOrigin`：其他网站的页面调用它返回 `403 http.cross_site`，防止用户在不知情时登录攻击者的账号。用 cookie 认证的写请求由 `rest.RequireToken` 检查同源，应用不需要 CSRF token。
- 同一个源下有多套账号时（例如运营后台和租户控制台），各用一个以 `__Host-` 开头的名称，例如 `httpx.SessionCookie{Name: "__Host-ops"}`，交给这套 API 的 `rest.Options.SessionCookie` 和 `realtime.Options.SessionCookie`，签发和清除时用同一个值。
- 页面和 API 必须同源，另一个域名上的前端用 Bearer。开发时用 `localhost` 访问：`Secure` 的 cookie 只在 HTTPS 和 `http://localhost` 上保存。

<a id="personal-access-tokens"></a>
## 个人访问令牌

用户为脚本或第三方集成创建令牌，并把它限制在部分权限内。在受 `rest.RequireToken` 保护的接口中，`sub` 是 `identity.FromContext(ctx)` 取出的主体：

```go
plain, err := tokens.Create(ctx, sub, apitoken.Personal{
    Name:      in.Body.Name,              // 1 到 255 个字符，显示在令牌列表中
    Scopes:    []string{"articles:read"}, // 至少一个，支持通配符，例如 articles:*
    ExpiresAt: time.Time{},               // 零值表示不过期
})
// plain 只在这个响应中返回一次
```

- 令牌能做的是用户当前的权限与 `Scopes` 的交集：`Scopes` 写了 `*` 也以用户本人的权限为上限，用户被收回权限后令牌随之收窄。
- 新令牌不比创建它的凭据更宽：请求用的是带 `Scopes` 的令牌时，`Personal.Scopes` 的每一项都要在它之内，否则返回 `403`，例如只读令牌不能创建可写的令牌。
- `sub.Tenant` 不为空时，令牌只能在这个租户中使用，见[令牌与租户](tenancy.md#tokens)。

<a id="listing-tokens"></a>
## 设备与令牌列表

`List` 返回用户全部未过期的 token，最近使用的在前。`sub` 是 `identity.FromContext(ctx)` 取出的主体：

```go
list, err := tokens.List(ctx, sub)
for _, t := range list {
    // t.Kind、t.Name、t.IP、t.UserAgent、t.LastUsedAt、t.ExpiresAt
    // t.Current：是否为本次请求所用的 token（按 sub.Credential 判断）
    // t.ClientName：OAuth 令牌的客户端名称，用于“已连接的应用”
    // t.Impersonator：不为零值时是客服等人代为登录时签发的
}
```

代为登录签发的 token 在列表中标出来，用户可以看到并撤销它，见[代为登录](authentication.md#impersonation)。

<a id="revoking-tokens"></a>
## 撤销令牌

| 方法 | 撤销范围 | 用途 |
| --- | --- | --- |
| `Revoke(ctx, sub, id)` | `sub` 的一个 token；`id` 为 `sub.Credential` 时即登出 | 登出、移除某台设备或某个令牌、断开已连接的应用 |
| `RevokeOthers(ctx, sub)` | 除本次请求所用 token 以外的全部登录 token；保留个人访问令牌和 OAuth 令牌 | “退出其他设备”、修改密码后 |
| `RevokeSubject(ctx, ref)` | 主体 `ref`（`identity.Ref`）的全部 token | 封禁账号、找回密码后 |
| `RevokeByImpersonator(ctx, actor)` | 操作者 `actor` 签发的全部代为登录令牌与未消费票据 | 停用操作者、收回代为登录权限后 |

被撤销的 token 在下一次请求时返回 `401`。用它建立的 SSE 长连接最多 30 秒内结束，见[凭据失效](../http/sse.md#revoked-credentials)；要立即断开时撤销后调用 `RecheckCredential` 或 `RecheckSubject`，见[立即断开](../http/sse.md#immediate-revocation)。

<a id="loading-permissions"></a>
## 加载权限

`Validate` 得到的主体没有 `Permissions`。`rest.Options.Accounts` 中这套账号的 Resolver 在每个请求中按用户当前的状态加载权限、检查账号是否停用，所以权限变化和停用在下一个请求生效，见[每次请求加载权限](authentication.md#resolving-subjects)。

个人访问令牌和 OAuth 令牌的 `Scopes` 由 `Validate` 填入，`sub.Can` 同时检查 `Permissions` 和 `Scopes`。

<a id="oauth-tokens"></a>
## OAuth 令牌

[OAuth 授权服务器](oauth-server.md)在用户同意后用 `Authorize` 签发 `Kind` 为 `oauth` 的令牌。它和个人访问令牌一样带 `Scopes`，另外记录 `ClientID`、`ClientName` 和 `Resource`。

OAuth 令牌只能用于授权时的资源：`Validate` 拒绝它；`ValidateFor(resource)` 接受发给 `resource` 的 OAuth 令牌，同时照常接受登录 token 和个人访问令牌：

```go
mcpx.HandlerOptions{Validate: tokens.ValidateFor("https://example.com/mcp") /* … */}
```

它出现在 `List` 中，`Revoke` 断开连接，`RevokeOthers` 保留它，`RevokeSubject` 一并撤销。

<a id="cleanup"></a>
## 过期清理

过期的 token 校验失败，但行仍然留在表中。`Cleanup(ctx, limit)` 每次最多删除 `limit` 个，注册为每小时的[定时任务](../background/scheduling.md#framework-cleanups)，`sched` 是入口中的 `*schedule.Scheduler`：

```go
sched.Add("apitoken:cleanup", "@hourly", schedule.Cleanup(tokens.Cleanup, 1000))
```

注销账号和导出个人数据时，把包级函数 `apitoken.Erase`、`apitoken.Export` 登记到[注销账号](../features/privacy.md#framework-sources)和[导出个人数据](../features/privacy.md#sources)。

<a id="configuration"></a>
## 配置

`apitoken.Config` 放进根配置结构体，字段标签为 `yaml:"api_token"`：

```yaml
api_token:
  lifetime: 720h              # 登录 token 从签发起的最长有效期；默认 720h
  idle_timeout: 24h           # 超过这么久没有使用即失效，0 表示不限制；默认 24h
  remember_idle_timeout: 168h # 登录时选择“记住我”的空闲时长；默认 168h
```

- 登录 token 的截止时间是“签发时间 + `lifetime`”与“最近使用时间 + 空闲时长”中较早的一个；收到 `401` 时让用户重新登录。
- 有效期在签发时写入 token，修改配置只影响之后签发的 token。
- [`config.Load`](../general/configuration.md#loading-configuration) 在启动时检查这些值，不合法时应用起不来。
- 这些配置只管登录 token：个人访问令牌按 `ExpiresAt`，OAuth 令牌按[授权服务器的选项](oauth-server.md#assembling)。
