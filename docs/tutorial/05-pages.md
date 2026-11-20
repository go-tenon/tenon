# 5. 文章页面

这一章写三个页面：文章列表、写文章、修改文章，用生成的客户端调用第 4 章的接口。

包：`@go-tenon/ui`。

<a id="client"></a>
## 生成的客户端

`tenon dev` 每次重新编译 Go 程序后重新生成 `packages/client/src/api`：`Article` 等类型、按 `OperationID` 分组的 `api.article.list()` 等方法、`ArticleErrors.SlugTaken` 等错误码常量。Go 的接口改了以后，用错的地方由类型检查报出来：

```bash
bun run typecheck
```

页面中的 `api` 来自模板中的 `apps/web/src/api.ts`。

<a id="routes"></a>
## 页面与路由

`apps/web/src/routes` 中一个文件是一个页面，文件名就是地址（[TanStack Router](https://tanstack.com/router) 的文件路由）：

| 文件 | 地址 |
| --- | --- |
| `routes/index.tsx` | `/` |
| `routes/write.tsx` | `/write` |
| `routes/articles/$id.edit.tsx` | `/articles/<文章 ID>/edit`，`$id` 是路径参数 |

`src/routeTree.gen.ts` 是生成的，不要手动修改。

`routes/__root.tsx` 是所有页面的外层，界面用 [Mantine](https://mantine.dev) 和 `@go-tenon/ui` 的布局。把 `apps/web/src/routes/__root.tsx` 改成下面这样：

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
  const { ssr, context } = useRouter().options
  const nonce = ssr?.nonce
  // 配色保存在这个前端自己的键中，同一个域名下的其他前端（例如管理员端）各记各的。
  const colorScheme = context.app.storage.key("color-scheme")
  return (
    <html lang="zh-CN" {...mantineHtmlProps}>
      <head>
        <ColorSchemeScript nonce={nonce} localStorageKey={colorScheme} />
        <HeadContent />
      </head>
      <body>
        <UIProvider theme={theme} colorSchemeKey={colorScheme}>{children}</UIProvider>
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

`AnchorLink`、`ButtonLink` 的 `to` 和路径参数都有类型检查。

<a id="list"></a>
## 文章列表

页面用 [TanStack Query](https://tanstack.com/query) 的 `useQuery` 读取、`useMutation` 修改。把首页 `apps/web/src/routes/index.tsx` 改成文章列表：

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

删除成功后，查询键以 `["article"]` 开头的查询失效，列表重新获取。

<a id="editor"></a>
## 编辑器

写文章和修改文章共用一个表单组件。新建 `apps/web/src/ArticleEditor.tsx`：

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

接口返回错误时，生成的客户端抛出 `ApiError`：`422` 时 `fields` 中每个字段一条提示，显示在输入框下面；其他业务错误（例如 slug 已被使用）的 `detail` 显示在表单上方。校验规则只写在 Go 的 `Changes` 结构体上。

<a id="app"></a>
## 写文章与修改

新建 `apps/web/src/routes/write.tsx`：

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

新建 `apps/web/src/routes/articles/$id.edit.tsx`，按地址中的 `$id` 读取文章，再交给编辑器：

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

打开 <http://localhost:8080>，写一篇文章，再修改、删除它；提交空标题，或者两篇文章用同一个 slug，看错误提示。

<a id="troubleshooting"></a>
## 常见问题

- **`bun run typecheck` 报错找不到 `api.article`**：客户端还没有重新生成。确认 `bun run dev` 正在运行、Go 程序编译成功，或者运行 `bun run generate`。
