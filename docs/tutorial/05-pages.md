# 5. 文章页面

上一章的接口已经可以在 `/api/docs` 中调用了，但用户需要的是页面。

**这一章要做的：** 写三个页面：文章列表、写文章、修改文章。

**你会学到：**

- 前端怎样调用 Go 写的接口：调用的代码和类型都是根据接口生成的；
- 用文件名定义页面的地址；
- 读取和修改数据，以及修改后怎样刷新列表；
- 把服务端返回的错误显示在表单上。

阅读前需要了解：[第 4 章](04-api.md)；React 的组件、`useState` 和 JSX。这一章用到的两个库会在用到时介绍。

- [生成的客户端](#client)
- [页面与路由](#routes)
- [文章列表](#list)
- [编辑器](#editor)
- [写文章与修改](#app)
- [常见问题](#troubleshooting)
- [小结](#summary)

<a id="client"></a>
## 生成的客户端

前端要调用接口，需要知道每个接口的地址、方法、参数和返回的字段。手写这些既麻烦又容易和后端不一致，所以它们是**生成**的：`tenon dev` 每次重新编译 Go 程序后，读出全部接口的描述（OpenAPI 文档），重新生成 `packages/client/src/api` 中的 TypeScript 代码。上一章加了文章接口，现在里面已经有：

- `Article`、`ArticleChanges` 等类型，与 Go 的结构体对应；
- `api.article.list()`、`api.article.create(changes)` 等方法，按 `OperationID` 分组；
- `ArticleErrors.SlugTaken` 等错误码常量。

例如，Go 中 `Article` 结构体有 `title` 字段，前端的 `Article` 类型也有 `title`。以后 Go 的接口改了，前端的类型随之改变，用错的地方类型检查会报出来：

```bash
bun run typecheck
```

页面中用的 `api` 对象来自 `apps/web/src/api.ts`，它创建客户端，并指定接口都在 `/api` 下。模板中已经写好了，不需要修改。

<a id="routes"></a>
## 页面与路由

前端的**路由**决定“哪个地址显示哪个页面”。我们用 [TanStack Router](https://tanstack.com/router) 的文件路由：`apps/web/src/routes` 中一个文件是一个页面，文件名就是地址。这一章有三个页面：

| 文件 | 地址 | 页面 |
| --- | --- | --- |
| `routes/index.tsx` | `/` | 文章列表 |
| `routes/write.tsx` | `/write` | 写文章 |
| `routes/articles/$id.edit.tsx` | `/articles/<文章 ID>/edit` | 修改文章，`$id` 是路径参数 |

所有页面汇总在 `src/routeTree.gen.ts` 中，它也是生成的：`tenon dev` 运行时，新增或删除页面文件后会自动重新生成，`bun run typecheck` 也会先重新生成它。不要手动修改它。

先写所有页面共用的部分。`routes/__root.tsx` 是所有页面的外层：`Document` 是整个 HTML 文档，`Layout` 是页头和页面，`<SiteLayout>` 的内容区显示当前地址对应的页面。界面用 [Mantine](https://mantine.dev) 的组件，`@go-tenon/ui` 提供布局和样式。把 `apps/web/src/routes/__root.tsx` 改成下面这样：

```tsx title="apps/web/src/routes/__root.tsx"
import "@go-tenon/ui/styles.css"
import type { ReactNode } from "react"
import { ColorSchemeScript, mantineHtmlProps } from "@mantine/core"
import { createRootRouteWithContext, HeadContent, Scripts, useRouter } from "@tanstack/react-router"
import type { RouterContext } from "@go-tenon/ui"
import { AnchorLink, ButtonLink, SiteLayout, UIProvider } from "@go-tenon/ui"
import { theme } from "../theme"

export const Route = createRootRouteWithContext<RouterContext>()({
  head: () => ({
    meta: [{ charSet: "utf-8" }, { name: "viewport", content: "width=device-width, initial-scale=1" }, { title: "博客" }],
  }),
  shellComponent: Document,
  component: Layout,
})

// Document 是整个 HTML 文档。nonce 由 Go 为每个请求生成，内联脚本带上它才能通过内容安全策略。
function Document({ children }: { children: ReactNode }) {
  const nonce = useRouter().options.ssr?.nonce
  return (
    <html lang="zh-CN" {...mantineHtmlProps}>
      <head>
        <ColorSchemeScript nonce={nonce} />
        <HeadContent />
      </head>
      <body>
        <UIProvider theme={theme}>{children}</UIProvider>
        <Scripts />
      </body>
    </html>
  )
}

function Layout() {
  return (
    <SiteLayout
      brand={
        <AnchorLink to="/" c="inherit" underline="never" fw={700}>
          博客
        </AnchorLink>
      }
      actions={<ButtonLink to="/write">写文章</ButtonLink>}
    />
  )
}
```

`AnchorLink`、`ButtonLink` 是页面之间的链接，外观分别是文字链接和按钮，点击后切换页面而不重新加载整个网页。它们的 `to` 和路径参数都有类型检查：写错地址，或者漏了 `$id`，`bun run typecheck` 会报出来。

<a id="list"></a>
## 文章列表

页面用 [TanStack Query](https://tanstack.com/query) 调用接口。它提供两个函数：

- **`useQuery` 读取数据。** 你告诉它“怎样读取”（调用哪个接口）和一个**查询键**（这份数据的名字，例如 `["article", "list"]`）。它负责调用接口、记录“正在加载”“出错了”等状态，并按查询键缓存结果：再次进入页面时先显示缓存，再在后台更新。
- **`useMutation` 修改数据**，例如删除文章。

把首页 `apps/web/src/routes/index.tsx` 改成文章列表：

```tsx title="apps/web/src/routes/index.tsx"
import { Button, Container, Group, Paper, Stack, Text, Title } from "@mantine/core"
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { createFileRoute } from "@tanstack/react-router"
import { ButtonLink } from "@go-tenon/ui"
import type { Article } from "@blog/client/api"
import { api } from "../api"

export const Route = createFileRoute("/")({ component: ArticleList })

function ArticleList() {
  const queryClient = useQueryClient()
  const articles = useQuery({ queryKey: ["article", "list"], queryFn: () => api.article.list() })
  const remove = useMutation({
    mutationFn: (a: Article) => api.article.delete(a.id),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ["article"] }),
  })

  return (
    <Container size="sm" py="xl">
      {articles.isPending && <Text c="dimmed">加载中…</Text>}
      {articles.error && <Text c="red">{articles.error.message}</Text>}
      {articles.data?.items.length === 0 && <Text c="dimmed">还没有文章。</Text>}
      <Stack>
        {articles.data?.items.map((a) => (
          <Paper key={a.id} p="lg">
            <Title order={3}>{a.title}</Title>
            <Text c="dimmed" mt="xs" lineClamp={3}>
              {a.content}
            </Text>
            <Group mt="md" gap="xs">
              <ButtonLink to="/articles/$id/edit" params={{ id: a.id }} variant="default" size="xs">
                编辑
              </ButtonLink>
              <Button variant="subtle" color="red" size="xs" onClick={() => confirm(`删除“${a.title}”？`) && remove.mutate(a)}>
                删除
              </Button>
            </Group>
          </Paper>
        ))}
      </Stack>
    </Container>
  )
}
```

删除一篇文章后，列表要随之更新。`useMutation` 的 `onSuccess` 在删除成功后，让查询键以 `["article"]` 开头的查询都**失效**：TanStack Query 认为这些缓存已经过时，正在显示的列表随之重新获取。

<a id="editor"></a>
## 编辑器

写文章和修改文章用的是同一个表单，所以写成一个组件 `ArticleEditor`，两个页面共用。它不是页面，放在 `routes` 之外；保存或取消之后去哪里，由使用它的页面通过 `onDone` 决定。新建 `apps/web/src/ArticleEditor.tsx`：

```tsx title="apps/web/src/ArticleEditor.tsx"
import { useState, type FormEvent } from "react"
import { Alert, Button, Group, Stack, Textarea, TextInput } from "@mantine/core"
import { useMutation, useQueryClient } from "@tanstack/react-query"
import { isApiError, type Article, type ArticleChanges } from "@blog/client/api"
import { api } from "./api"

// ArticleEditor 编辑 article；没有 article 时写一篇新文章。保存或取消后调用 onDone。
export function ArticleEditor({ article, onDone }: { article?: Article; onDone: () => void }) {
  const queryClient = useQueryClient()
  const [changes, setChanges] = useState<ArticleChanges>({
    title: article?.title ?? "",
    slug: article?.slug ?? "",
    content: article?.content ?? "",
  })
  const save = useMutation({
    mutationFn: () => (article ? api.article.update(article.id, changes) : api.article.create(changes)),
    onSuccess: async () => {
      await queryClient.invalidateQueries({ queryKey: ["article"] })
      onDone()
    },
  })

  // 422 时每个字段一条提示；其他业务错误（例如 slug 已被使用）显示在表单上方
  const fieldError = (name: keyof ArticleChanges) =>
    isApiError(save.error) ? save.error.fields.find((f) => f.location === `body.${name}`)?.message : undefined
  const formError = isApiError(save.error) && save.error.fields.length === 0 ? save.error.detail : undefined

  function submit(e: FormEvent) {
    e.preventDefault()
    save.mutate()
  }
  const field = (name: keyof ArticleChanges) => ({
    value: changes[name],
    error: fieldError(name),
    onChange: (e: { currentTarget: { value: string } }) => setChanges({ ...changes, [name]: e.currentTarget.value }),
  })

  return (
    <form onSubmit={submit}>
      <Stack>
        {formError && <Alert color="red">{formError}</Alert>}
        <TextInput label="标题" {...field("title")} />
        <TextInput label="Slug" placeholder="hello-world" {...field("slug")} />
        <Textarea label="正文" autosize minRows={12} {...field("content")} />
        <Group>
          <Button type="submit" loading={save.isPending}>
            保存
          </Button>
          <Button variant="default" onClick={onDone}>
            取消
          </Button>
        </Group>
      </Stack>
    </form>
  )
}
```

接口返回错误时，生成的客户端抛出 `ApiError`，`isApiError` 判断一个错误是不是它。编辑器把错误分成两类显示：

- `422` 时 `fields` 中每个字段一条提示，`location` 是 `body.title` 这样的位置，交给输入框的 `error`，显示在它下面；
- 其他业务错误，例如 slug 已被使用，`detail` 是翻译好的提示，显示在表单上方。

这些提示都来自服务端：校验规则只写在 Go 的 `Changes` 结构体上，前端不需要再写一遍，两边也不会不一致。

<a id="app"></a>
## 写文章与修改

现在加上两个页面。新建 `apps/web/src/routes/write.tsx`，地址是 `/write`：

```tsx title="apps/web/src/routes/write.tsx"
import { Container, Title } from "@mantine/core"
import { createFileRoute, useNavigate } from "@tanstack/react-router"
import { ArticleEditor } from "../ArticleEditor"

export const Route = createFileRoute("/write")({ component: Write })

function Write() {
  const navigate = useNavigate()
  return (
    <Container size="sm" py="xl">
      <Title order={2} mb="lg">
        写文章
      </Title>
      <ArticleEditor onDone={() => navigate({ to: "/" })} />
    </Container>
  )
}
```

`useNavigate` 返回一个跳转页面的函数，这里在保存或取消后回到首页。

修改文章的页面先按地址中的 `$id` 读取文章，再交给编辑器。新建 `apps/web/src/routes/articles/$id.edit.tsx`：

```tsx title="apps/web/src/routes/articles/$id.edit.tsx"
import { Container, Text, Title } from "@mantine/core"
import { useQuery } from "@tanstack/react-query"
import { createFileRoute, useNavigate } from "@tanstack/react-router"
import { ArticleEditor } from "../../ArticleEditor"
import { api } from "../../api"

export const Route = createFileRoute("/articles/$id/edit")({ component: EditArticle })

function EditArticle() {
  const { id } = Route.useParams()
  const navigate = useNavigate()
  const article = useQuery({ queryKey: ["article", id], queryFn: () => api.article.get(id) })

  return (
    <Container size="sm" py="xl">
      <Title order={2} mb="lg">
        修改文章
      </Title>
      {article.isPending && <Text c="dimmed">加载中…</Text>}
      {article.error && <Text c="red">{article.error.message}</Text>}
      {article.data && <ArticleEditor article={article.data} onDone={() => navigate({ to: "/" })} />}
    </Container>
  )
}
```

`Route.useParams()` 读出路径参数 `id`，它的类型由文件名推导出来。文章还在加载时显示“加载中”，加载完再交给编辑器。

每个页面有自己的地址，所以刷新页面、浏览器的前进后退、把地址发给别人，都会回到同一个页面。

样式来自 `@go-tenon/ui` 的样式预设（`apps/web/src/theme.ts`），不需要另写 CSS。要换品牌色，修改 `theme.ts` 中的 `primary`。

打开 <http://localhost:8080>，写一篇文章，再修改、删除它。试试提交空标题，或者两篇文章用同一个 slug，看看错误提示显示在哪里。

<a id="troubleshooting"></a>
## 常见问题

**`bun run typecheck` 报错找不到 `api.article`**

客户端还没有重新生成。确认 `bun run dev` 正在运行、Go 程序编译成功；没有运行开发环境时，手动运行 `bun run generate`。

**新建的页面访问时显示 404**

检查文件是否放在 `apps/web/src/routes` 中，以及文件名和地址是否对应，例如 `$id.edit.tsx` 对应 `/articles/<ID>/edit`（还要放在 `articles` 目录中）。

**删除文章后列表没有更新**

检查 `onSuccess` 中失效的查询键，是否是列表 `useQuery` 的查询键的开头部分。

**错误提示没有显示在输入框下面**

只有 `422` 的校验错误带有 `fields`。其他业务错误（例如 slug 已被使用）显示在表单上方。

<a id="summary"></a>
## 小结

这一章，你：

- 用生成的客户端调用接口，类型随 Go 的接口自动更新；
- 用文件名定义了三个页面的地址；
- 用 `useQuery` 读取数据、`useMutation` 修改数据，修改后让查询失效来刷新列表；
- 把服务端返回的错误显示在表单上，校验规则只写在服务端。

现在博客有了可以使用的页面，但谁都可以写文章、删文章。下一章，我们加上作者登录。

深入了解：[客户端代码](../http/clients.md)、[TanStack Start 的接入](../frontend/start.md)、[搭建前端应用](../frontend/app.md)、[布局与导航](../frontend/layout.md)。
