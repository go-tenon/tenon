# 安装

这一页带你从一个空目录开始，写出第一个 Tenon 接口：访问 `/ping`，返回 `{"message":"pong"}`。我们分三步完成，每一步都是一个能运行的程序：

1. 写出最小的服务，能响应请求；
2. 加上框架默认的中间件，每个请求都有一个 ID，方便排查问题；
3. 让程序能平稳地停止，不会中断正在处理的请求。

如果你想直接得到一个带数据库、带前端的完整项目，可以跳到[脚手架](scaffolding.md)；但建议先读完这一页，后面的项目都是在这个基础上搭起来的。

阅读前需要了解：Go 的基本语法，会用 `go run`。

- [准备工作](#prerequisites)
- [创建项目](#creating)
- [第一步：最小的服务](#first-api)
- [第二步：加上默认的中间件](#middleware)
- [第三步：平稳地停止](#graceful-shutdown)
- [常见问题](#troubleshooting)
- [下一步](#next-steps)

<a id="prerequisites"></a>
## 准备工作

你需要 Go 1.27.4 或更高的版本。在终端中查看版本：

```bash
go version
# go version go1.27.4 linux/amd64
```

版本太低时，从 [go.dev/dl](https://go.dev/dl/) 下载新版本安装。

<a id="creating"></a>
## 创建项目

Tenon 是一个普通的 Go module（模块），和其他依赖一样用 `go get` 安装。先创建一个目录，初始化 module：

```bash
mkdir hello-api
cd hello-api
go mod init example.com/hello-api
```

`example.com/hello-api` 是这个 module 的路径，也就是别人导入它时写的名字。自己的项目可以随意取，通常用代码仓库的地址，例如 `github.com/你的名字/hello-api`。

然后添加 Tenon：

```bash
go get github.com/go-tenon/tenon@main
```

`@main` 表示使用主分支上最新的代码。也可以写成一个具体的提交，例如 `@a1b2c3d`，这样每次构建用到的都是同一个版本。

<a id="first-api"></a>
## 第一步：最小的服务

在 `hello-api` 目录中新建 `main.go`。这是一个完整的程序，只做一件事：收到 `GET /ping` 时返回 `{"message":"pong"}`：

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

// PingOutput 是接口的返回值：Body 中的字段会被编码为 JSON。
type PingOutput struct {
	Body struct {
		Message string `json:"message"`
	}
}

// ping 处理 GET /ping。它不需要任何输入，所以输入的类型是空结构体。
func ping(ctx context.Context, in *struct{}) (*PingOutput, error) {
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
		Summary:     "检查服务是否在运行",
	}
	rest.Public()(&op)
	rest.Register(api, op, ping)

	log.Println("listening on http://127.0.0.1:8080")
	log.Fatal(http.ListenAndServe("127.0.0.1:8080", router))
}
```

从上往下看 `main` 做了什么：

- **`chi.NewRouter()`** 创建一个路由器。路由器负责“看请求的地址，决定交给哪个函数处理”。
- **`rest.New`** 在路由器上创建一个 API。`Title` 和 `Version` 会写进自动生成的接口文档。
- **`huma.Operation`** 描述一个接口：用什么方法（`GET`）、在哪个路径（`/ping`）。`OperationID` 是这个接口的名字，必须唯一，生成接口文档和客户端代码时会用到它。
- **`rest.Public()(&op)`** 声明这个接口是公开的，不需要登录。Tenon 要求每个接口都明确声明是公开的还是需要登录，忘记声明时程序在启动时就会报错，而不是悄悄地把需要保护的接口公开出去。
- **`rest.Register`** 把接口和处理它的函数 `ping` 连在一起。注意这里传的是 `ping` 本身，没有加括号，见[把方法当作值传递](../concepts/go-primer.md#method-values)。
- **`http.ListenAndServe`** 是 Go 标准库的函数，在 `127.0.0.1:8080` 上等待请求。

再看处理函数 `ping`：

- 它的签名是固定的形式：第一个参数是 `context.Context`（见 [context](../concepts/go-primer.md#context)），第二个参数是输入，返回输出和错误。
- 输出是一个结构体，其中的 `Body` 字段就是响应体。`json:"message"` 是结构体标签，表示这个字段在 JSON 中叫 `message`，见[结构体标签](../concepts/go-primer.md#struct-tags)。

整理依赖，运行它：

```bash
go mod tidy
go run .
# listening on http://127.0.0.1:8080
```

`go mod tidy` 根据代码中的 `import` 下载需要的依赖。保持程序运行，在另一个终端中访问这个接口：

```bash
curl -i http://127.0.0.1:8080/ping
```

你会看到：

```http
HTTP/1.1 200 OK
Content-Type: application/json

{"message":"pong"}
```

Tenon 还根据你的代码生成了接口文档。在浏览器中打开 <http://127.0.0.1:8080/docs>，可以看到 `/ping` 这个接口和它返回的字段；`/openapi.json` 是同样内容的 OpenAPI 文档，前端和其他服务可以用它生成调用代码。

在运行程序的终端按 Ctrl+C 停止它。

<a id="middleware"></a>
## 第二步：加上默认的中间件

线上的服务每天处理很多请求。用户反馈“刚才出错了”时，你需要从日志中找到那一次请求。为此，每个请求都应当有一个唯一的 ID，写在响应头和日志中。

这类“每个请求都要做”的工作由**中间件**完成。中间件包在处理函数的外面，请求先经过中间件，再到达处理函数；响应也先经过中间件，再返回给客户端。Tenon 提供了一组默认的中间件：生成请求 ID、记录访问日志、捕获处理函数中的 panic（返回 500，而不是让整个程序崩溃）等，见[中间件](../http/middleware.md#default-middleware)。

修改 `main` 函数，在注册接口之前安装它们：

```go
func main() {
	cfg := httpx.DefaultConfig()

	router := chi.NewRouter()
	router.Use(httpx.Defaults(cfg)...)     // 默认的中间件
	router.Use(httpx.Timeout(cfg.Timeout)) // 处理时间超过限制时返回 503
	api := rest.New(router, rest.Options{Title: "Hello API", Version: "1.0.0"})

	// ……注册接口和启动服务的代码不变
}
```

- `httpx.DefaultConfig()` 返回 HTTP 服务的默认配置，包括监听地址、超时时间等。之后可以从配置文件读取，见[配置](../general/configuration.md)。
- `router.Use` 安装中间件。它们要在注册接口之前安装，对之后注册的所有接口生效。
- 记得在 `import` 中加上 `"github.com/go-tenon/tenon/httpx"`。

再运行一次，访问 `/ping`。这次响应头中多了 `X-Request-Id`，终端中也出现了一行访问日志，带着同样的 ID：

```http
HTTP/1.1 200 OK
Content-Type: application/json
X-Request-Id: 3UHKWQUZP5TTP24PG3SQNGA5EO

{"message":"pong"}
```

<a id="graceful-shutdown"></a>
## 第三步：平稳地停止

部署新版本时，旧的程序会收到停止信号（`SIGTERM`）。如果它立刻退出，正在处理的请求就被中断了，用户看到的是连接断开。更好的做法是：先停止接收新请求，等正在处理的请求完成，再退出。

`httpx.ListenAndServe` 就是这样做的：它在 ctx 被取消时开始平稳地停止。我们用 `signal.NotifyContext` 得到一个“收到停止信号时被取消”的 ctx，见[defer 与结束后台工作](../concepts/go-primer.md#cleanup)。这是最终的完整程序：

```go
package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/danielgtaylor/huma/v2"
	"github.com/go-chi/chi/v5"

	"github.com/go-tenon/tenon/httpx"
	"github.com/go-tenon/tenon/httpx/rest"
	"github.com/go-tenon/tenon/logging"
)

type PingOutput struct {
	Body struct {
		Message string `json:"message"`
	}
}

func ping(ctx context.Context, in *struct{}) (*PingOutput, error) {
	out := &PingOutput{}
	out.Body.Message = "pong"
	return out, nil
}

func run() error {
	// 收到 Ctrl+C 或 SIGTERM 时，ctx 被取消
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	slog.SetDefault(logging.New(logging.Config{}))
	cfg := httpx.DefaultConfig()
	cfg.Addr = "127.0.0.1:8080"

	router := chi.NewRouter()
	router.Use(httpx.Defaults(cfg)...)
	router.Use(httpx.Timeout(cfg.Timeout))
	api := rest.New(router, rest.Options{Title: "Hello API", Version: "1.0.0"})

	op := huma.Operation{OperationID: "ping", Method: http.MethodGet, Path: "/ping", Summary: "检查服务是否在运行"}
	rest.Public()(&op)
	rest.Register(api, op, ping)

	// ctx 被取消后，等正在处理的请求完成再返回
	return httpx.ListenAndServe(ctx, cfg, router)
}

func main() {
	if err := run(); err != nil {
		slog.Error("application stopped", "error", err)
		os.Exit(1)
	}
}
```

和第二步相比，变化有三处：

- **`run` 函数**：程序的主体移到了 `run` 中，它返回错误；`main` 只负责在出错时记录日志并以非零的退出码结束。这样 `run` 中可以放心地用 `defer` 释放资源：`os.Exit` 会直接结束程序，跳过所有还没有执行的 `defer`，所以不能在 `run` 中调用它。
- **日志**：`logging.New` 创建一个日志器，`slog.SetDefault` 把它设为全局默认。之后整个程序都用标准库的 `slog` 记录日志，见[日志](../general/logging.md)。
- **`httpx.ListenAndServe`** 代替了标准库的 `http.ListenAndServe`，它在 ctx 被取消时平稳地停止。

运行后按 Ctrl+C，日志中依次出现 `http server draining`（停止接收新请求，等待正在处理的请求）和 `http server stopped`（全部完成），然后程序退出。

<a id="troubleshooting"></a>
## 常见问题

**启动时报错 `endpoint must explicitly declare Public, RequireBearer or another RequireAuth scheme`**

注册接口之前没有声明它是否需要登录。公开的接口加上 `rest.Public()(&op)`；需要登录的接口见[认证](../security/authentication.md)。

**启动时报错 `address already in use`**

8080 端口被其他程序占用了，可能是上一次运行的程序还没有停止。停止它，或者换一个端口，例如把 `cfg.Addr` 改成 `127.0.0.1:8081`。

**`go run .` 报错 `no required module provides package ...`**

代码中导入了新的包，但还没有下载。运行 `go mod tidy`。

**`go mod tidy` 报错 Go 版本太低**

Tenon 需要 Go 1.27.4 或更高的版本，见[准备工作](#prerequisites)。

**访问 `/ping` 得到 404**

检查地址是否写对，以及注册的 `Path` 是否是 `/ping`。路径区分大小写，`/Ping` 与 `/ping` 是两个地址。

<a id="next-steps"></a>
## 下一步

你已经写出了第一个接口，并且知道了路由器、接口描述、处理函数和中间件分别是做什么的。接下来：

- 跟着[入门教程](../tutorial/README.md)做一个完整的博客：数据库、登录、前端页面都会用到；
- 或者先用[脚手架](scaffolding.md)生成一个完整的项目，看看它由哪些部分组成；
- 想深入了解这一页用到的每一部分，读[路由](../http/routing.md)、[中间件](../http/middleware.md)和[请求生命周期](../concepts/lifecycle.md)。
