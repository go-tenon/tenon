# 提供和调用端点

RPC 让其他服务经由 NATS 同步调用你的业务动作，就像调用一个 HTTP 接口，但不需要知道地址、不经过负载均衡器：同一个服务的实例自动分担请求，增减实例不需要改任何配置。本页说明怎样用 `natsrpc` 包提供端点，以及怎样从另一个 Go 服务调用它。线路上的细节见 [RPC 协议](protocol.md)，其他语言的调用方按它实现。

阅读前需要了解：[业务动作](../concepts/actions.md)；[NATS](../libraries/nats.md)；[认证](../security/authentication.md)。

- [示例用法](#example-usage)
- [提供端点](#providing-endpoints)
    - [登记端点](#registering)
    - [认证与权限](#authentication)
    - [运行](#running)
    - [导出接口描述](#exporting)
- [调用端点](#calling)
    - [处理错误](#handling-errors)
- [测试](#testing)
- [常见问题](#troubleshooting)
- [API 参考](#api-reference)

<a id="example-usage"></a>
## 示例用法

博客服务提供“获取文章”这个端点，subject 是 `blog.v1.articles.get`：

```go
srv := natsrpc.New(natsrpc.Options{
    Prefix:   "blog.v1",
    Validate: tokens.Validate,
    Accounts: identity.Accounts{user.Type: nil},
})
natsrpc.Register(srv, natsrpc.Endpoint{
    Name:        "articles.get",
    Description: "按 ID 获取一篇文章",
    Permissions: []string{"articles:read"},
    Idempotent:  true,
}, article.NewGet(db).RPC)

err := srv.Serve(ctx, nc) // 直到 ctx 被取消
```

另一个服务调用它：

```go
item, err := natsrpc.Call[ArticleDTO](ctx, nc, "blog.v1.articles.get", GetInput{ID: id}, natsrpc.Token(token))
```

`nc` 是两边各自用 [`natsx.Connect`](../libraries/nats.md#connecting) 创建的连接，连到同一套 NATS 的同一个账号即可，不需要知道对方的地址；对方在另一个账号或另一套 NATS 上时，见[连接另一个账号或另一套 NATS](../libraries/nats.md#other-accounts)。

<a id="providing-endpoints"></a>
## 提供端点

<a id="registering"></a>
### 登记端点

和其他入口一样，业务动作为 RPC 写一个入口方法 `RPC`。签名是 `func(ctx, In) (Out, error)`，参数类型的标签与 REST 的请求体相同，同一个类型可以同时用于两者：

```go
type GetInput struct {
    ID uuid.UUID `json:"id" format:"uuid" doc:"文章 ID"`
}

// RPC 处理端点 articles.get。
func (a *Get) RPC(ctx context.Context, in GetInput) (*ArticleDTO, error) {
    subject, _ := identity.FromContext(ctx)
    item, err := a.Handle(ctx, subject, in.ID)
    if err != nil {
        return nil, err
    }
    dto := toDTO(item)
    return &dto, nil
}
```

`natsrpc.Register` 把它登记为一个端点：

- **`Name`** 是端点名，由 `.` 分隔的小写单词，例如 `articles.get`。subject 是前缀加端点名：`blog.v1.articles.get`。
- **`Description`** 说明端点做什么，出现在[服务发现](protocol.md#discovery)中。
- **`Permissions`** 是调用它需要的全部权限，见下一节。
- **`Idempotent`** 表示用相同的参数重复调用没有额外的效果，例如查询、按 ID 设置某个值。调用方超时之后，只有幂等的端点可以直接重试，见[超时与重试](protocol.md#timeouts)。

请求体不符合 `In` 的标签时（例如缺少必填字段、字符串太长），端点返回 `422` 和每个字段的错误，`RPC` 方法不会被调用。`Out` 编码为 JSON 作为响应体；没有返回值的端点用 `struct{}`，响应体为空。

**前缀**由 `natsrpc.New` 的 `Prefix` 决定，通常是应用名加 API 的版本，例如 `blog.v1`。参数或返回值有不兼容的修改时，把修改后的端点登记在新的前缀（`blog.v2`）中，旧的继续运行，直到调用方都迁移完。

<a id="authentication"></a>
### 认证与权限

调用方在请求头 `Authorization: Bearer` 中带 token，与 HTTP 接口用同一套令牌和同一个校验器。`Options.Validate` 校验 token，`Options.Accounts` 是接受的账号体系，和 `rest.Options` 中的同名选项相同。端点的 `Permissions` 用 `identity.Subject.Can` 检查：

- 没有 token、token 无效：`401`；
- 缺少权限，或者凭据绑定的租户与请求的不同：`403`；
- 校验 token 时后端出错，例如数据库不可用：`503`，原因记录在日志中。

主体写入 `ctx`，`RPC` 方法中用 `identity.FromContext` 取出，与其他入口相同。

**服务之间的调用**：给调用方的服务签发一个代表它的[个人访问令牌](../security/api-tokens.md)，权限只包含它需要调用的端点；调用方把它配置为密钥，每次调用时带上。转发最终用户的请求时，直接带上用户的 token，按用户的权限检查。

不需要登录的端点设置 `Public: true`，不能再声明 `Permissions`。

多租户应用用 `Options.Tenant` 从请求头中取出租户，例如 `func(h nats.Header) string { return h.Get("X-Tenant") }`，见[多租户](../security/tenancy.md#identifying-tenants)。

<a id="running"></a>
### 运行

`srv.Serve(ctx, nc)` 是进程的一个[运行部分](../concepts/dependencies.md#running-parts)，和 HTTP 服务、队列 worker 一样装配：

```go
// rpcServer 创建 RPC 服务并登记全部端点。它只保存 App 的字段、不使用它们，所以 DocRPC 也能调用它。
func (a *App) rpcServer() *natsrpc.Server {
    srv := natsrpc.New(natsrpc.Options{
        Prefix:    "blog.v1",
        Version:   "1.4.0",
        Validate:  a.tokens.Validate,
        Accounts:  identity.Accounts{user.Type: nil},
        Languages: a.languages,
    })
    article.RegisterRPC(srv, a.db)
    return srv
}

// RPC 装配 RPC 服务，返回的函数提供端点，直到 ctx 被取消。
func (a *App) RPC(*command.Daemon) (func(context.Context) error, error) {
    srv := a.rpcServer()
    return func(ctx context.Context) error { return srv.Serve(ctx, a.nc) }, nil
}
```

`a.nc` 是 `Open` 中创建的 NATS 连接，`Serve` 不关闭它；`a.tokens` 是 [apitoken.Store](../security/api-tokens.md)，`a.languages` 是[消息目录](../general/localization.md#loading-translations)，设置后错误提示按请求头 `Accept-Language` 翻译。`Version` 是应用的版本号（语义化版本），出现在服务发现中。`article.RegisterRPC` 是 `article` 模块中登记它的全部端点的函数，写在模块的 `rpc.go` 中，与登记 HTTP 路由的 `RegisterRoutes`（`routes.go`）并列，见[业务包](../concepts/structure.md#business-packages)。

最后把它登记为 `Parts` 中的一个部分，`serve` 就会运行它，`serve rpc` 只运行它，见[serve：运行常驻进程](../general/commands.md#serve-command)：

```go
{Name: "rpc", Short: "RPC 端点", Start: a.RPC},
```

`Serve` 做的事情：

- 每个端点订阅自己的 subject，同一个服务的所有实例在同一个队列组中，每个请求只交给其中一个实例。
- 每个请求在自己的 goroutine 中处理，同时最多 `Concurrency` 个（默认 100）。
- 每次调用的处理时间不超过调用方的 `X-Timeout` 和 `Options.Timeout`（默认 30 秒）中较短的一个，超过时 `ctx` 被取消，调用方收到 `504`。
- 请求 ID、链路（`traceparent`）和语言从请求头中取出，放进 `ctx`，日志和数据库查询都带着它们。
- 收到停止信号时先不再接收新的请求，处理完已经收到的；最多等待 `ShutdownTimeout`（默认 10 秒）。

RPC 可以和 HTTP 服务运行在同一个进程中，也可以单独运行（`serve rpc`），单独运行时不需要健康检查，见[一个进程，还是每个部分一个进程](../installation/deployment.md#process-forms)。

<a id="exporting"></a>
### 导出接口描述

调用方用其他语言编写时，需要知道每个端点的参数和返回值。`rpccmd.New` 提供 `rpc` 命令，把登记的端点导出为 [AsyncAPI 3.0](protocol.md#contract) 文档，交给对方的代码生成器。和 HTTP 接口的 `openapi` 命令一样，它离线运行，用零值的依赖构造 Server，只读取登记的端点：

```go
// DocRPC 用默认配置和没有打开任何资源的 App 构造 RPC 服务，rpc 命令用它。
func DocRPC() *natsrpc.Server {
    a := &App{cfg: config.Default[Config]()}
    return a.rpcServer()
}

// 在 cmd/blog/main.go 中
root.AddCommand(rpccmd.New(bootstrap.DocRPC))
```

`rpcServer` 见上一节，`RPC` 和 `DocRPC` 都调用它，所以导出的文档总是与运行的服务一致。登记端点时只保存依赖，处理请求时才使用，所以用零值的 `App` 也能构造成功。

```bash
go run ./cmd/blog rpc asyncapi > asyncapi.json
go run ./cmd/blog rpc endpoints
```

`rpc endpoints` 列出每个端点的 subject、需要的权限和是否幂等，用于检查暴露了哪些端点。

参数和返回值的类型在文档的 `components.schemas` 中，与 [OpenAPI 文档](../http/openapi.md)的规则相同：一个类型同时用于 HTTP 接口和 RPC 端点时，两份文档中的定义完全相同。`Options.Permissions` 传入应用的权限目录（例如 `authz.Catalog`）时，文档中带有 `x-tenon-permissions`，与 OpenAPI 文档相同。

<a id="calling"></a>
## 调用端点

另一个 Go 服务用 `natsrpc.Call` 调用端点，类型参数是返回值的类型：

```go
ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
defer cancel()
item, err := natsrpc.Call[ArticleDTO](ctx, a.nc, "blog.v1.articles.get", GetInput{ID: id}, natsrpc.Token(a.cfg.BlogToken))
```

- **超时**就是 `ctx` 的剩余时间，没有截止时间时是 10 秒；剩余时间同时作为 `X-Timeout` 告诉服务端，服务端不会在调用方放弃之后继续处理。
- **请求 ID、链路和语言**从 `ctx` 中取出，随请求带上：一次请求和它引起的远程调用共用同一个请求 ID，在两个服务的日志中都能找到。
- `natsrpc.Token` 设置 `Authorization`；`natsrpc.Header` 设置其他请求头，例如 `X-Tenant`。

调用方不需要引用服务方的代码：参数和返回值的类型可以自己定义，字段的 JSON 名字一致即可。调用别人的服务时，按对方提供的接口描述生成类型。

<a id="handling-errors"></a>
### 处理错误

`Call` 把服务端的错误响应还原为 `errorx` 的错误，处理方式和本地的业务错误相同：

```go
var ErrArticleNotFound = errorx.Define(errorx.NotFound, "article.not_found") // 与服务端的错误码相同

item, err := natsrpc.Call[ArticleDTO](ctx, a.nc, "blog.v1.articles.get", GetInput{ID: id}, natsrpc.Token(token))
switch {
case errors.Is(err, ErrArticleNotFound):
    // 按错误码匹配，服务端是什么语言都一样
case errors.Is(err, context.DeadlineExceeded):
    // 超时（包括服务端返回 504），结果不确定
case errors.Is(err, natsrpc.ErrNoResponders):
    // 没有在线的实例，请求没有送到
case err != nil:
    return err
}
```

- `errorx.CodeOf(err)` 是服务端的错误码，`errorx.KindOf(err)` 是状态码对应的分类，`errorx.PublicOf`、`errorx.RetryAfterOf` 也照常可用。
- `errors.As` 可以取出 `*natsrpc.RemoteError`，其中有完整的错误响应和请求 ID。
- 直接返回这个错误时，它按分类转换为你的入口的错误：例如服务端返回 `404`，你的 HTTP 接口也返回 `404`，带着同一个错误码。错误码的翻译要在你自己的翻译文件中提供。

<a id="testing"></a>
## 测试

`RPC` 方法是普通的函数，测试业务逻辑时直接调用它，或者调用 `Handle`，不需要 NATS。

需要测试整个端点时（认证、参数校验、错误响应），在测试中运行 `Serve`，再用 `Call` 调用。测试 NATS 的地址由环境变量 `NATS_TEST_URL` 给出，每个测试使用随机的前缀，测试之间互不干扰：

```go
func TestGetEndpoint(t *testing.T) {
    nc := queuetest.Conn(t)
    srv := natsrpc.New(natsrpc.Options{Prefix: "test" + strings.ToLower(rand.Text()[:8]) + ".v1"})
    natsrpc.Register(srv, natsrpc.Endpoint{Name: "articles.get", Public: true}, article.NewGet(db).RPC)
    ctx, cancel := context.WithCancel(context.Background())
    defer cancel()
    go srv.Serve(ctx, nc)
    // ……natsrpc.Call 调用 srv.Subject("articles.get")
}
```

`Serve` 在后台订阅，刚启动时的调用可能得到 `natsrpc.ErrNoResponders`，测试中先重试几次，等它能应答。

<a id="troubleshooting"></a>
## 常见问题

**调用总是返回 `natsrpc.ErrNoResponders`**

subject 上没有在线的实例：服务没有运行，subject 写错了，或者两边没有连到同一套 NATS。用 `nats request '$SRV.INFO.blog_v1' ''` 查看服务的实例和端点，服务名是前缀中的 `.` 换成 `_`。

**调用返回 `403`，权限明明有**

凭据绑定了租户，而请求没有带租户，或者带的租户不同。检查 `Options.Tenant` 和调用方的 `X-Tenant`。

**调用经常超时，服务端日志中有 `rpc call did not finish in time`**

处理超过了调用方的超时或 `Options.Timeout`。加大调用方的超时，或者把耗时的工作交给[队列](../background/queue.md)，端点只返回任务已接受。

**请求头读不到**

NATS 的请求头区分大小写：`X-Request-ID` 不能写成 `x-request-id`，见[请求](protocol.md#requests)。

<a id="api-reference"></a>
## API 参考

包 `github.com/go-tenon/tenon/natsrpc`。

### New

```go
func New(opts Options) *Server
```

创建没有端点的服务。`Prefix` 不合法、`Version` 不是语义化版本或 `Accounts` 不合法时 panic。

| 选项 | 说明 |
| --- | --- |
| `Prefix string` | 端点 subject 的前缀，例如 `blog.v1`，必填 |
| `Version string` | 服务的版本，语义化版本，默认 `0.0.0` |
| `Description string` | 说明这个 API，出现在服务发现和 AsyncAPI 文档中 |
| `Permissions []identity.Permission` | 应用的权限目录，写入 AsyncAPI 文档的 `x-tenon-permissions` |
| `Validate identity.TokenValidator`、`Accounts identity.Accounts` | 校验 token 和接受的账号体系；有需要登录的端点时必填 |
| `Tenant func(h nats.Header) string` | 从请求头中取出租户 |
| `Languages *goi18n.Bundle` | 消息目录，按 `Accept-Language` 翻译错误提示 |
| `Timeout time.Duration` | 每次调用的处理上限，默认 30 秒 |
| `Concurrency int` | 同时处理的调用数上限，默认 100 |
| `ShutdownTimeout time.Duration` | 停止时等待处理中的调用的最长时间，默认 10 秒 |
| `Logger *slog.Logger` | 记录内部错误，默认 `slog.Default()` |

### Register

```go
func Register[I, O any](s *Server, e Endpoint, fn func(context.Context, I) (O, error))
```

把 `fn` 登记为端点 `e`。`I` 必须是结构体。名称不合法或重复、`fn` 为 `nil`、在 `Serve` 之后登记、需要登录却没有设置 `Validate` 和 `Accounts`，或者 `Public` 的端点声明了 `Permissions` 时 panic。

| 字段 | 说明 |
| --- | --- |
| `Name string` | 端点名，例如 `articles.get` |
| `Description string` | 说明，出现在服务发现中 |
| `Public bool` | 不需要登录 |
| `Permissions []string` | 调用它需要的全部权限 |
| `Idempotent bool` | 重复调用没有额外的效果，超时后可以重试 |

### Server

```go
func (s *Server) Serve(ctx context.Context, nc *nats.Conn) error
func (s *Server) Endpoints() []Endpoint
func (s *Server) Subject(name string) string
func (s *Server) AsyncAPI() ([]byte, error)
```

`Serve` 提供全部端点，直到 `ctx` 被取消，然后平滑停止并返回 `nil`，订阅失败时返回错误。`Endpoints` 返回登记的端点，按名称排序；`Subject` 返回端点的 subject；`AsyncAPI` 返回描述全部端点的 AsyncAPI 3.0 文档（JSON）。

### rpccmd.New

```go
func New(server func() *natsrpc.Server) *cobra.Command
```

包 `github.com/go-tenon/tenon/natsrpc/rpccmd`。返回 `rpc` 命令，子命令 `asyncapi` 和 `endpoints`，见[导出接口描述](#exporting)。

### Call

```go
func Call[Out any](ctx context.Context, nc *nats.Conn, subject string, in any, opts ...CallOption) (Out, error)
func Token(token string) CallOption
func Header(key, value string) CallOption
```

调用 `subject` 上的端点，见[调用端点](#calling)和[处理错误](#handling-errors)。

### 错误

| 错误 | 含义 |
| --- | --- |
| `ErrNoResponders` | 没有在线的实例，分类 `Unavailable`，错误码 `rpc.no_responders` |
| `*RemoteError` | 服务端返回的错误响应：`Subject`、`Problem`（错误响应体）、`RequestID` |
