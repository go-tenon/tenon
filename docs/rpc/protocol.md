# RPC 协议

其他服务经由 NATS 调用 Tenon 应用的业务动作时的线路规范：subject、请求、响应、错误、超时与重试、服务发现和接口描述。任何语言的调用方都按本页实现，`natsrpc.Call` 是其中的一种实现。

包：`natsrpc`。

RPC 建立在 NATS 的[服务协议](https://github.com/nats-io/nats-architecture-and-design/blob/main/adr/ADR-32.md)（Service API）之上：负载均衡、服务发现和错误响应头沿用它，Tenon 补充本页规定的部分。数据格式、错误模型和认证与 [HTTP 接口](../http/protocol.md)相同。

<a id="calling"></a>
## 调用方式

一次调用是一个 NATS 请求：调用方向端点的 subject 发布一条带回复地址（reply subject）的消息，服务的一个实例处理后把响应发到回复地址。用 [nats 命令行工具](https://github.com/nats-io/natscli)调用：

```bash
nats request blog.v1.articles.get '{"id":"0198f6c2-7d41-7c3e-9a51-2c4b6f0e8a10"}' \
  -H 'Authorization:Bearer eyJhbGciOi…' -H 'X-Request-ID:9F2KQ1' --timeout 5s
```

成功时响应体是返回值的 JSON；失败时响应带 `Nats-Service-Error-Code` 头，响应体是错误说明，见[错误](#errors)。

同一个服务的所有实例订阅同一个队列组 `q`，每个请求只交给其中一个实例，实例可以随时增减。没有实例在线时，NATS 立即回复 no responders（状态码 `503` 的空消息），调用方不必等到超时。没有回复地址的请求被丢弃。

<a id="subjects"></a>
## 端点与 subject

端点对应一个业务动作，名字由 `.` 分隔的小写单词组成，例如 `articles.get`。端点的 subject 是 API 的前缀加端点名：

```text
blog.v1.articles.publish
└──┬──┘ └──────┬───────┘
  前缀       端点名
```

- 前缀由应用决定，通常是应用名加 API 版本，例如 `blog.v1`。一个应用可以有多个 API，按前缀区分。
- 不兼容的修改通过新的前缀发布，例如 `blog.v2`；旧前缀继续服务已上线的调用方，直到它们都迁移完。只增加可选字段、增加新的端点是兼容的修改，不需要新的前缀。
- 前缀和端点名的每一段由小写字母、数字、`_` 和 `-` 组成，不能为空，不能用通配符 `*` 和 `>`。

服务协议中，每个 API 是一个服务：服务名是前缀中的 `.` 换成 `_`，例如 `blog_v1`；端点在服务中的名字是端点名中的 `.` 换成 `_`，例如 `articles_publish`。调用只用 subject，这两个名字只出现在[服务发现](#discovery)和[接口描述](#contract)中。

<a id="requests"></a>
## 请求

请求体是 UTF-8 的 JSON 对象，字段由端点的[接口描述](#contract)规定；没有参数的端点，请求体是 `{}` 或为空（空和 `null` 按 `{}` 处理）。时间是 RFC 3339 字符串，例如 `2026-11-11T08:00:00Z`。

| 请求头 | 说明 |
| --- | --- |
| `Authorization: Bearer <token>` | 需要登录的端点用它认证。token 与 HTTP 接口相同：登录 token、个人访问令牌或 OAuth 令牌，见[会话](../http/protocol.md#sessions) |
| `Accept-Language` | 错误提示的语言，例如 `zh-CN`、`en`；不支持的语言使用应用的默认语言 |
| `X-Request-ID` | 可选，1–128 个不含空格的可打印 ASCII 字符。合法时服务端沿用它，否则生成一个 |
| `X-Timeout` | 可选，调用方还愿意等待的毫秒数（正整数），例如 `5000`，见[超时与重试](#timeouts)。不合法时忽略 |
| `traceparent`、`tracestate` | 可选，[W3C Trace Context](https://www.w3.org/TR/trace-context/)，服务端的处理接在调用方的链路之后 |

多租户应用按应用的约定从请求头（例如 `X-Tenant`）确定租户，见[多租户](../security/tenancy.md#identifying-tenants)。

> [!IMPORTANT]
> NATS 的请求头区分大小写，名字要和上表完全一致：`X-Request-ID` 不能写成 `x-request-id`。

服务之间的调用同样用 `Authorization: Bearer` 认证：给调用方的服务签发一个代表它的个人访问令牌，权限只包含它要调用的端点，见[API 令牌](../security/api-tokens.md#personal-access-tokens)。转发最终用户的请求时带用户的 token，服务端按用户的权限检查。NATS 的账号和权限决定谁能向哪些 subject 发布消息，可以作为额外的一层保护，不代表调用者的身份。

<a id="responses"></a>
## 响应

成功的响应没有 `Nats-Service-Error-Code` 头，响应体是端点返回值的 JSON；没有返回值的端点，响应体为空。

| 响应头 | 说明 |
| --- | --- |
| `X-Request-ID` | 这次调用的 ID，与服务端日志关联。报告问题时提供它 |
| `Content-Language` | 响应文案使用的语言；应用配置了消息目录时才有 |
| `Retry-After` | 建议的等待秒数，出现在 `429` 和部分 `503` 中 |

<a id="errors"></a>
## 错误

失败的响应带服务协议规定的两个响应头，响应体与 HTTP 接口的错误响应相同，是 [Problem Details](https://www.rfc-editor.org/rfc/rfc9457) 格式的 JSON：

| 响应头 | 说明 |
| --- | --- |
| `Nats-Service-Error-Code` | 状态码，十进制字符串，例如 `409`，含义与 HTTP 状态码相同 |
| `Nats-Service-Error` | 状态码的标准描述，例如 `Conflict` |

```json
{"title": "Conflict", "status": 409, "detail": "slug“hello-world”已被使用", "code": "article.slug_taken", "params": {"slug": "hello-world"}}
```

字段的含义和判断顺序与[客户端协议的错误](../http/protocol.md#errors)相同：有 `code` 时按 `code` 处理；有 `errors` 时把每一项的 `message` 显示在对应字段旁边；两者都没有时按 `status` 处理。`detail` 按 `Accept-Language` 翻译，不用于判断。字段错误的 `location` 是 `body.` 加请求体中的字段路径，例如 `body.items[0].name`。业务错误用 `errorx.Field` 指出了字段时，`errors` 中有这个字段的一项，它的 `code` 是业务错误码；Go 的调用方用 `errorx.FieldOf` 取回这个字段。

状态码与[错误的分类](../general/errors.md#status-mapping)一一对应：

| 状态码 | 含义 |
| --- | --- |
| `400` | 请求体不是合法的 JSON |
| `401` | 没有 token、token 无效或已过期 |
| `403` | 已登录但没有权限，或凭据绑定的租户与请求的租户不同 |
| `404` | 资源不存在，或无权知道它存在 |
| `409` | 与当前状态冲突 |
| `422` | 字段校验失败，或业务规则不允许 |
| `429` | 调用过于频繁，带 `Retry-After` |
| `500` | 服务端的意外故障，`detail` 不含内部信息，用 `X-Request-ID` 查日志 |
| `503` | 服务端依赖的服务暂时不可用（包括校验 token 的后端），稍后重试 |
| `504` | 处理超过了时限（调用方的 `X-Timeout` 或服务端的上限），或服务停止时没能及时完成。服务端可能已处理了一部分，按超时处理 |

调用方还会遇到两种不经过服务端代码的失败：

- **no responders**：subject 上没有在线的实例，NATS 回复状态码 `503` 的空消息。服务没有运行，或 subject 写错了。
- **超时**：在调用方设定的时间内没有收到响应。服务端可能还在处理，也可能已处理完、响应丢了。

缺少响应体或响应体不是 JSON 的错误（例如服务协议的其他实现返回的）只看两个响应头。

<a id="timeouts"></a>
## 超时与重试

每次调用都要设定超时。调用方在 `X-Timeout` 中告诉服务端还愿意等多久，服务端从收到请求时起算，超过就取消处理并返回 `504`。服务端自己也有处理时间的上限，`X-Timeout` 更长时以上限为准。

一次调用最多送达一次：NATS 不保存请求，服务端处理完之前崩溃或响应丢失时，调用方只看到超时，无法知道处理是否完成。所以：

- 失败后可以直接重试的只有 no responders、`429` 和 `503`：前者说明请求没有送到，后两者说明服务端没有完成。`429` 和 `503` 按 `Retry-After` 等待。
- 超时和 `504` 之后重试，只适用于[接口描述](#contract)中标明幂等的端点，即执行多次和执行一次效果相同的端点，例如查询、按 ID 设置某个值。其他端点超时后先查询结果，再决定是否重试。
- 需要保证执行、可以延后执行的工作（例如发送通知）交给[队列](../background/queue.md)。

<a id="discovery"></a>
## 服务发现

服务协议的三个发现 subject，任何 NATS 客户端都可以请求：

| subject | 返回 |
| --- | --- |
| `$SRV.PING.<服务名>` | 每个在线的实例回复一次：服务名、版本、实例 ID |
| `$SRV.INFO.<服务名>` | 每个实例的端点列表：端点名、subject、队列组和元数据 |
| `$SRV.STATS.<服务名>` | 每个实例每个端点的调用次数、错误次数和处理时间 |

服务名见[端点与 subject](#subjects)，例如 `$SRV.INFO.blog_v1`。不带服务名（`$SRV.PING`）时所有服务都应答，再加实例 ID（`$SRV.PING.blog_v1.<实例 ID>`）时只有这个实例应答。服务的版本是应用的版本号，格式为[语义化版本](https://semver.org/lang/zh-CN/)。

端点的元数据中有 Tenon 补充的几项，值都是字符串：

| 键 | 说明 |
| --- | --- |
| `tenon.permissions` | 调用这个端点需要的全部权限，用 `,` 分隔；为空时不需要额外的权限，是否需要登录看接口描述中的 `x-tenon-public` |
| `tenon.idempotent` | `true` 表示端点幂等，超时后可以重试；否则是 `false` |
| `tenon.description` | 端点的说明 |

参数和返回值的结构在[接口描述](#contract)中。

<a id="contract"></a>
## 接口描述

每个 API 的端点、参数和返回值由一份 [AsyncAPI 3.0](https://www.asyncapi.com/docs/reference/specification/v3.0.0) 文档描述，用应用的 `rpc asyncapi` 命令导出，见[导出接口描述](endpoints.md#exporting)。其他语言的代码生成器根据它生成类型和客户端。文档中：

- `info.title` 是前缀，`info.version` 是服务的版本。
- 每个端点是一个 `receive` 操作，名字是服务协议中的端点名（例如 `articles_get`）。它的 channel 的 `address` 是端点的 subject；`reply` 指向一个 `address` 为 `null` 的 channel，回复地址由调用方的 NATS 客户端为每个请求生成。回复有两种消息：成功的响应，以及所有端点共用的 `error`。
- 消息的 `headers` 列出[请求](#requests)和[响应](#responses)的头。`error` 消息的 payload 是 `components.schemas` 中的 `TenonRPCProblem`，即[错误](#errors)的响应体。
- 参数和返回值是 `components.schemas` 中的命名类型，与同一个应用的 [OpenAPI 文档](../http/openapi.md)用同样的规则生成：一个 Go 类型同时出现在 HTTP 接口和 RPC 端点中时，两份文档中的名字和结构相同，枚举同样带 `x-tenon-enum`。没有返回值的端点，成功的响应没有 payload。
- 根上的 `x-tenon-permissions` 是应用的权限目录，每个操作的 `x-required-permissions` 是调用它需要的权限，名字和含义与 [OpenAPI 文档中的扩展字段](../http/protocol.md#contract)相同。操作上还有 `x-tenon-idempotent`（是否幂等）和 `x-tenon-public`（是否不需要登录）。
- 操作的 `bindings.nats.queue` 是队列组 `q`。
- 错误码和它们的翻译与 HTTP 接口共用同一个来源，由各语言的生成器一起输出。

<a id="between-applications"></a>
## 两个应用之间的调用

两个应用（例如都用 Tenon 开发的 A 和 B）互相调用时按同一份协议进行。A 调用 B 时：

- **连接**：两个应用在同一套 NATS 的同一个账号中时，各自的连接就能互相调用；B 在另一个账号或另一套 NATS 上时，A 用 B 账号中的用户另建一条连接，见[连接另一个账号或另一套 NATS](../libraries/nats.md#other-accounts)。两个应用使用不同的前缀，例如 `a.v1`、`b.v1`。
- **认证**：A 带 B 认可的 token。两个应用各有自己的用户时，B 给 A 签发一个代表 A 的个人访问令牌，权限只包含 A 要调用的端点；两个应用接受同一个 [OAuth 服务](../security/oauth-server.md)签发的令牌时，A 可以转发用户的 token，B 按用户的权限检查。
- **类型**：A 根据 B 导出的[接口描述](#contract)生成 B 的客户端，不引用 B 的代码。B 返回的业务错误带错误码，A 按错误码判断。

不要形成同步的调用环（A 调用 B、B 在处理中又同步调用 A）：超时层层叠加，一端变慢时两端一起卡住。需要通知对方的工作交给各自的[队列](../background/queue.md)异步执行。

<a id="limits"></a>
## 限制

- **消息大小**受 NATS 服务器的 `max_payload` 限制，默认 1MB，请求和响应都包括在内。文件先[上传](../http/protocol.md#uploads)到文件存储，再在请求中传文件的 ID。
- **没有流式响应。** 持续推送的数据用[实时频道](../libraries/realtime.md)或 HTTP 的[事件流](../http/protocol.md#event-streams)。
- **调用之间没有顺序保证。** 同一个调用方先后发出的两个请求可能由不同的实例处理，完成的先后不确定。
