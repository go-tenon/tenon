# 路由

- [基本路由](#basic-routing)
    - [路由声明](#operations)
    - [处理器工厂](#handler-factories)
    - [注册时的检查](#registration-checks)
- [路由参数](#route-parameters)
- [访问策略](#access-policies)
- [组织路由](#organizing-routes)
- [OpenAPI 文档](#openapi)
    - [在线查看](#viewing-docs)
    - [离线导出](#exporting-openapi)
- [使用原生 chi 与 Huma](#native-apis)
    - [注册普通 HTTP 处理器](#plain-handlers)
    - [使用原生 huma.Register](#native-huma-register)

<a id="basic-routing"></a>
## 基本路由

mygoravel 的路由建立在 [Huma](https://huma.rocks) 之上。每条路由由三部分组成：一个 `huma.Operation` 描述路由本身，一个**输入类型**描述请求，一个**输出类型**描述响应。下面是[安装](installation.md#entrypoint)一章中注册 `/ping` 的代码：

```go
type PingInput struct{}

type PingOutput struct {
    Body struct {
        Message string `json:"message"`
    }
}

func registerPing(api *rest.API) {
    op := huma.Operation{
        OperationID: "ping",
        Method:      http.MethodGet,
        Path:        "/ping",
        Summary:     "检查 API 是否可响应",
    }
    rest.Public()(&op)
    rest.Register(api, op, func() func(context.Context, *PingInput) (*PingOutput, error) {
        return func(ctx context.Context, in *PingInput) (*PingOutput, error) {
            out := &PingOutput{}
            out.Body.Message = "pong"
            return out, nil
        }
    })
}
```

其中 `api` 是由 `rest.New` 创建的 API 实例：

```go
api := rest.New(rest.Options{
    Title:   "Hello API",
    Version: "1.0.0",
})
```

`Title` 和 `Version` 会出现在生成的 OpenAPI 文档中。

<a id="operations"></a>
### 路由声明

一条完整的路由声明包含以下几项：

| 声明 | 作用 |
| --- | --- |
| `OperationID` | 路由的唯一标识，会出现在 OpenAPI 文档中，例如 `note-create` |
| `Method`、`Path` | HTTP 方法与路径 |
| `Summary` | 路由的简短描述，显示在接口文档中 |
| 输入类型 | 路径参数、查询参数、请求头和请求体的来源、类型及验证规则 |
| 输出类型 | 响应体、状态码和响应头 |
| 访问策略 | `rest.Public()` 或 `rest.RequireBearer(...)`，必须二选一 |
| 处理器工厂 | 返回真正处理请求的函数 |

由于请求和响应的结构都写在 Go 类型中，Huma 可以据此自动完成参数解析、输入验证，并生成准确的 OpenAPI 文档。输入和输出类型的写法分别见[请求](requests.md)和[响应](responses.md)。

<a id="handler-factories"></a>
### 处理器工厂

你可能已经注意到，`rest.Register` 的最后一个参数不是处理器本身，而是一个**返回处理器的函数**。处理器的签名始终是：

```go
func(ctx context.Context, in *Input) (*Output, error)
```

之所以多包一层，是因为 API 有两种运行模式：

- **运行模式**（`rest.ModeServe`，默认）：`rest.Register` 调用工厂，得到处理器并挂载到路由上；
- **文档模式**（`rest.ModeOpenAPI`）：只生成接口文档，**不会调用工厂**。

这意味着在文档模式下，处理器依赖的数据库、缓存等资源都不需要存在。你可以在没有任何外部依赖的环境中（例如 CI）导出接口文档，详见[离线导出](#exporting-openapi)。

<a id="registration-checks"></a>
### 注册时的检查

`rest.Register` 会在注册时检查以下问题，一旦发现就直接 panic，让错误在应用启动时立刻暴露：

- 路由没有声明访问策略（既没有调用 `Public` 也没有调用 `RequireBearer`）；
- `OperationID` 与已注册的路由重复；
- 与已注册的路由方法相同且路径形状冲突，例如 `/notes/{id}` 与 `/notes/{noteID}`。

<a id="route-parameters"></a>
## 路由参数

路径中用花括号声明参数，并在输入类型中用 `path` 标签读取：

```go
type GetNoteInput struct {
    ID uuid.UUID `path:"id"`
}

op := huma.Operation{
    OperationID: "note-get",
    Method:      http.MethodGet,
    Path:        "/notes/{id}",
}
```

Huma 会把路径中的值转换为字段的类型（这里是 `github.com/google/uuid` 的 `uuid.UUID`），转换失败时自动返回验证错误。查询参数、请求头和请求体的读取方式见[请求](requests.md)。

<a id="access-policies"></a>
## 访问策略

mygoravel 要求每条路由都**显式**声明访问策略，避免因为疏忽而把需要保护的接口暴露出去。

公开路由在注册前调用 `rest.Public()`：

```go
rest.Public()(&op)
```

需要认证的路由使用 `rest.RequireBearer`，并可以要求调用者具备指定的 scope：

```go
requireNotes := rest.RequireBearer(api, validateToken, "notes")
requireNotes(&op)
```

`validateToken` 是你提供的 token 校验函数。`RequireBearer` 会同时完成两件事：在运行时检查请求的 `Authorization` 头，以及在 OpenAPI 文档中标记这条路由需要认证。完整用法见[认证](authentication.md)。

<a id="organizing-routes"></a>
## 组织路由

当路由变多时，推荐在每个业务模块的 HTTP 子包中提供一个 `Register` 函数，集中注册该模块的所有路由。下面是 `internal/note/notehttp` 包中的片段，`note.Service` 是业务服务，`NoteDTO` 与 `toDTO` 是该包定义的输出结构与转换函数：

```go
func Register(api *rest.API, requireNotes func(*huma.Operation), notes *note.Service) {
    op := huma.Operation{
        OperationID: "note-get",
        Method:      http.MethodGet,
        Path:        "/notes/{id}",
        Summary:     "获取笔记",
    }
    requireNotes(&op)
    rest.Register(api, op, func() func(context.Context, *GetNoteInput) (*NoteOutput, error) {
        return func(ctx context.Context, in *GetNoteInput) (*NoteOutput, error) {
            item, err := notes.Find(ctx, in.ID)
            if err != nil {
                return nil, err
            }
            return &NoteOutput{Body: toDTO(item)}, nil
        }
    })

    // 继续注册 note-list、note-create……
}
```

处理器只做三件事：把输入转换为业务参数、调用业务服务、把结果转换为输出。业务规则留在服务中。

入口在组装应用时调用它：

```go
notehttp.Register(api, rest.RequireBearer(api, validateToken, "notes"), notes)
```

<a id="openapi"></a>
## OpenAPI 文档

<a id="viewing-docs"></a>
### 在线查看

应用运行时，Huma 会自动提供接口文档：

- `/docs`：可交互的接口文档页面；
- `/openapi.json`：OpenAPI 3.1 文档。

<a id="exporting-openapi"></a>
### 离线导出

很多时候，你希望在不启动服务、不连接数据库的情况下生成 OpenAPI 文档，例如在 CI 中检查接口变更，或者为前端生成客户端代码。只需在创建 API 时指定文档模式，然后调用同一个注册函数：

```go
api := rest.New(rest.Options{
    Title: "Hello API", Version: "1.0.0",
    Mode:  rest.ModeOpenAPI,
})
registerPing(api)

data, err := api.OpenAPIJSON()
```

`data` 就是 OpenAPI 文档的 JSON 内容。因为运行模式和文档模式使用的是同一份路由声明，导出的文档与实际运行的接口始终保持一致。

> [!TIP]
> 结合[命令行](commands.md#openapi-command)中的 `openapi` 命令，你可以通过 `./bin/api openapi > openapi.json` 导出文档。

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

这类路由同样会经过默认中间件（请求 ID、访问日志、panic 恢复等），但不会出现在 OpenAPI 文档中，也没有类型化的输入验证。对于业务接口，请使用 `rest.Register`。

<a id="native-huma-register"></a>
### 使用原生 huma.Register

`rest.API` 内嵌了 `huma.API`，因此你也可以调用 Huma 原生的 `huma.Register`。不过这样做会绕过 mygoravel 的访问策略检查、业务错误转换和文档模式支持，这些都需要你自己处理。除非有特殊需求，否则推荐使用 `rest.Register`。

如果你需要传入自定义的 chi Router，请参阅[中间件](middleware.md#chi-middleware)。
