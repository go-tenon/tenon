# 列表与表单

自己写列表和表单页面：列表的查询条件、排序和分页保存在地址中，`DataTable` 接上分页、排序和批量操作；表单用生成的规则校验，规则和提示与服务端相同，服务端的字段错误显示在输入框下面。只对一个资源增删改查时用 `ResourcePage`，不写这些代码，见[资源与设置页面](resources.md)。

包：`@go-tenon/ui`。

<a id="lists"></a>
## 用 `listSearch` 和 `useList` 把列表状态放在地址中

列表的查询条件、排序和分页保存在地址中，刷新、分享链接、前进后退都回到同一个列表。地址中的参数与接口相同（`page`、`per_page`、`sort` 和各筛选条件），直接交给生成的查询：

```tsx title="src/routes/_app/orders.tsx"
import { useQuery } from "@tanstack/react-query"
import { createFileRoute } from "@tanstack/react-router"
import { listSearch, useList } from "@go-tenon/ui"
import type { OrderListInput } from "@shop/client/api"
import { kit } from "../../api"

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

- `listSearch` 按生成的查询参数规则解析地址：值转换为规则中的类型，不合法的值、不认识的参数、空值和等于默认值的值都去掉。
- 修改查询条件、排序或每页条数时回到第一页。筛选条件不放在 `useState` 中：刷新页面后会丢失。
- 游标分页的游标不放进地址，“加载更多”用 TanStack Query 的 `useInfiniteQuery`，`getNextPageParam` 取响应的 `next_cursor`。
- 列表接入[已保存视图](../features/saved-views.md)时，用 `useSavedView(savedConfig, currentConfig)` 对比当前筛选条件与已保存配置是否偏离（`dirty`），驱动“保存更改 / 另存为 / 重置”；`sameViewConfig` 可用于直接比较两份配置（忽略对象键顺序与 `undefined` 字段）。

<a id="data-table"></a>
## 用 `DataTable` 显示表格

`DataTable` 基于 mantine-datatable，应用不直接依赖它。把 `useList` 的结果交给 `list`，翻页和排序就写进地址。片段中的 `list`、`orders` 是[列表页面](#lists)中 `useList(Route)` 和 `useQuery` 的结果：

```tsx
import { type Column, DataTable, useSelection } from "@go-tenon/ui"

const columns: Column<Order>[] = [
  { accessor: "number", title: "订单号", fixed: true },
  { accessor: "amount", title: "金额", sortable: true },
  { accessor: "note", title: "备注", hidden: true },
]

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
```

- `sortable` 的列点击列头排序，`-accessor` 表示降序。没有 `total` 时（游标分页）不显示分页。
- `selection` 显示勾选框，有选中的行时表格上方显示 `bulkActions`。`useSelection(list.filters)` 翻页时保留选中，换了查询条件才清空。行的标识默认是 `id` 字段，用 `rowKey` 改。
- `storeKey` 开启列设置（宽度、显示哪些列），保存在浏览器中。`fixed` 的列不能隐藏和调整宽度，`hidden` 的列默认隐藏。
- `expand` 每行可以展开明细，`toolbar` 放在表格上方左侧，例如筛选条件。
- 表格的行不能点击，用户要能选中、复制每一列的内容。打开详情、对话框的入口用单元格中的 `CellLink` 或操作列的按钮：

```tsx
{ accessor: "error", title: "错误", ellipsis: true, render: (j) => <CellLink onClick={() => setOpen(j.id)}>{j.error}</CellLink> }
```

<a id="long-text"></a>
### 用 `TruncatedText` 显示长文字的列

说明、备注这类可能很长的文字用 `TruncatedText`：只显示一行，超出的部分用省略号代替，被截断时鼠标停在上面弹出全文，可以选中复制。列宽按文字本身的宽度，最宽 `maxWidth`（默认 320px），最窄 `minWidth`（默认 120px）。[`ResourcePage`](resources.md#admin) 的文字列自动使用它：

```tsx
{ accessor: "note", title: "备注", render: (o) => <TruncatedText value={o.note} /> }
```

<a id="description-list"></a>
### 用 `DescriptionList` 显示一个对象的属性

详情页的基本信息、系统状态这类“名称 — 值”的列表用 `DescriptionList`，不用表格：

```tsx
<DescriptionList
  title="复制槽"
  extra={<StatusDot color="green">转发中</StatusDot>}
  items={[
    { label: "名称", value: slot.name, mono: true }, // mono：值用等宽字体
    { label: "Worker 进程", value: slot.active_pid || undefined }, // 空值显示“—”
  ]}
