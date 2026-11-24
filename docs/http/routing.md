# 路由

用 `rest.Register` 注册 JSON 接口：每个接口是一个 `huma.Operation`、一个输入类型和一个输出类型。框架据此解析和校验请求、编码响应，并生成 OpenAPI 文档。

包：`httpx/rest`。

- 每个接口都显式声明访问策略：`rest.Public()` 或 `rest.RequireToken(...)`，没有声明时注册时 panic。
- 业务接口的处理器是[业务动作](../concepts/actions.md)的 `HTTP` 方法，每个模块在 `routes.go` 的 `RegisterRoutes` 中注册自己的接口。

<a id="basic-routing"></a>
## 示例

只有一个公开接口的完整程序：

```go
package main

import (
    "context"
    "log"
    "net/http"

    "github.com/danielgtaylor/huma/v2"
    "github.com/go-chi/chi/v5"

    "github.com/go-tenon/tenon/httpx/rest"
)

type PingOutput struct {
    Body struct {
        Message string `json:"message"`
    }
}

func ping(ctx context.Context, _ *struct{}) (*PingOutput, error) {
    out := &PingOutput{}
    out.Body.Message = "pong"
    return out, nil
}

func main() {
    router := chi.NewRouter()
    api := rest.New(router, rest.Options{Title: "Hello API", Version: "1.0.0"})

    op := huma.Operation{
        OperationID: "ping",
        Method:      http.MethodGet,
        Path:        "/ping",
        Summary:     "检查 API 是否可响应",
    }
    rest.Public()(&op)
    rest.Register(api, op, ping)

    log.Fatal(http.ListenAndServe(":8080", router))
}
```

