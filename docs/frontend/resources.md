# 资源与设置页面

增删改查页面和设置页面在 Go 中声明，`tenon generate` 生成定义，`@go-tenon/ui` 按定义渲染整个页面：`ResourcePage` 是列表、筛选、新建、修改、删除和行上的操作，`SettingsForm`、`SettingsPage` 是设置的表单。自己写列表或表单时见[列表与表单](lists-forms.md)。

包：`httpx/rest`、`@go-tenon/ui`。

- 字段的类型、枚举、是否必填和校验规则取自接口的 OpenAPI schema，声明中只写呈现上的选择；后端改了字段后重新生成，页面随之改变。
- 按钮按接口要求的[权限](../security/authorization.md)显示。界面上的条件只决定显示什么，接口仍要拒绝不允许的调用。

<a id="example"></a>
## 示例

Go 中先用 `rest.Register` 登记文章的列表、新建、修改、删除接口，再声明资源，`api` 是 `rest.New` 创建的 API：

```go
api.Resource(rest.Resource{
    Name:    "article",
    List:    "article-list",
    Create:  "article-create",
    Update:  "article-update",
    Delete:  "article-delete",
    Columns: []string{"title", "status", "updated_at"},
    Filters: []string{"status"},
})
```

前端由生成的定义得到资源模型，`kit` 是 `src/api.ts` 中 `createKit` 的结果：

```ts title="src/resources.ts"
import { resourceModel } from "@go-tenon/ui"
import { kit } from "./api"

export const articles = resourceModel(kit.resources.article, kit)
```

一个路由就是完整的页面，`form="modal"` 在对话框中新建和修改：

```tsx title="src/routes/_app/articles.tsx"
import { createFileRoute } from "@tanstack/react-router"
import { ResourcePage } from "@go-tenon/ui"
import { articles } from "../../resources"

export const Route = createFileRoute("/_app/articles")({
  staticData: { title: "文章", icon: "article", permission: "article:read" },
  validateSearch: articles.listSearch,
  component: () => <ResourcePage model={articles} route={Route} form="modal" />,
})
```