/>
```

- 值是 `undefined`、`null` 或空字符串时显示“—”，`0` 照常显示。`monoLabels` 让名称也用等宽字体。
- 放进对话框或已有的卡片中时用 `withBorder={false}`。几组属性用 `SimpleGrid` 并排。
- `StatusDot` 的颜色按状态取：正常 `green`，需要注意 `deep-orange`，出错 `red`。

<a id="analytics-components"></a>
### 报表组件：`PeriodPicker`、`StatCard` 与 `useFormatDuration`

报表和仪表盘页面用 `periodSearch` + `usePeriod` 把时间窗口（预设区间、自定义日期与粒度）同步到地址，与后端 [`period.Params`](../http/responses.md#period) 对接；用 `StatCard` 展示 KPI 与环比，用 `useFormatDuration` 按当前语言格式化秒或毫秒时长：

```tsx
import { PeriodPicker, periodSearch, StatCard, useFormatDuration, usePeriod } from "@go-tenon/ui"

export const Route = createFileRoute("/_app/reports/overview")({
  validateSearch: periodSearch(),
  component: Overview,
})

function Overview() {
  const period = usePeriod(Route)
  const formatDuration = useFormatDuration()
  // period.params 直接传给生成客户端接口：{ from, to, tz, granularity }
  return (
    <>
      <PeriodPicker period={period} />
      <StatCard
        label="首次回复中位数"
        value={formatDuration(142)}
        delta={{ value: -18, label: "-18%", invert: true }}
      />
    </>
  )
}
```

- `delta.invert` 设为 `true` 时表示指标越低越好（如响应时长、失败数），下降显示 `green`，上升显示 `deep-orange`。
- 模块内有多张报表时，在父路由设置 `staticData.preserveSearch`，切换二级导航时保留时间与筛选条件，见[双栏导航](layout.md#rail-layout)。

<a id="forms"></a>
## 用 `useSchemaForm` 做按生成的规则校验的表单

表单用 [@mantine/form](https://mantine.dev/form/use-form/)。`useSchemaForm` 用生成的 `schemas.ts` 校验，规则和提示与服务端相同（见[校验规则与提示](client-react.md#validation-rules)），不手写规则。`rules` 传生成的 `kit`，其中有提示的译文；不传时提示是 `validation.filled` 这样的错误码：

```tsx
import { Button, TextInput } from "@mantine/core"
import type { ErrorInfo } from "@go-tenon/client"
import { applyServerErrors, FormError, useSchemaForm, useUnsavedChanges } from "@go-tenon/ui"
import { useState } from "react"
import { api, kit } from "../../../api" // src/api.ts

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
      <FormError error={error ?? form.hiddenError} />
      <TextInput label="标题" {...form.getInputProps("title")} />
      <TextInput label="链接名" {...form.getInputProps("slug")} />
      <Button type="submit">保存</Button>
    </form>
  )
}
```

- 提交时校验整个表单，之后离开一个字段时校验它。`initialValues` 可以标注为生成的请求体类型，例如 `as ArticleChanges`。
- 字段路径用点连接，例如 `items.0.name`，对应服务端错误的位置 `body.items[0].name`。字段名与请求体不一致时，服务端的错误落不到输入框上。
- `applyServerErrors` 把字段错误（包括业务错误用 [`errorx.Field`](../general/errors.md#field-errors) 指出的字段）设置到输入框下面，返回其余的错误（例如 slug 已被使用、断网），交给 `FormError` 显示在表单上方。
- 出错的字段在表单中没有输入框时（例如数组本身的数量限制），校验错误放在 `form.hiddenError` 中，同样交给 `FormError`，否则看起来是按钮没有反应。
- 不用 `@mantine/form` 时，用 `formValidator(schema, rules)` 和 `serverErrors(err)`。

<a id="unsaved-changes"></a>
### 用 `useUnsavedChanges` 提醒未保存的修改

有未保存的修改时，`useUnsavedChanges(dirty)` 在应用内离开页面前询问，刷新或关闭浏览器时由浏览器提示。保存成功后、跳转之前调用 `release`，这次离开不再询问：

```tsx
const unsaved = useUnsavedChanges(form.isDirty()) // form 是 useSchemaForm 的表单
// 保存成功后：
unsaved.release()
void navigate({ to: "/articles" })
```

- 默认用浏览器的确认框，`useUnsavedChanges(dirty, { confirm: () => confirm({ title: "放弃修改？" }) })` 换成[确认对话框](#feedback)。
- 页面在[多标签](layout.md#tabs-unsaved)中时，切换标签不询问，关闭、刷新标签时才询问。

<a id="autosave"></a>
### 用 `createAutoSaver` 自动保存

设置类页面可以在修改后自动保存。`SettingsForm` 的 `autoSave` 已经做好，见[设置页面](resources.md#settings)；自己写时用 `createAutoSaver(initial, { save, validate, delay })`：

- 值的变化交给 `change`，停下 `delay`（默认 600 毫秒）后保存；`validate` 返回 `false`、与上次保存成功的值相同时不保存。
- effect 激活时调用 `resume`，清理时调用 `stop`（立即保存等待中的变化）：`StrictMode` 和隐藏的标签都会先清理再激活。
- `status()` 和 `subscribe` 给出 `pending`、`saving`、`saved`、`error`，界面据此显示“已保存”。

<a id="text-trigger"></a>
## 用 `useTextTrigger` 和 `TriggerMenu` 做输入框触发补全

在 `Textarea` 或 `TextInput` 中输入特定前缀（如 `@` 提及同事、`/` 快捷回复、`{{` 模板变量）时弹出候选菜单，支持键盘上下选择、`Enter` 或 `Tab` 确认、`Esc` 关闭、自动避开输入法组词，并在选中后精准替换触发区间与恢复光标：

```tsx
import { TriggerMenu, textTriggerRule, useTextTrigger } from "@go-tenon/ui"
import { Box, Text, Textarea } from "@mantine/core"
import { useRef, useState } from "react"

function ReplyInput({ members }: { members: { id: string; name: string }[] }) {
  const ref = useRef<HTMLTextAreaElement>(null)
  const [text, setText] = useState("")
  const trigger = useTextTrigger({
    ref,
    value: text,
    onChange: setText,
    rules: [
      textTriggerRule({
        id: "mention",
        trigger: "@", // boundary 默认 "word"，行首或空白后触发，避免误触邮箱与网址
        items: (q) => members.filter((m) => m.name.includes(q)),
        getKey: (m) => m.id,
        renderItem: (m) => <Text size="sm">{m.name}</Text>,
        onSelect: (m, { replace }) => replace(`@${m.name} `),
      }),
    ],
  })
  return (
    <Box pos="relative">
      <TriggerMenu trigger={trigger} position="top" />
      <Textarea
        ref={ref}
        value={text}
        onChange={(e) => trigger.handleChange(e.currentTarget.value, e.currentTarget.selectionStart)}
        onKeyDown={(e) => {
          if (trigger.handleKeyDown(e)) return
          // 其余快捷键处理
        }}
      />
    </Box>
  )
}
```

- 一套 `rules` 可以同时注册多个触发前缀，匹配最靠近光标的一条；`boundary: "any"` 允许在文字中间触发（例如 `{{` 配 `stop: "}}"`）。
- `trigger.open(ruleId)` 从工具栏按钮等外部入口打开候选菜单，选中后在当前光标位置插入内容。

<a id="feedback"></a>
## 确认与提示：`confirm`、`notifySuccess`、`notifyError`

```tsx
if (!(await confirm({ title: "删除这篇文章？", confirmLabel: "删除", danger: true }))) return
try {
  await api.article.delete(id)
  notifySuccess("已删除")
} catch (err) {
  notifyError(err, "删除失败") // 文案取服务端的翻译，没有时按错误的种类给出
}
```

它们需要根路由中的 `UIProvider`（其中安装了对话框和通知），见[根路由](app.md#root)。

<a id="links"></a>
## 用 `ButtonLink` 写类型安全的路由链接

`<Button component={Link} to=…>` 丢掉了路由参数的类型检查。`ButtonLink`、`AnchorLink`、`NavLinkLink`、`ActionIconLink` 是 Mantine 的组件，`to`、`params`、`search` 按路由表检查：

```tsx
<ButtonLink to="/articles/$id" params={{ id: article.id }} variant="default">
  查看
</ButtonLink>
```
