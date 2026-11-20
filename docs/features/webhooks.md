# Webhook

发送 webhook：租户配置端点（接收地址和订阅的事件），业务动作发送事件，worker 签名、投递、重试并留下投递记录。接收 webhook：用 `Verify` 校验签名。签名遵循 [Standard Webhooks](https://www.standardwebhooks.com/)。

包：`webhook`。

- `Send` 在业务事务中用 `tx` 调用：事务提交后才投递，回滚时一起取消。
- 投递至少一次，不保证顺序：接收方按 `webhook-id` 去重，按 `timestamp` 判断先后。

<a id="example-usage"></a>
## 示例

```go
var Published = webhook.Event[PublishedHook]{Name: "article.published", Summary: "文章发布后推送"}

// 在业务事务中发送，提交后由 worker 签名并投递给订阅了它的端点
err := database.RunInTx(ctx, db, nil, func(ctx context.Context, tx database.Tx) error {
    // ……发布文章
    return Published.Send(ctx, tx, subject, PublishedHook{ID: item.ID, Title: item.Title})
})

// worker 进程中注册投递
webhook.NewDeliverer(db, webhook.Options{}).Handle(w)
```

`db` 是入口打开的 `*bun.DB`，`subject` 是执行动作的 `identity.Subject`，`w` 是 `*queue.Worker`。

<a id="installation"></a>
## 装配

1. 把 `queue.Migrations()` 和 `webhook.Migrations()` 加入[发布命令](../database/migrations.md#publishing-framework-migrations)，发布后执行迁移：投递任务走[队列](../background/queue.md)。
2. 在 worker 中注册 `Deliverer`（[运行投递](#delivering)），用 `Document` 写入接口文档（[写入接口文档](#documenting)），定时删除旧的投递记录（[保留期限](#retention)）。

<a id="sending"></a>
## 用 `Event.Send` 发送事件

事件先声明再发送，声明放在模块的 `webhooks.go` 中。在[业务动作](../concepts/actions.md#writing-actions)的事务 `tx` 中调用 `Send`，`subject` 是执行动作的主体，`item` 是事务中更新的文章：

```go
// PublishedHook 是 article.published 事件的内容，字段一旦发布就保持稳定。
type PublishedHook struct {
    ID          uuid.UUID `json:"id"`
    Title       string    `json:"title"`
    PublishedAt time.Time `json:"published_at"`
}

var Published = webhook.Event[PublishedHook]{Name: "article.published", Summary: "文章发布后推送"}

// 业务事务中
err := Published.Send(ctx, tx, subject, PublishedHook{ID: item.ID, Title: item.Title, PublishedAt: *item.PublishedAt})
```

- `Send` 为 `subject.Tenant` 中每个订阅了这个事件的端点写一条投递记录，并在同一个事务中发布投递任务。没有端点订阅时什么也不写。
- 事件内容用专门的结构体，不直接用数据库模型：模型新加的字段会发给所有租户，改名会破坏他们的集成。

<a id="request-format"></a>
### 请求的格式

每个端点收到一个 `POST` 请求（签名为示意）：

```http
POST /hooks HTTP/1.1
Content-Type: application/json
webhook-id: 0193a5c2-7d1e-7b3a-9f61-2c4e8d0a1b2c
webhook-timestamp: 1767225600
webhook-signature: v1,K5oZfzN95Z9UVu1EsfQmfVNQhnkZ2pj9o9NDN/H/pI4=

{"type":"article.published","timestamp":"2026-01-01T00:00:00Z","data":{"id":"…","title":"…","published_at":"…"}}
```

- `webhook-id` 是事件 ID：同一个事件发给不同端点、重试多少次都相同，接收方用它去重。
- `webhook-timestamp` 是这次发送的 Unix 秒。
- `webhook-signature` 是 `v1,` 加上用端点密钥对 `id.timestamp.body` 计算的 HMAC-SHA256（base64）；[轮换密钥](#rotating-secrets)期间有两个，用空格分隔。
- 任何 2xx 响应表示收到。

<a id="documenting"></a>
### 用 `Document` 写入接口文档

在构造路由的 `App.Router`（见[依赖装配](../concepts/dependencies.md#running-parts)）中，注册完路由之后列出全部事件，[openapi 命令](../general/commands.md#openapi-command)导出的文档就在 `webhooks` 中描述它们。`api` 是 `rest.New` 创建的 API：

```go
webhook.Document(api.OpenAPI(), article.Published, article.Deleted)
```

- 新增事件时加到这里；事件名不合法或重复时 `Document` panic。
- 每个事件是一个 `POST` 请求：请求体 `{type, timestamp, data}`，`data` 的 schema 名称规则与[接口的类型](../http/routing.md#shared-responses)相同，请求头是三个签名请求头。租户可以用 [openapi-typescript](../frontend/client-react.md#other-ts-tools) 等工具生成事件的类型。
- OpenAPI 3.0 没有 `webhooks`：`openapi --spec 3.0` 和 `/openapi-3.0.json`、`/openapi-3.0.yaml` 不含它们。
- 事件内容是租户依赖的接口，修改前用 oasdiff 检查，见[检查接口变更](../http/clients.md#checking-changes)。

<a id="endpoints"></a>
## 管理端点

端点属于租户，由租户管理员在设置页中维护，路由用 `webhooks:manage` 这类权限保护。`db` 是 `bun.IDB`，`sub` 是 `identity.FromContext(ctx)` 取出的当前主体：

| 操作 | 调用 |
| --- | --- |
| 列出端点 | `webhook.ListEndpoints(ctx, db, sub.Tenant)` |
| 创建端点 | `webhook.CreateEndpoint(ctx, db, sub.Tenant, webhook.EndpointInput{URL, Events, Description})` |
| 查看、修改、停用 | `webhook.GetEndpoint`、`webhook.UpdateEndpoint`（`Disabled: true` 停用） |
| 删除端点和它的投递记录 | `webhook.DeleteEndpoint(ctx, db, sub.Tenant, id)` |

- 租户只取自 `sub.Tenant`，与[成员管理](../security/tenancy.md#managing-members)相同：其他租户的端点 ID 返回 `webhook.ErrEndpointNotFound`（404）。
- `Events` 是订阅的事件：完整的事件名如 `article.published`，以 `.*` 结尾的前缀如 `article.*`，或者 `*` 表示全部。
- 地址不是 http 或 https 的绝对地址时返回 `webhook.ErrInvalidURL`，`Events` 为空或写法不合法时返回 `webhook.ErrInvalidEvents`，响应都是 422。
- 可订阅的事件由应用在模块中列出，例如 `var WebhookEvents = []string{"article.published", "article.deleted"}`，订阅表单用它显示选项、校验提交。
- 创建时生成的 `Secret`（`whsec_` 开头）在端点详情页展示给租户，他们用它校验签名。

<a id="rotating-secrets"></a>
### 用 `RotateSecret` 轮换密钥

密钥泄露或定期更换时调用 `webhook.RotateSecret(ctx, db, sub.Tenant, id)`。之后 24 小时内，每个请求同时带有新旧两个密钥的签名：租户先换上新密钥，旧密钥到期后自动失效，切换期间的请求都能通过校验。

<a id="deliveries"></a>
### 投递记录与重发

端点详情页列出最近的投递，租户修好接收程序后重发失败的投递。`a.db` 是 `*bun.DB`，`id` 是端点 ID，`before` 是上一页最后一条记录的 ID：

```go
list, err := webhook.ListDeliveries(ctx, a.db, webhook.DeliveryQuery{Tenant: sub.Tenant, EndpointID: id, Before: before})
d, err := webhook.Resend(ctx, a.db, sub.Tenant, deliveryID)
```

- 每条记录有状态（`pending` 等待或重试中、`succeeded`、`failed`）、已发送的次数、最后一次的状态码和失败原因（含响应体的开头）。`DeliveryQuery` 还可以按 `EventID`、`Status` 筛选，`Limit` 默认 50。
- `Resend` 以相同的事件 ID 和请求体写入一条新记录并发布投递任务，原记录不变。

<a id="delivering"></a>
## 运行投递

在 [worker](../background/queue.md#running-workers) 中注册，`a.db` 是 `*bun.DB`，`w` 是 `*queue.Worker`：

```go
webhook.NewDeliverer(a.db, webhook.Options{}).Handle(w)
```

`webhook.Options` 的零值可用：每次请求超时 10 秒（`Timeout`），每次投递最多请求 16 次、最后一次约在 2 天后（`MaxAttempts`，不受 `queue` 段的 `max_attempts` 影响）。

<a id="retries"></a>
### 重试

| 端点的响应 | 结果 |
| --- | --- |
| 2xx | 投递成功 |
| 其他状态码（包括 3xx）、超时、连接失败 | 按队列的[退避时间](../background/queue.md#failures-and-retries)重试 |
| 429、503 带有 `Retry-After`（秒数） | 按它等待后重试 |
| 最后一次仍然失败；端点已停用或已删除；地址是内网地址 | 投递记录标记为 `failed` |

失败的投递只记在投递记录中，不进入[失败任务](../background/queue.md#failed-jobs)：接收方出错属于对方的系统，告警保持安静。

<a id="private-addresses"></a>
### 拒绝内网地址

端点地址由租户填写，可能指向 `169.254.169.254` 或内网服务。`Deliverer` 默认使用 [outbound](../libraries/outbound.md) 的客户端（端口不限），内网、回环等地址在连接之前被拒绝。本地开发时接收方在本机，传入放行回环地址的客户端：

```go
client := outbound.New(outbound.Options{
    AnyPort: true,
    Allow:   []netip.Prefix{netip.MustParsePrefix("127.0.0.1/32"), netip.MustParsePrefix("::1/128")},
})
webhook.NewDeliverer(a.db, webhook.Options{Client: client}).Handle(w)
```

无论传入什么客户端，投递都不跟随重定向，`3xx` 视为失败。

<a id="receiving"></a>
## 用 `Verify` 接收 webhook

按 Standard Webhooks 发送的服务（例如 Resend）用 `webhook.Verify` 校验。校验通过后把内容交给队列处理，立即返回 2xx。`h.secret` 是对方给的密钥，`h.db` 是 `*bun.DB`：

```go
// EmailEvent 是收到的邮件事件，由任务处理。
var EmailEvent = queue.Topic[Inbound]("resend.event")

type Inbound struct {
    ID   string          `json:"id"` // webhook-id，任务按它去重
    Body json.RawMessage `json:"body"`
}

func (h *Handlers) ResendWebhook(w http.ResponseWriter, r *http.Request) {
    body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
    if err != nil || webhook.Verify(h.secret, r.Header, body) != nil {
        http.Error(w, "invalid request", http.StatusUnauthorized)
        return
    }
    in := Inbound{ID: r.Header.Get(webhook.HeaderID), Body: body}
    if err := EmailEvent.Publish(r.Context(), h.db, in); err != nil {
        http.Error(w, "try again", http.StatusServiceUnavailable) // 对方会重发
        return
    }
    w.WriteHeader(http.StatusNoContent)
}
```

- 签名针对原始请求体：先读出原始字节校验，再解析 JSON。`Verify` 还拒绝与当前时间相差超过 5 分钟的时间戳。
- 同一个事件可能收到多次：任务按 `webhook-id` 去重，例如插入以它为主键的表，冲突时跳过。
- 处理器只校验并发布任务，业务逻辑放在任务中：对方等待超时也会重发。路由直接注册在 chi 上，由签名确认来源。

不按这个规范签名的服务用它们自己 SDK 中的校验函数，流程不变：Stripe 是 `stripe-go` 的 `webhook.ConstructEvent`（包名相同，导入时起别名），GitHub 是 `go-github` 的 `github.ValidatePayloadFromBody`。

<a id="testing"></a>
## 测试

测试发送端时用 `httptest.Server` 作为端点、`webhook.Verify` 检查收到的请求，本机地址按[拒绝内网地址](#private-addresses)放行。测试接收端时用 `webhook.Sign` 构造签名，`secret` 是处理器的密钥：

```go
body := `{"type":"email.delivered","timestamp":"2026-01-01T00:00:00Z","data":{}}`
now := time.Now()
sig, err := webhook.Sign(secret, "msg_1", now, []byte(body))
if err != nil {
    t.Fatal(err)
}
req := httptest.NewRequest(http.MethodPost, "/hooks/resend", strings.NewReader(body))
req.Header.Set(webhook.HeaderID, "msg_1")
req.Header.Set(webhook.HeaderTimestamp, strconv.FormatInt(now.Unix(), 10))
req.Header.Set(webhook.HeaderSignature, sig)
```

<a id="retention"></a>
## 保留期限

`webhook.Cleanup` 返回删除保留期以前的投递记录的清理函数，交给[定时任务](../background/scheduling.md#framework-cleanups)。`sched` 是入口中的 `*schedule.Scheduler`，`a.db` 是 `*bun.DB`：

```go
sched.Add("webhooks:cleanup", "0 4 * * *", schedule.Cleanup(webhook.Cleanup(a.db, 30*24*time.Hour), 1000))
```