显示文字写在[翻译文件](#labels)中。完整的例子见入门教程的[后台：作者与角色](../tutorial/08-admin.md)。

<a id="declaring"></a>
## 用 `api.Resource` 声明资源

`rest.Resource` 用操作 ID 引用已经登记的接口：

| 字段 | 说明 |
| --- | --- |
| `List` | 必须设置：GET，没有路径参数，响应体有 `items` 数组（[`page.Offset`](../http/responses.md#offset-pagination) 或 `page.Cursor`） |
| `Get`、`Create`、`Update`、`Delete` | 详情、新建、修改、删除接口，没有的留空 |
| `Columns` | 列表项的字段。为空时是除对象和数组外的全部字段 |
| `Filters` | 筛选条件：列表接口的查询参数 |
| `Sort` | 可以排序的列。列表接口要有 `sort` 查询参数，取值是列名，`-` 前缀表示降序，与 [`useList`](lists-forms.md#lists) 相同 |
| `CreateForm`、`UpdateForm` | 新建、修改表单的字段：各自接口请求体的字段，按显示的顺序。为空时是全部字段 |
| `Actions` | 行上的其他操作，例如 `{Name: "publish", Operation: "article-publish", Confirm: true}`；`Danger` 用醒目的颜色 |
| `UpdateWhen`、`DeleteWhen` | 一行上显示“修改”“删除”的条件，见[按行显示操作](#when) |
| `Refs` | 引用另一个资源的字段映射，例如 `map[string]string{"role_id": "role"}`；表单和筛选自动渲染关联资源下拉框，见[关联资源选择器](#resource-select) |

- 详情、修改、删除和操作的接口有且只有一个路径参数，它是标识一行的列表项字段，例如 `/articles/{id}` 对应 `id`。
- 名称是 kebab-case，不能与资源或[设置](#settings)重名：它们的显示文字在同一个命名空间中。

<a id="when"></a>
### 按行显示操作：`When`、`UpdateWhen`、`DeleteWhen`

`rest.Action` 的 `When` 的键是列表项的字段，值是允许的取值，一行的每个字段都取其中之一时才显示这个操作。取值按字符串比较，布尔写 `true`、`false`，枚举字段只能写枚举的值。`UpdateWhen`、`DeleteWhen` 写法相同：

```go
Actions: []rest.Action{
    {Name: "disable", Operation: "tenant-disable", Confirm: true, Danger: true, When: map[string][]string{"status": {"active"}}},
    {Name: "enable", Operation: "tenant-enable", Confirm: true, When: map[string][]string{"status": {"disabled"}}},
},
DeleteWhen: map[string][]string{"status": {"draft"}},
UpdateWhen: map[string][]string{"self": {"false"}}, // self 是列表接口为每一行算出的字段：当前用户自己那一行
```

<a id="labels"></a>
## 显示文字

源语言的显示文字可以直接写在 `rest.Resource` / `rest.Settings` 中，单语言项目无需另建翻译文件：

```go
api.Resource(rest.Resource{
    Name:         "article",
    Title:        "文章",
    List:         "article-list",
    Labels:       map[string]string{"title": "标题", "status": "状态"},
    Descriptions: map[string]string{"slug": "文章地址中的名称，只能用小写字母、数字和连字符"},
    Actions:      []rest.Action{{Name: "publish", Label: "发布", Operation: "article-publish"}},
})
```

多语言应用在附加语言的[翻译文件](../general/localization.md#translation-files)中按以下键补充或覆盖，`tenon generate` 按语言合并生成：

```yaml
# locales/en.yaml
article.title: Articles
article.fields.title: Title
article.fields.status: Status
article.actions.publish: Publish
article.descriptions.slug: Lowercase letters, digits, and hyphens only
```

- 翻译文件中的同名键覆盖定义处的默认文字；缺少显示文字时 `generate` 输出警告，界面中先用默认语言，再用字段名。
- `Descriptions` / `<资源>.descriptions.<字段>` 是表单中字段的说明，可以不写。Go 字段的 `doc` 标签只进接口文档。
- 枚举字段显示[枚举](client-react.md#ts-enums)的显示文字。

<a id="checks"></a>
## 声明时的检查

`api.Resource` 在启动和 `tenon generate` 时检查声明，不符合时 panic，报错中列出可用的字段：

- 引用的接口已经登记，方法和路径参数符合[声明资源](#declaring)中的要求；
- 列、排序的列、`When` 的键是列表项的字段，筛选条件是列表接口的查询参数，表单字段是对应接口请求体的字段；
- `Refs` 的键是筛选或表单中的字符串或字符串数组字段，值是 kebab-case 资源名；`tenon generate` 时进一步校验目标资源已声明；
- `sort` 参数用 `enum` 限定了取值时，每个排序的列的升序或降序至少有一个在其中，否则界面排序时得到 `422`。

<a id="generated"></a>
## 生成的定义：`resources.ts`、`kit.ts`

声明写入 OpenAPI 的 `x-tenon-resources`、`x-tenon-settings`，`tenon generate` 据此生成客户端目录中的 `resources.ts`、`settings.ts`：列、表单字段、显示文字和每个接口要求的权限。`kit.ts` 的 `createKit(api, { scope, language })` 把客户端、查询选项、校验规则、验证提示、枚举、资源和设置合成一个对象：

- `scope` 是会话的 `session.scope`，查询的缓存按登录的身份区分；
- `language` 是显示文字和验证提示的语言，不设置时用浏览器的语言。

<a id="model"></a>
## 资源模型：`resourceModel`

`resourceModel(kit.resources.<资源>, kit)` 得到资源模型。模型在模块中创建，query key 每次使用时按当前身份计算，登录、切换租户后属于新身份。自己写页面时用下面的 hook，`articles` 是资源模型，`Route` 是列表路由：

```tsx
export const Route = createFileRoute("/_app/articles")({ validateSearch: articles.listSearch, component: Articles })

function Articles() {
  const view = useResourceList(articles, Route) // rows、total、list（地址中的状态）、can（按权限能用的按钮）
  const { save, remove, run, invalidate } = useResourceMutations(articles)
  // ……
}
```

- `articles.columns`、`createForm` 等是带显示文字和[输入方式](#field-kinds)的字段；`articles.display("status", row.status)` 是枚举的显示文字。
- `useResourceItem(model, key)` 读取一行，`formValues(model.updateForm, row)` 是表单的初始值。
- `model.listAllQuery(params?, options?)` 经 `fetchAllPages` 翻页拉取全部列表项，与列表共用查询前缀，修改资源后缓存自动失效。
- 自己调用接口改了数据后 `await invalidate()`，列表和详情重新读取。
- 自己写的表单用同样的校验规则：`useSchemaForm({ schema: kit.schemas.article.create.body, rules: kit, initialValues })`。

<a id="admin"></a>
## 资源页面：`ResourcePage`、`ResourceFormPage`

新建和修改默认是单独的页面，放在列表路由的两个子路由 `articles.new.tsx`、`articles.$id.edit.tsx` 中；没有子路由时不显示“新建”“修改”。`articles` 是[资源模型](#model)，列表路由与[示例](#example)相同，去掉 `form="modal"`：

```tsx
// src/routes/_app/articles.new.tsx
export const Route = createFileRoute("/_app/articles/new")({
  component: () => <ResourceFormPage model={articles} />,
})
// src/routes/_app/articles.$id.edit.tsx
export const Route = createFileRoute("/_app/articles/$id/edit")({
  component: () => <ResourceFormPage model={articles} />,
})
```

- 列表用 [`DataTable`](lists-forms.md#data-table)，翻页、排序和筛选写进地址，文字筛选按回车或离开输入框时生效。
- 新建和修改在[多标签](layout.md#tabs)中各是一个标签。修改时有详情接口就读取完整的一行，没有时取列表中的那一行；保存或取消后回到列表，有未保存的修改时先询问。
- 删除和 `Confirm` 的操作先确认。

<a id="overrides"></a>
### 覆盖列、输入组件和按钮

| `ResourcePage` 的选项 | 作用 |
| --- | --- |
| `title`、`description` | 标题和说明；`title={null}` 不显示标题区 |
| `render` | 覆盖某一列的显示 |
| `inputs` | 覆盖某个表单字段的输入组件，`ResourceFormPage` 同样有 |
| `rowActions` | 行上的额外操作，排在声明的操作之后、删除之前 |
| `toolbar` | 标题区的额外按钮，例如[导入、导出](features.md#transfer) |
| `expand` | 行的展开内容 |
| `resizable` | 允许拖动表头边缘调整列宽，默认关闭 |
| `form` | `page`（默认）是单独的页面，`modal` 是对话框，`drawer` 是右侧抽屉 |
| `formSize` | `modal` 或 `drawer` 的宽度，默认 `lg` |
| `renderForm` | `modal` 或 `drawer` 中自己写的表单内容，见[`useResourceForm`](#custom-forms) |

`inputs` 的组件收到 `field`、`label`、`value`、`onChange`（直接传值）、`error`、`disabled`，列表页和表单页共用时放在一个模块中。`RichEditor` 是应用自己的组件：

```tsx
export const articleInputs: Record<string, FieldInput> = {
  content: ({ field, label, value, onChange, error }) => (
    <RichEditor label={label} required={field.required} value={value as string} onChange={onChange} error={error} />
  ),
}

<ResourcePage
  model={articles}
  route={Route}
  inputs={articleInputs}
  rowActions={(row) => [{ label: "预览", onClick: () => preview(row) }]}
/>
```

<a id="field-kinds"></a>
### 字段的输入组件：`FieldKind`

没有覆盖的字段按 `kind` 选择输入组件：

| `kind` | 字段 | 输入组件 |
| --- | --- | --- |
| `text`、`multiline` | 字符串；`format:"multiline"` | 文本框；多行文本框 |
| `secret` | `format:"password"` | `SecretInput`，遮住中间部分，点眼睛图标查看；列表中同样遮住 |
| `number`、`boolean` | 数字；布尔 | 数字输入；开关 |
| `select`、`multiselect` | 枚举；枚举的数组 | 下拉选择；多选 |
| `tags` | 字符串数组 | 标签输入 |
| `date`、`datetime` | `format:"date"`；`format:"date-time"` | 日期（`YYYY-MM-DD`）；日期和时间（ISO 时间） |
| `json` | 对象、其他数组 | JSON 编辑 |

自己写的页面中可以单独用 `DefaultInput`（按 `kind` 选择输入组件）、`FieldValue`（按类型显示一个值）和 `ResourceForm`（对话框中的表单）。

<a id="custom-forms"></a>
### 自己写表单的内容：`useResourceForm`

字段是嵌套的对象或者互相联动时，只自己写表单的内容，读取要修改的行、保存后返回、离开前询问仍由框架提供。`ResourceFormPage`、`ResourceForm` 的 `children` 或 `ResourcePage` 的 `renderForm` 收到 `ctx`，交给 `useResourceForm`。`providers` 是资源模型，`defaultURLs` 是应用自己的数据：

```tsx
<ResourceFormPage model={providers}>{(ctx) => <ProviderForm ctx={ctx} />}</ResourceFormPage>

function ProviderForm({ ctx }: { ctx: ResourceFormContext }) {
  const { form, error, submit, saving } = useResourceForm(ctx, {
    initialValues: { vendor: "", base_urls: { openai: "" } } as ProviderChanges,
  })
  return (
    <form noValidate onSubmit={submit}>
      <FormError error={error} />
      <Select
        label="供应商"
        data={Object.keys(defaultURLs)}
        {...form.getInputProps("vendor")}
        onChange={(v) => {
          form.setFieldValue("vendor", v ?? "")
          if (v) form.setFieldValue("base_urls", defaultURLs[v]) // 联动：按供应商预填地址
        }}
      />
      <TextInput label="OpenAI 兼容地址" {...form.getInputProps("base_urls.openai")} />
      <ResourceFormButtons ctx={ctx} saving={saving} />
    </form>
  )
}
```

- 校验和提交用新建接口，`ctx.editKey` 不为空时用修改接口。服务端的字段错误（包括 [`errorx.Field`](../general/errors.md) 指出的嵌套字段）显示在对应的输入框下面，其他错误由 `FormError` 显示。
- 修改时初始值由 `ctx.row` 得出，例如 `formValues(ctx.model.updateForm, ctx.row)` 再补上嵌套的字段；`toBody` 在提交前把表单的值转换为请求体。

<a id="resource-select"></a>
### 关联资源选择器：`ResourceSelect`、`ResourceMultiSelect`

表单字段关联另一个资源时，用 `ResourceSelect`（单选）和 `ResourceMultiSelect`（多选）。组件通过 `model.listAllQuery()` 翻页拉取全部选项并默认开启本地搜索，具备资源的 `list` 权限时才发请求。`providers`、`models` 是资源模型，`form` 是 `useResourceForm` 或 `useSchemaForm` 返回的表单：

```tsx
<ResourceSelect model={providers} label="供应商" {...form.getInputProps("provider_id")} />

<ResourceMultiSelect
  model={models}
  label="允许的模型"
  params={{ active: true }}
  labelField="display_name"
  filterRow={(row) => Boolean(row.enabled)}
  getOption={(row) => ({ value: String(row.id), label: `${row.name} (${row.slug})` })}
  {...form.getInputProps("model_ids")}
/>
```

- 选项的显示文字默认依次取 `labelField`、资源第一列（枚举自动转换为显示文字）、`name`、`title`、`label` 和行标识。
- 传入 `renderRow={(row, { checked }) => ...}` 可基于原始行数据自定义下拉选项渲染；其余属性与 Mantine 的 `Select`、`MultiSelect` 相同。
- 在 Go 的 `rest.Resource` 或 `rest.Settings` 中通过 `Refs` 声明引用关系（例如 `Refs: map[string]string{"role_id": "role"}`）后，`ResourcePage`、`ResourceFormPage` 和 `SettingsForm` 会自动为表单与筛选条件渲染 `ResourceSelect`（字符串）或 `ResourceMultiSelect`（字符串数组）；需要自定义选项文案或过滤时，仍可在 `inputs` 中显式返回组件覆盖。

<a id="settings"></a>
## 设置页面：`api.Settings`、`SettingsForm`

[设置](../features/settings.md)是只有一组值的表单。`api.Settings` 引用读取和保存的两个接口，两者都没有路径参数：设置属于哪个用户或租户由请求决定。`Form` 是表单字段，为空时是全部字段；检查和显示文字（`<名称>.title`、`<名称>.fields.<字段>`）与资源相同：

```go
rest.Register(api, getSettings, settings.Get)   // GET /settings/site，响应体是设置
rest.Register(api, saveSettings, settings.Save) // PUT /settings/site，请求体是设置
api.Settings(rest.Settings{Name: "site", Get: "site-settings-get", Update: "site-settings-update"})
```

前端用 `settingsModel` 得到设置模型，`kit` 是 `createKit` 的结果：

```tsx
export const siteSettings = settingsModel(kit.settings.site, kit)

<Page title="站点设置">
  <SettingsForm model={siteSettings} title={null} autoSave />
</Page>
```

- `SettingsForm` 读取设置作为初始值，没有保存接口的权限时只读；`inputs` 与资源表单相同。
- 默认有“保存”按钮。`autoSave` 去掉按钮，修改后停下一会儿就保存；校验不通过时不保存，离开页面时保存等待中的修改。

<a id="settings-groups"></a>
### 多组设置：`SettingsPage`

站点、邮件这类设置各是一组：Go 中各自用 `settings.New` 创建、用 `api.Settings` 声明，修改一组不覆盖另一组。`SettingsPage` 把它们放在一个页面，每组一个[页内标签](layout.md#page-tabs)，`siteSettings`、`mailSettings` 是设置模型：

```tsx
<SettingsPage
  title="系统设置"
  groups={[{ model: siteSettings, description: "显示在页面顶栏和邮件中。" }, mailSettings]}
  autoSave
/>
```

- 标签的文字是这组的 `<名称>.title`，地址中的 `?tab=` 是它的名称。
- 没有读取权限的组不显示，所以每组可以要求不同的权限；只剩一组时直接显示表单。
- `groups` 中写设置模型，或者 `{ model, description, inputs }`；其他选项与 `Page` 相同。
