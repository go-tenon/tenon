# 路由与 OpenAPI

[文档首页](README.md) / HTTP 基础

路由使用 Huma 的 `Operation` 和 Go 输入输出类型声明。mygoravel 在注册时连接安全策略、错误转换和离线文档模式；处理器仍然是普通 Go 函数。

本章内容：

- [定义第一个路由](#first-route)
- [理解路由声明](#operations)
- [公开与受保护的路由](#security)
- [离线导出 OpenAPI](#openapi)
- [使用原生 chi 和 Huma](#native-apis)

<a id="first-route"></a>

## 定义第一个路由

下面是完整的 `main.go`。在已经添加 mygoravel 依赖的 Go module 中保存并运行 `go mod tidy`、`go run .`：

```go
package main

import (
    "context"
    "log/slog"
    "net"
    "net/http"
    "os"
    "os/signal"
    "syscall"
    "time"

    "github.com/danielgtaylor/huma/v2"
    "github.com/ryan053857/mygoravel/httpx"
    "github.com/ryan053857/mygoravel/httpx/rest"
    "github.com/ryan053857/mygoravel/logging"
)

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

func run() error {
    ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
    defer stop()

    logger := logging.New(logging.Options{Writer: os.Stderr})
    api := rest.New(rest.Options{
        Title: "Hello API", Version: "1.0.0",
        Middleware: httpx.MiddlewareOptions{Logger: logger},
    })
    registerPing(api)

    listener, err := net.Listen("tcp", "127.0.0.1:8080")
    if err != nil {
        return err
    }
    server := &http.Server{
        Handler: api.Handler(), ReadHeaderTimeout: 5 * time.Second,
        ReadTimeout: 30 * time.Second, WriteTimeout: 30 * time.Second,
        IdleTimeout: time.Minute,
    }
    return httpx.Serve(ctx, server, listener, httpx.ServeOptions{
        ShutdownTimeout: 10 * time.Second,
        Logger: logger,
    })
}

func main() {
    if err := run(); err != nil {
        slog.Error("application stopped", "error", err)
        os.Exit(1)
    }
}
```

在另一个终端发起请求：

```bash
curl -i http://localhost:8080/ping
```

响应为 `200 OK`，正文包含业务字段和 Huma 默认提供的 schema 链接：

```json
{
  "$schema": "http://localhost:8080/schemas/PingOutputBody.json",
  "message": "pong"
}
```

程序没有数据库、缓存或认证配置。

<a id="operations"></a>

## 理解路由声明

| 声明 | 作用 |
| --- | --- |
| OperationID | 文档中的唯一操作标识，例如 note-create |
| Method、Path | HTTP 方法与路径 |
| 输入类型 | 参数和请求体的来源、类型及校验规则 |
| 输出类型 | 响应正文、状态和响应头 |
| Public 或 RequireBearer | 显式访问策略 |
| newHandler 回调 | 在运行模式下构造处理器 |

`rest.Register` 会拒绝没有安全声明、重复 OperationID 和冲突路径的登记。构造回调适合捕获已经装配的业务服务；不要在注册路径或描述信息时查询数据库。

<a id="security"></a>

## 公开与受保护的路由

公开路由先调用 `rest.Public()(&op)`。需要认证的路由使用：

```go
requireNotes := rest.RequireBearer(api, validateToken, "notes")
requireNotes(&op)
```

其中 `validateToken` 的类型为 `rest.TokenValidator`。再将 `op` 传给 `rest.Register`。完整校验方式见[认证与授权](authentication.md)。

认证声明会同时连接运行时中间件和 OpenAPI Security。不要只写文档中的 Security，然后遗漏实际认证逻辑。

<a id="openapi"></a>

## 离线导出 OpenAPI

复用上面的 `registerPing`，切换构造模式即可生成文档：

```go
api := rest.New(rest.Options{
    Title: "Hello API", Version: "1.0.0", Mode: rest.ModeOpenAPI,
})
registerPing(api)
data, err := api.OpenAPIJSON()
```

`data` 是文档 JSON。`ModeOpenAPI` 不调用 `newHandler`，也不构造业务服务。应将数据库打开操作放在运行入口，而不是放在路由声明函数中。

默认 Huma 文档路径为 `/docs`，OpenAPI JSON 路径为 `/openapi.json`。需要 CLI 导出时，可按[应用命令](commands.md#factories)连接同一个登记函数。文档生成方式与运行时必须使用同一份路由声明。

<a id="native-apis"></a>

## 使用原生 chi 和 Huma

`api.Router()` 返回原生 chi.Router，可以注册普通 `http.Handler`，例如应用自己的探针：

```go
api.Router().Get("/livez", func(w http.ResponseWriter, r *http.Request) {
    w.WriteHeader(http.StatusNoContent)
})
```

这种路由经过默认 HTTP 中间件，但不会自动加入 Huma 的 OpenAPI，也不使用类型化输入校验。

也可以通过 `api.API` 调用原生 `huma.Register`。此时应用自己负责安全声明、处理器错误转换和离线绑定行为。普通业务 API 推荐沿用本章的 `rest.Register` 路径。

提供自定义 `Options.Router` 时，应在注册路由之前构造 `rest.API`，因为构造过程会安装 chi 中间件。

---

[上一篇：请求与资源生命周期](lifecycle.md) · [下一篇：HTTP 中间件](middleware.md)
