# 在 React 中调用接口

在 React 页面中用 TanStack Query 和生成的客户端读写数据，复用生成的校验规则、权限和枚举，用 `@go-tenon/client` 处理错误、会话和分页。客户端的生成见[客户端代码](../http/clients.md)。

包：`@go-tenon/client`。

<a id="tanstack"></a>
## 用 TanStack Query 读取和修改数据

`kit` 是前端 `src/api.ts` 中 `createKit(api, { scope: session.scope })` 的结果，`kit.queries` 是每个接口的 TanStack Query 选项，不手写 query key：

```tsx
import { useMutation, useQuery } from "@tanstack/react-query"
import { kit } from "../../api"

function Article({ id }: { id: string }) {
  const { data } = useQuery(kit.queries.article.get(id)) // GET：{ queryKey, queryFn }，也交给 prefetchQuery、路由的 loader
  const publish = useMutation(kit.queries.article.publish()) // 其他方法：{ mutationFn, onSuccess }
  // publish.mutate([id, { idempotencyKey }])：多个参数时传数组，一个参数时直接传
}
```

- query key 是 `[...scope, API 名称, 分组, 接口, ...参数]`，分组是 operationId 的第一段。`scope` 是 `[账号, 租户]`；创建 `kit` 时漏传它，退出登录后下一个用户会短暂看到上一个用户的数据。
- 修改成功后同一分组的查询全部失效：`article-publish` 让文章列表和详情重新获取。在 `useMutation` 中覆盖 `onSuccess` 会丢掉这一步，这时自己调用 `queryClient.invalidateQueries({ queryKey: kit.queries.article.key() })`；`kit.queries.key()` 是整个 API 的前缀。
- 失效用执行这次修改的 QueryClient，所以 `kit` 在模块中创建，服务端渲染时每个请求各自失效。
- 事件流和下载接口没有生成的选项，直接调用客户端的方法。
- `@go-tenon/ui` 的 `createApp` 在登录、退出、切换身份时清空缓存；不用它时调用 `clearOnIdentityChange(session, queryClient)`。
- [实时频道](../libraries/realtime.md#subscribing)收到消息后让对应的查询失效，缓存始终来自接口。

<a id="pagination"></a>
## 分页列表

列表由服务端[固定分页](../http/responses.md#offset-pagination)。表格的 `pageIndex` 从 0 开始，接口的 `page` 从 1 开始，用 `toPageParams` 转换。`kit` 来自 `src/api.ts`，`pagination` 是表格的 `{ pageIndex, pageSize }`：

```ts
import { toPageParams } from "@go-tenon/client"
import { keepPreviousData, useQuery } from "@tanstack/react-query"

const { data } = useQuery({
  ...kit.queries.article.list(toPageParams(pagination)),
  placeholderData: keepPreviousData, // 翻页时保留上一页，表格不闪空白
})
// 记录是 data?.items，总数是 data?.total
```

- `parsePageParams` 从地址读取分页，不合法的值取默认值，`per_page` 不超过 100；`parseSort`、`formatSort` 处理 `-created` 这样的排序参数。
- 表单下拉框等场景需要拉取全量列表时，用 `fetchAllPages((p) => api.article.list(p))`：默认按每页 100 条先取第 1 页，再在 `maxPages`（默认 50）上限内并发拉取剩余页并按页序合并返回。
- 分页条件放在路由的 search 参数中时，用生成的 `ArticleListInput` 作为 `validateSearch` 的类型。`@go-tenon/ui` 的列表见[列表](lists-forms.md#lists)。

<a id="validation-rules"></a>
## 校验规则与验证提示

`schemas.ts` 是每个接口的请求体（`body`）和查询参数（`query`）的校验规则，取自 OpenAPI；`messages.ts` 是各语言的提示模板，键是[校验错误](../http/requests.md#validation-messages)的 `code`。`validate` 用它们校验表单的输入 `values`，结果与服务端相同：

```ts
import { validate } from "@go-tenon/client"
import { messages } from "@blog/client/api/messages"
import { components, schemas } from "@blog/client/api/schemas"

const errors = validate(schemas.article.create.body, values, { components, messages, language: "zh-CN" })
// [{ location: "body.title", code: "validation.filled", message: "不能为空", value: "" }]
```

- `@go-tenon/ui` 的表单用 `useSchemaForm` 做同样的事，见[表单](lists-forms.md#forms)。
- 规则随 Go 结构体的标签生成，改了标签要重新生成，否则前后端的提示不同。`oneOf`、`anyOf`、`not` 只在服务端校验。
- 应用覆盖了验证提示时，用 `httpcmd.Translations` 交给生成命令，见[生成客户端](../http/clients.md#client-command)。

<a id="ts-permissions"></a>
## 按权限显示：`permissions.ts`

```ts
import { permissions, required, type Permission } from "@blog/client/api/permissions"

permissions["articles:write"] // "撰写和修改自己的文章"
required.article.create // ["articles:write"]
```

- 权限目录来自 `rest.Options.Permissions`，见[声明权限目录](../security/authorization.md#permission-catalog)；没有设置时只有接口要求的权限。设置了目录时，接口要求的权限不在目录中会生成失败。
- `Permission` 是全部权限名的联合类型，写错的权限名在类型检查时报错。按权限显示菜单和按钮见[权限](app.md#permissions)。

<a id="ts-enums"></a>
## 枚举的取值和显示文字

用 [`enum` 包](../http/requests.md#enums)声明的枚举，`api.ts` 中是取值的联合类型，`enums.ts` 中是全部取值和各语言的显示文字。`kit` 来自 `src/api.ts`：

```tsx
import { enums } from "@blog/client/api/enums"

enums.ArticleStatus.values // ["draft", "published"]，按声明的顺序
<Badge>{kit.label("ArticleStatus", article.status)}</Badge> // 草稿
```

- 显示文字取自翻译文件中 `<前缀>.<取值>` 的消息；缺少时 `generate` 输出警告，界面中先用默认语言，再用取值本身。
- `kit.label` 用 `createKit` 的 `language`，没有设置时用浏览器的语言；没有这种语言时依次用主语言和第一种语言。

<a id="describe-error"></a>
## 用 `describeError` 处理错误

`describeError` 按[客户端协议](../http/protocol.md)给错误分类。`err` 是接口调用抛出的错误，`form` 是 `@mantine/form` 的表单：

```ts
import { describeError, fieldErrors } from "@go-tenon/client"
import { notifyError } from "@go-tenon/ui"

if (describeError(err).kind === "invalid") {
  form.setErrors(fieldErrors(err)) // body.items[0].name → { "items.0.name": "不能为空" }
} else {
  notifyError(err) // 显示服务端的文案；断网、限流等没有文案时按 kind 给出提示
}
```

| `kind` | 情况 |
| --- | --- |
| `unauthenticated` | `401`，会话变为未登录 |
| `forbidden`、`confirm_required`、`two_factor_required`、`method_required` | `403`，按错误码区分；`method_required` 的 `params.methods` 是可用的登录方式 |
| `upgrade_required`、`rate_limited` | `426` App 版本过低；`429`，`retryAfter` 是等待的秒数 |
| `business` | 带 `code` 的错误，`422` 也先看 `code` |
| `invalid` | `400`、`422` 带字段错误、没有 `code` |
| `not_found`、`server`、`network`、`aborted` | `404`、`5xx`、没有得到响应、请求被取消 |

按 `code` 处理业务错误时用生成的 `errors.ts` 中的常量，例如 `ArticleErrors.SlugTaken`。`@go-tenon/ui` 的表单用 `applyServerErrors` 代替 `fieldErrors`。

<a id="client-runtime"></a>
## 会话和不经过生成客户端的请求

`createSession` 创建的会话记录当前身份，生成的 `src/api.ts` 或 `src/auth.ts` 中已经写好，交给 `createClient({ session })` 和 `createApp({ session })`。受保护的接口返回 `401` 时会话变为未登录。

`session.fetch(fetch)` 用于不经过生成客户端的请求，同源发送会话 cookie，`401` 时同样让会话变为未登录；用 `ensureOK` 检查响应，失败时抛出 `HttpError`，`describeError` 照常分类。基于会话的还有：

| 函数 | 用途 | 见 |
| --- | --- | --- |
| `uploadFile(file, steps)` | 直传到对象存储：分片、重试、进度和取消 | [上传接口](../libraries/filesystem.md#rest-direct-upload) |
| `watchOperation(open)` | 读取后台操作的进度直到结束 | [后台操作](../features/operations.md#watching-progress) |
| `chatTransport`、`chatControls` | `useChat` 的 transport，停止、插话、历史 | [AI 助手](../ai/chat.md#conversation-api) |

`@go-tenon/client` 不依赖 DOM 和 Node，React Native、Electron 的主进程中同样可以用。

<a id="other-ts-tools"></a>
## 用其他 OpenAPI 工具生成类型

`openapi.json` 是公开的契约，[openapi-typescript](https://openapi-ts.dev) 等工具照常从它生成：

```bash
bunx openapi-typescript openapi.json -o src/api.d.ts --root-types --root-types-no-schema-prefix --root-types-keep-casing
```
