# 客户端代码

前端、App 和其他服务调用你的 JSON API 时，请求和响应的类型直接从服务端生成，与服务端始终一致：服务端加了字段、改了字段名，重新生成后前端的编译器就会提示。Tenon 的 `client` 命令从路由声明直接生成 TypeScript 和 Go 客户端：函数按 operationId 分组，类型和服务端一致，业务错误码是常量。其他语言用 [openapi 命令](../general/commands.md#openapi-command)导出的 OpenAPI 文档生成。所有接口共同遵守的请求头、错误、分页和会话等约定见[客户端协议](../http/protocol.md)，其他语言的客户端按它实现。

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
    - [TanStack Query](#tanstack)
    - [校验规则与验证提示](#validation-rules)
    - [权限](#ts-permissions)
    - [枚举](#ts-enums)
    - [错误、校验与分页](#client-runtime)
    - [其他工具](#other-ts-tools)
- [Go 客户端](#go)
    - [生成客户端](#generating-go-client)
    - [调用接口](#calling-go-client)
    - [错误](#go-client-errors)
    - [认证](#go-client-auth)
    - [事件流](#go-client-events)
    - [上传文件](#go-client-uploads)
    - [复制的类型](#copied-types)
- [其他语言](#other-languages)
- [App 版本](#app-version)
- [检查接口变更](#checking-changes)
- [API 参考](#api-reference)

<a id="introduction"></a>
## 示例用法

```bash
go run ./cmd/api client --ts packages/client/src   # TypeScript：每个 API 一个目录
go run ./cmd/api client --out ../api            # Go
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
./bin/api openapi > openapi.json
```

<a id="generated-names"></a>
## 生成的名称

生成器用 OpenAPI 中的名称命名生成的代码，这些名称来自 Go 代码：

| OpenAPI | 来源 | 例子 |
| --- | --- | --- |
| operationId | 路由的 `OperationID`，必须是 kebab-case | `article-publish`，生成函数 `articlePublish` |
| schema 名 | 包名加类型名，类型名已以包名开头时不重复 | `article.Changes` 为 `ArticleChanges`，`article.ArticleDTO` 为 `ArticleDTO` |
| 错误响应 | 所有接口共用 | `ErrorModel` |

规则见[共享响应结构](../http/routing.md#shared-responses)。改名会改变生成的代码，和修改接口一样需要通知调用方。

<a id="typescript"></a>
## TypeScript

<a id="generating-types"></a>
### 生成客户端

[tenon generate](../installation/tooling.md#generate) 从路由生成 TypeScript 客户端：项目中有共用的客户端包 `packages/client` 时，每个 API 生成到 `packages/client/src/<名称>`，名称默认是 `BasePath` 的最后一段，例如 `/api` 是 `api`、`/api/admin` 是 `admin`，见[前端与 API](../installation/tooling.md#conventions)。它编译应用，运行应用的 `client` 命令（见 [httpcmd.Commands](../installation/tooling.md#app-side)），与 [Go 客户端](#generating-go-client)是同一个命令：

```bash
go tool tenon generate   # 等价于 go run ./cmd/api client --ts packages/client/src
```

前端从客户端包的子路径导入：`@blog/client/api` 是 `api.ts`，`@blog/client/api/queries` 等是同一目录中的其他文件。下面的例子都是 `/api` 的客户端。

| 文件 | 内容 |
| --- | --- |
| `api.ts` | OpenAPI 中的全部类型、每个接口的输入类型、事件流的事件类型，以及 `createClient` |
| `errors.ts` | 服务端用 [`errorx.Define`](../general/errors.md#defining-errors) 定义的错误码，按包分组，例如 `ArticleErrors.SlugTaken` |
| `runtime.ts` | 发送请求、刷新 access token、`ApiError`、事件流和下载 |
| `queries.ts` | TanStack Query 的查询和修改选项，见 [TanStack Query](#tanstack) |
| `schemas.ts` | 每个接口的请求体和查询参数的校验规则，见[校验规则与验证提示](#validation-rules) |
| `messages.ts` | 各语言的验证提示模板，见[校验规则与验证提示](#validation-rules) |
| `permissions.ts` | 权限目录和每个接口要求的权限，见[权限](#ts-permissions) |
| `enums.ts` | 带显示文字的枚举，见[枚举](#ts-enums) |
| `resources.ts` | 用 `api.Resource` 声明的资源，见[资源](resources.md#generated) |

生成的代码是独立的 TypeScript，由 Go 程序直接生成。重新生成时只删除带生成标记的文件，目录中手写的文件保持不变。把它们提交到仓库，CI 中重新生成后用 `git diff --exit-code` 检查是否忘了更新。

开发时 [tenon dev](../installation/tooling.md#dev) 在每次重新编译后自动生成，改了 Go 接口，前端的类型随之更新。

<a id="creating-the-client"></a>
### 创建客户端

单页应用用 [JWT 模式](../security/api-tokens.md#jwt)登录，refresh token 保存在 [HttpOnly cookie](../security/api-tokens.md#single-page-applications) 中。access token 只放在内存里：请求自动带上它，受保护的接口返回 `401` 时刷新一次，再重试请求。框架的 `@go-tenon/client` 提供会话 `createSession`，按[客户端协议](../http/protocol.md#sessions)保存和刷新凭据。下面是 `apps/web/src/client.ts`：

```ts
import { createSession } from "@go-tenon/client"
import { createClient } from "@blog/client/api"
import { router } from "./router"

// 刷新接口靠 cookie 认证。单页应用与 API 在不同的子域名时需要 credentials: "include"。
const plain = createClient({ baseURL: "/api", credentials: "include" })

export const session = createSession({
  refresh: () => plain.auth.refresh(), // 浏览器：refresh token 在 cookie 中，请求体为空
  onSignedOut: () => router.navigate({ to: "/login" }),
})

export const api = createClient({
  baseURL: "/api",
  credentials: "include",
  auth: session.auth,
  // 403 auth.confirm_required（rest.Confirmed）：弹出密码框，确认成功后重试原请求
  onConfirmRequired: () => confirmDialog.open(),
})
session.connect(api)
```

- 打开页面（包括刷新页面、新标签页）时先调用一次 `await session.restore()`，返回 `true` 表示已经登录，决定显示登录页还是应用。
- 登录接口返回后调用 `await session.signIn(out, { account: me.id })`：所有连接的客户端得到 access token，身份记为这个账号。登出时先调用登出接口，服务端撤销这次登录并清除 cookie，再调用 `await session.signOut()`。
- 几个请求同时收到 `401` 时只刷新一次，它们都等这一次的结果。刷新返回 `401` 时这次登录已经失效：清除凭据和身份，调用 `onSignedOut`；网络错误不清除凭据，下次再试。
- 一个会话可以连接多个 API 的客户端（`session.connect`），例如同一次登录调用 `/api` 和 `/api/admin`。
- 公开的接口（[`rest.Public`](../http/routing.md#access-policies)）直接请求，`401` 原样返回给调用者。
- 请求带上 `Accept-Language`（默认是浏览器的语言，`language` 可以覆盖），错误的 `detail` 按它翻译。App 的 WebView 设置 `appVersion`，作为 [`X-App-Version`](#app-version) 发送。
- `headers` 是每次请求附加的请求头，每次请求时读取，值为空时不发送，接口自己的请求头优先，例如租户在请求头中时 `headers: () => ({ "X-Tenant": session.identity()?.tenant })`。`baseURL` 也可以是函数，例如租户在路径中时 `` baseURL: () => `/api/t/${tenant}` ``。见[租户](app-shell.md#tenants)。

`/auth/login`、`/auth/refresh` 是应用自己注册的公开接口，写法见[单页应用](../security/api-tokens.md#single-page-applications)。App、桌面端和小程序用响应体传递：登录时省略 `cookie`，refresh token 在响应体中，保存在系统的安全存储中。给 `createSession` 传入存储，`refresh` 收到保存的 refresh token，把它放进请求体：

```ts
const session = createSession({
  storage: secureStorage, // { get, set, remove }，例如 Expo SecureStore
  refresh: (refreshToken) => plain.auth.refresh({ refresh_token: refreshToken }),
})
```

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

非 2xx 的响应抛出 `ApiError`：`status` 是状态码，`code` 是业务错误码，`detail` 是可以展示给用户的说明，`fields` 是 `422` 时每个字段的问题（[翻译后的提示和规则 `code`](../http/validation.md#validation-error-response)），`params` 是业务错误的[公开参数](../general/errors.md#public-parameters)（没有时是空对象），`requestId` 用来报告问题，`retryAfter` 是建议的等待秒数。业务错误码用 `errors.ts` 中的常量判断：

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

<a id="tanstack"></a>
### TanStack Query

`queries.ts` 的 `createQueries` 为每个接口生成 TanStack Query 的选项，不需要手写 query key：

```ts
import { clearOnIdentityChange } from "@go-tenon/client"
import { createQueries } from "@blog/client/api/queries"

export const queries = createQueries(api, {
  queryClient,
  scope: session.scope, // [账号, 租户]：切换账号或租户后，旧的缓存不再命中
})
clearOnIdentityChange(session, queryClient) // 登录、退出、切换身份时清空缓存
```

| 接口 | 生成的选项 | 交给 |
| --- | --- | --- |
| GET | `queries.article.get(id)` 返回 `{ queryKey, queryFn }` | `useQuery`、`queryClient.prefetchQuery`、路由的 loader |
| 其他方法 | `queries.article.publish()` 返回 `{ mutationFn, onSuccess }` | `useMutation` |

```tsx
function Article({ id }: { id: string }) {
  const { data, error } = useQuery(queries.article.get(id))
  const publish = useMutation(queries.article.publish()) // publish.mutate([id, { idempotencyKey }])
  // ……
}
```

- query key 是 `[...scope, API 名称, 分组, 接口, ...参数]`，例如 `[42, "acme", "api", "article", "get", "0193…"]`。分组是 operationId 的第一段，API 名称是客户端的目录名。
- 修改成功后，同一分组的查询全部失效：`article-publish` 让文章列表和所有文章详情重新获取。需要不同的行为时，在 `useMutation` 中覆盖 `onSuccess`。
- `mutationFn` 的参数：方法只有一个参数时就是这个参数；有多个参数时（例如路径参数加请求体）是参数组成的数组。
- `queries.article.key()` 是分组的 key 前缀，`queries.key()` 是整个 API 的。
- 事件流和下载接口没有生成的选项，直接调用客户端的方法。

生成的选项是普通对象，不依赖 TanStack Query 的包，Vue、Solid 等版本的 TanStack Query 同样可以使用。

[实时频道](../libraries/realtime.md#subscribing)收到消息后，让对应的查询失效，由 TanStack Query 重新获取，缓存始终来自接口：

```ts
articles.on("publication", () => queryClient.invalidateQueries({ queryKey: queries.article.key() }))
```

[固定分页](responses.md#offset-pagination)的列表由服务端分页。TanStack Table 的 `pageIndex` 从 0 开始，`page` 从 1 开始；把 `items` 和 `total` 交给表格，并开启 `manualPagination`：

```ts
const { data } = useQuery({
  ...queries.article.list({ page: pagination.pageIndex + 1, per_page: pagination.pageSize }),
  placeholderData: keepPreviousData, // 翻页时保留上一页，表格不闪烁
})
// 表格：data = data?.items ?? []，rowCount = data?.total，manualPagination = true
```

列表的查询参数放进路由的 search params 时，用生成的 `ArticleListInput` 作为 `validateSearch` 的类型，地址和接口的参数是同一个类型。

<a id="validation-rules"></a>
### 校验规则与验证提示

`schemas.ts` 中是每个接口的请求体（`body`）和查询参数（`query`）的校验规则，取自 OpenAPI 的 JSON Schema，只保留类型、结构和校验规则（`minLength`、`pattern`、`enum`、`required` 等）。请求体通常引用命名类型，它们在 `components` 中：

```ts
import { schemas, components } from "@blog/client/api/schemas"

schemas.article.create.body // { $ref: "#/components/schemas/Changes" }
components.Changes.properties.slug // { type: "string", minLength: 1, pattern: "^[a-z0-9-]+$", patternDescription: "小写字母、数字和连字符" }
```

`messages.ts` 中是各语言的验证提示模板，键是[验证错误](../http/validation.md#validation-messages)的 `code`，与服务端返回的提示相同：

```ts
import { messages } from "@blog/client/api/messages"

messages["zh-CN"]["validation.min_length"] // "至少 {{.Min}} 个字符"
```

前端校验表单时用这两份数据，得到的错误码和提示与服务端一致，不需要手写校验规则。修改 Go 结构体的标签后重新生成，前端的规则随之改变。

应用在自己的翻译文件中覆盖了验证提示时，用 `httpcmd.Translations` 把翻译文件交给命令，生成的提示也使用覆盖后的文案：

```go
root.AddCommand(httpcmd.Commands(routes, httpcmd.Translations(locales.FS))...)
```

<a id="ts-permissions"></a>
### 权限

`permissions.ts` 中是权限目录和每个接口要求的权限：

```ts
import { permissions, required, type Permission } from "@blog/client/api/permissions"

permissions["articles:write"] // "撰写和修改自己的文章"
required.article.create // ["articles:write"]
```

权限目录来自 `rest.Options.Permissions`，见[声明权限目录](../security/authorization.md#permission-catalog)；没有设置时，只包含接口要求的权限。设置了权限目录时，接口要求的权限必须在目录中，否则生成失败。`Permission` 是全部权限名的联合类型，前端按权限显示菜单和按钮时，写错的权限名在类型检查时报错。

<a id="ts-enums"></a>
### 枚举

用 [`enum` 包](../http/validation.md#enums)声明的枚举，`api.ts` 中是取值的联合类型，`enums.ts` 中是全部取值和各语言的显示文字：

```ts
import type { ArticleStatus } from "@blog/client/api"
import { enums } from "@blog/client/api/enums"

enums.ArticleStatus.values // ["draft", "published"]，按声明的顺序
enums.ArticleStatus.labels["zh-CN"].draft // "草稿"
```

显示文字取自翻译文件中 `<前缀>.<取值>` 的消息，应用的翻译文件由 `httpcmd.Translations` 传入，见[校验规则与验证提示](#validation-rules)。某种语言只写了部分取值的显示文字，或者所有语言都没有时，`generate` 输出警告；界面中缺少的显示文字先用默认语言，再用取值本身。

<a id="client-runtime"></a>
### 错误、校验与分页

`@go-tenon/client` 还提供按客户端协议处理错误、校验和分页的函数。它们不依赖任何包和浏览器 API，React Native 等环境中同样可以使用。

**错误。** `describeError` 按协议把错误分为几类，界面按类处理：

```ts
import { describeError, fieldErrors } from "@go-tenon/client"

const info = describeError(err)
switch (info.kind) {
  case "invalid": // 400、422 带 errors
    form.setErrors(fieldErrors(err)) // { title: "不能为空", "tags.1": "不能为空" }
    break
  case "rate_limited":
    toast(`请 ${info.retryAfter} 秒后重试`)
    break
  case "method_required":
    showLogin({ methods: info.params.methods as string[] }) // 用其中一种方式重新登录
    break
  case "business":
    toast(info.message) // 按 info.code 处理时，用 errors.ts 中的常量
    break
}
```

| `kind` | 情况 |
| --- | --- |
| `unauthenticated` | `401`，刷新后仍然失败 |
| `forbidden`、`confirm_required`、`two_factor_required`、`method_required` | `403`，按错误码区分 |
| `upgrade_required` | `426`，App 版本过低 |
| `rate_limited` | `429`，`retryAfter` 是建议的等待秒数 |
| `invalid` | `400`、`422` 带字段错误 |
| `business` | 带 `code` 的业务错误 |
| `not_found`、`server` | `404`、`5xx` |
| `network`、`aborted` | 没有得到响应、请求被取消 |

`fieldErrors` 把位置转为表单的字段路径：`body.items[0].name` 是 `items.0.name`；`parseLocation` 得到各段 `["items", 0, "name"]`。

**校验。** `validate` 用生成的[校验规则与验证提示](#validation-rules)校验输入，位置、错误码和提示与服务端一致，提交前就能在字段旁边显示：

```ts
import { validate } from "@go-tenon/client"
import { components, schemas } from "@blog/client/api/schemas"
import { messages } from "@blog/client/api/messages"

const errors = validate(schemas.article.create.body, values, { components, messages, language: "zh-CN" })
// [{ location: "body.title", code: "validation.filled", message: "不能为空", value: "" }]
```

框架的测试用同一批输入分别在前端和服务端校验，保证两边的结果相同。`oneOf`、`anyOf`、`not` 不在前端校验，以服务端的结果为准。

**其他协议。** 会话提供带认证的 `session.fetch(fetch)`，用于不经过生成客户端的请求：带上 access token，`401` 时与其他请求共用一次刷新再重试。基于它和会话的还有：

| 函数 | 用途 | 见 |
| --- | --- | --- |
| `realtimeAuth(session)` | Centrifuge SDK 的 `token` 和 `getToken` | [实时频道](../libraries/realtime.md#tokens) |
| `uploadFile(file, steps)` | 直传到对象存储：单次或分片、重试、进度和取消 | [上传接口](../libraries/file-uploads.md#rest-direct-upload) |
| `watchOperation(open)` | 读取后台操作的进度直到结束，断线重连 | [后台操作](../features/operations.md#progress) |
| `chatTransport`、`chatControls` | `useChat` 的 transport，以及停止、插话、历史 | [AI 助手](../ai/chat.md#conversation-api) |

不经过生成客户端的请求失败时抛出 `HttpError`，形状与生成客户端的 `ApiError` 相同，`describeError` 照常分类。

**分页。** `toPageParams`、`toTableState` 在表格状态（`pageIndex` 从 0 开始）和查询参数（`page` 从 1 开始）之间转换；`parsePageParams` 从地址读取分页，不合法的值取默认值，`per_page` 不超过 100；`parseSort`、`formatSort` 处理 `-created` 这样的排序参数。

<a id="other-ts-tools"></a>
### 其他工具

`openapi.json` 仍然是公开的契约。已经在用 [openapi-typescript](https://openapi-ts.dev) 加 openapi-fetch、或者其他基于 OpenAPI 的工具时，照常从它生成：

```bash
bunx openapi-typescript openapi.json -o src/api.d.ts --root-types --root-types-no-schema-prefix --root-types-keep-casing
```

<a id="go"></a>
## Go 客户端

用 Go 写的调用方，例如 [mygo](https://github.com/egoist/mygo) 写的桌面和移动 App、其他 Go 服务、命令行工具和端到端测试，使用 `clientgen` 直接从路由声明生成的客户端：

```go
a, err := c.Article.Update(ctx, id, article.Changes{Title: "Hello", Slug: "hello"})
if errors.Is(err, article.ErrSlugTaken) {
    // ...
}
```

接口还是同一套 JSON API，Go 客户端只是又一个调用方。和 OpenAPI 生成的代码相比，类型与服务端同名、同包（例如 `article.Changes`），业务错误码是可以 `errors.Is` 的常量。

<a id="generating-go-client"></a>
### 生成客户端

`client` 命令由 [`httpcmd.Commands`](../installation/tooling.md#app-side) 加入命令行。在 `tenon.json` 中按 `BasePath` 写上输出目录，`tenon generate` 和 `tenon dev` 就会一起生成这个 API 的 Go 客户端：

```json
{ "apis": { "/api": { "go": "../api" } } }
```

也可以在服务端的源码目录中直接运行，`--out` 是输出目录：

```bash
go run ./cmd/api client --out ../api
```

类型声明和错误码从源码复制，所以命令要在源码目录中运行；它用 `nil` 依赖构造应用的路由，离线运行。输出目录通常是 [go.work](https://go.dev/ref/mod#workspaces) 中的另一个模块，调用方只依赖它，App 保持精简：

```text
blog/
├── go.work
├── server/          服务端
├── api/             生成的客户端，go.mod 只需要 github.com/google/uuid 这类小库
│   ├── client.go    Client 和 Options
│   ├── article.go   Client.Article 的方法
│   ├── article/     服务端 internal/article 中接口用到的类型和错误码
│   ├── page/        框架的分页类型
│   └── wire/        发送请求、刷新凭据、解码错误的运行时
└── app/             调用方，例如 mygo App
```

目录名就是根包的包名。重新生成时，只删除上次生成、这次已经移除的文件，你自己放进去的文件保持不变。把生成的代码提交到仓库，CI 中重新生成后用 `git diff --exit-code` 检查是否忘了更新，与 [openapi.json](#checking-changes) 的做法相同。

返回原始字节或非 SSE 流的接口、`RawBody` 不是 `huma.MultipartFormFiles` 的接口不生成，命令把它们列在标准错误中。

<a id="calling-go-client"></a>
### 调用接口

```go
c := api.New(api.Options{
    BaseURL:    "https://api.example.com",
    Auth:       tokens,                           // 见下文“认证”
    AppVersion: "macos/1.4.2",                    // 见中间件的“App 最低版本”
    Language:   func() string { return "zh-CN" }, // 错误的 Detail 按它翻译
})

created, err := c.Article.Create(ctx, article.Changes{Title: "Hello", Slug: "hello"})
list, err := c.Article.List(ctx, article.ListInput{Status: article.StatusPublished})
for _, a := range list.Items { // page.Offset[article.ArticleDTO]
    // ...
}
```

方法按以下规则生成：

| 服务端 | 客户端 |
| --- | --- |
| operationId `article-publish` | `c.Article.Publish`；只有一段的 `ping` 是 `c.Ping` |
| 路径参数 | 按在路径中出现的顺序作为参数 |
| 只有 `Body` | `Body` 的类型作为最后一个参数 |
| 有查询参数或请求头 | 输入类型去掉路径参数后作为最后一个参数 |
| 输出只有 `Body` | 返回 `Body` 的类型 |
| 输出有响应头或 `Status` | 返回输出类型，例如 `created.Location`、`created.Body` |

`Body` 是匿名结构体时，生成的类型以输入或输出类型的名字加 `Body` 命名，例如 `RefreshInput` 的请求体是 `RefreshInputBody`。查询参数和请求头为零值时不发送，服务端使用默认值，所以 `article.ListInput{}` 取第一页、每页 20 条。

<a id="go-client-errors"></a>
### 错误

非 2xx 的响应返回 `*wire.Error`：`Status` 是状态码，`Code` 是业务错误码，`Detail` 是可以展示给用户的说明，`Fields` 是 `422` 时每个字段的问题（[翻译后的提示和规则 `code`](../http/validation.md#validation-error-response)），`Params` 是业务错误的[公开参数](../general/errors.md#public-parameters)，`RequestID` 用来报告问题，`RetryAfter` 是 `Retry-After` 响应头建议的等待时间。

服务端用 [`errorx.Define`](../general/errors.md#defining-errors) 定义的错误码，在对应的包中生成为同名常量，用 `errors.Is` 判断：

```go
switch {
case errors.Is(err, article.ErrSlugTaken):
    form.SetError("slug", "已被使用")
case errors.Is(err, identity.ErrUnauthenticated):
    showLogin()
case errors.Is(err, wire.ErrUpgradeRequired):
    showUpgrade()
}
```

框架自带的错误码同样会生成，例如 `identity.ErrUnauthenticated`、`ratelimit.ErrExceeded`、`page.ErrInvalidCursor`。

<a id="go-client-auth"></a>
### 认证

`Options.Auth` 提供受保护接口的 Bearer token；公开的接口直接请求。不透明的 [API token](../security/api-tokens.md) 用 `wire.StaticToken(token)`。[JWT 模式](../security/api-tokens.md#jwt)实现 `wire.Auth` 的两个方法，下面是把 refresh token 放在内存里的客户端写法（浏览器后台更常见的是 HttpOnly cookie，见[单账号后台](../security/admin-example.md#auth-api)）：

```go
type tokens struct {
    mu      sync.Mutex
    access  string
    refresh string
    public  *api.Client // 只调用公开的刷新接口
}

func (t *tokens) Token(context.Context) (string, error) {
    t.mu.Lock()
    defer t.mu.Unlock()
    return t.access, nil
}

func (t *tokens) Refresh(ctx context.Context) error {
    t.mu.Lock()
    refresh := t.refresh
    t.mu.Unlock()
    s, err := t.public.Auth.Refresh(ctx, authapi.RefreshInputBody{RefreshToken: refresh})
    if err != nil {
        return err
    }
    t.mu.Lock()
    defer t.mu.Unlock()
    t.access = s.AccessToken
    if s.RefreshToken != "" {
        t.refresh = s.RefreshToken
    }
    return nil // 同时保存到系统的钥匙串，例如 mygo 的 Secrets
}
```

受保护的接口返回 `401` 时，客户端调用一次 `Refresh`，再用新 token 重试一次。几个请求同时收到 `401` 时只刷新一次；`Refresh` 返回错误时，调用方收到原来的 `401`，这时让用户重新登录。

<a id="go-client-events"></a>
### 事件流

用 [`rest.RegisterSSE`](sse.md) 登记的接口返回事件的迭代器。每个接口生成一个事件结构体，每种事件一个指针字段，一次只有一个不为 `nil`；默认的 `message` 事件是 `Message` 字段：

```go
for ev, err := range c.Article.Events(ctx, article.EventsInput{}) {
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

服务端结束响应、收到 `data: [DONE]`、`ctx` 取消或者你跳出循环时，迭代结束。服务端新增的、客户端还不认识的事件被跳过，旧版本的 App 照常工作。连接中断时由调用方决定是否重连：需要接着上次的位置继续时，把最后一个事件的 `ID` 用 `wire.WithLastEventID` 放进 `ctx`，请求会带上 `Last-Event-ID`：

```go
events := c.Article.Events(wire.WithLastEventID(ctx, lastID), article.EventsInput{})
```

<a id="go-client-uploads"></a>
### 上传文件

`RawBody` 为 `huma.MultipartFormFiles[T]` 的接口（见[服务端上传接口](../libraries/file-uploads.md#rest-upload)），表单 `T` 作为最后一个参数；`huma.FormFile` 字段是 `wire.File`，其余字段原样保留。表单是匿名结构体时，类型以输入类型的名字加 `Form` 命名：

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

请求体边读边发送，内存占用与文件大小无关。`Body` 为 `nil` 的文件跳过。受保护的接口返回 `401` 时，文件内容实现了 `io.Seeker`（例如 `*os.File`、`bytes.Reader`）就在刷新凭据后从头重传；否则只刷新凭据，调用方收到 `401`，再调用一次即可。

<a id="copied-types"></a>
### 复制的类型

接口用到的类型复制到客户端：字段、tag、注释、泛型参数和同一个包中这个类型的常量都会保留，方法不复制，`json:"-"` 和未导出的字段去掉。只有应用模块和 Tenon 的类型会复制；标准库和 `github.com/google/uuid` 的类型直接导入。

| 情况 | 结果 |
| --- | --- |
| 类型实现了 `MarshalJSON`、`UnmarshalText` 等自定义编码 | 生成失败：复制字段得不到相同的 JSON。把它放进客户端可以导入的包，或者改用普通字段 |
| 用到了其他第三方包的类型 | 生成失败。确认那个包适合在客户端使用后，用 `--import 包路径` 允许导入，子包一并允许 |
| 两个包同名，例如应用的 `settings` 和框架的 `settings` | 生成失败。用 `--rename 包路径=新名字` 给其中一个改名 |

<a id="other-languages"></a>
## 其他语言

各语言的生成器对 OpenAPI 3.1 的支持不一。生成器报错或丢失字段时，改用 `openapi --spec 3.0` 导出的文档：

```bash
./bin/api openapi --spec 3.0 > openapi-3.0.json
```

| 调用方 | 生成器 |
| --- | --- |
| Flutter | [openapi-generator](https://openapi-generator.tech/docs/generators/dart-dio) 的 `dart-dio`，或 [swagger_parser](https://pub.dev/packages/swagger_parser) |
| iOS | [swift-openapi-generator](https://github.com/apple/swift-openapi-generator) |
| Android | openapi-generator 的 `kotlin`，或 [Kiota](https://learn.microsoft.com/openapi/kiota/) |
| Python 脚本 | [openapi-python-client](https://github.com/openapi-generators/openapi-python-client) |

生成的客户端同样需要处理认证：请求带 `Authorization: Bearer`，JWT 模式按[客户端的规则](../security/api-tokens.md#jwt)刷新，错误按 `code` 区分。

<a id="app-version"></a>
## App 版本

安装在手机和电脑上的 App，每个请求都带上平台和版本，服务端才能在需要时[停止支持旧版本](../http/middleware.md#app-version)：

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

./bin/api openapi > openapi.json
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

<a id="api-reference"></a>
## API 参考

### client 命令

由 `httpcmd.Commands` 加入应用的命令行，在服务端的源码目录中运行，用 `nil` 依赖构造应用的路由、取出其中的 API，离线运行。

| 选项 | 说明 |
| --- | --- |
| `--ts <dir>` | TypeScript 客户端的输出目录，每个 API 写到其中以名称命名的子目录，例如 `packages/client/src`。应用的翻译文件由 `httpcmd.Translations` 传入，见[校验规则与验证提示](#validation-rules) |
| `--name <path>=<name>` | 按 `BasePath` 指定 TypeScript 客户端的目录名，默认是 `BasePath` 的最后一段，可重复；两个 API 名称相同时报错 |
| `--out <dir>` | Go 客户端的输出目录，目录名是根包的包名，例如 `../api` |
| `--main <pkg>` | 应用的主包，在它的依赖中查找错误码；默认是当前程序 |
| `--import <pkg>` | 生成的 Go 代码可以直接导入的包（含子包），可重复；默认只有 `github.com/google/uuid` |
| `--rename <path>=<name>` | 服务端包路径对应的镜像包名，可重复 |
| `--base-path <path>` | 只生成 `BasePath` 为它的 API，例如 `/api/v2`；路由中有[多个 API](../http/routing.md#api-versioning) 时 `--out` 需要它 |

### clientgen

包 `github.com/go-tenon/tenon/httpx/rest/clientgen`。

```go
func Command(router func() chi.Routes) *cobra.Command
func Generate(api *rest.API, opts Options) (*Result, error)
func GenerateTS(api *rest.API, opts TSOptions) (*Result, error)
```

`Command` 单独构造 `client` 命令；`Generate`、`GenerateTS` 在代码中生成，例如写自己的生成工具时。
