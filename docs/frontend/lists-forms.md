# 列表与表单

后台页面大多是一张表格和几个表单。这一页介绍自己写这类页面时用到的部分：列表的查询条件保存在地址中，表格接上分页、排序和批量操作，一个对象的属性用描述列表显示；表单用与服务端相同的规则校验，服务端的错误显示在字段下面，离开时提醒未保存的修改。

只是对一个资源增删改查时，不用写这些代码：在 Go 中声明资源，`ResourcePage` 按声明渲染整个页面，见[资源与设置页面](resources.md)。

阅读前需要了解：[搭建前端应用](app.md)；[在 React 中调用接口](client-react.md)。

- [列表](#lists)
    - [表格](#data-table)
    - [描述列表](#description-list)
- [表单](#forms)
    - [未保存的修改](#unsaved-changes)
    - [自动保存](#autosave)
- [确认与提示](#feedback)
- [路由链接](#links)
- [常见问题](#troubleshooting)
- [API 参考](#api-reference)

<a id="lists"></a>
## 列表

列表的查询条件、排序和分页保存在地址中：刷新页面、分享链接、浏览器前进后退都回到同一个列表。地址中的参数与接口相同（`page`、`per_page`、`sort` 和各筛选条件），可以直接交给生成的客户端：

```tsx
import { listSearch, useList } from "@go-tenon/ui"
import type { OrderListInput } from "@shop/client/admin"
import { kit } from "../../resources"

export const Route = createFileRoute("/_app/orders")({
  staticData: { title: "订单", icon: "order" },
  validateSearch: listSearch<OrderListInput>(kit.schemas.order.list.query, { components: kit.components }),
  component: Orders,
})

function Orders() {
  const list = useList(Route)
  const orders = useQuery(kit.queries.order.list(list.params))
  // list.page、list.perPage、list.sort、list.filters
  // list.setFilters({ status: "paid" })、list.setPage(2)、list.setSort("-created")、list.reset()
}
```

- `listSearch` 按生成的查询参数规则解析地址：值转换为规则中的类型（数字、布尔值、数组），不合法的值、不认识的参数、空值和等于默认值的值都去掉。
- 修改查询条件、排序或每页条数时回到第一页；等于默认值的参数不写进地址。
- 游标分页的游标不放进地址，“加载更多”用 TanStack Query 的 [useInfiniteQuery](https://tanstack.com/query/latest/docs/framework/react/guides/infinite-queries)，`getNextPageParam` 取响应的 `next_cursor`。

<a id="data-table"></a>
### 表格

`DataTable` 基于 [mantine-datatable](https://icflorescu.github.io/mantine-datatable/)，应用不直接依赖它。把 `useList` 的结果交给它，翻页和排序就写进地址：

```tsx
import { Button } from "@mantine/core"
import { type Column, DataTable, useList, useSelection } from "@go-tenon/ui"

const columns: Column<Order>[] = [
  { accessor: "number", title: "订单号", fixed: true },
  { accessor: "customer", title: "客户" },
  { accessor: "amount", title: "金额", sortable: true },
  { accessor: "note", title: "备注", hidden: true },
]

function Orders() {
  const list = useList(Route)
  const orders = useQuery(kit.queries.order.list(list.params))
  const selection = useSelection<string>(list.filters)
  return (
    <DataTable
      records={orders.data?.items ?? []}
      total={orders.data?.total}
      loading={orders.isPending}
      list={list}
      columns={columns}
      selection={selection}
      bulkActions={(ids) => <Button color="red" variant="light">删除 {ids.length} 项</Button>}
      storeKey="orders"
    />
  )
}
```

- 分页显示“1–20，共 N 条”，可以选择每页的条数；翻页、排序都写进地址，排序后回到第一页。没有 `total` 时（游标分页）不显示分页。
- `selection` 显示勾选框。`useSelection(resetOn)` 在 `resetOn` 变化时清空选中项；用 `list.filters` 时选中在翻页时保留，换了查询条件才清空。有选中的行时，表格上方显示 `bulkActions`。
- `storeKey` 开启 mantine-datatable 自带的列设置：拖动表头调整顺序，右键点击表头选择显示的列，拖动表头的边缘调整宽度，保存在浏览器中。列有增减或者 `accessor` 改了名，表格就按代码中的顺序重新开始，原来的设置随之清除。`fixed` 的列（例如名称、操作）不能隐藏、拖动和调整宽度；`hidden` 的列默认隐藏。
- `expand` 在每行开头加一个箭头按钮，点它展开明细；`toolbar` 放在表格上方左侧，例如筛选条件。
- 表格的行不能点击：用户要能选中、复制每一列的内容。打开详情、对话框的入口放在单元格里，用 `CellLink`（例如错误摘要、订单号，过长时省略），或者放在操作列的按钮中：

```tsx
{ accessor: "error", title: "错误", ellipsis: true, render: (j) => <CellLink onClick={() => setOpen(j.id)}>{j.error}</CellLink> }
```
- 行高、字号随主题的预设：运营后台更紧凑。

<a id="description-list"></a>
### 描述列表

一个对象的各项属性，例如详情页的基本信息、系统状态、请求的元数据，用 `DescriptionList` 显示成“名称 — 值”的列表，不用表格：

```tsx
import { DescriptionList, StatusDot } from "@go-tenon/ui"

<DescriptionList
  title="复制槽"
  description="PostgreSQL 逻辑复制"
  extra={<StatusDot color="green">转发中</StatusDot>}
  items={[
    { label: "名称", value: slot.name, mono: true },
    { label: "积压", value: "12.4 KiB" },
    { label: "Worker 进程", value: slot.active_pid || undefined }, // 空值显示“—”
  ]}
/>
```

- 一张卡片，名称在左、值靠右，行间一条淡线；值太长时在右侧换行。用 `<dl>` 渲染。
- `title`、`description` 显示在卡片顶部，`extra` 在标题右边，通常是 `StatusDot` 或按钮。几组属性用 `SimpleGrid` 并排，例如队列面板的“系统状态”。
- `mono` 让值用等宽字体（ID、名称、路径），`monoLabels` 让名称也用等宽字体（例如元数据的键）。
- 值是 `undefined`、`null` 或空字符串时显示“—”，`0` 照常显示。
- 放进对话框或已有的卡片中时用 `withBorder={false}`。

`StatusDot` 是一个带浅色光圈的圆点和文字，表示状态：正常用 `green`，需要注意用 `deep-orange`，出错用 `red`。

<a id="forms"></a>
## 表单

表单用 [@mantine/form](https://mantine.dev/form/use-form/)。`useSchemaForm` 用生成的 `schemas.ts` 校验，规则和提示的文字都与服务端相同（见[校验规则与验证提示](client-react.md#validation-rules)），`rules` 传生成的 `kit`：

```tsx
import { Button, TextInput } from "@mantine/core"
import type { ErrorInfo } from "@go-tenon/client"
import { applyServerErrors, FormError, useSchemaForm, useUnsavedChanges } from "@go-tenon/ui"
import { useState } from "react"
import { api } from "../auth"
import { kit } from "../resources"

function NewArticle() {
  const form = useSchemaForm({
    schema: kit.schemas.article.create.body,
    rules: kit,
    initialValues: { title: "", slug: "" },
  })
  const [error, setError] = useState<ErrorInfo>()
  const unsaved = useUnsavedChanges(form.isDirty())
  return (
    <form
      onSubmit={form.onSubmit(async (values) => {
        try {
          await api.article.create(values)
          unsaved.release()
        } catch (err) {
          setError(applyServerErrors(form, err))
        }
      })}
    >
      <FormError error={error} />
      <TextInput label="标题" {...form.getInputProps("title")} />
      <TextInput label="链接名" {...form.getInputProps("slug")} />
      <Button type="submit">保存</Button>
    </form>
  )
}
```

- 初始值的类型可以直接用生成的客户端中请求体的类型，例如 `initialValues: { title: "", slug: "" } as ArticleChanges`；嵌套的对象同样可以。这样的值也可以直接交给资源模型保存（`create`、`update`、`useResourceMutations` 的 `save`），它们接受的类型是 `RequestValues`，约束与表单相同。
- 提交时校验整个表单，之后离开一个字段时校验它，修改时清除它的错误。
- 字段的路径用点连接，例如 `items.0.name`，与服务端错误的位置 `body.items[0].name` 对应。
- `applyServerErrors(form, err)` 把请求体的字段错误设置到对应字段，包括业务错误用 [`errorx.Field`](../general/errors.md#field-errors) 指出的字段；返回不属于某个字段的错误（例如 slug 已被使用、断网），以及指出的字段不在这个表单中的错误，交给 `FormError` 显示在表单上方。它是 `describeError` 的结果：`message` 是服务端翻译好的提示，断网等没有服务端提示时为空，`FormError` 按 `kind` 显示。
- 按字段类型选择输入组件用 `DefaultInput`，按类型显示一个值用 `FieldValue`，见[资源页面](resources.md#admin)。

它们建立在两个与表单库无关的函数上：`formValidator(schema, rules)` 返回校验整个表单的函数，`serverErrors(err)` 把错误拆成 `{ fields, form }`。

<a id="unsaved-changes"></a>
### 未保存的修改

有未保存的修改时，`useUnsavedChanges(dirty)` 在应用内离开页面前询问，刷新或关闭浏览器时由浏览器提示。保存成功后先调用 `release`，跳转时不再询问：

```tsx
const unsaved = useUnsavedChanges(form.isDirty())
const save = useMutation({
  mutationFn: …,
  onSuccess: () => {
    unsaved.release()
    void navigate({ to: "/articles" })
  },
})
```

默认用浏览器的确认框，`useUnsavedChanges(dirty, { confirm })` 换成自己的对话框，例如 `() => confirm({ title: "放弃修改？" })`（返回 `true` 时离开）。页面在[标签](layout.md#tabs)中时，切换标签不询问，关闭标签时才询问。

<a id="autosave"></a>
### 自动保存

设置这类页面可以不要“保存”按钮，修改后自动保存。`SettingsForm` 的 `autoSave` 已经做好，见[设置页面](resources.md#settings)；自己写时用 `createAutoSaver(initial, { save, validate, delay })`：

- 把值的变化交给 `change`，它在用户停下 `delay`（默认 600 毫秒）后保存；校验不通过、与上次保存成功的值相同时不保存。
- 保存一次只进行一个，期间的变化在完成后再保存一次。
- `stop` 立即保存还在等待的变化并停止接收，`resume` 恢复接收，`discard` 放弃等待的变化。在 React 中，effect 激活时调用 `resume`、清理时调用 `stop`：`StrictMode` 会先清理再重新激活，`Activity` 隐藏时清理、显示时重新激活，同一个实例要能停下再继续。
- `status()` 和 `subscribe` 给出状态：`pending`、`saving`、`saved`、`error`。

<a id="feedback"></a>
## 确认与提示

- `confirm({ title, message, confirmLabel, danger })` 弹出确认对话框，用户确认时返回 `true`。
- `notifySuccess(message)` 在右上角提示操作成功；`notifyError(err, title)` 提示失败，文案取服务端的翻译，没有时按错误的种类给出。

它们需要 `UIProvider`（其中安装了对话框和通知），见[根路由](app.md#root)。

<a id="links"></a>
## 路由链接

直接写 `<Button component={Link} to=…>` 会丢掉路由参数的类型检查。`ButtonLink`、`AnchorLink`、`NavLinkLink`、`ActionIconLink` 用 TanStack Router 的 `createLink` 包装了 Mantine 的组件，`to`、`params`、`search` 按路由表检查：

```tsx
import { ButtonLink } from "@go-tenon/ui"

<ButtonLink to="/articles/$id" params={{ id: article.id }} variant="default">
  查看
</ButtonLink>
```

<a id="troubleshooting"></a>
## 常见问题

**表单提交后，服务端返回的字段错误没有显示在输入框下面**

在出错时把错误交给 `applyServerErrors`，它把 `422` 的字段错误设置到对应的字段上。还要检查表单字段的名字是否与接口请求体中的字段名一致，嵌套字段用点连接，例如 `items.0.name`。

**表单中的提示是 `validation.filled` 这样的错误码**

`useSchemaForm` 的 `rules` 中没有 `messages`。传生成的 `kit`（其中有 `messages`），或者 `{ components, messages, language }`。

**列表的筛选条件刷新页面后丢失了**

筛选条件保存在了组件的状态（例如 `useState`）中。改用 `listSearch` 和 `useList`，条件保存在地址中，刷新、前进后退、分享链接时都能保留。

<a id="api-reference"></a>
## API 参考

| 名称 | 说明 |
| --- | --- |
| `listSearch(querySchema, options)` | 列表路由的 `validateSearch`：查询条件、排序和分页保存在地址中 |
| `useList(Route)` | 地址中的列表状态：`params`、`page`、`perPage`、`sort`、`filters`、`setFilters`、`setPage`、`setSort`、`reset`、`tableState` |
| `useSelection(resetOn)` | 列表的选中项 |
| `DataTable`、`Column`、`TableList`、`CellLink` | 表格和单元格中打开详情的链接 |
| `DescriptionList`、`DescriptionItem`、`StatusDot` | 描述列表和状态圆点 |
| `useSchemaForm({ schema, rules, initialValues })` | 用生成的规则校验的 `@mantine/form` 表单 |
| `applyServerErrors(form, err)`、`FormError` | 服务端的字段错误设置到字段；表单上方的错误 |
| `formValidator`、`serverErrors` | 与表单库无关的校验和错误拆分 |
| `useUnsavedChanges(dirty, options)` | 有未保存的修改时离开前询问 |
| `createAutoSaver(initial, options)` | 自动保存 |
| `DefaultInput`、`FieldInput`、`FieldInputProps`、`FieldValue` | 按字段类型输入和显示 |
| `confirm`、`notifySuccess`、`notifyError` | 确认对话框和提示 |
| `ButtonLink`、`AnchorLink`、`NavLinkLink`、`ActionIconLink` | 类型安全的路由链接 |
