# 资源

后台的大部分页面是增删改查：列表、详情、表单和行上的操作。资源在 Go 中声明，`tenon generate` 生成与组件库无关的资源定义 `resources.ts`，运营后台（antd）和控制台（shadcn）用同一份定义渲染。入门教程的[后台：作者与角色](../tutorial/08-admin.md)是一个完整的例子。字段的类型、枚举和校验规则都取自接口的 OpenAPI schema，后端改了字段、校验或枚举，重新生成后页面随之改变。

阅读前需要了解：[路由](../http/routing.md)；[应用骨架](app-shell.md)；[入门教程第 8 章](../tutorial/08-admin.md)。

- [声明资源](#declaring)
- [显示文字](#labels)
- [检查](#checks)
- [生成的定义](#generated)
- [前端的资源模型](#model)
- [运营后台中的资源页面](#admin)
- [控制台中的资源页面](#console)
- [设置页面](#settings)
- [常见问题](#troubleshooting)
- [API 参考](#api-reference)

<a id="declaring"></a>
## 声明资源

资源引用已经登记的接口，写在登记接口之后：

```go
rest.Register(api, list, h.List)     // GET /articles，响应 page.Offset[ArticleDTO]
rest.Register(api, get, h.Get)       // GET /articles/{id}
rest.Register(api, create, h.Create) // POST /articles
rest.Register(api, update, h.Update) // PUT /articles/{id}
rest.Register(api, remove, h.Delete) // DELETE /articles/{id}
rest.Register(api, publish, h.Publish)

api.Resource(rest.Resource{
    Name:   "article",
    List:   "article-list",
    Get:    "article-get",
    Create: "article-create",
    Update: "article-update",
    Delete: "article-delete",
    Columns: []string{"title", "status", "updated_at"},
    Search:  "q",
    Filters: []string{"status"},
    Sort:    []string{"updated_at"},
    Form:    []string{"title", "content", "status"},
    Actions: []rest.Action{{Name: "publish", Operation: "article-publish", Confirm: true}},
})
```

| 字段 | 说明 |
| --- | --- |
| `List` | 列表接口：GET，没有路径参数，响应体有 `items` 数组（[`page.Offset`](../http/responses.md#offset-pagination) 或 `page.Cursor`）。必须设置 |
| `Get`、`Create`、`Update`、`Delete` | 详情、创建、修改、删除接口，没有的留空 |
| `Columns` | 列表的列：列表项的字段。为空时是除对象和数组外的全部字段，按结构体中的顺序 |
| `Search` | 关键词搜索的查询参数 |
| `Filters` | 筛选条件：列表接口的查询参数 |
| `Sort` | 可以排序的列。列表接口要有 `sort` 查询参数，取值是列名，前面加 `-` 表示降序，与 [`useList`](app-shell-features.md#lists) 相同 |
| `Form` | 创建和修改的表单字段：请求体的字段。为空时是请求体的全部字段，按结构体中的顺序 |
| `Actions` | 行上的其他操作：`Operation` 是接口，`Confirm` 执行前询问，`Danger` 用醒目的颜色 |

详情、修改、删除和操作的接口有且只有一个路径参数，它是标识一行的字段，例如 `/articles/{id}` 对应列表项的 `id`。列的类型、枚举，表单字段的类型、是否必填和校验规则，都来自接口的 schema，不在资源中重复。资源只描述呈现上的选择：哪些列、哪些筛选条件、表单中有哪些字段、行上有哪些操作。

每个接口要求的[权限](../security/authorization.md)随定义一起生成，界面据此隐藏当前用户不能使用的按钮。

<a id="labels"></a>
## 显示文字

资源、字段和操作的显示文字来自[翻译文件](../general/localization.md)，与[枚举](client-react.md#ts-enums)的显示文字一样按语言生成：

```yaml
# zh-CN.yaml
article.title: 文章
article.fields.title: 标题
article.fields.status: 状态
article.fields.updated_at: 更新时间
article.actions.publish: 发布
```

某种语言只写了部分显示文字，或者所有语言都没有时，`generate` 输出警告，列出缺少的消息。界面中缺少的显示文字先用默认语言，再用字段名。枚举字段的取值显示为枚举的显示文字。

<a id="checks"></a>
## 检查

`api.Resource` 在声明时检查，问题与 `rest.Register` 一样在启动和生成代码时就暴露：

- 名称是 kebab-case，同名的资源只声明一次；
- 引用的接口已经登记，方法和路径参数符合上面的要求，路径参数都相同，且是列表项的字段；
- 列、排序的列是列表项的字段，搜索和筛选条件是列表接口的查询参数，表单字段是创建和修改的请求体都有的字段；
- `sort` 参数限定了取值（`enum`）时，每个排序的列的升序或降序至少有一个在其中，否则界面排序时会得到 `422`。

不符合时 panic，说明哪个字段不存在，并列出可用的字段。

<a id="generated"></a>
## 生成的定义

资源写入 OpenAPI 文档的 `x-tenon-resources`，`generate` 据此生成客户端目录中的 `resources.ts`：

```ts
import { resources } from "@blog/client/api/resources"

resources.article.operations.list     // ["article", "list"]：client.article.list
resources.article.columns[1]          // { name: "status", type: "string", enum: "ArticleStatus", required: true }
resources.article.form[0]             // { name: "title", type: "string", required: true }
resources.article.permissions.create  // ["article:write"]
resources.article.labels["zh-CN"].fields.title // "标题"
```

`operations` 是客户端方法的位置，`schemas.ts`、`queries.ts` 中的位置相同，表单的校验规则就是 `schemas.article.create.body`。`enum` 是 `enums.ts` 中的命名类型。

<a id="model"></a>
## 前端的资源模型

`@go-tenon/shell` 的 `resourceModel` 把定义和生成的客户端、校验规则、枚举接在一起，得到与组件库无关的模型，运营后台和控制台都用它渲染：

```ts
import { resourceModel } from "@go-tenon/shell"
import { createQueries } from "@blog/client/api/queries"
import { resources } from "@blog/client/api/resources"
import { components, schemas } from "@blog/client/api/schemas"
import { enums } from "@blog/client/api/enums"
import { messages } from "@blog/client/api/messages"

const queries = createQueries(api, { queryClient, scope: session.scope })
const kit = { client: api, queries, schemas, components, enums, messages, language: "zh-CN" }
export const articles = resourceModel(resources.article, kit, queryClient)
```

模型通常在模块中创建，那时还没有登录。query key 在每次使用时按当前的身份计算，所以 `createQueries` 可以带上 [`scope`](client-react.md#tanstack)：登录、切换租户之后，列表和失效的范围都属于新的身份。

| 成员 | 说明 |
| --- | --- |
| `title`、`columns`、`filters`、`form`、`actions` | 带显示文字。字段有 `kind`（`text`、`number`、`boolean`、`select`、`multiselect`、`tags`、`date`、`datetime`、`json`，由类型、格式和枚举得出）、`required`、`sortable`，枚举字段有 `options` |
| `listSearch` | 列表路由的 `validateSearch`，查询条件、排序和分页保存在地址中，规则来自列表接口的查询参数 |
| `allowed(can, operation)` | 当前身份能否使用一个接口或操作 |
| `listQuery`、`getQuery` | 列表和详情的查询选项，query key 与 `createQueries` 相同 |
| `create`、`update`、`remove`、`run` | 调用生成的客户端，完成后让这个分组的查询失效 |
| `formSchema(mode)`、`ruleOptions` | 表单的校验规则，交给 [`resolver`、`antdRule`](app-shell-features.md#forms)。`kit` 带上 `messages` 时提示是翻译好的文字，没有时是错误码，例如 `validation.filled` |
| `display(field, value)` | 枚举字段显示它的文字 |

在页面中：

```tsx
export const Route = createFileRoute("/_app/articles")({ validateSearch: articles.listSearch, component: Articles })

function Articles() {
  const view = useResourceList(articles, Route) // rows、total、list（地址中的状态）、can（按权限能用的按钮）
  const { save, remove, run } = useResourceMutations(articles)
  // ……
}
```

`useResourceItem(model, key)` 读取一行的详情，`formValues(model, row)` 得到表单的初始值。显示文字的语言与验证提示相同：`language` 本身，没有时它的主语言，再没有时第一种语言。

<a id="admin"></a>
## 运营后台中的资源页面

`@go-tenon/admin` 的 `ResourcePage` 用 ProTable 渲染列表，用 DrawerForm 渲染创建和修改的表单。没有覆盖时，一个资源的页面只有路由：

```tsx
import { ResourcePage } from "@go-tenon/admin"

export const Route = createFileRoute("/_app/articles")({
  staticData: { title: "文章", icon: "file" },
  validateSearch: articles.listSearch,
  component: () => <ResourcePage model={articles} route={Route} />,
})
```

- 列按字段的类型显示：枚举显示文字，布尔显示“是”“否”，时间按本地格式，标签逐个显示。可以排序的列点击列头排序。
- 搜索框和筛选条件在表格上方，枚举的筛选条件是下拉选择。翻页、排序和筛选都写进地址。
- 表单的输入组件按字段的类型选择：文字、数字、开关、下拉、多选、标签、日期、日期时间，时间以 ISO 字符串提交。
- 校验规则与服务端相同，服务端返回的字段错误显示在输入框下面。
- “新建”“修改”、删除和行上的操作按权限显示。删除和带 `Confirm` 的操作先询问，完成后列表随之刷新。

只写需要覆盖的部分：

```tsx
<ResourcePage
  model={articles}
  route={Route}
  render={{ title: (v, row) => <Link to="/articles/$id" params={{ id: String(row.id) }}>{String(v)}</Link> }}
  inputs={{ content: (p) => <RichEditor id={p.id} value={p.value as string} onChange={p.onChange} /> }}
  rowActions={(row) => [{ key: "preview", label: "预览", onClick: () => preview(row) }]}
/>
```

`inputs` 中的组件收到 `id`、`value`、`onChange`，`id` 要放在控件上，表单项的标签才能指向它。`FieldValue`、`defaultInput` 和 `ResourceForm` 也单独导出，可以在自己写的页面中使用。

<a id="console"></a>
## 控制台中的资源页面

shadcn 的外观是 registry 中的 `tenon-resource-page`，console 模板已经带着它（`src/components/tenon/resource-page.tsx`）。用法与运营后台相同，同一个模型：

```tsx
import { ResourcePage } from "@/components/tenon/resource-page"

export const Route = createFileRoute("/_app/articles")({
  staticData: { title: "文章", icon: "folder" },
  validateSearch: articles.listSearch,
  component: () => <ResourcePage model={articles} route={Route} />,
})
```

- 列表是 [`DataTable`](registry.md#blocks)：服务端分页，可以排序的列点击列头排序，状态写进地址；搜索框在输入停顿后更新，枚举的筛选条件是下拉选择。
- 新建和修改在对话框中，表单是 react-hook-form 加上生成的校验规则（[`useApiForm`](registry.md#blocks)）。输入组件按字段的类型选择：文字、数字、复选框、下拉、多选、标签（逗号分隔）、日期、日期时间。
- 删除和带 `Confirm` 的操作用 `useConfirm` 询问，根路由中要有 `ConfirmProvider`。
- 覆盖方式与运营后台相同：`render`、`inputs`（组件收到 `id`、`value`、`onChange`、`invalid`）、`rowActions`、`actions`。

组件复制在项目中，外观归项目所有，可以直接修改；逻辑仍然来自 `@go-tenon/shell` 的资源模型，随框架升级。

<a id="settings"></a>
## 设置页面

[设置](../features/settings.md)是只有一组值的表单，例如站点名称、是否开放注册。它没有列表，也没有一行一行的数据，所以单独声明：引用读取和保存设置的两个接口，其他都从接口得到：

```go
rest.Register(api, getSettings, settings.Get)   // GET /settings/site，响应体是设置
rest.Register(api, saveSettings, settings.Save) // PUT /settings/site，请求体是设置
api.Settings(rest.Settings{Name: "site", Get: "site-settings-get", Update: "site-settings-update"})
```

| 字段 | 说明 |
| --- | --- |
| `Get` | 读取设置的接口：GET，没有路径参数，响应体包含全部表单字段 |
| `Update` | 保存设置的接口：没有路径参数，请求体是设置 |
| `Form` | 表单的字段：保存接口请求体的字段。为空时是全部字段，按结构体中的顺序 |

两个接口都没有路径参数：设置属于当前的用户、租户或者平台，由请求本身决定，见[租户的设置](../features/settings.md#tenant-settings)。声明的检查与资源相同，接口不存在、形式不对、字段不存在时 panic；名称不能与资源相同，因为显示文字在同一个命名空间中。

显示文字是 `<名称>.title` 和 `<名称>.fields.<字段>` 的消息。`tenon generate` 生成 `settings.ts`，`@go-tenon/shell` 的 `settingsModel` 把它和生成的客户端接在一起：

```ts
import { settingsModel } from "@go-tenon/shell"
import { settings } from "@blog/client/api/settings"

export const siteSettings = settingsModel(settings.site, kit, queryClient)
```

两套外观都有 `SettingsForm`：读取设置作为初始值，按权限决定能否修改（没有保存的权限时只读），字段的输入组件与资源表单相同。默认有“保存”按钮，`autoSave` 改为修改后自动保存，并显示保存的状态：

```tsx
<SettingsForm model={siteSettings} autoSave />
```

`inputs` 覆盖某个字段的输入组件，与 `ResourcePage` 相同。较早生成的项目中，`packages/client/package.json` 的 `exports` 还没有 `"./*/settings": "./src/*/settings.ts"`，手动加上这一行。

<a id="troubleshooting"></a>
## 常见问题

**启动时 panic，提示资源的某个字段不存在**

列、筛选条件或表单字段写了接口中没有的字段。报错中列出了可用的字段，对照修改，见[检查](#checks)。

**点击列头排序时得到 422**

列表接口的 `sort` 参数用 `enum` 限定了取值，但没有包含这一列的升序或降序。在 `enum` 中加上，见[检查](#checks)。

**后台中显示的是字段名，而不是中文**

翻译文件中缺少这个资源或字段的显示文字。`generate` 会输出警告并列出缺少的消息，补上后重新生成，见[显示文字](#labels)。

**“新建”“删除”按钮不见了**

当前用户缺少对应接口要求的权限，按钮按权限隐藏。也可能是资源没有设置 `Create`、`Delete`。

<a id="api-reference"></a>
## API 参考

| 名称 | 说明 |
| --- | --- |
| `rest.Resource` | 资源的声明 |
| `rest.Action` | 行上的操作 |
| `(*rest.API).Resource(r)` | 声明资源，写入 `x-tenon-resources` |
| `(*rest.API).Resources()` | 已声明的资源，列、表单字段和 `Key` 已经补全 |
| `resourceModel(def, kit, queryClient)` | `@go-tenon/shell`：资源模型 |
| `useResourceList`、`useResourceItem`、`useResourceMutations`、`formValues` | `@go-tenon/shell`：列表、详情、修改、表单的初始值 |
| `rest.Settings`、`(*rest.API).Settings(s)`、`(*rest.API).SettingsPages()` | 设置页面的声明，写入 `x-tenon-settings` |
| `settingsModel(def, kit, queryClient)`、`useSettings(model)` | `@go-tenon/shell`：设置模型，读取与保存 |
| `ResourcePage`、`ResourceForm`、`FieldValue`、`defaultInput` | `@go-tenon/admin`：antd 的资源页面 |
| `SettingsForm` | `@go-tenon/admin`：antd 的设置表单；registry 的 `tenon-settings-form` 是 shadcn 的 |
| `tenon-resource-page` | registry：shadcn 的资源页面，导出的名称与 antd 的相同 |
