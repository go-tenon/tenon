# 客户端代码

前端、App 和其他服务调用你的 JSON API 时，请求和响应的类型直接从服务端生成，与服务端始终一致：服务端加了字段、改了字段名，重新生成后前端的编译器就会提示。Tenon 的 `client` 命令从路由声明直接生成 TypeScript 和 Go 客户端：函数按 operationId 分组，类型和服务端一致，业务错误码是常量。其他语言用 [openapi 命令](../general/commands.md#openapi-command)导出的 OpenAPI 文档生成。所有接口共同遵守的请求头、错误、分页和会话等约定见[客户端协议](protocol.md)，其他语言的客户端按它实现。

这一页介绍怎样生成和调用 TypeScript 客户端。在 React 页面中配合 TanStack Query 使用、复用校验规则和权限，见[在 React 中调用接口](../frontend/client-react.md)；Go 客户端见 [Go 客户端](go-client.md)。

阅读前需要了解：[路由](routing.md)；[入门教程第 5 章](../tutorial/05-pages.md#client)用过生成的客户端。

- [示例用法](#introduction)
- [生成的名称](#generated-names)
- [TypeScript](#typescript)
    - [生成客户端](#generating-types)
    - [创建客户端](#creating-the-client)
    - [调用接口](#calling-endpoints)
    - [错误](#ts-errors)
    - [事件流](#event-streams)
    - [上传文件](#uploading-files)
    - [下载文件](#downloading-files)
- [其他语言](#other-languages)
- [App 版本](#app-version)
- [检查接口变更](#checking-changes)
- [常见问题](#troubleshooting)
- [API 参考](#api-reference)

<a id="introduction"></a>
## 示例用法

```bash
go run ./cmd/blog client --ts packages/client/src   # TypeScript：每个 API 一个目录
go run ./cmd/blog client --out ../api            # Go
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

开发时 `go tool tenon dev` 在每次重新编译后自动生成，改了 Go 接口，前端的类型随之更新，见[开发工具](../installation/tooling.md)。

把 OpenAPI 文档也提交到仓库，接口的每次变化都会出现在代码评审中，CI 还可以[检查不兼容的变更](#checking-changes)：

```bash
./bin/blog openapi > openapi.json
```

<a id="generated-names"></a>
## 生成的名称

生成器用 OpenAPI 中的名称命名生成的代码，这些名称来自 Go 代码：

| OpenAPI | 来源 | 例子 |
| --- | --- | --- |
| operationId | 路由的 `OperationID`，必须是 kebab-case | `article-publish`，生成函数 `articlePublish` |
| schema 名 | 包名加类型名，类型名已以包名开头时不重复 | `article.Changes` 为 `ArticleChanges`，`article.ArticleDTO` 为 `ArticleDTO` |
| 错误响应 | 所有接口共用 | `ErrorModel` |

规则见[共享响应结构](routing.md#shared-responses)。改名会改变生成的代码，和修改接口一样需要通知调用方。

<a id="typescript"></a>
## TypeScript

<a id="generating-types"></a>
### 生成客户端

[tenon generate](../installation/tooling.md#generate) 从路由生成 TypeScript 客户端：项目中有共用的客户端包 `packages/client` 时，每个 API 生成到 `packages/client/src/<名称>`，名称默认是 `BasePath` 的最后一段，例如 `/api` 是 `api`、`/api/admin` 是 `admin`，见[前端与 API](../installation/tooling.md#conventions)。它编译应用，运行应用的 `client` 命令（见 [httpcmd.Commands](../installation/tooling.md#app-side)），与 [Go 客户端](go-client.md#generating-go-client)是同一个命令：

```bash
go tool tenon generate   # 等价于 go run ./cmd/blog client --ts packages/client/src
```

前端从客户端包的子路径导入：`@blog/client/api` 是 `api.ts`，`@blog/client/api/queries` 等是同一目录中的其他文件。下面的例子都是 `/api` 的客户端。

| 文件 | 内容 |
| --- | --- |
| `api.ts` | OpenAPI 中的全部类型、每个接口的输入类型、事件流的事件类型，以及 `createClient` |
| `errors.ts` | 服务端用 [`errorx.Define`](../general/errors.md#defining-errors) 定义的错误码，按包分组，例如 `ArticleErrors.SlugTaken` |
| `runtime.ts` | 发送请求、`ApiError`、事件流和下载 |
| `queries.ts` | TanStack Query 的查询和修改选项，见 [TanStack Query](../frontend/client-react.md#tanstack) |
| `schemas.ts` | 每个接口的请求体和查询参数的校验规则，见[校验规则与验证提示](../frontend/client-react.md#validation-rules) |
| `messages.ts` | 各语言的验证提示模板，见[校验规则与验证提示](../frontend/client-react.md#validation-rules) |
| `permissions.ts` | 权限目录和每个接口要求的权限，见[权限](../frontend/client-react.md#ts-permissions) |
| `enums.ts` | 带显示文字的枚举，见[枚举](../frontend/client-react.md#ts-enums) |
| `resources.ts` | 用 `api.Resource` 声明的资源，见[资源](../frontend/resources.md#generated) |
| `settings.ts` | 用 `api.Settings` 声明的设置页面，见[设置页面](../frontend/resources.md#settings) |
| `kit.ts` | `createKit`：把客户端和上面的元数据合成一个对象，交给资源和设置的模型，见[前端的资源模型](../frontend/resources.md#model) |

生成的代码是独立的 TypeScript，由 Go 程序直接生成。重新生成时只删除带生成标记的文件，目录中手写的文件保持不变。把它们提交到仓库，CI 中重新生成后用 `git diff --exit-code` 检查是否忘了更新。

开发时 [tenon dev](../installation/tooling.md#dev) 在每次重新编译后自动生成，改了 Go 接口，前端的类型随之更新。

<a id="creating-the-client"></a>
### 创建客户端

浏览器中的登录凭据是服务端写入的 HttpOnly [会话 cookie](../security/api-tokens.md#browser-sessions)，浏览器随同源请求自动带上它，页面上的代码不保管 token。框架的 `@go-tenon/client` 提供会话 `createSession`，它只记录当前是谁。下面是 `apps/web/src/api.ts`：

```ts
import { createSession } from "@go-tenon/client"
import { apiFetch } from "@go-tenon/start"
import { createClient } from "@blog/client/api"

// session 记录当前的身份：打开页面、登录后调用 load 读取当前用户，没有登录时接口返回 401。
export const session = createSession({ load: async () => ({ account: (await api.user.me()).id }) })

export const api = createClient({
  baseURL: "/api",
  // 浏览器中就是 fetch；服务端渲染时经内部 socket 调用 Go，并带上页面请求的 cookie
  fetch: apiFetch,
  // 受保护的接口返回 401：登录已经失效（过期、在别处退出或被撤销），会话随之变为未登录
  session,
  // 403 auth.confirm_required（rest.Confirmed）：弹出密码框，确认成功后重试原请求
  onConfirmRequired: () => confirmDialog.open(),
})
```

- 打开页面（包括刷新页面、新标签页）时先调用一次 `await session.restore()`：它调用 `load`，返回 `true` 表示已经登录，决定显示登录页还是应用。已经知道身份时不再读取。
- 登录接口成功后（服务端已经写入 cookie）调用 `await session.signIn()`，它用 `load` 读取身份；也可以直接传入身份。登出时先调用登出接口，服务端撤销这次登录并清除 cookie，再调用 `session.signOut()`。
- 受保护的接口返回 `401` 时，客户端调用 `session.expired()`：清除身份，调用 `onSignedOut`（传了时）。用 `@go-tenon/ui` 的 [`createApp`](../frontend/app.md#creating-the-app) 时不需要它：会话变化后应用重新运行路由的守卫，需要登录的页面跳到登录页。
- 请求只对发出时的身份有效：请求发出之后登录、退出或切换了身份（`session.generation()` 变了），收到的 `401` 原样抛出，不会让新的会话失效；`403 auth.confirm_required` 也不再确认重试，免得用别人的身份重发。`session.fetch` 同样如此。
- 公开的接口（[`rest.Public`](routing.md#access-policies)）的 `401` 不影响会话，原样返回给调用者。
- 请求带上 `Accept-Language`（默认是浏览器的语言，`language` 可以覆盖），错误的 `detail` 按它翻译。App 的 WebView 设置 `appVersion`，作为 [`X-App-Version`](#app-version) 发送。
- `headers` 是每次请求附加的请求头，每次请求时读取，值为空时不发送，接口自己的请求头优先，例如租户在请求头中时 `headers: () => ({ "X-Tenant": session.identity()?.tenant })`。`baseURL` 也可以是函数，例如租户在路径中时 `` baseURL: () => `/api/t/${tenant}` ``。见[租户](../frontend/layout.md#tenants)。

会话 cookie 只在同源请求中发送，页面和 API 要由同一个源提供。在浏览器之外使用生成的客户端（例如 Node 脚本）时，用 `headers` 传 `Authorization: Bearer <token>`。

<a id="calling-endpoints"></a>
### 调用接口

接口按 operationId 分组，与 Go 客户端的规则相同：`article-publish` 生成 `api.article.publish`，只有一段的 `ping` 生成 `api.ping`。

```ts
const article = await api.article.get(id) // ArticleDTO
const list = await api.article.list({ q: "go", page: 2 }) // 查询参数，类型是 ArticleListInput
await api.article.publish(id, { idempotencyKey: crypto.randomUUID() }) // 请求头
const created = await api.article.create({ title: "Hello", slug: "hello" })
created.body.id
created.headers.location // 有响应头的接口返回 { body, headers }
```

| 服务端 | 客户端 |
| --- | --- |
| 路径参数 | 按在路径中出现的顺序作为参数 |
| 只有请求体 | 请求体的类型作为最后一个参数 |
| 有查询参数或请求头 | 最后一个参数是 `<OperationId>Input`，查询参数用原名，请求头转为 camelCase，请求体在 `body` 中 |
| 响应体 | 返回它的类型；`204` 返回 `void` |
| 响应头 | 返回 `{ body, headers }` |

方法的注释来自路由的 `Summary` 和 `Description`，类型的注释来自字段的 `doc` 标签和 Go 注释，在编辑器中悬停即可看到。

<a id="ts-errors"></a>
### 错误

非 2xx 的响应抛出 `ApiError`：`status` 是状态码，`code` 是业务错误码，`detail` 是可以展示给用户的说明，`fields` 是 `422` 时每个字段的问题（[翻译后的提示和规则 `code`](validation.md#validation-error-response)），`params` 是业务错误的[公开参数](../general/errors.md#public-parameters)（没有时是空对象），`requestId` 用来报告问题，`retryAfter` 是建议的等待秒数。业务错误码用 `errors.ts` 中的常量判断：

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

框架自带的错误码同样会生成，例如 `IdentityErrors.ConfirmRequired`、`RatelimitErrors.Exceeded`。

<a id="event-streams"></a>
### 事件流

[`rest.RegisterSSE`](sse.md) 注册的接口返回异步迭代器。事件是按 `event` 区分的联合类型，`data` 的类型随之确定：

```ts
for await (const ev of api.article.publishBatch({ ids })) {
  if (ev.event === "progress") showProgress(ev.data) // ev.data: PublishProgress
}
```

迭代器建立在 `fetch` 上，所以能带 Bearer token，`401` 时同样刷新。推送开始之前的错误照常抛出 `ApiError`。服务端新增的、客户端还不认识的事件被跳过。最后一个参数可以传 `signal` 停止接收，以及 `lastEventId` 接着上次的位置继续：

```ts
const stop = new AbortController()
for await (const ev of api.article.events({}, { signal: stop.signal, lastEventId })) {
  lastEventId = ev.id ?? lastEventId
}
```

<a id="uploading-files"></a>
### 上传文件

[服务端上传接口](../libraries/file-uploads.md#rest-upload)的表单字段生成为 `<OperationId>Form`，文件字段的类型是 `Blob`，`File` 可以直接传入：

```ts
const uploaded = await api.file.upload({ file: input.files[0] })
```

<a id="downloading-files"></a>
### 下载文件

[文件存储](../libraries/filesystem.md)中的文件有公开地址，`<a href>`、`<img src>` 直接使用。返回 `huma.StreamResponse` 的接口生成为下载，例如[导入失败的行](../features/import-export.md)，返回 `{ blob, filename }`，`saveBlob` 让浏览器保存它：

```ts
import { saveBlob } from "@blog/client/api"

const { blob, filename } = await api.article.failures(id, { format: "xlsx" })
saveBlob(blob, filename || "failures.xlsx")
```

<a id="other-languages"></a>
## 其他语言

各语言的生成器对 OpenAPI 3.1 的支持不一。生成器报错或丢失字段时，改用 `openapi --spec 3.0` 导出的文档：

```bash
./bin/blog openapi --spec 3.0 > openapi-3.0.json
```

| 调用方 | 生成器 |
| --- | --- |
| Flutter | [openapi-generator](https://openapi-generator.tech/docs/generators/dart-dio) 的 `dart-dio`，或 [swagger_parser](https://pub.dev/packages/swagger_parser) |
| iOS | [swift-openapi-generator](https://github.com/apple/swift-openapi-generator) |
| Android | openapi-generator 的 `kotlin`，或 [Kiota](https://learn.microsoft.com/openapi/kiota/) |
| Python 脚本 | [openapi-python-client](https://github.com/openapi-generators/openapi-python-client) |

生成的客户端同样需要处理认证：请求带 `Authorization: Bearer`，token 来自应用在响应体中返回 token 的登录接口（见[登录与登出](../security/api-tokens.md#login-and-logout)），错误按 `code` 区分。

<a id="app-version"></a>
## App 版本

安装在手机和电脑上的 App，每个请求都带上平台和版本，服务端才能在需要时[停止支持旧版本](optional-middleware.md#app-version)：

```http
X-App-Version: android/2.3.1
```

从第一个发布的版本就开始发送。带了它的版本，服务端才能按版本拦截。平台名自己决定，常用 `ios`、`android`、`macos`、`windows`、`linux`；版本就是商店中显示的版本号，例如 `2.3.1`。浏览器中的单页应用总是最新的，可以省略。

收到 `426`、`code` 为 `app.upgrade_required` 时，App 停在升级页面，打开应用商店或开始自动更新，等升级后再请求。`detail` 是可以展示给用户的提示。

<a id="checking-changes"></a>
## 检查接口变更

已经上线的接口，删掉响应中的字段、新增必填参数，都会让旧版本的 App 出错。在 PR 的 CI 中用 [oasdiff](https://github.com/oasdiff/oasdiff) 把导出的文档与主分支上的比较，发现不兼容的变更时失败：

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

[webhook](../features/webhooks.md#documenting) 要单独比较。oasdiff 把 webhook 当作别人发给你的请求，而实际上你是发送方，判断方向正好相反：给事件内容加一个字段，它报不兼容；删掉一个字段，它只给出警告。交换新旧文档的位置，结论就对了。

确实要做不兼容的变更时，例如接口还没有调用方，把 oasdiff 输出中的那一行写进一个文件，用 `--err-ignore` 传入：

```text
PUT /articles/{id} added the new required request property `slug`
```

oasdiff 只比较请求和响应的结构。schema 改名时结构相同，由前端重新生成类型后的编译器发现。

<a id="troubleshooting"></a>
## 常见问题

**前端代码中找不到新加的接口或字段**

客户端还没有重新生成。开发环境（`bun run dev`）会在 Go 程序重新编译后自动生成；没有运行开发环境时，运行 `bun run generate`。

**生成时报错，Go 程序编译失败**

客户端从 Go 程序导出的接口描述生成，Go 代码编译不过就无法生成。先修好编译错误。

**两个模块的同名类型冲突**

生成的类型名带着包名，例如 `article.Changes` 是 `ArticleChanges`，见[生成的名称](#generated-names)。如果仍然冲突，给其中一个类型改名。

**调用接口时报错 401，但已经登录了**

会话 cookie 没有带上。页面和 API 必须同源：直接访问前端开发服务器的端口、或者用局域网 IP 访问 HTTP 页面时（`Secure` 的 cookie 只在 HTTPS 和 `localhost` 上保存），请求不带 cookie。见[浏览器的会话 cookie](../security/api-tokens.md#browser-sessions)。

<a id="api-reference"></a>
## API 参考

### client 命令

由 `httpcmd.Commands` 加入应用的命令行，在服务端的源码目录中运行，用 `nil` 依赖构造应用的路由、取出其中的 API，离线运行。

| 选项 | 说明 |
| --- | --- |
| `--ts <dir>` | TypeScript 客户端的输出目录，每个 API 写到其中以名称命名的子目录，例如 `packages/client/src`。应用的翻译文件由 `httpcmd.Translations` 传入，见[校验规则与验证提示](../frontend/client-react.md#validation-rules) |
| `--name <path>=<name>` | 按 `BasePath` 指定 TypeScript 客户端的目录名，默认是 `BasePath` 的最后一段，可重复；两个 API 名称相同时报错 |
| `--out <dir>` | Go 客户端的输出目录，目录名是根包的包名，例如 `../api` |
| `--main <pkg>` | 应用的主包，在它的依赖中查找错误码；默认是当前程序 |
| `--import <pkg>` | 生成的 Go 代码可以直接导入的包（含子包），可重复；默认只有 `github.com/google/uuid` |
| `--rename <path>=<name>` | 服务端包路径对应的镜像包名，可重复 |
| `--base-path <path>` | 只生成 `BasePath` 为它的 API，例如 `/api/v2`；路由中有[多个 API](openapi.md#api-versioning) 时 `--out` 需要它 |

### clientgen

包 `github.com/go-tenon/tenon/httpx/rest/clientgen`。

```go
func Command(router func() chi.Routes, translations ...fs.FS) *cobra.Command
func Generate(api *rest.API, opts Options) (*Result, error)
func GenerateTS(api *rest.API, opts TSOptions) (*Result, error)
```

`Command` 单独构造 `client` 命令，`translations` 与 `httpcmd.Translations` 相同；`Generate`、`GenerateTS` 在代码中生成，例如写自己的生成工具时。
