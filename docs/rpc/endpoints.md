# RPC 端点

用 `natsrpc` 把业务动作提供为 NATS 上的 RPC 端点，供其他服务同步调用；Go 服务用 `natsrpc.Call` 调用其他服务的端点。同一个服务的实例自动分担请求。其他语言的调用方按 [RPC 协议](protocol.md)实现。

包：`natsrpc`、`natsrpc/rpccmd`。

<a id="example-usage"></a>
## 示例

`article` 模块提供端点 `articles.get`。业务动作 `Get` 的 `RPC` 方法写在 `get.go`，参数是结构体，标签与 REST 的请求体相同，同一个类型可以同时用于两者：

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

模块的 `rpc.go` 登记全部端点，`srv` 由 bootstrap 创建并运行，见[运行 RPC 服务](#running)：

```go
// RegisterRPC 登记文章模块的 RPC 端点。
func RegisterRPC(srv *natsrpc.Server, db *bun.DB) {
    natsrpc.Register(srv, natsrpc.Endpoint{
        Name:        "articles.get",
        Description: "按 ID 获取一篇文章",
        Permissions: []string{"articles:read"},
        Idempotent:  true,
    }, NewGet(db).RPC)
}
```

前缀为 `blog.v1` 时，其他服务调用 subject `blog.v1.articles.get`，Go 服务用 [`natsrpc.Call`](#calling)。

<a id="registering"></a>
## 用 `natsrpc.Register` 登记端点

`natsrpc.Register(srv, natsrpc.Endpoint{...}, fn)` 登记一个端点，`fn` 是业务动作的 `RPC` 方法值，见[业务动作](../concepts/actions.md#as-rpc)。登记写在模块 `rpc.go` 的 `RegisterRPC` 中，见[业务包](../concepts/structure.md#business-packages)。

- `Name` 是由 `.` 分隔的小写单词，subject 是 `natsrpc.New` 的 `Prefix` 加端点名：`blog.v1.articles.get`。不兼容的修改登记在新的前缀 `blog.v2` 中，旧前缀继续运行到调用方都迁移完。
- `Permissions` 是调用需要的全部权限，见[认证与权限](#authentication)；`Description` 出现在[服务发现](protocol.md#discovery)中。
- `Idempotent` 表示重复调用没有额外的效果；只有幂等的端点在超时后可以直接重试，见[超时与重试](protocol.md#timeouts)。
- `Public: true` 的端点不需要登录，不能声明 `Permissions`。
- 请求体不符合参数的标签时返回 `422` 和每个字段的错误，`RPC` 不会被调用。返回值编码为 JSON；返回 `struct{}` 时响应体为空。
- 名称不合法或重复、`Serve` 之后登记、有需要登录的端点却没设置 `Validate` 和 `Accounts` 时，`Register` panic。

<a id="authentication"></a>
## 认证与权限

调用方在请求头 `Authorization: Bearer` 中带 token，与 HTTP 接口用同一套令牌。`Options.Validate` 和 `Options.Accounts` 与 `rest.Options` 的同名选项相同。主体写入 `ctx`，用 `identity.FromContext` 取出。

| 情况 | 状态码 |
| --- | --- |
| 没有 token、token 无效 | `401` |
| 缺少 `Permissions` 中的权限，或凭据绑定的租户与请求的不同 | `403` |
| 校验 token 时后端出错，例如数据库不可用 | `503`，原因记在日志中 |

- **服务之间的调用**：给调用方的服务签发一个代表它的[个人访问令牌](../security/api-tokens.md#personal-access-tokens)，权限只包含它要调用的端点。转发最终用户的请求时带用户的 token，按用户的权限检查。
- **多租户**：`Options.Tenant` 从请求头取出租户，例如 `func(h nats.Header) string { return h.Get("X-Tenant") }`，见[多租户](../security/tenancy.md#identifying-tenants)。

<a id="running"></a>
## 运行 RPC 服务：`Serve`

`srv.Serve(ctx, nc)` 是进程的一个[运行部分](../concepts/dependencies.md#running-parts)。在 `internal/bootstrap` 中：

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

`a.nc` 是 `Open` 中创建的 NATS 连接，`Serve` 不关闭它；`a.languages` 是[消息目录](../general/localization.md#loading-translations)，错误提示按 `Accept-Language` 翻译；`Version` 是语义化版本。在 `Parts` 中登记后，`serve` 运行它，`serve rpc` 只运行它，见[serve 命令](../general/commands.md#serve-command)：

```go
{Name: "rpc", Short: "RPC 端点", Start: a.RPC},
```

| 选项 | 作用 | 默认 |
| --- | --- | --- |
| `Timeout` | 每次调用的处理上限，调用方的 `X-Timeout` 更短时以它为准；超时时 `ctx` 被取消，返回 `504` | 30 秒 |
| `Concurrency` | 同时处理的调用数，超过时请求在 NATS 客户端中排队 | 100 |
| `ShutdownTimeout` | 停止时等待处理中的调用，超时后取消它们的 `ctx` | 10 秒 |

单独运行 `serve rpc` 时不需要健康检查，见[进程形态](../installation/deployment.md#process-forms)。

<a id="exporting"></a>
## 导出接口描述：`rpc asyncapi`

`rpccmd.New` 提供 `rpc` 命令，把登记的端点导出为 [AsyncAPI 3.0](protocol.md#contract) 文档，交给其他语言的代码生成器。它离线运行，用零值的依赖构造服务：

```go
// DocRPC 用默认配置和没有打开任何资源的 App 构造 RPC 服务，rpc 命令用它。
func DocRPC() *natsrpc.Server {
    a := &App{cfg: config.Default[Config]()}
    return a.rpcServer()
}

// 在 cmd/blog/main.go 中
root.AddCommand(rpccmd.New(bootstrap.DocRPC))
```

`rpcServer` 见[运行 RPC 服务](#running)，`RPC` 和 `DocRPC` 都调用它，文档与运行的服务一致。登记时只保存依赖、处理请求时才使用，零值的 `App` 才能构造成功；违反时命令 panic。

```bash
go run ./cmd/blog rpc asyncapi > asyncapi.json
go run ./cmd/blog rpc endpoints   # 每个端点的 subject、权限、是否幂等
```

类型与 [OpenAPI 文档](../http/openapi.md)用同样的规则生成。`Options.Permissions` 传入权限目录（例如 `authz.Catalog`）时，文档带 `x-tenon-permissions`。

<a id="calling"></a>
## 调用其他服务：`natsrpc.Call`

类型参数是返回值的类型。`a.nc` 是 NATS 连接，`a.cfg.BlogToken` 是对方签发的令牌：

```go
ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
defer cancel()
item, err := natsrpc.Call[ArticleDTO](ctx, a.nc, "blog.v1.articles.get", GetInput{ID: id}, natsrpc.Token(a.cfg.BlogToken))
```

- 超时是 `ctx` 的剩余时间，没有截止时间时是 10 秒，并作为 `X-Timeout` 传给服务端。
- `ctx` 中的请求 ID、链路和语言随请求带上，两个服务的日志用同一个请求 ID。
- `natsrpc.Header` 设置其他请求头，例如 `X-Tenant`，名字区分大小写。
- 参数和返回值的类型由调用方定义，JSON 字段名一致即可，不引用服务方的代码。

<a id="handling-errors"></a>
### 处理调用错误

`natsrpc.Call` 返回的 `err` 把错误响应还原为 `errorx` 的错误，`errors.Is` 按错误码匹配本地定义的同名错误：

```go
var ErrArticleNotFound = errorx.Define(errorx.NotFound, "article.not_found") // 与服务端的错误码相同

switch {
case errors.Is(err, ErrArticleNotFound): // 按错误码匹配
case errors.Is(err, context.DeadlineExceeded): // 超时，包括服务端返回 504，结果不确定
case errors.Is(err, natsrpc.ErrNoResponders): // 没有在线的实例，请求没有送到
}
```

- `errorx.CodeOf`、`KindOf`、`PublicOf`、`FieldOf`、`RetryAfterOf` 照常可用；`errors.As` 取出 `*natsrpc.RemoteError`，带错误响应体和请求 ID。
- 原样返回时按分类转换为本入口的错误：对方返回 `404`，你的 HTTP 接口也返回 `404` 和同一个错误码。错误码的翻译放在自己的翻译文件中。
- `ErrNoResponders`：服务没有运行、subject 写错，或两边没连到同一套 NATS。`nats request '$SRV.INFO.blog_v1' ''` 列出在线的实例和端点。
- 服务端日志有 `rpc call did not finish in time`：加大调用方的超时，或把耗时的工作交给[队列](../background/queue.md)。

<a id="testing"></a>
## 测试端点

测试业务逻辑时直接调用 `RPC` 或 `Handle`，不需要 NATS。测试认证、参数校验和错误响应时运行 `Serve`，再用 `Call` 调用。`queuetest.Conn` 连接 `NATS_TEST_URL` 给出的测试 NATS，随机的前缀让测试互不干扰；`db` 来自 [`dbtest.Open`](../testing/testing.md#creating-test-databases)：

```go
func TestGetEndpoint(t *testing.T) {
    t.Parallel()
    nc := queuetest.Conn(t)
    srv := natsrpc.New(natsrpc.Options{Prefix: "t" + strings.ToLower(rand.Text()[:8]) + ".v1"})
    natsrpc.Register(srv, natsrpc.Endpoint{Name: "articles.get", Public: true}, article.NewGet(db).RPC)
    go srv.Serve(t.Context(), nc) // 测试结束时停止
    // ……natsrpc.Call 调用 srv.Subject("articles.get")
}
```

刚启动时订阅可能还没生效，调用得到 `natsrpc.ErrNoResponders`，先重试几次。
