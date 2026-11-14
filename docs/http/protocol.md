# 客户端协议

本页从线路层面说明 Tenon API 的约定：请求头、状态码、请求体和响应体、事件流。网页、App、桌面端和其他服务的客户端，无论用什么语言编写，都按本页实现；`tenon generate` 生成的 TypeScript 和 Go 客户端是其中的一种实现。每个接口的路径和数据结构见应用导出的 OpenAPI 文档，本页说明所有接口共同遵守的部分。

- [契约与接口路径](#contract)
- [请求与响应](#requests)
- [错误](#errors)
- [分页、筛选与排序](#pagination)
- [会话](#sessions)
- [事件流](#event-streams)
- [实时频道](#realtime)
- [后台操作](#operations)
- [上传](#uploads)
- [AI 对话](#ai-chat)
- [App 版本](#app-version)

<a id="contract"></a>
## 契约与接口路径

每个 API 的接口和数据结构由 OpenAPI 3.1 文档描述，用应用的 [openapi 命令](../general/commands.md#openapi-command)导出；只支持 3.0 的生成器使用 `--spec 3.0`。一个应用可以有多个 API，按 `BasePath` 区分，例如 `/api/admin`、`/api/v1`。不兼容的修改通过新的 `BasePath` 发布，旧的 API 继续服务已经发布的客户端。

OpenAPI 文档中有三个框架的扩展字段：根上的 `x-tenon-permissions` 是应用的权限目录（`name`、`description`）；每个操作上的 `x-required-permissions` 是调用它需要的全部权限；带显示文字的枚举是命名类型，上面的 `x-tenon-enum` 是显示文字的消息前缀，取值 `v` 的显示文字是翻译中 `<前缀>.v` 的消息。

接口的路径由应用决定。登录、刷新、上传、AI 对话这类接口的请求体、响应体和语义由本页规定，具体是哪个路径由应用告诉客户端，例如在创建客户端时传入。

数据格式：

- 请求体和响应体是 UTF-8 的 JSON，`Content-Type: application/json`；错误响应是 `application/problem+json`，见[错误](#errors)。
- 时间是 RFC 3339 格式的字符串，例如 `2026-11-11T08:00:00Z`。
- 值为空的可选字段可能不出现在响应中，客户端把缺少的字段当作空值。

<a id="requests"></a>
## 请求与响应

请求头：

| 请求头 | 说明 |
| --- | --- |
| `Authorization: Bearer <token>` | 需要登录的接口都用它认证，token 是 access token、登录 token 或个人访问令牌，见[会话](#sessions) |
| `Accept-Language` | 错误提示和其他文案的语言，例如 `zh-CN`、`en`；不支持的语言使用应用的默认语言 |
| `X-App-Version` | 安装在设备上的 App 的平台和版本，见 [App 版本](#app-version)；浏览器中的单页应用不带 |
| `X-Request-ID` | 可选，1–128 个不含空格的可打印 ASCII 字符。带上时服务端沿用它，否则生成一个 |
| `Last-Event-ID` | 事件流重连时带上最后收到的事件 ID，见[事件流](#event-streams) |

多租户应用按应用的约定从路径（`/t/{tenant}/…`）、子域名或请求头（例如 `X-Tenant`）确定租户，见[多租户](../security/tenancy.md#identifying-tenants)。

响应头：

| 响应头 | 说明 |
| --- | --- |
| `X-Request-ID` | 这个请求的 ID，与服务端日志关联。用户反馈问题时提供它 |
| `Content-Language` | 响应文案使用的语言 |
| `Retry-After` | 建议的等待秒数，出现在 `429` 和部分 `503` 中 |
| `WWW-Authenticate: Bearer` | 出现在 `401` 中 |

跨域访问的客户端可以读取上面这些响应头，以及 `Location`、`Content-Disposition`，见[跨域请求](middleware.md#cors)。

<a id="errors"></a>
## 错误

失败的请求返回 4xx 或 5xx 状态码，响应体是 [Problem Details](https://www.rfc-editor.org/rfc/rfc9457) 格式，`Content-Type: application/problem+json`：

```json
{"title": "Conflict", "status": 409, "detail": "slug“hello-world”已被使用", "code": "article.slug_taken"}
```

| 字段 | 说明 |
| --- | --- |
| `status` | HTTP 状态码 |
| `title` | 状态码的标准描述 |
| `detail` | 展示给用户的文案，按 `Accept-Language` 翻译。可能随语言和版本变化，不用于判断 |
| `code` | 稳定的错误码，只有业务错误包含，例如 `article.slug_taken` |
| `errors` | 字段错误的明细，只有请求格式错误包含 |

客户端按这个顺序判断：

1. 有 `code` 时按 `code` 处理。
2. 有 `errors` 时把每一项的 `message` 显示在 `location` 对应的字段旁边。
3. 两者都没有时按 `status` 处理，例如显示通用的错误提示。

请求格式错误：请求体不是合法的 JSON 时返回 `400`，字段验证失败时返回 `422`，两者都带 `errors`：

```json
{
  "title": "Unprocessable Entity",
  "status": 422,
  "detail": "提交的内容有误",
  "errors": [
    {"message": "不能为空", "location": "body.title", "value": "", "code": "validation.filled"}
  ]
}
```

| 字段 | 说明 |
| --- | --- |
| `location` | 出错的位置：`body.` 之后是请求体中的字段路径，例如 `body.items[0].name`；`query.`、`path.`、`header.` 分别是查询参数、路径参数和请求头 |
| `message` | 展示给用户的提示，按请求的语言翻译 |
| `value` | 服务端收到的值 |
| `code` | 违反的规则，例如 `validation.min_length`，不随语言变化，见[验证提示](validation.md#validation-messages) |

`422` 也可能是业务规则不允许，这时带 `code` 而没有 `errors`，所以先看 `code`。

状态码：

| 状态码 | 含义 |
| --- | --- |
| `400` | 请求体不是合法的 JSON，或请求头格式错误 |
| `401` | 没有登录、token 无效或已过期，带 `WWW-Authenticate: Bearer` |
| `403` | 已登录，但没有权限或需要额外的验证 |
| `404` | 资源不存在，或无权知道它存在；写错的接口路径得到 `detail` 为 `route not found` 的 `404` |
| `409` | 与当前状态冲突，例如名称已被使用 |
| `422` | 字段验证失败，或业务规则不允许 |
| `426` | App 版本过低，见 [App 版本](#app-version) |
| `429` | 请求过于频繁，带 `Retry-After` |
| `500` | 服务端的意外故障，`detail` 不含内部信息，用 `X-Request-ID` 查日志 |
| `503` | 依赖的服务暂时不可用，稍后重试 |

框架定义的错误码：

| 错误码 | 状态码 | 客户端的处理 |
| --- | --- | --- |
| `auth.unauthenticated` | 401 | 有 refresh token 时[刷新](#sessions)一次再重试，否则让用户登录。无论服务端因为什么拒绝，`401` 都是这一个错误码 |
| `auth.forbidden` | 403 | 提示没有权限 |
| `auth.confirm_required` | 403 | 让用户再次验证身份（例如输入密码），确认后重试 |
| `auth.two_factor_required` | 403 | 让用户完成二次验证后重试 |
| `auth.method_required` | 403 | 让用户换一种方式重新登录 |
| `rate_limit.exceeded` | 429 | 等 `Retry-After` 秒后重试 |
| `app.upgrade_required` | 426 | 停在升级页面 |
| `page.invalid_cursor` | 422 | 游标无效，从第一页重新读取 |

其他错误码由框架的功能（例如 `filesystem.too_large`、`ai.busy`）和应用定义，生成的客户端把它们生成为常量。

<a id="pagination"></a>
## 分页、筛选与排序

列表接口使用两种分页之一，见[分页](../responses/responses.md#pagination)：

| | 固定分页 | 游标分页 |
| --- | --- | --- |
| 查询参数 | `page`（从 1 开始）、`per_page` | `cursor`、`per_page` |
| 响应体 | `items`、`page`、`per_page`、`total` | `items`、`next_cursor` |

```json
{"items": [], "page": 2, "per_page": 20, "total": 41}
```

```json
{"items": [], "next_cursor": "eyJhdCI6…"}
```

- `per_page` 默认 20，范围 1–100，超出范围返回 `422`。
- 游标是不透明的字符串，客户端原样传回。`next_cursor` 为空或不出现时没有下一页；第一页不传 `cursor`。
- 排序参数通常叫 `sort`，值是字段名，前面加 `-` 表示降序，例如 `sort=-created`。可选的值和筛选参数见每个接口的 OpenAPI 定义，不在可选值中的返回 `422`。

<a id="sessions"></a>
## 会话

客户端用三种 token 认证，请求都带 `Authorization: Bearer <token>`：

| token | 获得方式 | 过期 |
| --- | --- | --- |
| access token（JWT 模式） | 登录、刷新 | 短期，默认 10 分钟，用 refresh token 换新的 |
| 登录 token（不透明） | 登录 | 一段时间不用后过期，没有 refresh token |
| 个人访问令牌 | 用户在设置中创建 | 按创建时的设置 |

JWT 模式的登录、刷新和确认身份接口都返回同一个格式：

```json
{"access_token": "eyJ…", "refresh_token": "kq3…", "expires_at": "2026-11-11T08:10:00Z"}
```

| 字段 | 说明 |
| --- | --- |
| `access_token` | 放在 `Authorization: Bearer` 中 |
| `refresh_token` | 不为空时替换已保存的 refresh token；为空或不出现时继续使用已保存的那一个 |
| `expires_at` | access token 的过期时间 |

刷新接口的请求体：

```json
{"refresh_token": "kq3…"}
```

refresh token 有两种保存方式：

| 客户端 | 保存方式 |
| --- | --- |
| App、桌面端、小程序、脚本 | 保存在设备的安全存储中，刷新时放在请求体中 |
| 浏览器中的单页应用 | 登录请求体带 `"cookie": true`，服务端把 refresh token 写入 `HttpOnly`、`SameSite=Strict` 的 cookie，响应体中没有 `refresh_token`；刷新时请求体为空，浏览器自动带上 cookie |

客户端的规则：

1. 请求带上 access token。access token 过期或收到 `401` 时刷新一次，再重试原来的请求。
2. 同一时间只发起一次刷新：刷新进行中的请求等它完成，再用新的 access token 重试。
3. 刷新返回 `401` 时，这次登录已经失效，让用户重新登录。
4. 打开浏览器页面（包括刷新页面、新标签页）时先刷新一次，拿到 access token；失败时显示登录页。

每次刷新都会更换 refresh token。上一个 refresh token 在更换后 30 秒内仍可使用，但只换得新的 access token，`refresh_token` 为空，这样同时发起的几次刷新都能成功。超过 30 秒还有人用上一个 refresh token 刷新，服务端认为它已被盗用，撤销这次登录，双方都要重新登录。

登出接口撤销当前这次登录，使用 cookie 的同时清除 cookie。服务端也可能在用户修改密码、管理员停用账号时撤销登录，之后的请求和刷新返回 `401`。

<a id="event-streams"></a>
## 事件流

持续推送的接口使用 [Server-Sent Events](https://html.spec.whatwg.org/multipage/server-sent-events.html)，`Content-Type: text/event-stream`，见[服务端推送](../responses/sse.md)：

```text
event: published
id: 0193e7a2-7c1e-7b4a-9d2f-3c5a1e8b6f10
data: {"id":"0193e7a2-7c1e-7b4a-9d2f-3c5a1e8b6f10","title":"Hello"}

: ping

```

- 每个事件有事件名（`event`）和 JSON 数据（`data`），可能有 `id`。每个接口有哪些事件、数据是什么结构，见 OpenAPI 文档。
- 以 `:` 开头的行是心跳，大约每 15 秒一次，客户端忽略它。
- 认证与普通请求相同，带 `Authorization` 请求头。浏览器原生的 `EventSource` 不能设置请求头，所以用 `fetch` 读取事件流。
- 认证、权限和参数错误在开始推送之前返回，是普通的错误响应。
- 连接可能因为服务器重启、网络切换而断开。长连接的接口由客户端重新连接，带上 `Last-Event-ID`，服务端据此补发断线期间的事件；服务端可能用 `retry` 字段建议重连的间隔（毫秒）。
- 登录被撤销或失去权限后，服务端在 30 秒内结束连接，重新连接时得到 `401`。
- 一次操作的流式响应（例如 AI 的回答）在操作完成后结束，不需要重连。

<a id="realtime"></a>
## 实时频道

双向的实时连接使用 [Centrifuge](https://centrifugal.dev) 的协议，用它的官方 SDK 连接（JavaScript、Dart、Swift、Java、Go 等），见[实时频道](../libraries/realtime.md)。

- 连接地址是应用挂载的 WebSocket 路径，例如 `wss://example.com/realtime`。
- 连接时传入 token（与 API 相同的 access token 或登录 token）。没有 token 的连接是匿名的，只能订阅公开频道。
- JWT 模式下设置 SDK 的 `getToken`：重连时 access token 可能已经过期，SDK 调用它[刷新](#sessions)后取得新的 token；返回空字符串表示需要重新登录。
- token 无效或这次登录已被撤销时，连接以关闭码 `3500` 结束，SDK 不再重连。连接期间服务端每 30 秒重新检查一次。
- 订阅被拒绝时，错误码 `103` 表示没有权限，`102` 表示没有这个频道。
- 频道中的消息（publication）的数据是 JSON。频道名称和消息结构由应用定义。

<a id="operations"></a>
## 后台操作

导入、导出、批量处理这类耗时的操作在后台执行。发起操作的接口返回操作的 ID，进度接口是一个[事件流](#event-streams)，每次进度变化推送一个 `progress` 事件，连接（包括重连）时先推送一次当前进度：

```text
event: progress
data: {"id":"0193e7a2-7c1e-7b4a-9d2f-3c5a1e8b6f10","kind":"articles.bulk_publish","status":"running","total":100,"processed":42,"failed":1,"created_at":"2026-11-11T08:00:00Z","updated_at":"2026-11-11T08:00:07Z"}
```

| 字段 | 说明 |
| --- | --- |
| `kind` | 操作的类型，由应用定义 |
| `status` | `pending`、`running`、`completed`、`failed` |
| `total` | 预计的单位数，`0` 表示还不知道 |
| `processed` | 已经处理的单位数，包括失败的 |
| `failed` | 失败的单位数 |
| `error` | 操作失败时的说明 |
| `completed_at` | 结束的时间 |

`status` 为 `completed` 或 `failed` 时操作结束。见[后台操作](../features/operations.md)。

<a id="uploads"></a>
## 上传

文件有两种上传方式，见[上传接口](../libraries/file-uploads.md)：

**通过应用服务器**：`multipart/form-data` 请求，返回文件。

**直传到对象存储**：文件不经过应用服务器。

1. 登记：请求体 `{"filename": "a.pdf", "content_type": "application/pdf", "size": 1048576}`，返回 `status` 为 `temporary` 的文件。
2. 小文件请求一次签名，得到 `{"url": "…", "method": "PUT", "headers": {"Content-Type": ["application/pdf"]}}`，按这个方法、地址和请求头把文件内容发给对象存储。
3. 大文件先创建分片会话，得到 `{"upload_id": "…", "part_size": 8388608, "part_count": 13}`；每一片请求一次签名后上传，除最后一片外每片正好 `part_size` 字节，记下对象存储返回的 `ETag` 响应头；最后提交 `{"parts": [{"number": 1, "etag": "\"…\""}]}` 合并。放弃时删除这个分片会话。
4. 把文件的 `id` 交给使用它的业务接口，例如创建文章时的封面，业务接口确认这个文件，它变为 `ready`。没有被确认的文件在 `expires_at` 之后删除。

文件的 JSON：

```json
{"id": "0193e7a2-…", "filename": "a.pdf", "content_type": "application/pdf", "size": 1048576, "status": "temporary", "expires_at": "2026-11-12T08:00:00Z", "url": "https://…", "created_at": "…", "updated_at": "…"}
```

`status` 是 `temporary`（已登记，等待上传或确认）、`ready`（已被业务使用）或 `deleted`。

上传相关的错误码：`filesystem.too_large`、`filesystem.invalid`、`filesystem.not_accepted`（类型不被接受）、`filesystem.not_uploaded`（确认时对象存储中还没有文件）、`filesystem.direct_upload_unsupported`（磁盘不支持直传）、`filesystem.not_found`。

<a id="ai-chat"></a>
## AI 对话

AI 对话的消息流使用 [Vercel AI SDK 的 UI 消息流协议](https://ai-sdk.dev/docs/ai-sdk-ui/stream-protocol)（v1），响应头 `x-vercel-ai-ui-message-stream: v1`，见 [AI 助手](../ai/chat.md)。一个对话有五个接口，路径由应用决定：

| 接口 | 请求 | 响应 |
| --- | --- | --- |
| 发送 | `POST`，请求体是 UI 消息（新消息或审批结果） | 这次回答的消息流 |
| 恢复 | `GET`，对话 ID | 进行中的回答从头重放；没有进行中的回答时 `204` |
| 停止 | `POST`，对话 ID | 取消进行中的回答，消息流以 `abort` 结束；没有进行中的回答时直接成功 |
| 插话 | `POST`，请求体 `{"text": "…"}` | 消息进入进行中的回答；回答已经结束时请求失败，客户端改为发送新消息 |
| 历史 | `GET`，对话 ID | UI 消息的数组 |

消息流中除协议的标准片段外，Tenon 还会发送 `steering-message`：插话进入对话时出现，`parts` 中是插话的内容，客户端把它显示为一条用户消息。

- 回答在服务端独立运行，客户端断开连接不会停止它，用停止接口停止。
- 对话正在回答时再次发送返回 `409`，错误码 `ai.busy`；有等待审批的工具调用时返回 `409`，错误码 `ai.awaiting_approval`。
- 审批结果以 UI 消息发送，回答随即继续。

<a id="app-version"></a>
## App 版本

安装在设备上的 App 从第一个发布的版本起，在每个请求中带上平台和版本：

```http
X-App-Version: android/2.3.1
```

平台名由应用决定，常用 `ios`、`android`、`macos`、`windows`、`linux`；版本是商店中显示的版本号，按语义化版本比较。版本低于服务端设置的最低版本时返回：

```http
HTTP/1.1 426 Upgrade Required
Content-Type: application/problem+json

{"title": "Upgrade Required", "status": 426, "detail": "当前版本过低，请升级到 1.4.0 或更高版本", "code": "app.upgrade_required"}
```

App 停在升级页面，打开应用商店或开始自动更新。请求头格式错误时返回 `400`。见 [App 最低版本](middleware.md#app-version)。
