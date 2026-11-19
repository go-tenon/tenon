# 资源与设置页面

后台的大部分页面是增删改查：列表、详情、表单和行上的操作。资源在 Go 中声明，`tenon generate` 生成资源定义 `resources.ts`，`@go-tenon/ui` 的 `ResourcePage` 按它渲染整个页面，不用写页面代码。只有一组值的设置页面同样声明，由 `SettingsForm` 渲染。入门教程的[后台：作者与角色](../tutorial/08-admin.md)是一个完整的例子。字段的类型、枚举和校验规则都取自接口的 OpenAPI schema，后端改了字段、校验或枚举，重新生成后页面随之改变。

需要自己写列表或表单时，见[列表与表单](lists-forms.md)。

阅读前需要了解：[路由](../http/routing.md)；[搭建前端应用](app.md)；[入门教程第 8 章](../tutorial/08-admin.md)。

- [声明资源](#declaring)
- [显示文字](#labels)
- [检查](#checks)
- [生成的定义](#generated)
- [前端的资源模型](#model)
- [资源页面](#admin)
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
    Name:       "article",
    List:       "article-list",
    Get:        "article-get",
    Create:     "article-create",
    Update:     "article-update",
    Delete:     "article-delete",
    Columns:    []string{"title", "status", "updated_at"},
    Filters:    []string{"status"},
    Sort:       []string{"updated_at"},
    CreateForm: []string{"title", "content", "status"},
    Actions:    []rest.Action{{Name: "publish", Operation: "article-publish", Confirm: true}},
})
```

| 字段 | 说明 |
| --- | --- |
| `List` | 列表接口：GET，没有路径参数，响应体有 `items` 数组（[`page.Offset`](../http/responses.md#offset-pagination) 或 `page.Cursor`）。必须设置 |
| `Get`、`Create`、`Update`、`Delete` | 详情、创建、修改、删除接口，没有的留空 |
| `Columns` | 列表的列：列表项的字段。为空时是除对象和数组外的全部字段，按结构体中的顺序 |
| `Filters` | 列表上方的筛选条件：列表接口的查询参数，按业务命名，例如 `name`、`status`、`min_age`。文字是输入框（提示是字段的显示文字），枚举是下拉选择，布尔是“是 / 否” |
| `Sort` | 可以排序的列。列表接口要有 `sort` 查询参数，取值是列名，前面加 `-` 表示降序，与 [`useList`](lists-forms.md#lists) 相同 |
| `CreateForm` | 新建表单的字段：新建接口请求体的字段，按显示的顺序。为空时是请求体的全部字段，按结构体中的顺序 |
| `UpdateForm` | 修改表单的字段：修改接口请求体的字段。为空时同样是请求体的全部字段。新建和修改的字段常常不同，例如新建租户时还要填写管理员的账号，修改时只改名称 |
| `Actions` | 行上的其他操作：`Operation` 是接口，`Confirm` 执行前询问，`Danger` 用醒目的颜色，`When` 是显示的条件，见下文 |

有的操作只对一部分行有意义，例如“停用”只对启用的行、“启用”只对停用的行。`When` 写出显示的条件：键是列表项的字段，值是允许的取值，一行的每个字段都取其中之一时，“更多操作”中才有这个操作。取值按字符串比较，布尔是 `true`、`false`；字段是枚举时，取值必须是枚举的值，声明时检查：

```go
Actions: []rest.Action{
    {Name: "disable", Operation: "tenant-disable", Confirm: true, Danger: true, When: map[string][]string{"status": {"active"}}},
    {Name: "enable", Operation: "tenant-enable", Confirm: true, When: map[string][]string{"status": {"disabled"}}},
},
```

条件只决定界面上显示什么，接口仍要自己拒绝不合规则的调用（或者像这个例子一样，重复操作是安全的）。

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
resources.article.createForm[0]       // { name: "title", type: "string", required: true }
resources.article.permissions.create  // ["article:write"]
resources.article.labels["zh-CN"].fields.title // "标题"
```

`operations` 是客户端方法的位置，`schemas.ts`、`queries.ts` 中的位置相同，表单的校验规则就是 `schemas.article.create.body`。`enum` 是 `enums.ts` 中的命名类型。

<a id="model"></a>
## 前端的资源模型

`tenon generate` 为每个 API 生成 `kit.ts`，其中的 `createKit` 把客户端和生成的查询选项、校验规则、验证提示、枚举、资源和设置合成一个对象。`@go-tenon/ui` 的 `resourceModel` 由资源的定义和它得到资源模型，`ResourcePage` 按模型渲染：

```ts
import { resourceModel } from "@go-tenon/ui"
import { createKit } from "@blog/client/api/kit"

export const kit = createKit(api, { scope: session.scope, language: "zh-CN" })
export const articles = resourceModel(kit.resources.article, kit)
```

模型通常在模块中创建，那时还没有登录。query key 在每次使用时按当前的身份计算，所以 `createKit` 可以带上 [`scope`](client-react.md#tanstack)：登录、切换租户之后，列表和失效的范围都属于新的身份。`language` 是显示文字和验证提示的语言，不设置时用浏览器的语言。

自己写的表单用同样的规则时，把 `kit` 作为 `useSchemaForm` 的 `rules`：

```ts
const form = useSchemaForm({ schema: kit.schemas.article.create.body, rules: kit, initialValues })
```

| 成员 | 说明 |
| --- | --- |
| `title`、`columns`、`filters`、`createForm`、`updateForm`、`actions` | 带显示文字。字段有 `kind`（`text`、`secret`、`number`、`boolean`、`select`、`multiselect`、`tags`、`date`、`datetime`、`json`，由类型、格式和枚举得出）、`required`、`sortable`，枚举字段有 `options` |
| `listSearch` | 列表路由的 `validateSearch`，查询条件、排序和分页保存在地址中，规则来自列表接口的查询参数 |
| `allowed(can, operation)` | 当前身份能否使用一个接口或操作 |
| `listQuery`、`getQuery` | 列表和详情的查询选项，query key 与 `createQueries` 相同 |
| `create`、`update`、`remove`、`run` | 调用生成的客户端，完成后让这个分组的查询失效 |
| `formSchema(mode)`、`ruleOptions` | 表单的校验规则，交给 `@go-tenon/ui` 的 [`useSchemaForm`](lists-forms.md#forms) |
| `display(field, value)` | 枚举字段显示它的文字，其他原样返回。`field` 可以是字段名，例如在自定义的列中显示枚举：`articles.display("status", row.status)` |

在页面中：

```tsx
export const Route = createFileRoute("/_app/articles")({ validateSearch: articles.listSearch, component: Articles })

function Articles() {
  const view = useResourceList(articles, Route) // rows、total、list（地址中的状态）、can（按权限能用的按钮）
  const { save, remove, run, invalidate } = useResourceMutations(articles)
  // ……
}
```

自己调用接口修改了数据时（不经过 `save`、`remove`、`run`，例如在自己写的对话框中更换所有者），完成后调用 `invalidate()`，列表和详情重新读取：

```tsx
await api.tenant.changeOwner(tenant.id, values)
await invalidate()
```

`useResourceItem(model, key)` 读取一行的详情，`formValues(model, row)` 得到表单的初始值。显示文字的语言与验证提示相同：`language` 本身，没有时它的主语言，再没有时第一种语言。

<a id="admin"></a>
## 资源页面

`ResourcePage` 按资源模型渲染完整的增删改查页面：列表、搜索和筛选、新建和修改的表单、删除和其他操作，按权限显示按钮。没有覆盖时，一个资源的页面只有路由：列表一个，新建和修改的页面各一个，放在列表路由之下：

```tsx title="src/routes/_app/articles.tsx"
import { createFileRoute } from "@tanstack/react-router"
import { ResourcePage } from "@go-tenon/ui"
import { articles } from "../../resources"

export const Route = createFileRoute("/_app/articles")({
  staticData: { title: "文章", icon: "article", permission: "article:read" },
  validateSearch: articles.listSearch,
  component: () => <ResourcePage model={articles} route={Route} />,
})
```

```tsx title="src/routes/_app/articles.new.tsx"
import { createFileRoute } from "@tanstack/react-router"
import { ResourceFormPage } from "@go-tenon/ui"
import { articles } from "../../resources"

export const Route = createFileRoute("/_app/articles/new")({
  component: () => <ResourceFormPage model={articles} />,
})
```

```tsx title="src/routes/_app/articles.$id.edit.tsx"
import { createFileRoute } from "@tanstack/react-router"
import { ResourceFormPage } from "@go-tenon/ui"
import { articles } from "../../resources"

export const Route = createFileRoute("/_app/articles/$id/edit")({
  component: () => <ResourceFormPage model={articles} />,
})
```

- **列表**：用 [`DataTable`](lists-forms.md#data-table) 渲染。列按字段的类型显示：枚举显示文字，布尔显示“是”“否”，时间按主题的格式，标签逐个显示。文字只显示一行，超出列宽的部分用省略号代替，鼠标停在上面时弹出全文，可以选中复制，见 [`TruncatedText`](lists-forms.md#long-text)。可以排序的列点击列头排序；拖动表头调整列的顺序，拖动表头的边缘调整宽度，在表格右上方的“列设置”按钮中选择显示的列、恢复默认，按资源的名称保存在浏览器中，见 [`DataTable`](lists-forms.md#data-table)。
- **搜索和筛选**：在表格上方。关键词搜索按回车或离开输入框时生效；枚举字段是下拉选择，布尔字段是“是/否”。翻页、排序和筛选都写进地址，有条件时显示“清除筛选”。
- **新建和修改**：默认是单独的页面，`/articles/new` 和 `/articles/{id}/edit`，由上面的两个子路由渲染；开启了[多标签](layout.md#tabs)时各是一个标签，可以和列表同时开着，填了一半的内容会保留。页面上的面包屑是“文章 / 新建”。表单字段来自请求体，校验与服务端相同，服务端的字段错误显示在输入框下面；修改时有详情接口就先读取完整的一行，没有时取列表中的那一行。保存或取消后回到列表，并关闭这个标签；有未保存的修改时，离开之前询问。没有这两个子路由时，列表中不显示“新建”和“修改”按钮。
- **对话框**：字段少的资源可以设置 `form="modal"`，在列表上方的对话框中新建和修改，不需要子路由。
- **删除和操作**：在每一行的“更多操作”菜单中，按权限显示。删除和声明了 `Confirm` 的操作先确认；完成或失败后右上角提示，列表随之刷新。

需要覆盖的部分通过选项传入：

| 选项 | 作用 |
| --- | --- |
| `title`、`description` | 页面的标题和说明；`title={null}` 不显示标题区 |
| `render` | 覆盖某一列的显示 |
| `inputs` | 覆盖某个表单字段的输入组件 |
| `rowActions` | 行上的额外操作，排在声明的操作之后、删除之前 |
| `toolbar` | 标题区的额外按钮，例如导入、导出 |
| `expand` | 展开的内容：每行开头一个箭头按钮，点它展开 |
| `form` | 新建和修改的表单：`page`（默认）是单独的页面，`modal` 是对话框 |

```tsx
<ResourcePage
  model={articles}
  route={Route}
  render={{ title: (v, row) => <AnchorLink to="/articles/$id" params={{ id: String(row.id) }}>{String(v)}</AnchorLink> }}
  inputs={{ content: ({ field, label, value, onChange, error }) => (
    <RichEditor label={label} required={field.required} value={value as string} onChange={onChange} error={error} />
  ) }}
  rowActions={(row) => [{ label: "预览", onClick: () => preview(row) }]}
/>
```

新建和修改的页面也要用到覆盖的输入组件：`ResourceFormPage` 同样有 `inputs`。把它们放在一个模块中，三个路由共用：

```tsx title="src/inputs.tsx"
import type { FieldInput } from "@go-tenon/ui"

export const articleInputs: Record<string, FieldInput> = {
  content: ({ field, label, value, onChange, error }) => (
    <RichEditor label={label} required={field.required} value={value as string} onChange={onChange} error={error} />
  ),
}
```

```tsx title="src/routes/_app/articles.$id.edit.tsx"
export const Route = createFileRoute("/_app/articles/$id/edit")({
  component: () => <ResourceFormPage model={articles} inputs={articleInputs} />,
})
```

`inputs` 中的组件收到 `FieldInputProps`：`field`、`label`、`value`、`onChange`（直接是值）、`error`、`disabled`。没有覆盖的字段按 `kind`（`FieldKind`）选择输入组件：

| `kind` | 输入组件 |
| --- | --- |
| `text` | 文本框 |
| `number` | 数字输入 |
| `boolean` | 开关 |
| `select` | 下拉选择，非必填时可以清除 |
| `multiselect` | 多选 |
| `tags` | 标签输入，回车添加 |
| `date` | 日期选择，值是 `YYYY-MM-DD` |
| `datetime` | 日期和时间选择，值是 ISO 时间 |
| `json` | JSON 编辑，是合法的 JSON 时才交给表单 |
| `secret` | 密钥：`SecretInput`，平时遮住中间部分、不能编辑，点眼睛图标查看和修改。格式为 `password` 的字符串字段（Go 中写 `format:"password"`）是这一类；列表中同样遮住中间部分 |

`FieldValue`（按类型显示一个值）、`TruncatedText`（列表中的长文本）、`DefaultInput`（按类型选择输入组件）、`ResourceForm`（对话框中的新建和修改表单）和 `SecretInput`（密钥的输入框，可以直接展开 `form.getInputProps`）也单独导出，可以在自己写的页面中使用。

<a id="custom-forms"></a>
### 自己写表单的内容

有的表单声明表达不了：字段是嵌套的对象（例如按协议分开的 `base_urls`），或者字段之间有联动（选了供应商就预填协议和地址）。这时只自己写表单的内容，页面的其余部分仍由框架提供：标签标题、读取要修改的行、保存或取消后回到列表并关闭标签、有未保存的修改时离开前询问。`ResourceFormPage` 和 `ResourceForm` 都接受一个渲染函数作为 `children`，它收到 `ResourceFormContext`，交给 `useResourceForm`：

```tsx
import { Select, Stack, TextInput } from "@mantine/core"
import { FormError, ResourceFormButtons, type ResourceFormContext, ResourceFormPage, useResourceForm } from "@go-tenon/ui"

export const Route = createFileRoute("/_app/providers/new")({
  component: () => <ResourceFormPage model={providers}>{(ctx) => <ProviderForm ctx={ctx} />}</ResourceFormPage>,
})

function ProviderForm({ ctx }: { ctx: ResourceFormContext }) {
  const { form, error, submit, saving } = useResourceForm(ctx, {
    initialValues: { vendor: "", base_urls: { openai: "", anthropic: "" } } as ProviderChanges,
  })
  return (
    <form noValidate onSubmit={submit}>
      <Stack>
        <FormError error={error} />
        <Select
          label="供应商"
          data={vendors}
          {...form.getInputProps("vendor")}
          onChange={(v) => {
            form.setFieldValue("vendor", v ?? "")
            if (v) form.setFieldValue("base_urls", defaultURLs[v]) // 联动：按供应商预填地址
          }}
        />
        <TextInput label="OpenAI 兼容地址" {...form.getInputProps("base_urls.openai")} />
        <TextInput label="Anthropic 兼容地址" {...form.getInputProps("base_urls.anthropic")} />
        <ResourceFormButtons ctx={ctx} saving={saving} />
      </Stack>
    </form>
  )
}
```

`useResourceForm` 用资源的新建或修改接口（`ctx.editKey` 为空时是新建）的校验规则，提交时调用这个接口；服务端的字段错误，包括业务错误用 [`errorx.Field`](../general/errors.md) 指出的嵌套字段，显示在对应的输入框下面，其他错误由 `FormError` 显示；保存成功后提示“已保存”或“已创建”，再调用 `ctx.onSaved()` 回到列表或关闭对话框。修改时用 `ctx.row` 得出初始值，例如 `formValues(ctx.model.updateForm, ctx.row)` 再补上嵌套的字段；`toBody` 在提交前把表单的值转换为请求体。`ResourceFormButtons` 是右侧的“取消”和“保存”（新建时是“创建”）。

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

显示文字是 `<名称>.title` 和 `<名称>.fields.<字段>` 的消息。`tenon generate` 生成 `settings.ts`，`@go-tenon/ui` 的 `settingsModel` 把它和生成的客户端接在一起：

```ts
import { settingsModel } from "@go-tenon/ui"

export const siteSettings = settingsModel(kit.settings.site, kit)
```

`SettingsForm` 读取设置作为初始值，按权限决定能否修改（没有保存的权限时只读），字段的输入组件与资源表单相同：

```tsx
import { Page, SettingsForm } from "@go-tenon/ui"
import { siteSettings } from "../../resources"

function Settings() {
  return (
    <Page title="站点设置">
      <SettingsForm model={siteSettings} title={null} autoSave />
    </Page>
  )
}
```

- 默认有“保存”按钮，有修改时才可用；校验规则与服务端相同，服务端的字段错误显示在输入框下面。
- `autoSave` 去掉保存按钮：修改后停下一会儿就保存，表单下方显示“有未保存的修改”“保存中…”“已保存”；校验不通过时不保存，离开页面时保存还在等待的修改。
- `inputs` 覆盖某个字段的输入组件，与 `ResourcePage` 相同。

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
| `createKit(client, { scope, queryClient, language })` | 生成的 `kit.ts`：客户端和元数据 |
| `resourceModel(def, kit, queryClient)` | `@go-tenon/ui`：资源模型 |
| `useResourceList`、`useResourceItem`、`useResourceMutations`、`formValues` | `@go-tenon/ui`：列表、详情、修改、表单的初始值 |
| `rest.Settings`、`(*rest.API).Settings(s)`、`(*rest.API).SettingsPages()` | 设置页面的声明，写入 `x-tenon-settings` |
| `settingsModel(def, kit, queryClient)`、`useSettings(model)` | `@go-tenon/ui`：设置模型，读取与保存 |
| `ResourcePage`、`ResourceFormPage`、`ResourceForm`、`RowAction`、`FieldValue`、`DefaultInput`、`FieldInputProps` | `@go-tenon/ui`：资源页面；新建和修改的页面；对话框中的表单 |
| `useResourceForm`、`ResourceFormContext`、`ResourceFormButtons` | `@go-tenon/ui`：自己写表单的内容，页面的其余部分由 `ResourceFormPage`、`ResourceForm` 提供，见[自己写表单的内容](#custom-forms) |
| `FieldKind`、`ResourceField` | 模型中字段的输入方式和显示文字 |
| `SettingsForm` | `@go-tenon/ui`：设置表单 |
