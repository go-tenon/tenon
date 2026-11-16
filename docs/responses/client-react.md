# 在 React 中调用接口

[客户端代码](clients.md)一页介绍了怎样生成和调用 TypeScript 客户端。这一页介绍在 React 页面中怎样用好它：用 TanStack Query 读取和修改数据，在表单中复用服务端的校验规则，按权限显示按钮，显示枚举的文字，以及处理错误和分页。

**TanStack Query** 是一个管理“从服务端读来的数据”的库：它负责调用接口、记录加载和出错的状态、缓存结果，并在数据修改后重新获取。入门教程的[第 5 章](../tutorial/05-pages.md#list)已经用过它。

阅读前需要了解：[客户端代码](clients.md)；React 的组件和 Hook。

- [TanStack Query](#tanstack)
- [校验规则与验证提示](#validation-rules)
- [权限](#ts-permissions)
- [枚举](#ts-enums)
- [错误、校验与分页](#client-runtime)
- [其他工具](#other-ts-tools)
- [常见问题](#troubleshooting)

<a id="tanstack"></a>
## TanStack Query

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
## 校验规则与验证提示

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
## 权限

`permissions.ts` 中是权限目录和每个接口要求的权限：

```ts
import { permissions, required, type Permission } from "@blog/client/api/permissions"

permissions["articles:write"] // "撰写和修改自己的文章"
required.article.create // ["articles:write"]
```

权限目录来自 `rest.Options.Permissions`，见[声明权限目录](../security/authorization.md#permission-catalog)；没有设置时，只包含接口要求的权限。设置了权限目录时，接口要求的权限必须在目录中，否则生成失败。`Permission` 是全部权限名的联合类型，前端按权限显示菜单和按钮时，写错的权限名在类型检查时报错。

<a id="ts-enums"></a>
## 枚举

用 [`enum` 包](../http/validation.md#enums)声明的枚举，`api.ts` 中是取值的联合类型，`enums.ts` 中是全部取值和各语言的显示文字：

```ts
import type { ArticleStatus } from "@blog/client/api"
import { enums } from "@blog/client/api/enums"

enums.ArticleStatus.values // ["draft", "published"]，按声明的顺序
enums.ArticleStatus.labels["zh-CN"].draft // "草稿"
```

显示文字取自翻译文件中 `<前缀>.<取值>` 的消息，应用的翻译文件由 `httpcmd.Translations` 传入，见[校验规则与验证提示](#validation-rules)。某种语言只写了部分取值的显示文字，或者所有语言都没有时，`generate` 输出警告；界面中缺少的显示文字先用默认语言，再用取值本身。

<a id="client-runtime"></a>
## 错误、校验与分页

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
| `uploadFile(file, steps)` | 直传到对象存储：单次或分片、重试、进度和取消 | [上传接口](../libraries/direct-uploads.md#rest-direct-upload) |
| `watchOperation(open)` | 读取后台操作的进度直到结束，断线重连 | [后台操作](../features/operations.md#progress) |
| `chatTransport`、`chatControls` | `useChat` 的 transport，以及停止、插话、历史 | [AI 助手](../ai/chat.md#conversation-api) |

不经过生成客户端的请求失败时抛出 `HttpError`，形状与生成客户端的 `ApiError` 相同，`describeError` 照常分类。

**分页。** `toPageParams`、`toTableState` 在表格状态（`pageIndex` 从 0 开始）和查询参数（`page` 从 1 开始）之间转换；`parsePageParams` 从地址读取分页，不合法的值取默认值，`per_page` 不超过 100；`parseSort`、`formatSort` 处理 `-created` 这样的排序参数。

<a id="other-ts-tools"></a>
## 其他工具

`openapi.json` 仍然是公开的契约。已经在用 [openapi-typescript](https://openapi-ts.dev) 加 openapi-fetch、或者其他基于 OpenAPI 的工具时，照常从它生成：

```bash
bunx openapi-typescript openapi.json -o src/api.d.ts --root-types --root-types-no-schema-prefix --root-types-keep-casing
```

<a id="troubleshooting"></a>
## 常见问题

**修改成功了，但页面上的列表没有更新**

在 `useMutation` 中覆盖了 `onSuccess`，丢掉了生成的失效逻辑；或者列表的查询不是用 `queries` 生成的，query key 不在同一个分组下。用生成的选项，或者在自己的 `onSuccess` 中调用 `queryClient.invalidateQueries({ queryKey: queries.article.key() })`。

**退出登录后，下一个用户短暂地看到了上一个用户的数据**

没有调用 `clearOnIdentityChange`，或者创建 `queries` 时没有传 `scope`，见 [TanStack Query](#tanstack)。

**翻页时表格闪一下空白**

查询的参数变了，TanStack Query 在加载新的一页时没有数据可显示。加上 `placeholderData: keepPreviousData`。

**表单中的校验提示与服务端返回的不一样**

前端的校验规则来自生成的 `schemas`，修改 Go 中的标签后要重新生成客户端。
