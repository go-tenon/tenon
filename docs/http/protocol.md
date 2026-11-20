# 客户端协议

Tenon API 在线路上的规则：请求头、状态码、请求体和响应体、事件流。网页、App、桌面端和其他服务的客户端，用任何语言编写，都按这一页实现；[生成的 TypeScript 和 Go 客户端](clients.md)是其中的一种实现。每个接口的路径和数据结构见应用导出的 OpenAPI 文档。

<a id="contract"></a>
## 契约与接口路径

- 每个 API 的接口和数据结构由 OpenAPI 3.1 文档描述，用应用的 [openapi 命令](../general/commands.md#openapi-command)导出；只支持 3.0 的生成器用 `--spec 3.0`。
- 一个应用可以有多个 API，按 `BasePath` 区分，例如 `/api/admin`、`/api/v1`。不兼容的修改通过新的 `BasePath` 发布，旧的 API 继续服务已经发布的客户端。
- 接口的路径由应用决定。登录、刷新、上传、AI 对话这类接口的请求体、响应体和语义由这一页规定，路径由应用告诉客户端，例如在创建客户端时传入。

OpenAPI 文档中框架的扩展字段：

| 字段 | 位置 | 含义 |
| --- | --- | --- |
| `x-tenon-permissions` | 根 | 应用的权限目录（`name`、`description`） |
| `x-required-permissions` | 每个操作 | 调用它需要的全部权限 |
| `x-tenon-enum` | 带显示文字的枚举（命名类型） | 显示文字的消息前缀：取值 `v` 的显示文字是翻译中 `<前缀>.v` 的消息 |

数据格式：

- 请求体和响应体是 UTF-8 的 JSON，`Content-Type: application/json`；错误响应是 `application/problem+json`，见[错误](#errors)。
- 时间是 RFC 3339 格式的字符串，例如 `2026-11-11T08:00:00Z`。
- 值为空的可选字段可能不出现在响应中，客户端把缺少的字段当作空值。

<a id="requests"></a>
## 请求与响应

| 请求头 | 说明 |
| --- | --- |
| `Authorization: Bearer <token>` | 浏览器以外的客户端用它认证，token 是登录 token、个人访问令牌或 OAuth 令牌；浏览器用会话 cookie，见[会话](#sessions) |
| `Accept-Language` | 错误提示和其他文案的语言，例如 `zh-CN`、`en`；不支持的语言使用应用的默认语言 |
| `X-App-Version` | 安装在设备上的 App 的平台和版本，见 [App 版本](#app-version)；浏览器中的单页应用不带 |
| `X-Request-ID` | 可选，1–128 个不含空格的可打印 ASCII 字符。带上时服务端沿用它，否则生成一个 |
| `Last-Event-ID` | 事件流重连时带上最后收到的事件 ID，见[事件流](#event-streams) |

多租户应用按应用的约定从路径（`/t/{tenant}/…`）、子域名或请求头（例如 `X-Tenant`）确定租户，见[多租户](../security/tenancy.md#identifying-tenants)。

| 响应头 | 说明 |
| --- | --- |
| `X-Request-ID` | 这个请求的 ID，与服务端日志关联，用户反馈问题时提供它 |
| `Content-Language` | 响应文案使用的语言 |
| `Retry-After` | 建议的等待秒数，出现在 `429` 和部分 `503` 中 |
| `WWW-Authenticate: Bearer` | 出现在 `401` 中 |

跨域访问的客户端可以读取上面这些响应头，以及 `Location`、`Content-Disposition`，见[跨域请求](middleware.md#cors)。

JSON 中的数组和对象不为 `null`：没有元素的数组是 `[]`，没有键的对象是 `{}`。请求中把数组写成 `null` 是校验错误，接口文档中声明为可为空（`nullable`）的除外。单个值可以为 `null` 的字段在接口文档中标为可为空。HTTP 响应、事件流、RPC 的响应、MCP 工具的结果和 Webhook 的载荷都按这个规则编码。

<a id="errors"></a>
## 错误

失败的请求返回 4xx 或 5xx，响应体是 [Problem Details](https://www.rfc-editor.org/rfc/rfc9457)，`Content-Type: application/problem+json`：

```json
{"title": "Conflict", "status": 409, "detail": "slug“hello-world”已被使用", "code": "article.slug_taken", "params": {"slug": "hello-world"}}
```

| 字段 | 说明 |
| --- | --- |
| `status` | HTTP 状态码 |
| `title` | 状态码的标准描述 |
| `detail` | 展示给用户的文案，按 `Accept-Language` 翻译。随语言和版本变化，不用于判断 |
| `code` | 稳定的错误码，只有业务错误包含，例如 `article.slug_taken` |
| `params` | 业务错误的参数，只有带参数的业务错误包含，例如 `auth.method_required` 的 `{"methods": ["acme_sso"]}`。每个错误码的参数是固定的，可以用于处理错误 |
| `errors` | 字段错误的明细：请求格式错误时包含；业务错误指出了某个字段时也有这个字段的一项，它的 `code` 是业务错误码，`message` 与 `detail` 相同 |

客户端按这个顺序判断：

1. 有 `code` 时按 `code` 处理。
2. 有 `errors` 时把每一项的 `message` 显示在 `location` 对应的字段旁边；界面中没有这个字段时，显示在表单上方。
3. 两者都没有时按 `status` 处理，例如显示通用的错误提示。

业务错误可以同时有 `code` 和 `errors`：例如缺少某个条件下必填的地址，客户端按 `code` 判断是什么错误，把提示显示在 `errors` 中的地址字段旁边。

请求体不是合法的 JSON 时返回 `400`，字段校验失败时返回 `422`，两者都带 `errors`：

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
| `location` | `body.` 之后是请求体中的字段路径，例如 `body.items[0].name`；`query.`、`path.`、`header.` 分别是查询参数、路径参数和请求头 |
| `message` | 展示给用户的提示，按请求的语言翻译 |
| `value` | 服务端收到的值 |
| `code` | 违反的规则，例如 `validation.min_length`，不随语言变化，见[校验提示](requests.md#validation-messages) |

`422` 也可能是业务规则不允许，这时带 `code` 而没有 `errors`，所以先看 `code`。

<a id="status-codes"></a>
### 状态码与框架的错误码

| 状态码 | 含义 |
| --- | --- |
| `400` | 请求体不是合法的 JSON，或请求头格式错误 |
| `401` | 没有登录、token 无效或已过期，带 `WWW-Authenticate: Bearer` |
| `403` | 已登录，但没有权限或需要额外的验证 |
| `404` | 资源不存在，或无权知道它存在；写错的接口路径得到 `detail` 为 `route not found` 的 `404` |
| `409` | 与当前状态冲突，例如名称已被使用 |
| `422` | 字段校验失败，或业务规则不允许 |
| `426` | App 版本过低，见 [App 版本](#app-version) |
| `429` | 请求过于频繁，带 `Retry-After` |
| `500` | 服务端的意外故障，`detail` 不含内部信息，用 `X-Request-ID` 查日志 |
| `503` | 依赖的服务暂时不可用，稍后重试 |

| 错误码 | 状态码 | 客户端的处理 |
| --- | --- | --- |
| `auth.unauthenticated` | 401 | 让用户重新登录。无论服务端因为什么拒绝，`401` 都是这一个错误码 |
| `http.cross_site` | 403 | 浏览器从其他站点的页面发出了带会话 cookie 的写请求，见[会话](#sessions)。同源页面不会遇到 |
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

列表接口使用两种分页之一，见[分页](responses.md#pagination)：

| | 固定分页 | 游标分页 |
| --- | --- | --- |
| 查询参数 | `page`（从 1 开始）、`per_page` | `cursor`、`per_page` |
| 响应体 | `items`、`page`、`per_page`、`total` | `items`、`next_cursor` |

```json
{"items": [], "page": 2, "per_page": 20, "total": 41}
{"items": [], "next_cursor": "eyJhdCI6…"}
```

- `per_page` 默认 20，范围 1–100，超出范围返回 `422`。
- 游标是不透明的字符串，客户端原样传回。第一页不传 `cursor`；`next_cursor` 为空或不出现时没有下一页。
- 排序参数通常叫 `sort`，值是字段名，前面加 `-` 表示降序，例如 `sort=-created`。可选的值和筛选参数见每个接口的 OpenAPI 定义，不在可选值中的返回 `422`。

<a id="sessions"></a>
## 会话

token 是不透明的随机字符串，服务端每个请求查一次表，撤销立即生效，没有刷新的步骤：

| token | 获得方式 | 过期 |
| --- | --- | --- |
| 登录 token | 登录 | 一段时间不用后过期，到最长期限后过期 |
| 个人访问令牌 | 用户在设置中创建 | 按创建时的设置 |
| OAuth 令牌 | 用户授权第三方客户端 | 按授权时的设置 |

| 客户端 | 携带方式 |
| --- | --- |
| 浏览器中的页面 | 登录接口把 token 写进会话 cookie：`HttpOnly`、`Secure`、`SameSite=Lax`、`Path=/`，名称以 `__Host-` 开头（默认 `__Host-session`）。浏览器随同源请求自动带上它，页面脚本读不到它 |
| App、桌面端、小程序、脚本 | 登录接口在响应体中返回 token，保存在设备的安全存储中，请求时放在 `Authorization: Bearer` 中。App 和桌面端在系统浏览器中登录，见[原生客户端](#native-clients) |

浏览器的规则：

1. 页面和 API 由同一个源提供，cookie 只随同源请求发送。
2. 打开页面（包括刷新页面、新标签页）时读一次当前用户，例如 `GET /api/me`：成功就是已登录，`401` 时显示登录页。
3. 收到 `401` 时这次登录已经失效（过期、在别处退出或被撤销），让用户重新登录。
4. 用 cookie 认证的写请求（GET、HEAD、OPTIONS 以外）必须来自同源的页面：服务端按 `Sec-Fetch-Site` 判断，没有时比较 `Origin`，否则返回 `403 http.cross_site`。浏览器自动带上这两个请求头，页面不需要 CSRF token。会设置 cookie 的登录接口同样只接受同源页面的请求。

登出接口撤销当前这次登录，并清除 cookie。服务端也可能在用户修改密码、管理员停用账号时撤销登录，之后的请求返回 `401`。

<a id="native-clients"></a>
## 原生客户端

安装在设备上的客户端（桌面端、App，与语言和界面技术无关）在系统浏览器中登录（RFC 8252），用授权码 + PKCE 换取 token。客户端由应用在授权服务器中登记，见[应用自己的客户端](../security/oauth-server.md#first-party-clients)。

1. 生成 `code_verifier`（43–128 个字符的随机串）、它的 S256 摘要 `code_challenge`，以及随机的 `state`。
2. 在系统浏览器中打开授权地址，不带 `scope` 和 `resource`：

   ```text
   https://example.com/oauth/authorize?response_type=code&client_id=admin-desktop
     &redirect_uri=http://127.0.0.1:53123/callback&state=…&code_challenge=…&code_challenge_method=S256
   ```

3. 用户在网页上登录并确认，浏览器跳回回调地址，带着 `code`、`state` 和 `iss`。客户端检查 `state` 与第 1 步相同、`iss` 是应用的地址。出错时带着 `error`（例如 `access_denied`）。
4. 用表单换取 token：

   ```http
   POST /oauth/token
   Content-Type: application/x-www-form-urlencoded

   grant_type=authorization_code&code=…&client_id=admin-desktop&redirect_uri=http://127.0.0.1:53123/callback&code_verifier=…
   ```

   响应是 `{"access_token": "…", "token_type": "Bearer"}`，与密码登录得到的登录 token 相同。

| 客户端 | 回调地址 |
| --- | --- |
| 桌面端 | `http://127.0.0.1:<端口>/callback`：在随机端口上临时监听，收到回调后关闭 |
| App | 自定义 scheme（例如 `com.example.admin:/oauth/callback`）或 Universal Link / App Link |

之后：

- **保存 token**：放在系统的安全存储中（钥匙串、Windows 凭据管理器、Android Keystore），请求时放在 `Authorization: Bearer` 中。收到 `401` 时删除它，回到登录。
- **退出**：调用应用的退出接口，或者把 token 交给 `POST /oauth/revoke`（表单 `token=…`），再删除本地的 token。
- **版本**：每个请求带上 `X-App-Version`，见 [App 版本](#app-version)。同一个应用有多个客户端时，每个客户端用自己的平台名，例如 `admin-desktop`、`ios`。
- **实时频道**：连接时传入同一个 token，见[实时频道](#realtime)。
- **网页视图**：自己网站的页面和登录都在系统浏览器中打开，不放进内嵌的网页视图：应用能读到其中输入的密码，Google 等第三方登录也会拒绝。
- **深度链接**：客户端中的页面使用与网页相同的路径，例如 `/admin/orders/123`。邮件和通知中只生成一种链接，各客户端把它对应到自己的页面。

<a id="event-streams"></a>
## 事件流

持续推送的接口使用 [Server-Sent Events](https://html.spec.whatwg.org/multipage/server-sent-events.html)，`Content-Type: text/event-stream`，见[服务端推送](sse.md)：

```text
event: published
id: 0193e7a2-7c1e-7b4a-9d2f-3c5a1e8b6f10
data: {"id":"0193e7a2-7c1e-7b4a-9d2f-3c5a1e8b6f10","title":"Hello"}

: ping

```

- 每个事件有事件名（`event`）和 JSON 数据（`data`），可能有 `id`。每个接口的事件和数据结构见 OpenAPI 文档。
- 以 `:` 开头的行是心跳，大约每 15 秒一次，客户端忽略它。
- 认证与普通请求相同：浏览器带会话 cookie，其他客户端带 `Authorization` 请求头。
- 认证、权限和参数错误在开始推送之前返回，是普通的错误响应。
- 连接可能因为服务器重启、网络切换而断开。长连接的接口由客户端重新连接，带上 `Last-Event-ID`，服务端据此补发断线期间的事件；服务端可能用 `retry` 字段建议重连的间隔（毫秒）。
- 登录被撤销或失去权限后，服务端在 30 秒内结束连接，重新连接时得到 `401`。
- 一次操作的流式响应（例如 AI 的回答）在操作完成后结束，不需要重连。

<a id="realtime"></a>
## 实时频道

双向的实时连接使用 [Centrifuge](https://centrifugal.dev) 的协议，用它的官方 SDK 连接（JavaScript、Dart、Swift、Java、Go 等），见[实时频道](../libraries/realtime.md)。

- 连接地址是应用挂载的 WebSocket 路径，例如 `wss://example.com/realtime`。
- 浏览器中的页面不传 token，同源的连接请求带着会话 cookie。其他客户端用与 API 相同的 token：由 SDK 在连接时传入，或者放在连接请求的 `Authorization: Bearer` 请求头中。服务端依次取 SDK 传入的、请求头中的、cookie 中的；都没有的连接是匿名的，只能订阅公开频道。
- 其他站点的页面不能用 cookie 建立连接。带 `Authorization` 请求头的连接不检查来源：浏览器不会自动附加这个请求头。
- token 无效或这次登录已被撤销时，连接以关闭码 `3500` 结束，SDK 不再重连。连接期间服务端每 30 秒重新检查一次。
- 订阅被拒绝时，错误码 `103` 表示没有权限，`102` 表示没有这个频道。
- 订阅之后用户失去频道的权限时，服务端推送退订（unsubscribe，代码 `2000`），SDK 不再重新订阅。服务端在每次重新检查 token 时一并重新检查已订阅的频道。
- 频道中的消息（publication）的数据是 JSON，频道名称和消息结构由应用定义。

<a id="operations"></a>
## 后台操作

导入、导出、批量处理这类耗时的操作在后台执行，见[后台操作](../features/operations.md)。发起操作的接口返回操作的 ID；进度接口是一个[事件流](#event-streams)，每次进度变化推送一个 `progress` 事件，连接（包括重连）时先推送一次当前进度：

```text
event: progress
data: {"id":"0193e7a2-7c1e-7b4a-9d2f-3c5a1e8b6f10","kind":"articles.bulk_publish","status":"running","total":100,"processed":42,"failed":1,"created_at":"2026-11-11T08:00:00Z","updated_at":"2026-11-11T08:00:07Z"}
```

| 字段 | 说明 |
| --- | --- |
| `kind` | 操作的类型，由应用定义 |
| `status` | `pending`、`running`、`completed`、`failed`；`completed` 或 `failed` 时操作结束 |
| `total` | 预计的单位数，`0` 表示还不知道 |
| `processed` | 已经处理的单位数，包括失败的 |
| `failed` | 失败的单位数 |
| `error` | 操作失败时的错误码，与[错误响应](#errors)的 `code` 相同；给用户看的说明由应用按错误码翻译 |
| `completed_at` | 结束的时间 |

<a id="uploads"></a>
## 上传

文件有两种上传方式，服务端的接口见[在 REST 接口中使用](../libraries/filesystem.md#rest)。

**通过应用服务器**：`multipart/form-data` 请求，返回文件。

**直传到对象存储**：

1. 登记：请求体 `{"filename": "a.pdf", "content_type": "application/pdf", "size": 1048576}`，返回 `status` 为 `temporary` 的文件。
2. 小文件请求一次签名，得到 `{"url": "…", "method": "PUT", "headers": {"Content-Type": ["application/pdf"], "If-None-Match": ["*"]}}`，按这个方法、地址和请求头把文件内容发给对象存储。请求头都在签名中，要原样发送。
3. 大文件先创建分片会话，得到 `{"upload_id": "…", "part_size": 16777216, "part_count": 7}`（`part_size` 由服务端决定）；每一片请求一次签名后上传，除最后一片外每片正好 `part_size` 字节，记下对象存储返回的 `ETag` 响应头；最后提交 `{"parts": [{"number": 1, "etag": "\"…\""}]}` 合并。放弃时删除这个分片会话。
4. 把文件的 `id` 交给使用它的业务接口（例如创建文章时的封面），业务接口确认这个文件，它变为 `ready`。没有被确认的文件在 `expires_at` 之后删除。

文件的 JSON：

```json
{"id": "0193e7a2-…", "disk": "s3", "key": "…", "filename": "a.pdf", "content_type": "application/pdf", "size": 1048576, "status": "temporary", "expires_at": "2026-11-12T08:00:00Z", "url": "https://…", "created_at": "…", "updated_at": "…"}
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
| 插话 | `POST`，请求体 `{"text": "…"}` | 消息进入进行中的回答；回答已经结束时返回 `409 ai.finished`，客户端改为发送新消息 |
| 历史 | `GET`，对话 ID | UI 消息的数组 |

- 插话进入对话时，消息流中出现数据片段 `{"type": "data-steering", "id": "…", "data": {"text": "…"}}`（协议的标准片段，`useChat` 把它作为回答中的一个 part），客户端把它显示为一条用户消息。历史中它也是回答的一个 part。
- 回答在服务端独立运行，客户端断开连接不会停止它，用停止接口停止。
- 对话正在回答时再次发送返回 `409 ai.busy`；有等待审批的工具调用时返回 `409 ai.awaiting_approval`。
- 审批结果以 UI 消息发送，回答随即继续。

<a id="app-version"></a>
## App 版本

安装在设备上的 App 从第一个发布的版本起，在每个请求中带上平台和版本：

```http
X-App-Version: android/2.3.1
```

平台名由应用决定，常用 `ios`、`android`、`macos`、`windows`、`linux`；版本是商店中显示的版本号，按语义化版本比较。请求头格式错误时返回 `400`。版本低于服务端设置的[最低版本](middleware.md#app-version)时返回：

```http
HTTP/1.1 426 Upgrade Required
Content-Type: application/problem+json

{"title": "Upgrade Required", "status": 426, "detail": "当前版本过低，请升级到 1.4.0 或更高版本", "code": "app.upgrade_required"}
```

App 停在升级页面，打开应用商店或开始自动更新。
