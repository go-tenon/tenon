# 资源

后台的大部分页面是增删改查：列表、详情、表单和行上的操作。资源在 Go 中声明，`tenon generate` 生成与组件库无关的资源定义 `resources.ts`，运营后台（antd）和控制台（shadcn）用同一份定义渲染。字段的类型、枚举和校验规则都取自接口的 OpenAPI schema，后端改了字段、校验或枚举，重新生成后页面随之改变。

- [声明资源](#declaring)
- [显示文字](#labels)
- [检查](#checks)
- [生成的定义](#generated)
- [前端的资源模型](#model)
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
| `List` | 列表接口：GET，没有路径参数，响应体有 `items` 数组（[`page.Offset`](responses.md#offset-pagination) 或 `page.Cursor`）。必须设置 |
| `Get`、`Create`、`Update`、`Delete` | 详情、创建、修改、删除接口，没有的留空 |
| `Columns` | 列表的列：列表项的字段。为空时是除对象和数组外的全部字段，按结构体中的顺序 |
| `Search` | 关键词搜索的查询参数 |
| `Filters` | 筛选条件：列表接口的查询参数 |
| `Sort` | 可以排序的列。列表接口要有 `sort` 查询参数，取值是列名，前面加 `-` 表示降序，与 [`useList`](app-shell.md#lists) 相同 |
| `Form` | 创建和修改的表单字段：请求体的字段。为空时是请求体的全部字段，按结构体中的顺序 |
| `Actions` | 行上的其他操作：`Operation` 是接口，`Confirm` 执行前询问，`Danger` 用醒目的颜色 |

详情、修改、删除和操作的接口有且只有一个路径参数，它是标识一行的字段，例如 `/articles/{id}` 对应列表项的 `id`。列的类型、枚举，表单字段的类型、是否必填和校验规则，都来自接口的 schema，不在资源中重复。资源只描述呈现上的选择：哪些列、哪些筛选条件、表单中有哪些字段、行上有哪些操作。

每个接口要求的[权限](../security/authorization.md)随定义一起生成，界面据此隐藏当前用户不能使用的按钮。

<a id="labels"></a>
## 显示文字

资源、字段和操作的显示文字来自[翻译文件](../general/localization.md)，与[枚举](clients.md#ts-enums)的显示文字一样按语言生成：

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

const kit = { client: api, queries: createQueries(api, { queryClient }), schemas, components, enums, language: "zh-CN" }
export const articles = resourceModel(resources.article, kit, queryClient)
```

| 成员 | 说明 |
| --- | --- |
| `title`、`columns`、`filters`、`form`、`actions` | 带显示文字。字段有 `kind`（`text`、`number`、`boolean`、`select`、`multiselect`、`tags`、`date`、`datetime`、`json`，由类型、格式和枚举得出）、`required`、`sortable`，枚举字段有 `options` |
| `listSearch` | 列表路由的 `validateSearch`，查询条件、排序和分页保存在地址中，规则来自列表接口的查询参数 |
| `allowed(can, operation)` | 当前身份能否使用一个接口或操作 |
| `listQuery`、`getQuery` | 列表和详情的查询选项，query key 与 `createQueries` 相同 |
| `create`、`update`、`remove`、`run` | 调用生成的客户端，完成后让这个分组的查询失效 |
| `formSchema(mode)`、`ruleOptions` | 表单的校验规则，交给 [`resolver`、`antdRule`](app-shell.md#forms) |
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
