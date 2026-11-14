# 5. 文章页面

- [生成的客户端](#client)
- [文章列表](#list)
- [编辑器](#editor)
- [页面](#app)

<a id="client"></a>
## 生成的客户端

`tenon dev` 每次重新编译后都会重新生成 `frontend/src/api`。上一章加了文章接口，现在里面已经有：

- `Article`、`ArticleChanges` 等类型，与 Go 的结构体对应；
- `api.article.list()`、`api.article.create(changes)` 等方法，按 `OperationID` 分组；
- `ArticleErrors.SlugTaken` 等错误码常量。

不用手写请求，也不用手写类型。Go 接口改了，前端的类型随之改变，用错的地方类型检查会报出来：

```bash
bun run typecheck
```

`frontend/src/client.ts` 创建客户端，模板中已经写好了，接口都在 `/api` 下。

<a id="list"></a>
## 文章列表

页面用 [TanStack Query](https://tanstack.com/query) 调用接口：`useQuery` 读取数据，负责加载状态、缓存和重新获取；`useMutation` 修改数据。新建 `frontend/src/ArticleList.tsx`：

```tsx title="frontend/src/ArticleList.tsx"
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import type { Article } from "./api/api"
import { api } from "./client"

export function ArticleList({ onEdit }: { onEdit: (a: Article) => void }) {
  const queryClient = useQueryClient()
  const articles = useQuery({ queryKey: ["article", "list"], queryFn: () => api.article.list() })
  const remove = useMutation({
    mutationFn: (a: Article) => api.article.delete(a.id),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ["article"] }),
  })

  if (articles.isPending) return <p>加载中…</p>
  if (articles.error) return <p className="error">{articles.error.message}</p>
  if (articles.data.items.length === 0) return <p>还没有文章。</p>
  return (
    <ul className="articles">
      {articles.data.items.map((a) => (
        <li key={a.id}>
          <h2>{a.title}</h2>
          <p>{a.content.slice(0, 120)}</p>
          <button onClick={() => onEdit(a)}>编辑</button>
          <button onClick={() => confirm(`删除“${a.title}”？`) && remove.mutate(a)}>删除</button>
        </li>
      ))}
    </ul>
  )
}
```

删除成功后，让键以 `["article"]` 开头的查询都失效，列表随之重新获取。

<a id="editor"></a>
## 编辑器

新建 `frontend/src/ArticleEditor.tsx`，新建和修改文章共用它：

```tsx title="frontend/src/ArticleEditor.tsx"
import { useState, type FormEvent } from "react"
import { useMutation, useQueryClient } from "@tanstack/react-query"
import { isApiError, type Article, type ArticleChanges } from "./api/api"
import { api } from "./client"

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
    onChange: (e: { target: { value: string } }) => setChanges({ ...changes, [name]: e.target.value }),
  })

  return (
    <form className="editor" onSubmit={submit}>
      {formError && <p className="error">{formError}</p>}
      <label>
        标题 <input {...field("title")} />
      </label>
      {fieldError("title") && <p className="error">{fieldError("title")}</p>}
      <label>
        Slug <input {...field("slug")} placeholder="hello-world" />
      </label>
      {fieldError("slug") && <p className="error">{fieldError("slug")}</p>}
      <label>
        正文 <textarea rows={12} {...field("content")} />
      </label>
      <div>
        <button disabled={save.isPending}>保存</button>
        <button type="button" onClick={onDone}>
          取消
        </button>
      </div>
    </form>
  )
}
```

接口返回错误时，客户端抛出 `ApiError`：

- `422` 时 `fields` 中每个字段一条提示，`location` 是 `body.title` 这样的位置，显示在对应的输入框下面；
- 其他业务错误，例如 slug 已被使用，`detail` 是翻译好的提示，显示在表单上方。

提示都来自服务端，前端不用再写一遍校验规则。

<a id="app"></a>
## 页面

把 `frontend/src/App.tsx` 改成下面这样，在列表和编辑器之间切换：

```tsx title="frontend/src/App.tsx"
import { useState } from "react"
import type { Article } from "./api/api"
import { ArticleEditor } from "./ArticleEditor"
import { ArticleList } from "./ArticleList"

export function App() {
  // 正在编辑的文章："new" 是新文章，null 表示显示列表
  const [editing, setEditing] = useState<Article | "new" | null>(null)
  return (
    <main>
      <header>
        <h1>博客</h1>
        {editing === null && <button onClick={() => setEditing("new")}>写文章</button>}
      </header>
      {editing === null ? (
        <ArticleList onEdit={setEditing} />
      ) : (
        <ArticleEditor article={editing === "new" ? undefined : editing} onDone={() => setEditing(null)} />
      )}
    </main>
  )
}
```

再换一份样式 `frontend/src/app.css`：

```css title="frontend/src/app.css"
body {
  margin: 0;
  font-family: system-ui, sans-serif;
  background: #f6f6f6;
}

main {
  max-width: 44rem;
  margin: 2rem auto;
  padding: 1.5rem;
  background: #fff;
  border-radius: 0.75rem;
}

header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 0.5rem;
}

.articles {
  list-style: none;
  padding: 0;
}

.articles li {
  padding: 1rem 0;
  border-bottom: 1px solid #eee;
}

.editor label {
  display: block;
  margin: 0.75rem 0 0.25rem;
}

.editor input,
.editor textarea {
  display: block;
  width: 100%;
  box-sizing: border-box;
  padding: 0.5rem;
}

button + button {
  margin-left: 0.5rem;
}

.error {
  color: #c00;
}
```

打开 http://localhost:8080，写一篇文章，再修改、删除它。试试提交空标题，或者两篇文章用同一个 slug。

现在谁都可以写文章、删文章。下一章加上作者登录。

深入了解：[客户端代码](../responses/clients.md)、[单页应用](../responses/spa.md)。
