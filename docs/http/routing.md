# 路由

路由决定一个 HTTP 请求由谁处理：它把一个 HTTP 方法和路径交给一个处理器函数。

Tenon 的路由建立在 [Huma](https://huma.rocks) 之上。每条路由由三部分组成：一个 `huma.Operation` 描述路由本身，一个**输入类型**描述请求，一个**输出类型**描述响应。因为请求和响应的结构都写在 Go 类型中，Huma 可以替你解析参数、验证输入，并生成准确的 OpenAPI 文档。

- [示例用法](#basic-routing)
    - [路由声明](#operations)
    - [处理器](#handlers)
    - [注册时的检查](#registration-checks)
- [路由参数](#route-parameters)
- [访问策略](#access-policies)
- [组织路由](#organizing-routes)
    - [模块内注册](#module-registration)
    - [动作的 HTTP 入口](#http-actions)
    - [共享响应结构](#shared-responses)
    - [查看全部路由](#listing-routes)
- [API 版本](#api-versioning)
- [OpenAPI 文档](#openapi)
    - [在线查看](#viewing-docs)
    - [说明与示例](#describing-apis)
    - [离线导出](#exporting-openapi)
- [使用原生 chi 与 Huma](#native-apis)
    - [注册普通 HTTP 处理器](#plain-handlers)
    - [使用原生 huma.Register](#native-huma-register)
- [API 参考](#api-reference)

<a id="basic-routing"></a>
## 示例用法

下面是一个只有一个接口的完整程序，你可以把它保存为 `main.go` 直接运行：

```go
package main

import (
    "context"
    "log"
    "net/http"

    "github.com/danielgtaylor/huma/v2"

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
    api := rest.New(rest.Options{Title: "Hello API", Version: "1.0.0"})

    op := huma.Operation{
        OperationID: "ping",
        Method:      http.MethodGet,
        Path:        "/ping",
        Summary:     "检查 API 是否可响应",
    }
    rest.Public()(&op)
    rest.Register(api, op, ping)

    log.Fatal(http.ListenAndServe(":8080", api.Handler()))
}
```

启动之后，在另一个终端请求它：

```bash
curl localhost:8080/ping
# {"message":"pong"}
```

我们按顺序看一下这段代码：

1. `rest.New` 创建一个 API，`Title` 和 `Version` 会出现在生成的接口文档中；
2. `huma.Operation` 声明路由：唯一的 `OperationID`、HTTP 方法、路径和一句话说明；
3. `rest.Public()` 把这条路由声明为公开的，不需要登录；
4. `rest.Register` 把处理器 `ping` 注册到这条路由上。

打开 `http://localhost:8080/docs`，你还能看到自动生成的、可交互的接口文档。

> [!NOTE]
> 为了简短，这个例子直接使用了 `http.ListenAndServe`。实际的应用用 `httpx.Serve` 平滑停止，并安装[默认中间件](middleware.md#default-middleware)，见[依赖装配](../concepts/dependencies.md#assembling-the-application)。

<a id="operations"></a>
### 路由声明

现在来看一条路由声明包含哪些内容：

| 声明 | 作用 |
| --- | --- |
| `OperationID` | 路由的唯一标识，kebab-case，例如 `article-create`；客户端生成器用它命名函数 |
| `Method`、`Path` | HTTP 方法与路径 |
| `Summary` | 路由的简短描述，显示在接口文档中 |
| 输入类型 | 路径参数、查询参数、请求头和请求体的来源、类型及验证规则 |
| 输出类型 | 响应体、状态码和响应头 |
| 访问策略 | `rest.Public()` 或 `rest.RequireBearer(...)`，必须选择一个 |
| 处理器 | 处理请求的函数，通常是[业务动作](../concepts/actions.md)的 `HTTP` 方法 |

输入和输出类型的写法分别在[请求](requests.md)和[响应](../responses/responses.md)中介绍。

<a id="handlers"></a>
### 处理器

无论哪条路由，处理器的签名始终是这样的：

```go
func(ctx context.Context, in *Input) (*Output, error)
```

业务接口的处理器通常是[业务动作](../concepts/actions.md)的方法值，例如 `article.NewPublish(db).HTTP`。

API 有两种运行模式：

- **运行模式**（`rest.ModeServe`，默认）：处理器挂载到路由上；
- **文档模式**（`rest.ModeOpenAPI`）：只生成接口文档，**不会调用处理器**，也可以传 `nil`。

文档模式下路由照常注册，动作也会被构造，只是传入的数据库等依赖都是 `nil`。只要构造函数只保存依赖、不使用它们，就可以在没有任何外部依赖的环境中（例如 CI）导出接口文档，详见[离线导出](#exporting-openapi)。

<a id="registration-checks"></a>
### 注册时的检查

路由的错误越早发现越好。`rest.Register` 会在注册时检查以下问题，一旦发现就直接 panic，让错误在应用启动时（甚至在 CI 中导出文档时）立刻暴露，而不是上线后才发现：

- 路由没有声明访问策略（没有调用 `Public` 或 `RequireBearer`）；
- `OperationID` 为空或不是 kebab-case，例如 `articleCreate`；
- `OperationID` 与已注册的路由重复；
- 与已注册的路由方法相同且路径形状冲突，例如 `/articles/{id}` 与 `/articles/{articleID}`。

<a id="route-parameters"></a>
## 路由参数

很多路由的路径中包含参数，例如文章的 ID。在路径中用花括号声明参数，然后在输入类型中用 `path` 标签读取它：

```go
type GetArticleInput struct {
    ID uuid.UUID `path:"id"`
}

op := huma.Operation{
    OperationID: "article-get",
    Method:      http.MethodGet,
    Path:        "/articles/{id}",
}
```

Huma 会把路径中的值转换为字段的类型（这里是 `github.com/google/uuid` 的 `uuid.UUID`），转换失败时自动返回验证错误。查询参数、请求头和请求体的读取方式见[请求](requests.md)。

<a id="access-policies"></a>
## 访问策略

忘记保护一个接口，是最常见也最危险的错误之一：它不会报错，只会悄悄地把数据暴露出去。所以 Tenon 要求每条路由都**显式**声明访问策略，没有声明的路由在注册时就会 panic。

公开的路由，在注册之前调用 `rest.Public()`：

```go
rest.Public()(&op)
```

需要登录的路由使用 `rest.RequireBearer`，还可以要求调用者具备指定的权限：

```go
requireArticles := rest.RequireBearer(api, validateToken, "articles")
requireArticles(&op)
```

`validateToken` 是你提供的令牌校验函数。`RequireBearer` 同时完成两件事：在运行时检查请求的 `Authorization` 头；在 OpenAPI 文档中标记这条路由需要认证。这样，接口的实际行为和文档始终一致。完整的用法见[认证](../security/authentication.md)。

<a id="organizing-routes"></a>
## 组织路由

示例用法中只有一条路由，直接写在 `main` 中就好。但真实的应用有几十上百条路由，全部堆在一处很快就难以维护。

在 Tenon 中，业务代码按[业务动作](../concepts/actions.md)组织，每个动作一个文件，HTTP 入口是动作的 `HTTP` 方法。每个业务模块再用一个 `routes.go` 集中声明自己的路由。想知道文章模块有哪些接口、各自需要什么权限，打开 `routes.go` 就一目了然：

```text
internal/article/
├── article.go      模型、业务错误与共享的规则
├── create.go       Create：创建文章
├── get.go          Get：获取文章
├── publish.go      Publish：发布文章
├── dto.go          共享的响应结构与转换
└── routes.go       注册文章模块的路由
```

<a id="module-registration"></a>
### 模块内注册

`routes.go` 与业务动作同属一个包，提供模块的 `Register` 入口。它只做一件事：声明路由，并把业务动作的 `HTTP` 方法注册为处理器：

```go
// Register 注册文章模块的全部路由。
func Register(api *rest.API, requireArticles func(*huma.Operation), db *bun.DB) {
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

    publish := huma.Operation{
        OperationID: "article-publish",
        Method:      http.MethodPost,
        Path:        "/articles/{id}/publish",
        Summary:     "发布文章",
    }
    requireArticles(&publish)
    rest.Register(api, publish, NewPublish(db).HTTP)
}
```

`Register` 接收模块的动作需要的依赖，在注册时构造它们。生成 OpenAPI 或查看路由时，`db` 为 `nil`，处理器不会被调用。

入口在组装应用时调用它，并决定这些路由的访问策略。`validateToken` 是令牌校验器，见[认证](../security/authentication.md)：

```go
article.Register(api, rest.RequireBearer(api, validateToken, "articles"), db)
```

<a id="http-actions"></a>
### 动作的 HTTP 入口

`get.go` 中的“获取文章”。`find`、`ErrNotFound` 等是模块内共享的代码，见[业务动作](../concepts/actions.md#shared-code)：

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

`create.go` 的输入类型直接使用模块的 `Changes` 作为请求体，验证规则写在 `Changes` 上，见[数据对象](../concepts/actions.md#data-objects)：

```go
type CreateInput struct {
    Body Changes
}
```

每个 `HTTP` 方法只做三件事：从输入和 context 中取出参数、调用 `Handle`、把结果转换为输出。业务规则，包括“只有作者能发布”、“不能重复发布”，都在 `Handle` 中。不同动作使用各自的输入类型，后续可以独立增加字段或验证规则。

<a id="shared-responses"></a>
### 共享响应结构

这几个动作返回同一种文章表示，所以把 `ArticleDTO`、`ArticleOutput` 和转换函数放在 `dto.go` 中共享。某个动作独有的输出类型放在它自己的文件中：

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

`ArticleDTO` 决定了 API 返回哪些字段。不要把模型 `Article` 直接作为响应体：给模型加一个内部字段，就会意外地把它暴露给客户端。请求和响应类型的详细写法分别见[请求](requests.md)和[响应](../responses/responses.md)。

请求和响应中的具名类型在 OpenAPI 文档中以包名加类型名命名，类型名已经以包名开头时不重复：`article.Changes` 为 `ArticleChanges`，`comment.Changes` 为 `CommentChanges`，`article.ArticleDTO` 仍为 `ArticleDTO`；匿名的请求体以外层类型的名称加字段名命名，例如 `article.RejectInput` 的 `Body` 为 `ArticleRejectInputBody`。所以每个包都可以用 `Changes` 这样的短名，前端[生成的类型](../responses/clients.md#generated-names)也不会重名。

<a id="listing-routes"></a>
### 查看全部路由

模块增多之后，你可以通过应用的 `routes` 命令查看全局路由表，无需逐个打开模块。按[命令行](../general/commands.md#routes-command)一章安装命令后，执行：

```bash
./bin/api routes
```

该命令复用应用的路由注册入口，离线生成路由表，不需要启动 HTTP 服务或连接数据库。路由的真实声明仍保存在各模块的 `routes.go` 中，查看命令不需要另一份手工维护的路由清单。

<a id="api-versioning"></a>
## API 版本

只提供 JSON API 的应用，通常直接挂在根路径上，由部署决定前缀，例如 `https://api.example.com/v1`。但如果 API 要和[页面](../responses/views.md)或单页应用共用一个 Router，就需要把它挂在带版本号的子路由下，并把同一个前缀设为 `BasePath`：

```go
router.Route("/api/v1", func(r chi.Router) {
    r.Use(httpx.Timeout(cfg.HTTP.Timeout))
    opts := API
    opts.Router, opts.BasePath = r, "/api/v1"
    Routes(rest.New(opts))
})
```

路由按子路由注册，接口地址是 `/api/v1/articles`。`BasePath` 写入 OpenAPI 文档的 `servers`，文档页 `/api/v1/docs` 和生成的客户端据此拼出完整地址；`routes` 命令显示的也是完整路径。

需要不兼容的修改时，新版本挂在另一个子路由下，用自己的 `rest.API`。两个版本各有一份 OpenAPI 文档，共用同一套业务动作：

```go
router.Route("/api/v2", func(r chi.Router) {
    r.Use(httpx.Timeout(cfg.HTTP.Timeout))
    opts := APIv2
    opts.Router, opts.BasePath = r, "/api/v2"
    RoutesV2(rest.New(opts))
})
```

<a id="openapi"></a>
## OpenAPI 文档

<a id="viewing-docs"></a>
### 在线查看

应用运行时，Huma 会自动提供接口文档：

- `/docs`：可交互的接口文档页面；
- `/openapi.json`：OpenAPI 3.1 文档。

<a id="describing-apis"></a>
### 说明与示例

Go 类型只能说明字段的类型和取值规则，字段的含义和示例值需要你自己写进声明：

| 写在哪里 | 写什么 | 在文档中成为 |
| --- | --- | --- |
| `huma.Operation` | `Summary` 一句话说明，`Description` 写更长的说明，支持 Markdown | 路由的标题和说明 |
| 字段的 `doc` 标签 | 字段的含义，以及类型和验证规则看不出来的约定 | 字段的 `description` |
| 字段的 `example` 标签 | 一个典型的值 | 字段的 `examples` |

不写 `example` 时，文档页只能按类型填 `"string"`、`0` 这样的占位值。给创建文章的请求体加上示例：

```go
type Changes struct {
    Title   string `json:"title" minLength:"1" maxLength:"200" example:"Hello World"`
    Slug    string `json:"slug" maxLength:"100" pattern:"^[a-z0-9]+(-[a-z0-9]+)*$" patternDescription:"小写字母、数字和连字符" doc:"文章地址中的标识，全站唯一；发布后不能修改" example:"hello-world"`
    Content string `json:"content" example:"这是我的第一篇文章。"`
}
```

`/docs` 中这个接口的请求示例就会是 `{"title":"Hello World","slug":"hello-world","content":"这是我的第一篇文章。"}`，调用方可以直接修改后发送。路径参数、查询参数和请求头的写法相同，例如 `` ID uuid.UUID `path:"id" example:"0193a5c2-7d1e-7b3a-9f61-2c4e8d0a1b2c"` ``；响应体的写法见[共享响应结构](#shared-responses)中的 `ArticleDTO`。

`doc` 只写类型和规则看不出来的约定。`minLength`、`pattern`、`enum` 等验证规则本身就会出现在文档中，不需要在 `doc` 里重复。

`example` 的值按字段类型解析：数字写 `example:"50"`，数组和对象写 JSON，例如 `example:"[\"go\",\"api\"]"`。值和类型不符时，`rest.Register` 在注册时 panic，执行 `routes` 或 `openapi` 命令就能发现。这些标签只写入文档，不影响验证和 JSON 编码。

<a id="exporting-openapi"></a>
### 离线导出

很多时候，你希望在不启动服务、不连接数据库的情况下生成 OpenAPI 文档，例如在 CI 中检查接口变更，或者为前端[生成客户端代码](../responses/clients.md)。只需在创建 API 时指定文档模式，然后调用同一个注册函数：

```go
api := rest.New(rest.Options{
    Title: "Hello API", Version: "1.0.0",
    Mode:  rest.ModeOpenAPI,
})
Routes(api) // 与运行时相同的注册函数

data, err := api.OpenAPIJSON()
```

`data` 就是 OpenAPI 文档的 JSON 内容。因为运行模式和文档模式使用的是同一份路由声明，导出的文档与实际运行的接口始终保持一致。

> [!TIP]
> 应用的 [`openapi` 命令](../general/commands.md#openapi-command)就是这样导出的：`./bin/api openapi > openapi.json`。

<a id="native-apis"></a>
## 使用原生 chi 与 Huma

<a id="plain-handlers"></a>
### 注册普通 HTTP 处理器

`api.Router()` 返回底层的 `chi.Router`，你可以直接在上面注册普通的 `http.Handler`，例如健康检查：

```go
api.Router().Get("/livez", func(w http.ResponseWriter, r *http.Request) {
    w.WriteHeader(http.StatusNoContent)
})
```

这类路由同样会经过 Router 上安装的中间件（例如请求 ID、访问日志、panic 恢复），但不会出现在 OpenAPI 文档中，也没有类型化的输入验证。对于业务接口，请使用 `rest.Register`。

<a id="native-huma-register"></a>
### 使用原生 huma.Register

`rest.API` 内嵌了 `huma.API`，因此你也可以调用 Huma 原生的 `huma.Register`。不过这样做会绕过 Tenon 的访问策略检查、业务错误转换和文档模式支持，这些都需要你自己处理。除非有特殊需求，否则推荐使用 `rest.Register`。

`rest.New` 不安装任何中间件，中间件的安装方式请参阅[中间件](middleware.md)。

<a id="api-reference"></a>
## API 参考

包 `github.com/go-tenon/tenon/httpx/rest`。

### New

```go
func New(opts Options) *API
```

- **参数**
    - `opts`：见下面的 `Options`
- **返回**：API 实例

`New` 不安装任何中间件，中间件要在传入 `Router` 之前安装，见[中间件](middleware.md)。

### Options

| 字段 | 说明 |
| --- | --- |
| `Title`、`Version` | 写入 OpenAPI 文档 |
| `Mode` | `ModeServe`（默认）或 `ModeOpenAPI` |
| `Router chi.Router` | 挂载路由的 Router，`nil` 时新建一个。中间件在传入前安装 |
| `BasePath` | API 挂载的路径前缀，写入 OpenAPI 的 `servers` |
| `Logger *slog.Logger` | 记录 5xx 错误，`nil` 时用 `slog.Default()` |
| `Transformers []huma.Transformer` | 追加在 Huma 默认的 Transformer 之后 |
| `Accounts identity.Accounts` | 接受的账号体系，见[认证](../security/authentication.md) |
| `Tenant func(huma.Context) string` | 从请求取出租户，见[多租户](../security/tenancy.md) |
| `Credentials httpx.CredentialWatcher` | 长连接复查凭据的信号，见[服务端推送](../responses/sse.md#immediate-revocation) |

### Register

```go
func Register[I, O any](api *API, op huma.Operation, handler func(context.Context, *I) (*O, error))
```

- **参数**
    - `api`：由 `New` 创建的 API
    - `op`：路由声明，已经用 `Public` 或 `RequireBearer` 声明了访问策略
    - `handler`：处理器，通常是业务动作的 `HTTP` 方法

运行模式下把处理器挂载到路由上，文档模式下只生成文档。发现[注册时的检查](#registration-checks)中的问题时 panic。

```go
rest.Register(api, op, NewPublish(db).HTTP)
```

### Public、RequireBearer

```go
func Public() func(op *huma.Operation)
func RequireBearer(api *API, validate TokenValidator, permissions ...string) func(op *huma.Operation)
```

- **参数**
    - `api`：路由所在的 API
    - `validate`：令牌校验器，例如 `tokens.Validate`
    - `permissions`：调用者必须具备的全部权限
- **返回**：作用于路由声明的函数，在 `Register` 之前调用

`Public` 把路由声明为公开的；`RequireBearer` 要求有效的 Bearer 令牌，并且调用者具备全部 `permissions`。

### API

```go
func (a *API) Handler() http.Handler
func (a *API) Router() chi.Router
func (a *API) Mode() Mode
func (a *API) OpenAPIJSON() ([]byte, error)
func (a *API) OpenAPI30JSON() ([]byte, error)
func (a *API) Endpoints() []Endpoint
```

`Handler` 是挂载了全部路由的处理器；`OpenAPIJSON`、`OpenAPI30JSON` 导出 OpenAPI 3.1、3.0 文档；`Endpoints` 列出已注册的路由及其访问策略，`routes` 命令用它。