`curl localhost:8080/ping` 返回 `{"message":"pong"}`，`/docs` 是交互式的接口文档。应用中用 `httpx.Serve` 启动服务，并安装[默认中间件](middleware.md#default-middleware)，见[依赖装配](../concepts/dependencies.md#assembling-the-application)。

<a id="creating-api"></a>
## 创建 API：`rest.New`

`rest.New` 在一个 chi 路由上创建 API。应用把接口挂在 `/api` 下，`BasePath` 与挂载的前缀相同：

```go
router.Route("/api", func(r chi.Router) {
    r.Use(httpx.Timeout(cfg.HTTP.Timeout))
    api := rest.New(r, rest.Options{
        Title: "Blog", Version: "0.1.0", BasePath: "/api",
        Accounts: identity.Accounts{user.Type: nil},
    })
    article.RegisterRoutes(api, rest.RequireToken(api, tokens.Validate, "articles"), db)
})
```

`router` 是应用的 `chi.Router`，`cfg` 是应用的配置，`db` 是入口打开的 `*bun.DB`，`tokens.Validate` 是令牌校验器。

| `rest.Options` 字段 | 作用 |
| --- | --- |
| `Title`、`Version` | 写入 OpenAPI 文档 |
| `BasePath` | 挂载的路径前缀，写入 OpenAPI 的 `servers`；一个应用有多个 API 时，`openapi`、`client` 命令用 `--base-path` 按它选择 |
| `Accounts` | 接受的账号体系；有需要认证的接口时必须设置，见[认证](../security/authentication.md#protecting-routes) |
| `Tenant` | 从请求取出租户，见[多租户](../security/tenancy.md) |
| `Permissions` | 应用的权限目录，写入 OpenAPI 的 `x-tenon-permissions` |
| `Credentials` | 让 SSE 等长连接在凭据撤销时立即结束，见[服务端推送](sse.md#immediate-revocation) |
| `SessionCookie` | 会话 cookie 的名称，零值是默认名称 |

`rest.New` 不安装中间件：中间件在调用它之前用 `r.Use` 安装，见[中间件](middleware.md)。没有匹配的路径和方法返回 `404`、`405` 的错误响应。

<a id="operations"></a>
## 注册接口：`huma.Operation` 和 `rest.Register`

```go
op := huma.Operation{
    OperationID:   "article-create",
    Method:        http.MethodPost,
    Path:          "/articles",
    Summary:       "创建文章",
    DefaultStatus: http.StatusCreated,
}
requireArticles(&op)
rest.Register(api, op, NewCreate(db).HTTP)
```

`api` 是 `rest.New` 创建的 API，`requireArticles` 是 `rest.RequireToken` 返回的访问策略，`db` 是 `*bun.DB`。

| 声明 | 作用 |
| --- | --- |
| `OperationID` | 接口的唯一名字，kebab-case，写成 `资源-动作`；生成的客户端用它命名函数，`article-create` 生成 `api.article.create` |
| `Method`、`Path` | HTTP 方法和路径，路径参数写成 `{id}` |
| `Summary` | 一句话说明，显示在接口文档中 |
| `DefaultStatus` | 成功时的状态码，默认 `200`，见[状态码](responses.md#status-codes) |
| 输入类型 | 参数、请求体和校验规则，见[请求与校验](requests.md) |
| 输出类型 | 响应体、响应头和状态码，见[响应](responses.md) |

<a id="handlers"></a>
### 处理器

处理器的签名是 `func(ctx context.Context, in *Input) (*Output, error)`，没有输入时 `Input` 写 `struct{}`。处理器返回的错误由框架转换为错误响应，见[错误处理](../general/errors.md#returning-errors)。

处理器只在请求到来时调用。`openapi`、`routes`、`client` 命令用 `nil` 依赖构造全部路由，所以 `RegisterRoutes` 和动作的构造函数只保存依赖，构造时使用依赖会 panic。

<a id="registration-checks"></a>
### 注册时的检查

`rest.Register` 在注册时发现下面的问题就 panic，应用启动和导出文档时立即暴露：

| panic 信息 | 原因 |
| --- | --- |
| `endpoint must explicitly declare Public, RequireToken or another RequireAuth scheme` | 没有声明访问策略 |
| `operation ID must be kebab-case` | `OperationID` 为空，或者不是小写字母、数字和连字符，例如 `articleCreate` |
| `duplicate operation ID or conflicting route` | `OperationID` 重复，或者方法相同且路径形状相同，例如 `/articles/{id}` 与 `/articles/{articleID}` |
| `Options.Accounts is required for authenticated operations` | 用了 `RequireToken`，`rest.Options` 没有设置 `Accounts` |

<a id="route-parameters"></a>
## 路径参数

路径中用 `{id}` 声明参数，输入类型用同名的 `path` 标签读取：

```go
type GetInput struct {
    ID uuid.UUID `path:"id"`
}
```

值转换为字段的类型，转换失败返回 `422`。标签与 `{}` 中的名字区分大小写，不一致时返回 `422` `required path parameter is missing`。其他参数的写法见[请求与校验](requests.md#input-types)。

<a id="access-policies"></a>
## 访问策略：`rest.Public` 和 `rest.RequireToken`

公开的接口在注册之前调用 `rest.Public()`：

```go
rest.Public()(&op)
```

需要登录的接口用 `api.RequireToken`（在 `rest.Options.Validate` 设置了默认令牌校验器时）或 `rest.RequireToken`，列出调用者必须具备的全部权限：

```go
requireArticles := api.RequireToken("articles") // 或 rest.RequireToken(api, tokens.Validate, "articles")
requireArticles(&op)
```

`op` 是要注册的 `huma.Operation`，`api` 是 `rest.New` 创建的 API，`tokens.Validate` 是令牌校验器，见[API 令牌](../security/api-tokens.md)。多租户应用通过 `rest.Options.CheckTenant` 在认证后校验租户状态。

- 令牌来自 `Authorization: Bearer` 或浏览器的会话 cookie。没有令牌或令牌无效返回 `401`，缺少权限返回 `403`；用 cookie 认证的写请求必须来自同源页面，否则返回 `403`。
- 它同时在 OpenAPI 中把接口标记为需要认证，所需权限写在 `x-required-permissions` 中。
- 返回的函数可以用于多条路由。入口创建它并传给模块的 `RegisterRoutes`，模块决定哪些接口用它。
- `rest.Options.Accounts` 必须设置，否则 panic。

认证失败的响应、`RequireAuth` 和读取调用者见[认证](../security/authentication.md#protecting-routes)，权限见[授权](../security/authorization.md)。

<a id="organizing-routes"></a>
## 按模块组织路由：`routes.go`

业务代码按[业务动作](../concepts/actions.md)组织，每个动作一个文件，模块的全部接口集中在 `routes.go` 中：

```text
internal/article/
├── article.go      模型、业务错误与共享的规则
├── create.go       Create：创建文章
├── get.go          Get：获取文章
├── publish.go      Publish：发布文章
├── dto.go          共享的响应结构与转换
└── routes.go       RegisterRoutes：注册文章模块的接口
```

<a id="module-registration"></a>
### 在 `RegisterRoutes` 中注册模块的接口

`RegisterRoutes` 接收 API、访问策略和动作的依赖，构造动作并注册它们的 `HTTP` 方法：

```go
// RegisterRoutes 注册文章模块的全部接口。
func RegisterRoutes(api *rest.API, requireArticles func(*huma.Operation), db *bun.DB) {
    create := huma.Operation{
        OperationID:   "article-create",
        Method:        http.MethodPost,
        Path:          "/articles",
        Summary:       "创建文章",
        DefaultStatus: http.StatusCreated,
    }
    requireArticles(&create)
    rest.Register(api, create, NewCreate(db).HTTP)

    get := huma.Operation{
        OperationID: "article-get",
        Method:      http.MethodGet,
        Path:        "/articles/{id}",
        Summary:     "获取文章",
    }
    requireArticles(&get)
    rest.Register(api, get, NewGet(db).HTTP)
}
```

入口在[创建 API](#creating-api) 之后调用它：

```go
article.RegisterRoutes(api, rest.RequireToken(api, tokens.Validate, "articles"), db)
```

生成文档和查看路由时 `db` 是 `nil`，所以 `RegisterRoutes` 和构造函数只保存依赖。

<a id="http-actions"></a>
### 业务动作的 `HTTP` 方法

`get.go` 中的“获取文章”。`find`、`ErrNotFound`、`ErrForbidden` 是模块内共享的代码，见[业务动作](../concepts/actions.md#shared-code)；`ArticleOutput` 和 `toDTO` 见[共享响应结构](#shared-responses)：

```go
// Get 获取文章。
type Get struct {
    db *bun.DB
}

func NewGet(db *bun.DB) *Get {
    return &Get{db: db}
}

// Handle 返回一篇文章。草稿只有作者本人能看到。
func (a *Get) Handle(ctx context.Context, subject identity.Subject, id uuid.UUID) (*Article, error) {
    if subject.ID == "" {
        return nil, ErrForbidden.New()
    }
    item, err := find(ctx, a.db, id)
    if err != nil {
        return nil, err
    }
    if item.Status == StatusDraft && item.AuthorID != subject.ID {
        return nil, ErrNotFound.New() // 别人的草稿，当作不存在
    }
    return item, nil
}

type GetInput struct {
    ID uuid.UUID `path:"id"`
}

// HTTP 处理 GET /articles/{id}。
func (a *Get) HTTP(ctx context.Context, in *GetInput) (*ArticleOutput, error) {
    subject, _ := identity.FromContext(ctx)
    item, err := a.Handle(ctx, subject, in.ID)
    if err != nil {
        return nil, err
    }
    return &ArticleOutput{Body: toDTO(item)}, nil
}
```

- `HTTP` 只取出参数和调用者、调用 `Handle`、把结果转换为输出。业务规则都在 `Handle` 中。
- 每个动作用自己的输入类型，例如 `CreateInput` 写成 `struct{ Body Changes }`，`Changes` 见[请求与校验](requests.md#json-body)。

<a id="shared-responses"></a>
### 共享响应结构：DTO 与 schema 名

同一种资源的响应结构和转换函数放在 `dto.go` 中，几个动作共用；某个动作独有的输出类型放在它自己的文件中：

```go
type ArticleDTO struct {
    ID          string     `json:"id" example:"0193a5c2-7d1e-7b3a-9f61-2c4e8d0a1b2c"`
    Title       string     `json:"title" example:"Hello World"`
    Slug        string     `json:"slug" example:"hello-world"`
    AuthorID    string     `json:"author_id" example:"alice"`
    Status      string     `json:"status" enum:"draft,published" example:"published"`
    PublishedAt *time.Time `json:"published_at,omitempty" doc:"发布时间，草稿没有这个字段" example:"2026-11-10T08:30:00Z"`
}

type ArticleOutput struct {
    Body ArticleDTO
}

func toDTO(a *Article) ArticleDTO {
    return ArticleDTO{
        ID: a.ID.String(), Title: a.Title, Slug: a.Slug,
        AuthorID: a.AuthorID, Status: a.Status, PublishedAt: a.PublishedAt,
    }
}
```

响应体用 DTO，不直接用模型 `Article`：给模型加的内部字段会随响应暴露给客户端。

OpenAPI 中的 schema 名是包名加类型名，类型名已经以包名开头时只保留一次：`article.Changes` 是 `ArticleChanges`，`article.ArticleDTO` 是 `ArticleDTO`；匿名的请求体是外层类型名加字段名，例如 `article.RejectInput` 的 `Body` 是 `ArticleRejectInputBody`。每个包都可以用 `Changes` 这样的短名，[生成的类型](clients.md#generated-names)不会重名。改类型名会改变生成的客户端代码。

<a id="listing-routes"></a>
### 用 `routes` 命令查看全部路由

```bash
./bin/blog routes
```

`routes` 用 `nil` 依赖构造应用的路由，离线列出全部接口及其访问策略，见[命令行](../general/commands.md#routes-command)。请求返回 `404` 时用它核对方法和完整路径：挂在 `/api` 下的接口要访问 `/api/articles`，路径区分大小写。

<a id="native-apis"></a>
## 使用原生 chi 与 Huma

<a id="plain-handlers"></a>
### 注册普通 `http.Handler`

`api.Router()` 返回创建 API 时传入的 `chi.Router`，可以在上面注册普通的处理器：

```go
api.Router().Get("/livez", func(w http.ResponseWriter, r *http.Request) {
    w.WriteHeader(http.StatusNoContent)
})
```

这类路由经过 Router 上的中间件，不进入 OpenAPI 文档，没有输入校验和访问策略的检查。业务接口用 `rest.Register`。

<a id="native-huma-register"></a>
### 原生 `huma.Register`

`rest.API` 内嵌 `huma.API`，可以调用 Huma 的 `huma.Register`。它绕过访问策略的检查和业务错误的转换，业务接口用 `rest.Register`。
