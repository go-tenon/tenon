# 客户端代码

应用的 `client` 命令从路由声明生成 TypeScript 和 Go 客户端：方法按 operationId 分组，类型与服务端一致，业务错误码是常量。其他语言用导出的 OpenAPI 文档生成。所有接口共同遵守的线路规则见[客户端协议](protocol.md)。

包：`clientgen`、`clientgen/wire`、`command/httpcmd`。

<a id="introduction"></a>
## 示例

```bash
go tool tenon generate                             # 按约定和 tenon.json 生成全部客户端
go run ./cmd/blog client --ts packages/client/src  # TypeScript：每个 API 一个子目录
go run ./cmd/blog client --out ../api              # Go
./bin/blog openapi > openapi.json                  # 其他语言使用的 OpenAPI 文档
```

```ts
const article = await api.article.update(id, { title: "Hello", slug: "hello", content: "" })
```

```go
a, err := c.Article.Update(ctx, id, article.Changes{Title: "Hello", Slug: "hello"})
if errors.Is(err, article.ErrSlugTaken) {
    // ...
}
```

[`tenon dev`](../installation/tooling.md#dev) 每次重新编译后自动重新生成。生成的代码和 `openapi.json` 提交到仓库；CI 重新生成后用 `git diff --exit-code` 检查是否忘了更新，并[检查不兼容的变更](#checking-changes)。

<a id="generated-names"></a>
## 生成的名称

| OpenAPI | 来源 | 例子 |
| --- | --- | --- |
| operationId | 路由的 `OperationID`，kebab-case | `article-publish` 生成 `api.article.publish`、`c.Article.Publish`；`ping` 生成 `api.ping`、`c.Ping` |
| schema 名 | 包名加类型名，类型名已以包名开头时不重复 | `article.Changes` 为 `ArticleChanges`，`article.ArticleDTO` 为 `ArticleDTO` |
| 错误响应 | 所有接口共用 | `ErrorModel` |

规则见[共享响应结构](routing.md#shared-responses)。改名会改变生成的代码，要和修改接口一样通知调用方。两个包的同名类型冲突时，给其中一个改名。

<a id="client-command"></a>
## `client` 命令

`httpcmd.Commands` 把 `client` 命令加入应用的命令行。它用 `nil` 依赖构造路由，离线运行；类型声明和错误码从源码复制，所以在服务端的源码目录中运行，Go 代码要能编译。

| 选项 | 说明 |
| --- | --- |
| `--ts <dir>` | TypeScript 客户端的输出目录，每个 API 一个以名称命名的子目录 |
| `--name <BasePath>=<name>` | TypeScript 客户端的目录名，可重复；两个 API 同名时报错 |
| `--out <dir>` | Go 客户端的输出目录，目录名是根包的包名 |
| `--base-path <path>` | 只生成这一个 API；有[多个 API](openapi.md#api-versioning) 时 `--out` 需要它 |
| `--main <pkg>` | 在这个主包的依赖中查找错误码；默认是当前程序 |
| `--import <pkg>` | Go 客户端可以直接导入的包（含子包），可重复；默认只有 `github.com/google/uuid` |
| `--rename <pkg>=<name>` | 服务端包路径对应的镜像包名，可重复 |

应用的翻译文件用 `httpcmd.Commands(routes, httpcmd.Translations(locales.FS))` 传入，生成的验证提示与服务端相同。没有生成的接口及原因输出到标准错误。

<a id="typescript"></a>
## TypeScript 客户端

网页和 App 的 WebView 用 TypeScript 客户端。在 React 中配合 TanStack Query 使用，见[在 React 中调用接口](../frontend/client-react.md)。

<a id="generating-types"></a>
### 生成 TypeScript 客户端

[tenon generate](../installation/tooling.md#generate) 把每个 API 生成到 `packages/client/src/<名称>`。名称默认是 `BasePath` 从后往前第一个不是 `api`、也不是路径参数的段：`/api` 是 `api`，`/api/admin` 和 `/admin/api` 都是 `admin`。改名写在 `tenon.json` 中：

```json
{ "apis": { "/api/v1": { "name": "public" } } }
```

前端从子路径导入，例如 `@blog/client/api` 是 `api.ts`，`@blog/client/api/queries` 是 `queries.ts`：

| 文件 | 内容 |
| --- | --- |
| `api.ts` | 类型、`createClient`，并导出 `errors.ts` 和 `ApiError`、`isApiError`、`saveBlob` |
| `errors.ts` | [`errorx.Define`](../general/errors.md#defining-errors) 的错误码，按包分组，例如 `ArticleErrors.SlugTaken` |
| `runtime.ts` | 发送请求、事件流和下载 |
| `queries.ts` | [TanStack Query](../frontend/client-react.md#tanstack) 的选项 |
| `schemas.ts`、`messages.ts` | [校验规则](../frontend/client-react.md#validation-rules)和各语言的提示 |
| `permissions.ts`、`enums.ts` | [权限](../frontend/client-react.md#ts-permissions)、带显示文字的[枚举](../frontend/client-react.md#ts-enums) |
| `resources.ts`、`settings.ts`、`kit.ts` | [资源](../frontend/resources.md#generated)、[设置页面](../frontend/resources.md#settings)、[`createKit`](../frontend/resources.md#model) |

重新生成时只删除带生成标记的文件，手写的文件保持不变。找不到新加的接口或字段时运行 `bun run generate`。

<a id="creating-the-client"></a>
### 用 `createClient` 创建客户端

浏览器的登录凭据是服务端写入的 HttpOnly [会话 cookie](../security/api-tokens.md#browser-sessions)，页面代码不保管 token。`apps/web/src/api.ts`：

```ts
import { createSession } from "@go-tenon/client"
import { apiFetch } from "@go-tenon/start"
import { createClient } from "@blog/client/api"

// load 读取当前用户；没有登录时接口返回 401
export const session = createSession({ load: async () => ({ account: (await api.user.me()).id }) })

export const api = createClient({
  baseURL: "/api",
  fetch: apiFetch, // 服务端渲染时经内部 socket 调用 Go，带上页面请求的 cookie
  session, // 受保护的接口返回 401 时调用 session.expired()
  onConfirmRequired: () => confirmDialog.open(), // 403 auth.confirm_required：确认后重试一次
})
```

- 打开页面时调用 `await session.restore()`，返回 `true` 表示已经登录。登录接口成功后调用 `session.signIn()`；先调用登出接口，再调用 `session.signOut()`。
- 用 `@go-tenon/ui` 的 [`createApp`](../frontend/app.md#creating-the-app) 时，会话失效后路由守卫跳到登录页。
- 请求发出之后身份变了（`session.generation()` 变了），`401` 原样抛出、`403 auth.confirm_required` 不重试，免得用别人的身份重发。[`rest.Public`](routing.md#access-policies) 接口的 `401` 不影响会话。
- `language` 覆盖 `Accept-Language`（默认是浏览器的语言）；App 的 WebView 设置 `appVersion`，作为 [`X-App-Version`](#app-version) 发送。
- `headers` 每次请求时读取，值为空时不发送，接口自己的请求头优先，例如 `() => ({ "X-Tenant": session.identity()?.tenant })`；`baseURL` 也可以是函数，例如 `` () => `/api/t/${tenant}` ``。见[租户](../frontend/layout.md#tenants)。

> [!IMPORTANT]
> 页面和 API 必须同源，否则请求不带 cookie，已登录也返回 `401`：直接访问前端开发服务器的端口、用局域网 IP 访问 HTTP 页面（`Secure` cookie 只在 HTTPS 和 `localhost` 上保存）都会这样。浏览器之外（例如 Node 脚本）用 `headers` 传 `Authorization: Bearer <token>`。

<a id="calling-endpoints"></a>
### 调用接口

`api` 是 `createClient` 创建的客户端，`c` 是 [Go 客户端](#calling-go-client)。

```ts
const article = await api.article.get(id) // ArticleDTO
const list = await api.article.list({ q: "go", page: 2 }) // 查询参数，类型是 ArticleListInput
await api.article.publish(id, { idempotencyKey: crypto.randomUUID() }) // 请求头
const created = await api.article.create({ title: "Hello", slug: "hello" })
created.headers.location // 有响应头的接口返回 { body, headers }
```

| 服务端 | TypeScript | Go |
| --- | --- | --- |
| 路径参数 | 按在路径中的顺序作为参数 | 同左 |
| 只有请求体 | 请求体作为最后一个参数 | 同左 |
| 有查询参数或请求头 | 最后一个参数是 `<OperationId>Input`，请求头转为 camelCase，请求体在 `body` 中 | 输入类型去掉路径参数后作为最后一个参数 |
| 只有响应体 | 返回它的类型；`204` 返回 `void` | 返回 `Body` 的类型 |
| 有响应头或 `Status` | 返回 `{ body, headers }` | 返回输出类型，例如 `created.Location` |

- 匿名结构体的 `Body` 以输入或输出类型的名字加 `Body` 命名，例如 `RefreshInputBody`。
- 查询参数和请求头为零值时不发送，服务端使用默认值。

<a id="ts-errors"></a>
### 处理 `ApiError`

非 2xx 的响应抛出 `ApiError`：`status`、`code`、`detail`（可以展示给用户）、`fields`（`422` 时每个字段的问题，见[校验错误的响应](requests.md#validation-error-response)）、`params`（[公开参数](../general/errors.md#public-parameters)，没有时是 `{}`）、`requestId`、`retryAfter`（秒）。业务错误码用 `errors.ts` 中的常量判断，框架的错误码同样生成，例如 `IdentityErrors.ConfirmRequired`：

```ts
import { ArticleErrors, isApiError } from "@blog/client/api"

try {
  await api.article.create(changes)
} catch (err) {
  if (isApiError(err, ArticleErrors.SlugTaken)) return form.setError("slug", err.detail)
  if (isApiError(err) && err.status === 422) {
    for (const f of err.fields) form.setError(f.location?.replace(/^body\./, "") ?? "", f.message)
    return
  }
  throw err
}
```

<a id="event-streams"></a>
### 事件流

[`rest.RegisterSSE`](sse.md) 注册的接口返回异步迭代器，事件是按 `event` 区分的联合类型。认证和 `401` 的处理与普通请求相同；推送开始之前的错误抛出 `ApiError`；不认识的事件被跳过。

```ts
for await (const ev of api.article.publishBatch({ ids })) {
  if (ev.event === "progress") showProgress(ev.data) // ev.data: PublishProgress
}

// signal 停止接收，lastEventId 接着上次的位置继续
for await (const ev of api.article.events({}, { signal: stop.signal, lastEventId })) {
  lastEventId = ev.id ?? lastEventId
}
```

<a id="uploading-files"></a>
### 上传文件

[上传接口](../libraries/filesystem.md#rest-upload)的表单生成为 `<OperationId>Form`，文件字段的类型是 `Blob`，可以直接传 `File`：

```ts
const uploaded = await api.file.upload({ file: input.files[0] })
```

<a id="downloading-files"></a>
### 下载文件

[文件存储](../libraries/filesystem.md)中的文件有公开地址，直接用在 `<a href>`、`<img src>` 中。返回 `huma.StreamResponse` 的接口（例如[导入失败的行](../features/import-export.md)）返回 `{ blob, filename }`：

```ts
import { saveBlob } from "@blog/client/api"

const { blob, filename } = await api.article.failures(id, { format: "xlsx" })
saveBlob(blob, filename || "failures.xlsx")
```

<a id="go-client"></a>
## Go 客户端

其他 Go 服务、命令行工具、端到端测试和 Go 写的 App 用生成的 Go 客户端。类型与服务端同名、同包（例如 `article.Changes`）。

<a id="generating-go-client"></a>
### 生成 Go 客户端

在 `tenon.json` 中按 `BasePath` 写上输出目录，`tenon generate` 和 `tenon dev` 一起生成：

```json
{ "apis": { "/api": { "go": "../api" } } }
```

输出目录通常是 [go.work](https://go.dev/ref/mod#workspaces) 中的另一个模块，`go.mod` 只需要 `github.com/google/uuid` 这类小库。根包有 `Client`、`Options` 和每组接口的方法；子包是接口用到的类型和错误码，与服务端的包同名（例如 `article`、`page`）；`wire` 是运行时。

- 重新生成时只删除上次生成、这次已经移除的文件，手写的文件保持不变。
- 返回原始字节或非 SSE 流的接口、`RawBody` 不是 `huma.MultipartFormFiles` 的接口不生成，列在标准错误中。

<a id="calling-go-client"></a>
### 用 `api.New` 调用接口

`api` 是生成的客户端的根包，`tokens` 是 [`wire.Auth`](#go-client-auth)。参数和返回值的规则见[调用接口](#calling-endpoints)。

```go
c := api.New(api.Options{
    BaseURL:    "https://api.example.com",
    Auth:       tokens,
    AppVersion: "macos/1.4.2",                    // X-App-Version
    Language:   func() string { return "zh-CN" }, // 错误的 Detail 按它翻译
})

created, err := c.Article.Create(ctx, article.Changes{Title: "Hello", Slug: "hello"})
list, err := c.Article.List(ctx, article.ListInput{}) // 零值不发送：第一页、每页 20 条
for _, a := range list.Items { // page.Offset[article.ArticleDTO]
    // ...
}
```

额外的请求头在 `Options.HTTPClient` 的 `Transport` 中添加。

<a id="go-client-errors"></a>
### 用 `errors.Is` 判断错误

非 2xx 的响应返回 `*wire.Error`：`Status`、`Code`、`Detail`、`Fields`、`Params`、`RequestID`、`RetryAfter`（`time.Duration`）。错误码在对应的包中生成为同名常量，框架的同样生成，例如 `identity.ErrUnauthenticated`、`ratelimit.ErrExceeded`、`page.ErrInvalidCursor`、`wire.ErrUpgradeRequired`：

```go
switch {
case errors.Is(err, article.ErrSlugTaken): // article 是生成的客户端中的包
    form.SetError("slug", "已被使用")
case errors.Is(err, identity.ErrUnauthenticated):
    showLogin()
case errors.Is(err, wire.ErrUpgradeRequired):
    showUpgrade()
}
```

<a id="go-client-auth"></a>
### 认证：`wire.Auth`

`Options.Auth` 提供受保护接口的 Bearer token；没有设置时受保护的接口都返回 `401`。[API token](../security/api-tokens.md) 用 `wire.StaticToken`：

```go
c := api.New(api.Options{BaseURL: "https://blog.example.com/api", Auth: wire.StaticToken(token)})
```

- 返回 `401` 时调用 `Auth.Refresh`，成功后用新 token 重试一次，并发的请求只刷新一次。`StaticToken` 的 `Refresh` 总是失败，调用方收到 `identity.ErrUnauthenticated`，让用户重新登录。
- token 存在别处（例如系统的钥匙串）时，自己实现 `wire.Auth`。

<a id="go-client-events"></a>
### 事件流

[`rest.RegisterSSE`](sse.md) 登记的接口返回迭代器。每个接口生成一个事件结构体，每种事件一个指针字段，一次只有一个不为 `nil`；默认的 `message` 事件是 `Message` 字段：

```go
for ev, err := range c.Article.Events(wire.WithLastEventID(ctx, lastID), article.EventsInput{}) {
    if err != nil {
        return err // 建立连接前的错误（例如 401）也从这里返回
    }
    switch {
    case ev.Changed != nil:
        refresh(*ev.Changed)
    case ev.Deleted != nil:
        remove(*ev.Deleted)
    }
}
```

- 服务端结束响应、收到 `data: [DONE]`、`ctx` 取消或跳出循环时结束。不认识的事件被跳过。
- 断线后由调用方决定是否重连；`wire.WithLastEventID` 带上最后一个事件的 `ID`，作为 `Last-Event-ID` 发送。

<a id="go-client-uploads"></a>
### 上传文件：`wire.File`

`RawBody` 为 `huma.MultipartFormFiles[T]` 的[上传接口](../libraries/filesystem.md#rest-upload)把表单 `T` 作为最后一个参数，`huma.FormFile` 字段生成为 `wire.File`；匿名的表单以输入类型的名字加 `Form` 命名：

```go
f, err := os.Open("cover.png")
if err != nil {
    return err
}
defer f.Close()
uploaded, err := c.File.Upload(ctx, file.UploadInputForm{
    File: wire.File{Name: "cover.png", ContentType: "image/png", Body: f},
})
```

- 请求体边读边发送。`Body` 为 `nil` 的文件不发送。
- 返回 `401` 时，`Body` 实现了 `io.Seeker` 就在刷新凭据后重传；否则只刷新凭据，调用方收到 `401`，再调用一次。

<a id="copied-types"></a>
### 复制的类型

接口用到的应用和 Tenon 的类型复制到客户端：字段、tag、注释、泛型参数和同一个包中这个类型的常量保留；方法、`json:"-"` 和未导出的字段去掉。标准库和 `github.com/google/uuid` 的类型直接导入。

| 情况 | 结果 |
| --- | --- |
| 类型有 `MarshalJSON`、`UnmarshalText` 等自定义编码 | 生成失败：复制字段得不到相同的 JSON。把它放进客户端可以导入的包，或改用普通字段 |
| 用到其他第三方包的类型 | 生成失败。确认那个包适合在客户端使用后用 `--import` 允许 |
| 两个包同名，例如应用和框架的 `settings` | 生成失败。用 `--rename 包路径=新名字` 给其中一个改名 |

<a id="other-languages"></a>
## 其他语言

用 [openapi 命令](../general/commands.md#openapi-command)导出的文档生成。生成器报错或丢失字段时改用 `openapi --spec 3.0`。常用的生成器：Flutter 用 [openapi-generator](https://openapi-generator.tech/docs/generators/dart-dio) 的 `dart-dio` 或 [swagger_parser](https://pub.dev/packages/swagger_parser)，iOS 用 [swift-openapi-generator](https://github.com/apple/swift-openapi-generator)，Android 用 openapi-generator 的 `kotlin` 或 [Kiota](https://learn.microsoft.com/openapi/kiota/)，Python 用 [openapi-python-client](https://github.com/openapi-generators/openapi-python-client)。

请求带 `Authorization: Bearer`，token 来自在响应体中返回 token 的[登录接口](../security/api-tokens.md#login-and-logout)；错误按 `code` 区分。其余规则见[客户端协议](protocol.md)。

<a id="app-version"></a>
## App 版本：`X-App-Version`

安装在设备上的 App 从第一个发布的版本起，在每个请求中带上平台和版本，服务端才能[停止支持旧版本](middleware.md#app-version)。浏览器中的单页应用省略它。

```http
X-App-Version: android/2.3.1
```

- 平台名由应用决定，常用 `ios`、`android`、`macos`、`windows`、`linux`；版本是商店中显示的版本号。
- TypeScript 用 `appVersion` 设置，Go 用 `Options.AppVersion`。
- 收到 `426`、`code` 为 `app.upgrade_required` 时，停在升级页面，打开应用商店或开始自动更新。`detail` 可以展示给用户。

<a id="checking-changes"></a>
## 用 oasdiff 检查接口变更

删掉响应中的字段、新增必填参数，都会让旧版本的 App 出错。在 PR 的 CI 中用 [oasdiff](https://github.com/oasdiff/oasdiff) 和主分支的文档比较：

```bash
go install github.com/oasdiff/oasdiff@v1.37.0

./bin/blog openapi > openapi.json
git diff --exit-code openapi.json   # 改了接口，却没有重新导出、提交

git show origin/main:openapi.json > base.json
# 接口：旧文档在前，新文档在后
oasdiff breaking <(jq 'del(.webhooks)' base.json) <(jq 'del(.webhooks)' openapi.json) --fail-on ERR
# webhook：新旧交换位置，--unmatch-path 排除全部接口
oasdiff breaking openapi.json base.json --unmatch-path '.*' --fail-on ERR
```

- [webhook](../features/webhooks.md#documenting) 交换新旧文档单独比较：应用是发送方，oasdiff 按接收方判断。
- 确实要做不兼容的变更时，把 oasdiff 输出中的那一行写进一个文件，用 `--err-ignore` 传入。
- schema 改名时结构不变，由前端重新生成后的编译器发现。
