# 请求与校验

[文档首页](README.md) / HTTP 基础

用 Go 结构体描述接口输入，Huma 负责读取路径、查询参数和 JSON 请求体，并在进入处理器前完成校验。

本章内容：

- [路径与查询参数](#parameters)
- [JSON 请求体](#json-body)
- [校验失败](#validation-errors)
- [大小限制与超时](#limits)
- [读取请求标识](#request-id)

<a id="parameters"></a>

## 路径与查询参数

下面的输入类型适用于 `/notes/{id}`。使用 `github.com/google/uuid` 中的 UUID 类型读取笔记标识：

```go
type GetNoteInput struct {
    ID uuid.UUID `path:"id"`
}
```

列表查询可以定义为：

```go
type ListNotesInput struct {
    Before string `query:"before"`
    Limit  int    `query:"limit" default:"20" minimum:"1" maximum:"100"`
}
```

这个定义把越界的 limit 当作输入错误拒绝。若业务选择截断或重置越界值，应在自己的服务中明确实现该策略，并让接口文档与实际行为一致。

请求头也可以作为字段读取：

```go
type RequestInput struct {
    ClientVersion string `header:"X-Client-Version"`
}
```

认证头交给[Bearer 认证](authentication.md)处理，凭据校验成功后得到主体。

<a id="json-body"></a>

## JSON 请求体

输入结构体中名为 `Body` 的字段表示正文：

```go
type CreateNoteInput struct {
    Body struct {
        Title string `json:"title" minLength:"1" maxLength:"120"`
    }
}
```

客户端发送：

```http
POST /notes
Content-Type: application/json
Authorization: Bearer <token>

{"title":"准备发布"}
```

标题的结构校验会在处理器执行前完成。业务服务检查业务规则，直接调用与 HTTP 入口遵循同样的约束。

<a id="validation-errors"></a>

## 校验失败

JSON 语法错误通常返回 400，字段校验失败返回 422。错误正文使用统一的 Problem Details 格式，例如：

```json
{
  "status": 422,
  "title": "Unprocessable Entity",
  "detail": "Invalid request",
  "code": "error.validation",
  "request_id": "<请求标识>",
  "errors": [
    {"message": "Invalid value", "location": "body.title"}
  ]
}
```

上面展示启用框架英文目录时的正文；未启用语言目录时，detail 使用状态描述。客户端根据 `code` 和 `errors[].location` 决定展示位置与交互。

字段错误只包含公开提示和位置，默认 message 为 Invalid value。认证失败可能在正文校验前就返回 401。

<a id="limits"></a>

## 大小限制与超时

`rest.Options.Middleware` 可以设置整个 API 的请求体上限和请求 context 时限：

```go
api := rest.New(rest.Options{
    Middleware: httpx.MiddlewareOptions{
        MaxBodyBytes:   1 << 20,
        RequestTimeout: 30 * time.Second,
    },
})
```

省略时默认 1 MiB 和 30 秒。Huma Operation 也有原生的正文限制设置；如果要允许某个接口更大的正文，需要同时检查该 Operation 和外层中间件的上限。

超时会取消请求 context，处理器负责响应取消。数据库查询和外部客户端调用应使用处理器收到的 `ctx`。服务器的 `ReadTimeout`、`WriteTimeout`、`ReadHeaderTimeout` 则由应用创建 `http.Server` 时设置；见[最小应用](routing.md#first-route)。

<a id="request-id"></a>

## 读取请求标识

处理器内可以调用：

```go
requestID := httpx.RequestID(ctx)
```

每个请求获得新的标识，响应头 `X-Request-ID` 和错误正文中的 `request_id` 与它一致。它用于关联本次请求的日志；trace ID 由应用的追踪设施提供。

---

[上一篇：HTTP 中间件](middleware.md) · [下一篇：响应](responses.md)
