# 认证

想先看能跑通的后台登录，读[单账号后台](admin-example.md)；运营 + 租户 + 代登录读[运营后台与多租户](saas-example.md)。本页拆开讲主体、校验器和路由保护。

简单说：很多 API 需要知道「是谁在调用」。

Tenon 用 Bearer Token 做这件事。客户端在请求头里带上 `Authorization: Bearer <token>`，框架拿你写的校验函数认出这个人，再把身份交给后面的处理器。

阅读前需要了解：[路由](../http/routing.md)；[context](../concepts/go-primer.md#context)；[入门教程第 6 章](../tutorial/06-login.md#how-it-works)解释了 token。多套账号、多租户和代为登录见[认证进阶](authentication-advanced.md)。

整条链路可以想成：

1. 客户端出示一把「钥匙」（token）
2. 你告诉框架怎么验这把钥匙（校验器）
3. 验过之后，框架知道「来的是谁」，以及「他能干什么」

JSON API 只用 Bearer token。需要登录的一端——后台、网页、桌面客户端、移动端——都走 JSON API；网页和后台是[单页应用](../responses/spa.md)。服务端渲染的[页面](../responses/views.md)只放公开内容。

Token 本身可以是什么都行：数据库里的 API Key，或者认证服务签发的 JWT。框架不限定种类，你只要写一个函数，把「这串 token → 对应哪个人」说清楚就行。

> [!NOTE]
> 认证回答的是「调用者是谁」；「调用者能不能做这件事」属于授权，见[授权](authorization.md)。

- [示例用法](#example-usage)
    - [核心概念](#key-concepts)
- [编写 Token 校验器](#token-validators)
- [保护路由](#protecting-routes)
    - [认证失败的响应](#failed-authentication)
- [每次请求加载权限](#resolving-subjects)
- [获取已认证的主体](#retrieving-the-subject)
- [管理凭据](#managing-credentials)
- [常见问题](#troubleshooting)
- [API 参考](#api-reference)

<a id="example-usage"></a>
## 示例用法

先看一眼完整流程：建令牌存储 → 声明 API 接受哪些账号 → 用 `RequireBearer` 护住一条路由：

```go
tokens := apitoken.NewStore(db, cfg.APIToken)

api := rest.New(router, rest.Options{
    Accounts: identity.Accounts{user.Type: nil}, // 接受哪些账号体系
})

op := huma.Operation{OperationID: "article-create", Method: http.MethodPost, Path: "/articles"}
rest.RequireBearer(api, tokens.Validate, "articles:write")(&op)
rest.Register(api, op, NewCreate(db).HTTP)

// 处理器中取出主体
sub, _ := identity.FromContext(ctx)
```

没带有效令牌的请求，进不了处理器，直接 `401`；令牌有效但没有 `articles:write`，得到 `403`。通过认证后，处理器用 `identity.FromContext` 拿到调用者，再交给业务逻辑。

下面先把几个名字说清楚，再讲怎么写校验器、怎么护路由、怎么处理多套账号。

<a id="key-concepts"></a>
### 核心概念

**主体（Subject）** 就是「已经验过身份的调用者」。可以把它理解成一张临时通行证，上面写着：你是谁、属于哪套账号、能做什么。它由 `identity` 包定义：

```go
type Subject struct {
    Type         SubjectType
    ID           string
    Impersonator Ref
    Method       string
    Credential   string
    Tenant       string
    Permissions  []string
    Scopes       []string
    Auth         Auth
}
```

| 字段 | 说明 |
| --- | --- |
| `Type` | 属于哪套账号，例如 `user`、`staff`、`customer`，由应用自己声明；队列任务这类系统操作用 `identity.System` |
| `ID` | 在这套账号里的标识，不透明字符串，常见是用户表主键 |
| `Impersonator` | 代为登录时「真正动手的人」，例如客服；零值表示本人操作，见[代为登录](authentication-advanced.md#impersonation) |
| `Method` | 怎么认出来的，例如 `bearer` |
| `Credential` | 这次用的凭据 ID，例如 [API 令牌](api-tokens.md)的 ID；方便在设备列表里标出「当前设备」、退出其他设备，也可以为空 |
| `Tenant` | 这次请求落在哪个租户；`""` 表示平台本身。凭据绑了租户时，只能在该租户里用，见[多租户](authentication-advanced.md#tenants) |
| `Permissions` | 这个人有哪些权限，例如 `articles:write` |
| `Scopes` | 这把钥匙被限制在哪些权限里，例如只读令牌的 `articles:read`；`nil` 表示不额外限制 |
| `Auth` | 这次登录怎么验的身份：`Methods` 是通过的方式，如 `[password totp]`，`Time` 是最近一次验证时间（登录或[确认身份](login-security.md#confirming)）；`Auth.TwoFactor()` 表示是否经过二次验证。个人访问令牌、代为登录等凭据为零值 |

`Type` + `ID` 合起来是 `identity.Ref`，用 `sub.Ref()` 取得。框架里凡是要记下「是谁」的地方——令牌、角色、操作日志、通知——都会同时存这两个字段。所以 `ID` 只要在同一种 `Type` 里唯一就行：员工表和客户表都可以用自增 ID，不会打架。

只有一套账号时，在用户模块（例如 `internal/user/user.go`）声明类型即可：

```go
const Type identity.SubjectType = "user"
```

`Type` 以小写字母开头，只含小写字母、数字和下划线。

权限和范围可以这么记：

- `Permissions`：这个人本来能做什么
- `Scopes`：这把钥匙这次被允许做什么

`sub.Can(perm)` 要两边都放行才返回 `true`。路由检查、业务服务、页面都用它判断。权限名用冒号分段；授予时最后一段可以写 `*`：`articles:*` 包含 `articles:read` 和 `articles:write`，单独的 `*` 表示全部权限。

**校验器（TokenValidator）** 是你提供的函数：输入一串 token，输出一个主体（或错误）：

```go
type TokenValidator func(ctx context.Context, token string) (identity.Subject, error)
```

<a id="token-validators"></a>
## 编写 Token 校验器

为了好读，下面的例子把可用 token 直接写在配置里：每个 token 对应一个人和他的权限。

这里会出现两个名字很像的函数，但它们干的是两件不同的事，发生在不同阶段：

| 函数 | 什么时候跑 | 干什么 |
| --- | --- | --- |
| `Config.Validate` | **启动时**，加载配置之后 | 检查配置本身写得对不对（有没有空 token、有没有重复） |
| `Validator` 返回的那个函数 | **每次请求**，`RequireBearer` 调校验器时 | 拿请求里的 token 去查「这是谁」 |

二者共用同一份 `Config` 数据，但互不调用：`Validate` 不参与验请求；请求时的校验器也不再跑一遍配置检查——它假定启动时已经查过了。

```go
// Credential 是一个 API token 及其代表的调用者。
type Credential struct {
    Token       string   `yaml:"token"`
    SubjectID   string   `yaml:"subject_id"`
    Permissions []string `yaml:"permissions"`
}

type Config struct {
    Credentials []Credential `yaml:"credentials"`
}

// Validate 是配置约定：把 Config 挂到根配置后，Load 会自动调用它。
// 这里只检查「配置合不合法」，不碰任何 HTTP 请求。
func (c Config) Validate() error {
    seen := make(map[string]bool, len(c.Credentials))
    for _, cred := range c.Credentials {
        if cred.Token == "" || cred.SubjectID == "" {
            return errors.New("credentials: token and subject_id are required")
        }
        if seen[cred.Token] {
            return errors.New("credentials: duplicate token")
        }
        seen[cred.Token] = true
    }
    return nil
}

// Validator 用配置造出一个「请求时用的」TokenValidator。
// 启动时调用一次；返回的闭包会在之后的每个受保护请求里被调用。
func Validator(cfg Config) rest.TokenValidator {
    byToken := make(map[string]Credential, len(cfg.Credentials))
    for _, cred := range cfg.Credentials {
        byToken[cred.Token] = cred
    }
    return func(_ context.Context, token string) (identity.Subject, error) {
        cred, ok := byToken[token]
        if !ok {
            return identity.Subject{}, identity.ErrUnauthenticated.New()
        }
        return identity.Subject{
            Type:        user.Type,
            ID:          cred.SubjectID,
            Method:      "bearer",
            Permissions: cred.Permissions,
        }, nil
    }
}
```

串起来看，生命周期大概是：

1. 配置文件写出可用凭据（见下）
2. 应用启动：`config.Load` 解出 `Config`，自动调用 `Config.Validate`；配错了直接起不来
3. 装配路由：调用 `Validator(cfg)` 一次，得到校验器，交给 `RequireBearer`
4. 请求到来：`RequireBearer` 取出 Bearer token，调用那个校验器，得到主体或 `401`

配置可以这样写，并把这个 `Config` 挂到应用的[根配置结构体](../general/configuration.md#loading-configuration)上（字段名 `Auth`，配置段 `auth`）。任何实现了 `Validate() error` 的配置类型，`Load` 都会在解码后调用它：

```yaml
auth:
  credentials:
    - token: ${API_TOKEN_ALICE}
      subject_id: alice
      permissions: [articles]
```

真实项目里，校验器多半会查数据库里的 API Key，或用现成库验 JWT 的签名和有效期——那时往往没有这份写死在配置里的凭据表，也就不一定需要上面的 `Config.Validate`；但请求时的 `TokenValidator` 仍然要有。不管哪种做法，**把该查的都查完再返回主体**：签名、过期、是否已吊销、权限从哪来，都要在这一步搞定。

校验器的返回值直接决定请求结果：

| 校验器返回 | 结果 |
| --- | --- |
| 有效的主体（`ID` 非空） | 认证通过 |
| `errorx.Unauthenticated` 等 4xx 类错误，或 `ID` 为空的主体 | `401` |
| `errorx.Unavailable` 等 5xx 类错误 | 对应的 `5xx`，例如认证服务暂时不可用时返回 `503` |

<a id="protecting-routes"></a>
## 保护路由

需要登录才能访问的 API，先在 `rest.Options.Accounts` 里声明「接受哪些账号」。例如只有一套用户账号：

```go
api := rest.New(router, rest.Options{Accounts: identity.Accounts{user.Type: nil}})
```

键是账号体系，值是这套账号的 `identity.Resolver`。`nil` 表示：校验器给什么主体，就直接用什么主体，见[每次请求加载权限](#resolving-subjects)。

如果校验器认出的人，`Type` 不在 `Accounts` 里——比如客户 token 打到了只接受员工的后台——效果和无效 token 一样，返回 `401`。产品有多套账号时，每个 API 只收它该服务的那几种，见[多套账号体系](authentication-advanced.md#account-systems)。没设 `Accounts` 就去注册需要认证的路由，会 panic。

用 `rest.RequireBearer` 给路由加上认证。参数是：API 实例、校验器、以及调用者必须具备的权限。

下面是「获取文章」接口的片段：`api` 由 `rest.New` 创建，`validate` 是令牌校验器（例如 [`tokens.Validate`](api-tokens.md)），`db` 是入口打开的数据库。`Get` 业务动作的定义见[路由](../http/routing.md#http-actions)：

```go
requireArticles := rest.RequireBearer(api, validate, "articles")

op := huma.Operation{
    OperationID: "article-get",
    Method:      http.MethodGet,
    Path:        "/articles/{id}",
}
requireArticles(&op)
rest.Register(api, op, NewGet(db).HTTP)
```

`RequireBearer` 返回的函数可以复用到多条路由。可以传多个权限：`sub.Can` 必须对**全部**权限都为 `true` 才能进；不传权限则只要求「认得出人」。

它同时做两件事：

- 运行时检查 `Authorization` 头，并调用校验器
- 在 OpenAPI 文档里把这条路由标成需要 Bearer 认证，所需权限写在 `x-required-permissions` 扩展字段里

需要认证的响应还会自动带上 `Cache-Control: no-store`，避免被中间代理缓存。

`RequireBearer` 建立在更通用的 `rest.RequireAuth` 之上。如果认证方式不是 Bearer（比如从自定义请求头读 API Key），可以用 `RequireAuth` 自己声明 OpenAPI 安全方案和认证函数；失败时的响应规则一样。

> [!TIP]
> 公开路由也要显式写 `rest.Public()(&op)`。忘了声明访问策略的路由，注册时会 panic——每个接口的「能不能匿名访问」都必须是你亲手写下的。

<a id="failed-authentication"></a>
### 认证失败的响应

| 情况 | 状态码 | 错误码 |
| --- | --- | --- |
| 没有 `Authorization` 头，或格式不是 `Bearer <token>` | 401 | `auth.unauthenticated` |
| 校验器拒绝了 token | 401 | `auth.unauthenticated` |
| token 属于 API 不接受的账号体系 | 401 | `auth.unauthenticated` |
| 缺少所需权限 | 403 | `auth.forbidden` |
| 路由要求最近确认过身份（[`rest.Confirmed`](login-security.md#confirming)），而凭据太久没确认 | 403 | `auth.confirm_required` |
| Resolver 要求这次登录经过二次验证，返回 `identity.ErrTwoFactorRequired` | 403 | `auth.two_factor_required` |
| 校验器返回 5xx 类错误 | 与错误分类对应，例如 503 | 无 |

有一点值得注意：无论校验器内部返回什么错误码，客户端看到的 401 都是统一的 `auth.unauthenticated`——具体原因只记在服务端，避免把「为什么不行」泄露给外面。`401` 响应会带 `WWW-Authenticate: Bearer`。校验器返回 5xx 时，响应的 `detail` 是 `authentication unavailable`，真实错误码写在框架诊断日志里。

例如，不带 token 去创建文章：

```bash
curl -i -X POST http://localhost:8080/articles \
  -H 'Content-Type: application/json' \
  -d '{"title":"Hello World","slug":"hello-world","content":"……"}'
```

```http
HTTP/1.1 401 Unauthorized
Content-Type: application/problem+json
Cache-Control: no-store
Www-Authenticate: Bearer

{"title":"Unauthorized","status":401,"detail":"Unauthorized","code":"auth.unauthenticated"}
```

启用[本地化](../general/localization.md)后，`detail` 会被翻译，例如中文为「请先认证」。

认证发生在输入验证之前，所以未认证的请求只会拿到 `401`，不会先看到一堆字段校验错误。

<a id="resolving-subjects"></a>
## 每次请求加载权限

前面例子里，校验器直接把权限写死了。实际更常见的是：

- 凭据只说明「这是谁」
- 权限来自数据库里的角色，而且改了要马上生效

这时在 `Accounts` 里给这套账号挂一个 Resolver：认证通过之后、权限检查之前跑一遍。它收到凭据给出的主体，返回「这次请求真正要用的主体」：

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

| Resolver 返回 | 结果 |
| --- | --- |
| 主体 | 权限检查和处理器都用它 |
| `errorx.Unauthenticated` 类错误，或 `ID` 为空的主体 | `401`，和凭据无效一样 |
| 其他 4xx 类错误 | 按错误分类响应，错误码就是你定义的业务错误码；例如不是租户成员时返回 `errorx.NotFound`，得到 `404` |
| 5xx 类错误 | 与校验器的 5xx 相同 |

Resolver 只负责补权限和租户。返回主体里的 `Type`、`ID`、`Method`、`Credential`、`Scopes`、`Impersonator`、`Auth` 始终来自凭据——你可以改收到的主体再返回，也可以新建一个主体返回，结果一样：身份还是凭据里的那个人。

[MCP 服务](../ai/mcp.md)和[实时频道](../libraries/realtime.md#tokens)的 `Accounts` 也是同一个类型 `identity.Accounts`，所以同一个 Resolver 可以共用；三者对返回值的处理规则一致。[API 令牌](api-tokens.md)只标识用户，通常总会搭配 `Resolve` 一起用。

<a id="retrieving-the-subject"></a>
## 获取已认证的主体

认证通过后，主体会放进请求的 context。在处理器里用 `identity.FromContext` 取出，再传给业务动作的 `Handle`。

例如把[获取文章](../http/routing.md#http-actions)的 `HTTP` 改成下面这样，显式拒绝没有主体的请求：

```go
func (a *Get) HTTP(ctx context.Context, in *GetInput) (*ArticleOutput, error) {
    subject, ok := identity.FromContext(ctx)
    if !ok {
        return nil, identity.ErrUnauthenticated.New()
    }
    item, err := a.Handle(ctx, subject, in.ID)
    if err != nil {
        return nil, err
    }
    return &ArticleOutput{Body: toDTO(item)}, nil
}
```

对受 `RequireBearer` 保护的路由，`ok` 一定是 `true`。保留这个检查，是为了防止路由被误改成公开后，处理器在没身份的情况下继续跑。

另一种做法：让 `Handle` 拒绝空主体，见[授权](authorization.md#ownership)。这样即使路由被误改成公开，也会在 `Handle` 里被拦住；`HTTP` 就可以写成 `subject, _ := identity.FromContext(ctx)`。[路由](../http/routing.md#organizing-routes)一章里的业务动作就是这么写的。

<a id="managing-credentials"></a>
## 管理凭据

把 token 写在配置文件里，适合内部服务或少数固定调用方。面向用户的 API 用 [API 令牌](api-tokens.md)：单页应用和 App 登录后拿到滑动过期的 token，或一对 [JWT access token 和 refresh token](jwt.md#jwt)；用户也可以给脚本建个人访问令牌。`Store.Validate` 或 `JWT.Validate` 可以直接当校验器。

用外部认证服务签发的 JWT 时，在校验器里用成熟库验证签名、签发者和有效期，再转成 `identity.Subject`。凭据换了，路由和业务代码不用动。

用 GitHub、Google 等账号登录见[第三方登录](social-login.md)；用户注册、找回密码等由应用自己实现。

<a id="troubleshooting"></a>
## 常见问题

**带了 token 仍然得到 401**

依次检查：请求头是否是 `Authorization: Bearer <token>` 的格式；校验器是否返回了 `ID` 为空的主体或 4xx 类错误；主体的 `Type` 是否在这个 API 的 `Accounts` 中，见[认证失败的响应](#failed-authentication)。

**认证服务暂时不可用时，接口返回 503**

校验器返回了 5xx 类错误（例如数据库不可用），框架按错误的分类返回对应的状态码，而不是把用户当作未登录。这是预期的行为。

**注册路由时 panic**

需要认证的路由没有设置 `Accounts`，或者路由没有声明访问策略。公开的路由也要写 `rest.Public()(&op)`，见[保护路由](#protecting-routes)。

**令牌有效，但得到 403**

用户缺少路由要求的权限。权限在每个请求中由 Resolver 加载，见[每次请求加载权限](#resolving-subjects)。

<a id="api-reference"></a>
## API 参考

### identity.Subject

```go
type Subject struct {
    Type         SubjectType
    ID           string
    Impersonator Ref
    Method       string
    Credential   string
    Tenant       string
    Permissions  []string
    Scopes       []string
    Auth         Auth
}

func FromContext(ctx context.Context) (Subject, bool)
func (s Subject) Can(perm string) bool
func (s Subject) Ref() Ref
```

包 `github.com/go-tenon/tenon/identity`。字段见[核心概念](#key-concepts)；`Can` 会同时检查 `Permissions` 和 `Scopes`，支持通配符。

### identity.Accounts、Resolver

```go
type Accounts map[SubjectType]Resolver
type Resolver func(ctx context.Context, sub Subject) (Subject, error)
```

API 接受的账号体系，以及认证通过后加载主体的函数，见[每次请求加载权限](#resolving-subjects)。

### rest.RequireBearer、RequireAuth

```go
func RequireBearer(api *API, validate TokenValidator, permissions ...string) func(op *huma.Operation)
func RequireAuth(api *API, name string, scheme *huma.SecurityScheme, authenticate Authenticator, permissions ...string) func(op *huma.Operation)
func Confirmed(maxAge time.Duration) func(op *huma.Operation)
```

包 `github.com/go-tenon/tenon/httpx/rest`。`RequireBearer` 要求 `Authorization: Bearer` 令牌；`RequireAuth` 用于其他认证方式；`Confirmed` 要求最近 `maxAge` 内确认过身份，见[确认身份](login-security.md#confirming)。

### 错误

| 错误 | 状态码 | 含义 |
| --- | --- | --- |
| `identity.ErrUnauthenticated` | 401 | 没有凭据或凭据无效 |
| `identity.ErrForbidden` | 403 | 缺少所需权限 |
| `identity.ErrExpired` | 401 | 令牌过期，客户端刷新后重试 |

完整情况见[认证失败的响应](#failed-authentication)。
