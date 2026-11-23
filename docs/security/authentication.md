# 认证

认证认出调用者，得到一个主体 `identity.Subject`：入口把它写入 context，业务动作从参数中拿到它，不关心调用来自哪个入口。各入口用同一个令牌校验器和同一组 `identity.Accounts`。

包：`identity`、`httpx/rest`。

| 入口 | 凭据 |
| --- | --- |
| HTTP 接口 | 浏览器的[会话 cookie](api-tokens.md#browser-sessions)，或 `Authorization: Bearer <token>` |
| [RPC](../rpc/endpoints.md)、[MCP 服务](../ai/mcp.md) | `Authorization: Bearer <token>` |
| [实时频道](../libraries/realtime.md#tokens) | 连接请求中的会话 cookie，或客户端建立连接时传入的 token |
| 队列任务、定时任务、命令 | 没有外部凭据，代码构造主体，后台操作用 `identity.System`，见[作为队列任务](../concepts/actions.md#as-jobs) |

调用者能不能做某件事属于授权，见[授权](authorization.md)。

<a id="example-usage"></a>
## 示例

用 [API 令牌](api-tokens.md)认证的接口：

```go
tokens := apitoken.NewStore(db, cfg.APIToken)

api := rest.New(router, rest.Options{
    Accounts: identity.Accounts{user.Type: nil}, // 接受的账号体系
})

op := huma.Operation{OperationID: "article-create", Method: http.MethodPost, Path: "/articles"}
rest.RequireToken(api, tokens.Validate, "articles:write")(&op)
rest.Register(api, op, NewCreate(db).HTTP)
```

`db` 是入口打开的 `*bun.DB`，`cfg.APIToken` 是令牌的配置，`router` 是应用的 `chi.Router`。没有有效令牌的请求得到 `401`，缺少 `articles:write` 得到 `403`。处理器用 `identity.FromContext(ctx)` 取出主体，见[获取已认证的主体](#retrieving-the-subject)。

<a id="key-concepts"></a>
## 主体：`identity.Subject`

| 字段 | 说明 |
| --- | --- |
| `Type` | 所属的账号体系，例如 `user`、`staff`、`customer`，由应用声明；后台操作用 `identity.System` |
| `ID` | 在这套账号中的标识，不透明的字符串，通常是用户表的主键 |
| `Impersonator` | 代为登录时实际操作的人，零值表示本人，见[代为登录](#impersonation) |
| `Method` | 认证方式，例如 `bearer` |
| `Credential` | 本次请求所用凭据的 ID，例如 API 令牌的 ID，用于在设备列表中标出当前设备；可以为空 |
| `Tenant` | 本次请求所在的租户，`""` 是平台本身，见[多租户](#tenants) |
| `BoundTenant` | 凭据绑定的租户边界，`""` 表示不限制；由凭据决定，Resolver 保持不变 |
| `Permissions` | 主体在 `Tenant` 中拥有的权限 |
| `Scopes` | 凭据的权限限制，例如只读令牌的 `articles:read`；`nil` 表示不限制 |
| `Auth` | 这次登录通过的验证方式（`Methods`）和最近一次验证身份的时间（`Time`）；`Auth.TwoFactor()` 报告是否经过二次验证。个人访问令牌、代为登录的令牌为零值 |

- 账号体系在拥有用户表的模块中声明，例如 `internal/user/user.go` 中的 `const Type identity.SubjectType = "user"`。名称以小写字母开头，只含小写字母、数字和下划线。
- `sub.Ref()` 返回 `identity.Ref{Type, ID}`。令牌、角色、操作日志、通知都同时保存类型和 ID，所以 `ID` 只需在同一个 `Type` 中唯一。
- `sub.Can(perm)` 在 `Permissions` 和 `Scopes` 都允许 `perm` 时返回 `true`，权限名和通配符见[权限](authorization.md#permissions)。

<a id="token-validators"></a>
## 编写令牌校验器：`identity.TokenValidator`

校验器的类型是 `func(ctx context.Context, token string) (identity.Subject, error)`。REST 接口、RPC、MCP 服务和实时频道都用它。用 [API 令牌](api-tokens.md)时直接用 `tokens.Validate`；用认证服务签发的 JWT 时，在校验器中用成熟的库检查签名、签发者和有效期，再构造主体。

内部服务用配置中固定的 token 时：

```go
// Validator 返回校验固定 token 的校验器，creds 是 token 到调用方 ID 的映射。
func Validator(creds map[string]string) identity.TokenValidator {
    return func(_ context.Context, token string) (identity.Subject, error) {
        id, ok := creds[token]
        if !ok {
            return identity.Subject{}, identity.ErrUnauthenticated.New()
        }
        return identity.Subject{Type: service.Type, ID: id, Method: "bearer", Permissions: []string{"*"}}, nil
    }
}
```

`service.Type` 是应用为内部调用方声明的账号体系。校验器在返回主体之前完成全部检查：签名、过期、是否已撤销。

| 校验器返回 | 结果 |
| --- | --- |
| `ID` 非空的主体 | 认证通过 |
| `ID` 为空的主体，或不是系统故障的错误（例如 `identity.ErrUnauthenticated`） | `401` |
| [系统故障](../general/errors.md#error-kinds)：`errorx.Unavailable`、`errorx.Internal` 类错误或普通错误 | 对应的 `5xx`，例如认证服务不可用时 `503`；客户端的登录状态保持不变 |

用户的登录、注册和找回密码见[登录](login.md)，用 GitHub、Google 等账号登录见[第三方登录](social-login.md)。

<a id="protecting-routes"></a>
## 保护路由：`rest.Options.Accounts` 和 `rest.RequireToken`

需要认证的 API 在 `rest.Options.Accounts` 中声明接受的账号体系，键是 `SubjectType`，值是这套账号的 [Resolver](#resolving-subjects)，`nil` 表示直接使用校验器给出的主体：

```go
api := rest.New(router, rest.Options{Accounts: identity.Accounts{user.Type: nil}})
requireArticles := rest.RequireToken(api, tokens.Validate, "articles")
```

`router` 是应用的 `chi.Router`，`tokens.Validate` 是令牌校验器。`requireArticles(&op)` 的注册写法见[访问策略](../http/routing.md#access-policies)。

- 校验器给出的主体的 `Type` 不在 `Accounts` 中时返回 `401`，与无效的令牌相同，例如客户的令牌调用只接受员工的后台，见[多套账号体系](#account-systems)。
- 有需要认证的路由而 `Accounts` 为空时，`RequireToken` panic。
- 传入多个权限时，`sub.Can` 对全部权限都为 `true` 才放行；不传权限时只要求认证通过。
- 令牌优先取 `Authorization: Bearer`，没有时取会话 cookie。用 cookie 认证的写请求必须来自同源页面，否则返回 `403` `http.cross_site`：cookie 由浏览器自动附加。
- 需要认证的响应带 `Cache-Control: no-store`。
- 认证在输入校验之前，未认证的请求只得到 `401`。
- 修改邮箱、删除账号这类敏感操作再加上 [`rest.Confirmed`](login.md#confirming)，要求最近确认过身份。

<a id="require-auth"></a>
### 其他认证方式：`rest.RequireAuth`

凭据不是 Bearer 令牌时（例如请求头 `X-API-Key`），用 `rest.RequireAuth` 声明 OpenAPI 的安全方案和取出调用者的函数，失败的响应与 `RequireToken` 相同：

```go
requireKey := rest.RequireAuth(api, "apiKey",
    &huma.SecurityScheme{Type: "apiKey", In: "header", Name: "X-API-Key"},
    func(ctx huma.Context) (identity.Subject, error) {
        return validate(ctx.Context(), ctx.Header("X-API-Key"))
    },
    "reports:read",
)
```

`validate` 是令牌校验器。没有凭据时返回 `ID` 为空的主体或 `identity.ErrUnauthenticated`。每条路由只能声明一种认证方案。

<a id="failed-authentication"></a>
### 认证失败的响应

| 情况 | 状态码 | 错误码 |
| --- | --- | --- |
| 没有会话 cookie，也没有 `Authorization: Bearer` | 401 | `auth.unauthenticated` |
| 校验器拒绝了令牌，或主体的 `Type` 不在 `Accounts` 中 | 401 | `auth.unauthenticated` |
| 用会话 cookie 认证的写请求来自其他站点 | 403 | `http.cross_site` |
| 凭据绑定的租户与本次请求的租户不同 | 403 | `auth.forbidden` |
| 缺少所需的权限 | 403 | `auth.forbidden` |
| 路由用了 `rest.Confirmed`，凭据太久没有确认身份 | 403 | `auth.confirm_required` |
| 路由用了 `rest.DirectOnly`，凭据是代为登录签发的 | 403 | `auth.impersonation_forbidden` |
| Resolver 返回 `identity.ErrTwoFactorRequired`（要求二次验证） | 403 | `auth.two_factor_required` |
| Resolver 返回 `identity.ErrMethodRequired`（要求特定的登录方式） | 403 | `auth.method_required` |
| 校验器或 Resolver 返回系统故障 | 对应的 `5xx`，例如 503 | 无，`detail` 是 `authentication unavailable` |

- 校验器返回的错误码不出现在响应中，客户端只看到 `auth.unauthenticated`，原因只记在服务端的日志中。
- `401` 响应带 `WWW-Authenticate: Bearer`。
- 响应格式见[错误处理](../general/errors.md#response-format)，`detail` 按请求的语言翻译。

<a id="resolving-subjects"></a>
## 每次请求加载权限：Resolver

凭据只说明是谁，权限来自数据库中的角色并且修改后立即生效时，给 `Accounts` 中的账号体系设置 `identity.Resolver`。它在认证通过之后、权限检查之前执行，收到凭据给出的主体，返回本次请求的主体：

```go
api := rest.New(router, rest.Options{
    Accounts: identity.Accounts{
        user.Type: func(ctx context.Context, sub identity.Subject) (identity.Subject, error) {
            u, err := users.Get(ctx, sub.ID)
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
})
```

`router` 是应用的 `chi.Router`，`users.Get` 是应用按 ID 读取用户的函数。用 `authz` 管理角色时，Resolver 直接用 `roles.Resolve`，见[每次请求加载角色的权限](authorization.md#resolving-permissions)。

| Resolver 返回 | 结果 |
| --- | --- |
| 主体 | 权限检查和处理器都用它 |
| `errorx.Unauthenticated` 类错误，或 `ID` 为空的主体 | `401` |
| 其他不是系统故障的错误 | 按错误分类响应，错误码是你定义的业务错误码，例如不是租户成员时返回 `errorx.NotFound` 类错误，得到 `404` |
| 系统故障 | 对应的 `5xx`，与校验器相同 |

- Resolver 只加载权限和租户：返回的主体中 `Type`、`ID`、`Method`、`Credential`、`Scopes`、`Impersonator`、`Auth` 总是取自凭据。
- `rest.Options`、RPC、[MCP 服务](../ai/mcp.md)和[实时频道](../libraries/realtime.md#tokens)的 `Accounts` 是同一个类型，同一组 Resolver 可以共用。
- SSE 这类长连接每 30 秒重新执行校验器和 Resolver，用户被停用或失去权限后连接结束，见[凭据撤销](../http/sse.md#revoked-credentials)。

<a id="retrieving-the-subject"></a>
## 获取已认证的主体：`identity.FromContext`

处理器用 `identity.FromContext` 取出主体，作为参数传给业务动作的 `Handle`：

```go
subject, _ := identity.FromContext(ctx)
item, err := a.Handle(ctx, subject, in.ID)
```

`ctx` 和 `in` 是处理器的参数，`a` 是业务动作，完整的处理器见[业务动作的 `HTTP` 方法](../http/routing.md#http-actions)。

- 受 `RequireToken` 保护的路由中，`FromContext` 的第二个返回值总是 `true`；公开的路由中它是 `false`，主体是零值。
- `Handle` 开头拒绝 `ID` 为空的主体，路由被误改成公开、或者命令和其他代码直接调用 `Handle` 时，身份检查同样有效，见[资源级授权](authorization.md#ownership)。
- 本次请求的租户用 `identity.TenantFromContext(ctx)` 读取，公开的路由中也可以读取。

<a id="account-systems"></a>
## 多套账号体系

一个产品可以有几套互不相干的账号，例如平台运营、商家员工、终端客户，各自一张表，主键和登录方式各不相同。每套账号在拥有那张表的模块中声明一个 `SubjectType`，例如 `internal/staff/staff.go` 中的 `const Type identity.SubjectType = "staff"`。每个 API 在 `Accounts` 中只列出它服务的账号体系：

```go
router.Route("/ops/api", func(r chi.Router) {
    ops := rest.New(r, rest.Options{BasePath: "/ops/api", Accounts: identity.Accounts{staff.Type: roles.Resolve}})
    // 注册运营后台的接口
})
router.Route("/api", func(r chi.Router) {
    shop := rest.New(r, rest.Options{BasePath: "/api", Accounts: identity.Accounts{customer.Type: customers.Resolve}, Tenant: fromHost})
    // 注册 App 的接口
})
```

`router` 是应用的 `chi.Router`，`roles` 是 [`authz.Store`](authorization.md#roles)，`customers.Resolve` 是应用检查客户账号是否停用的 Resolver，`fromHost` 从子域名取出租户。

- 几个 API 共用一个令牌校验器。客户的令牌调用运营后台得到 `401`，与无效的令牌相同。
- 一个 API 可以接受多套账号，例如 `identity.Accounts{member.Type: roles.Resolve, customer.Type: customers.Resolve}`。
- `authz` 按主体的类型和 ID 保存角色，一个 `Store` 可以服务多套账号。终端客户通常不需要角色，按“数据归属本人”授权。
- 每套账号用自己的 [`login.Realm`](login.md) 管理登录，用自己配置的 `apitoken.Store` 签发令牌，见[装配令牌](api-tokens.md#assembling)。
- 主键是 `(tenant_id, id)` 这类组合时，把租户编进 `ID`，例如 `t42:1001`。
- 主体要放进一个字符串时（缓存键、广播主题），用 `sub.Ref().String()`，结果形如 `customer:42`。

<a id="tenants"></a>
## 多租户：`rest.Options.Tenant`

本次请求所在的租户由请求决定，例如路径中的 `/t/{tenant}`、子域名或请求头。`rest.Options.Tenant` 从请求中取出它：

```go
api := rest.New(router, rest.Options{
    Tenant:   func(ctx huma.Context) string { return ctx.Param("tenant") },
    Accounts: identity.Accounts{user.Type: resolve},
})
```

`router` 是应用的 `chi.Router`，`resolve` 是这套账号的 Resolver。

- 租户在每个操作开始时写入 context，公开的操作也一样，用 `identity.TenantFromContext(ctx)` 读取；没有设置 `Tenant` 或取到空字符串时是 `""`，即平台本身。
- Resolver 读取租户，加载主体在其中的权限，写入 `sub.Tenant` 和 `sub.Permissions`；不是成员时返回 `errorx.NotFound` 类错误，得到 `404`。用 `authz` 时直接用 [`roles.Resolve`](authorization.md#resolving-permissions)。
- 校验器返回的主体 `Tenant` 不为空时，凭据只能在这个租户中使用，与本次请求的租户不同时在 Resolver 之前返回 `403`，例如只属于某个租户的个人访问令牌。
- 租户的识别、成员管理、数据隔离和运营后台见[多租户](tenancy.md)。

<a id="impersonation"></a>
## 代为登录：`Impersonator`

客服或平台管理员以目标用户身份排查问题时，使用 `apitoken.Store.Impersonate` 签发独立的代为登录令牌（`KindImpersonation`）。主体是目标用户，权限也是他的；实际操作的人记在 `Impersonator` 中，之后每个请求都带着它。运营人员和用户可以属于不同的账号体系。

运营后台的“以此用户登录”是一个要求 `users:impersonate` 的接口：

```go
staff, _ := identity.FromContext(ctx)
plain, err := tokens.Impersonate(ctx, staff, customerRef, apitoken.Impersonation{
    Tenant: in.TenantID,
    Scopes: []string{"*:read"}, // 可选：限制为只读排查
    Reason: in.Reason,
    Device: device,
})
```

`tokens` 是目标账号的 `apitoken.Store`，`customerRef` 是目标主体的 `identity.Ref`，`device` 是 `apitoken.Device`（IP 和 User-Agent）。

- `Impersonate` 强制单层代登（操作者本身处于代为登录状态时返回 `identity.ErrImpersonationForbidden`），默认有效期 1 小时（最长 4 小时），`Auth` 为零值。
- 跨域或子域名场景用 `CreateImpersonationTicket` 签发 30 秒一次性交换票据，在目标域用 `ExchangeImpersonationTicket` 消费换取令牌写入目标域 cookie，见[运营代为登录租户成员](tenancy.md#impersonation)。
- 认证中间件自动将 `impersonator` 写入 `logging` 的 context 字段和链路属性；[操作日志](../features/activity.md)自动记下 `Impersonator`。
- 用户端前端用 `@go-tenon/ui` 的 `ImpersonationBanner` 显示当前代登状态和退出按钮；退出时[撤销](api-tokens.md#revoking-tokens)该令牌并清除代登 cookie。

<a id="impersonation-rules"></a>
### 代为登录的限制

入口默认拒绝所有代为登录凭据（fail-closed）。接受代为登录的入口在 `rest.Options.Impersonation`（以及 `realtime.Options`、`mcpx.HandlerOptions`、`natsrpc.Options`）中显式声明允许的操作者体系和权限：

```go
api := rest.New(router, rest.Options{
    Accounts: identity.Accounts{member.Type: member.Resolve(db, roles)},
    Impersonation: &identity.Impersonation{
        Actors:      identity.Accounts{staff.Type: staff.Resolve(db, roles)},
        Permissions: []string{"users:impersonate"},
    },
})
```

`db` 是 `*bun.DB`，`roles` 是 [`authz.Store`](authorization.md#roles)。

- **操作者实时校验**：每个请求都在平台域（`""`）执行操作者的 Resolver 并检查 `Permissions`；操作者被停用或失去权限后，下一个请求返回 `401`。停用操作者时调用 `tokens.RevokeByImpersonator` 和 `creds.RecheckSubject` 立即回收会话并断开实时连接。
- **单租户边界与账号自省**：签发时设置 `Tenant` 会写入 `Subject.BoundTenant`，凭据只能访问该租户和标记了 `rest.Options.AccountScope` 或 `rest.AccountScoped()` 的账号自省接口（如 `/api/me`、`/api/auth/logout`）。
- **禁止代为执行的敏感操作**：改密码、改个人资料、管理令牌、付款等路由加上 `rest.DirectOnly()`，代为登录请求直接返回 `403 auth.impersonation_forbidden`；[`rest.Confirmed`](login.md#confirming) 保护的接口同样拒绝代为登录。
- [OAuth 授权](oauth-server.md)的同意接口拒绝代为登录的主体；代为登录期间创建的个人访问令牌继承 `Impersonator`。
