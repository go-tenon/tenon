# 服务端推送

用 Server-Sent Events（SSE）推送一次操作的进度，或者界面打开期间的通知。事件格式、心跳、重连和凭据失效时客户端看到什么，见[客户端协议的事件流](protocol.md#event-streams)；这一页讲服务端的写法。

包：`httpx`、`httpx/rest`、`broadcast`。

需要在线成员、“正在输入”或双向消息时用[实时频道](../libraries/realtime.md)。

<a id="introduction"></a>
## 示例：`rest.RegisterSSE` 和 `rest.SSE`

`api` 是 `rest.New` 创建的 API，`op` 是这个接口的 `huma.Operation`：

```go
type Tick struct {
    N int `json:"n"`
}

rest.RegisterSSE(api, op, map[string]any{"tick": Tick{}},
    func(ctx context.Context, _ *struct{}) (*huma.StreamResponse, error) {
        return rest.SSE(func(s *httpx.Stream) error {
            for n := 1; n <= 3; n++ {
                if err := s.SendJSON(httpx.Event{Name: "tick"}, Tick{N: n}); err != nil {
                    return nil // 客户端已经离开
                }
            }
            return nil
        }), nil
    })
```

- 事件表的键是事件名，值是事件数据的类型，写入 OpenAPI 文档，用于生成[客户端代码](clients.md#event-streams)。
- 返回 `rest.SSE(fn)` 之前返回的错误照常是错误响应；`fn` 开始运行后响应已经开始，它返回的错误只记录日志。要告诉客户端的错误在推送之前返回，或者作为事件发送。
- `SendJSON` 发送一行 JSON，`Send` 发送原样的文本；客户端离开后返回错误。
- 心跳、写超时、停机和凭据复查由框架处理。

| 用法 | 客户端 | 适合 |
| --- | --- | --- |
| [流式响应](#streaming-responses) | POST 等请求，操作完成后结束 | 一次操作的进度 |
| [长连接](#long-lived-connections) | GET 请求，界面打开期间保持，断开后重连 | 持续的通知、消息 |

<a id="streaming-responses"></a>
## 流式响应：推送一次操作的进度

批量发布草稿，每发布一篇推送一条进度。`PublishBatchRequest` 的请求体是 `IDs []uuid.UUID`，`PublishProgress` 有 `ID` 和 `Result` 两个字段，`a.publish` 是“发布文章”的[业务动作](../concepts/actions.md)：

```go
rest.RegisterSSE(api, op, map[string]any{"progress": PublishProgress{}},
    func(ctx context.Context, in *PublishBatchRequest) (*huma.StreamResponse, error) {
        sub, _ := identity.FromContext(ctx)
        return rest.SSE(func(s *httpx.Stream) error {
            for _, id := range in.Body.IDs {
                p := PublishProgress{ID: id, Result: "已发布"}
                if _, err := a.publish.Handle(s.Context(), sub, id); err != nil {
                    p.Result = httpx.ErrorDetail(s.Context(), err) // 按请求的语言给出错误提示
                }
                if err := s.SendJSON(httpx.Event{Name: "progress"}, p); err != nil {
                    return nil
                }
            }
            return nil
        }), nil
    })
```

- 推送中的工作用 `s.Context()`：客户端断开或服务器开始停机时它被取消。
- 浏览器的 `EventSource` 只能发 GET；单页应用用生成的客户端读取 POST 的响应流。

<a id="long-lived-connections"></a>
## 长连接：推送广播的消息

处理器订阅[广播](../libraries/broadcasting.md)的主题，收到一条推送一条，直到 `s.Done()` 关闭。`hub` 是入口创建的 `*broadcast.Hub`，其他实例上发布的消息也会送到这里。处理器返回：

```go
return rest.SSE(func(s *httpx.Stream) error {
    sub := hub.Subscribe("articles")
    defer sub.Close()
    for {
        select {
        case <-s.Done():
            return nil
        case m, ok := <-sub.C():
            if !ok {
                return nil // 订阅已结束，客户端会重连
            }
            var ev ArticlePublished
            if err := json.Unmarshal(m.Data, &ev); err != nil {
                return err
            }
            if err := s.SendJSON(httpx.Event{Name: "published", ID: ev.ID.String()}, ev); err != nil {
                return nil
            }
        }
    }
}), nil
```

`s.Done()` 在客户端断开、服务器开始停机或凭据失效时关闭，处理器必须在这时返回。

<a id="reconnecting"></a>
### 断线重连：`s.LastEventID()`

事件带 `ID` 时，客户端重连会在 `Last-Event-ID` 中发回最后收到的 ID，`s.LastEventID()` 返回它（首次连接为空）。处理器据此补发断线期间的消息，见[断线补发](../libraries/broadcasting.md#replaying-missed-events)。`httpx.Event` 的 `Retry` 建议客户端的重连间隔。

<a id="routes-and-timeouts"></a>
## 认证与超时

SSE 接口的认证与其他接口相同，例如 `rest.RequireToken(api, tokens.Validate, "articles")`。推送开始后：

- [`httpx.Timeout`](middleware.md#request-timeout) 和 `http.write_timeout` 对这个连接不再生效，流式路由可以和普通路由装在同一组中间件下。
- 服务器开始停机时 `s.Done()` 关闭，客户端重连到其他实例，停机不用等长连接，见[平滑停止](../concepts/lifecycle.md#graceful-shutdown)。

<a id="revoked-credentials"></a>
## 凭据失效：每 30 秒复查

长连接只在建立时认证。`rest.RequireToken` 登记了复查函数，框架每 30 秒重新执行 token 校验器、[`Options.Accounts`](../security/authentication.md#resolving-subjects) 中的 Resolver 和权限检查：

| 复查结果 | 连接 |
| --- | --- |
| 仍然有效 | 保持 |
| token 无效、主体或代为登录者变了、缺少所需的权限、Resolver 返回不是系统故障的错误 | 结束 |
| 暂时无法判断，例如数据库故障 | 保持，30 秒后再查 |

- 校验器有副作用时（例如 `apitoken` 延长登录 token 的有效期），长连接期间每 30 秒也发生一次；用 `identity.Rechecking(ctx)` 区分复查和新的请求。
- 自己实现认证中间件时，用 `identity.WithCredentialCheck` 登记复查函数。

<a id="immediate-revocation"></a>
### 立即断开：`broadcast.Credentials`

撤销后要立即断开时，在入口中用 Hub 创建 `broadcast.Credentials` 交给 API，`accounts` 是应用的 `identity.Accounts`：

```go
api := rest.New(router, rest.Options{Accounts: accounts, Credentials: broadcast.NewCredentials(hub)})
```

凭据变化后调用对应的方法，所有实例上相关的长连接立即复查：

| 方法 | 立即复查的连接 | 用途 |
| --- | --- | --- |
| `RecheckCredential(ctx, id)` | 使用 token `id` 的连接（主体的 `Credential`） | 退出某一台设备 |
| `RecheckSubject(ctx, ref)` | 主体 `ref` 的全部连接 | 退出其他设备、修改密码、停用账号、调整角色、移出租户 |
| `RecheckTenant(ctx, tenant)` | 租户 `tenant` 中的全部连接 | 修改角色的权限 |

“退出这台设备”的业务动作中，`a.tokens` 是 `*apitoken.Store`，`a.creds` 是由构造函数传入的 `*broadcast.Credentials`：

```go
if err := a.tokens.Revoke(ctx, subject, id); err != nil {
    return err
}
if err := a.creds.RecheckCredential(ctx, id); err != nil {
    slog.WarnContext(ctx, "recheck credential", "error", err) // 连接仍会在 30 秒内断开
}
```

- 提醒在修改提交之后发出，否则复查读到旧数据；修改在事务中时用 [AfterCommit](../database/database.md#after-commit) 登记。
- 提醒只触发一次复查，是否断开仍由复查决定。提醒丢失时连接仍在 30 秒的定期复查中断开。

<a id="plain-handlers"></a>
## 在普通 HTTP 处理器中使用：`httpx.SSE`

`rest` 之外的 `http.Handler` 用 `httpx.SSE(w, r, fn)`，`fn` 与 `rest.SSE` 的相同，心跳、超时、停机和凭据复查也相同。

<a id="deployment"></a>
## 部署：代理缓冲和 HTTP/2

- SSE 响应带 `X-Accel-Buffering: no`，nginx 不缓冲它。其他代理要对 `text/event-stream` 直接转发，否则事件会延迟或成批到达。
- HTTP/1.1 下浏览器对同一个站点最多同时打开 6 个连接，每个标签页的长连接占一个。生产环境在反向代理上启用 HTTP/2。
