# 7. 草稿与发布

这是入门教程的最后一章。上一章加上了登录，但所有作者的权限都一样。这一章，我们定下文章的规则：

- 每篇文章有作者，只有作者能修改、删除它；
- 新写的文章是草稿，只有作者能看到；
- 作者发布后，所有人都能看到。

我们先给文章表加上作者和发布时间两列，再把这些规则写进业务动作，最后修改前端。

- [加列](#migration)
- [谁能做什么](#rules)
- [动作](#actions)
- [前端](#frontend)

<a id="migration"></a>
## 加列

```bash
go run ./cmd/api migrate make add_author_and_published_at_to_articles_table
```

```sql title="migrations/20261113080010_add_author_and_published_at_to_articles_table.sql"
-- +goose Up
ALTER TABLE articles
    ADD COLUMN author_id    uuid REFERENCES users (id),
    ADD COLUMN published_at timestamptz;
-- 之前写的文章还没有作者，归给最早创建的作者，然后才能要求每篇文章都有作者
UPDATE articles SET author_id = (SELECT id FROM users ORDER BY created_at LIMIT 1);
ALTER TABLE articles ALTER COLUMN author_id SET NOT NULL;
CREATE INDEX articles_author_id_idx ON articles (author_id);

-- +goose Down
ALTER TABLE articles DROP COLUMN author_id, DROP COLUMN published_at;
```

表中已经有数据时，加 `NOT NULL` 的列分三步：先允许为空，补上数据，再加约束。`published_at` 为空就是草稿。

```bash
go run ./cmd/api migrate up
```

<a id="rules"></a>
## 谁能做什么

规则写在动作里：路由只知道“要登录”，动作才知道“是不是这篇文章的作者”。动作的 `Handle` 接收调用者 `identity.Subject`，命令行、队列任务和测试都能以任何身份调用它。

在 `internal/article/article.go` 中加上两个字段、两个错误，以及修改前读取文章用的 `findOwn`：

```go title="internal/article/article.go"
// Package article 是博客的文章：模型、业务错误和各个动作，每个动作一个文件。
package article

import (
    "context"
    "database/sql"
    "time"

    "github.com/cockroachdb/errors"
    "github.com/google/uuid"
    "github.com/uptrace/bun"

    "github.com/go-tenon/tenon/errorx"
    "github.com/go-tenon/tenon/identity"
)

// Article 是一篇文章。bun 标签对应 articles 表的列，json 标签是接口返回的字段。
type Article struct {
    bun.BaseModel `bun:"table:articles"`

    ID       uuid.UUID `bun:"id,pk" json:"id"`
    AuthorID string    `bun:"author_id,notnull" json:"author_id"`
    Title    string    `bun:"title,notnull" json:"title"`
    Slug     string    `bun:"slug,notnull" json:"slug"`
    Content  string    `bun:"content,notnull" json:"content"`
    // PublishedAt 为空时是草稿，只有作者能看到。
    PublishedAt *time.Time `bun:"published_at" json:"published_at,omitempty"`
    CreatedAt   time.Time  `bun:"created_at,notnull" json:"created_at"`
    UpdatedAt   time.Time  `bun:"updated_at,notnull" json:"updated_at"`
}

// 业务错误：错误码返回给客户端，前端按它显示提示。
var (
    ErrNotFound         = errorx.Define(errorx.NotFound, "article.not_found")
    ErrSlugTaken        = errorx.Define(errorx.Conflict, "article.slug_taken")
    ErrForbidden        = errorx.Define(errorx.PermissionDenied, "article.forbidden")
    ErrAlreadyPublished = errorx.Define(errorx.Conflict, "article.already_published")
)

// Changes 是新建和修改文章时提交的内容，标签就是校验规则。
type Changes struct {
    Title   string `json:"title" minLength:"1" maxLength:"200"`
    Slug    string `json:"slug" minLength:"1" maxLength:"100" pattern:"^[a-z0-9]+(-[a-z0-9]+)*$" patternDescription:"小写字母、数字和连字符"`
    Content string `json:"content" maxLength:"100000"`
}

// findOwn 读取 sub 自己的文章，用于修改、删除和发布：别人的文章返回 ErrForbidden。
// 在事务中调用并加上 FOR UPDATE，检查和修改之间文章被锁住。
func findOwn(ctx context.Context, tx bun.IDB, sub identity.Subject, id uuid.UUID) (*Article, error) {
    item := new(Article)
    err := tx.NewSelect().Model(item).Where("id = ?", id).For("UPDATE").Scan(ctx)
    if errors.Is(err, sql.ErrNoRows) {
        return nil, ErrNotFound.New()
    }
    if err != nil {
        return nil, errors.Wrap(err, "load article")
    }
    if item.AuthorID != sub.ID {
        return nil, ErrForbidden.New()
    }
    return item, nil
}

// ArticleOutput 是返回一篇文章的响应。
type ArticleOutput struct {
    Body *Article
}

// IDInput 是路径中带文章 ID 的请求。
type IDInput struct {
    ID uuid.UUID `path:"id"`
}
```

`findOwn` 在事务中用 `FOR UPDATE` 读取文章，检查作者之后、修改之前，别的请求改不了这一行。检查用的必须是事务中刚读到的数据。

在 `locales/zh-CN.yaml` 中加上两个错误的提示：

```yaml title="locales/zh-CN.yaml"
article.not_found: 文章不存在
article.slug_taken: "slug“{{.slug}}”已被使用"
article.forbidden: 只有作者能修改这篇文章
article.already_published: 文章已经发布了
```

<a id="actions"></a>
## 动作

首页只列出已发布的文章。`internal/article/list.go`：

```go title="internal/article/list.go"
package article

import (
    "context"

    "github.com/uptrace/bun"

    "github.com/go-tenon/tenon/page"
)

// List 按发布时间倒序分页列出已发布的文章，所有人都能看到。
type List struct{ db *bun.DB }

func NewList(db *bun.DB) *List { return &List{db: db} }

type ListInput struct {
    page.OffsetParams
}

type ListOutput struct {
    Body page.Offset[Article]
}

func (a *List) HTTP(ctx context.Context, in *ListInput) (*ListOutput, error) {
    var items []Article
    total, err := a.db.NewSelect().Model(&items).
        Where("published_at IS NOT NULL").
        OrderExpr("published_at DESC, id DESC").
        Limit(in.Limit()).Offset(in.Offset()).
        ScanAndCount(ctx)
    if err != nil {
        return nil, err
    }
    return &ListOutput{Body: page.NewOffset(items, in.OffsetParams, int64(total))}, nil
}
```

作者在“我的文章”中看到自己的全部文章，包括草稿。新建 `internal/article/mine.go`：

```go title="internal/article/mine.go"
package article

import (
    "context"

    "github.com/uptrace/bun"

    "github.com/go-tenon/tenon/identity"
    "github.com/go-tenon/tenon/page"
)

// Mine 列出当前作者的全部文章，包括草稿。
type Mine struct{ db *bun.DB }

func NewMine(db *bun.DB) *Mine { return &Mine{db: db} }

func (a *Mine) HTTP(ctx context.Context, in *ListInput) (*ListOutput, error) {
    sub, _ := identity.FromContext(ctx)
    var items []Article
    total, err := a.db.NewSelect().Model(&items).
        Where("author_id = ?", sub.ID).
        OrderExpr("created_at DESC, id DESC").
        Limit(in.Limit()).Offset(in.Offset()).
        ScanAndCount(ctx)
    if err != nil {
        return nil, err
    }
    return &ListOutput{Body: page.NewOffset(items, in.OffsetParams, int64(total))}, nil
}
```

读取文章的接口是公开的，只返回已发布的文章，草稿对外就像不存在一样。`internal/article/get.go`：

```go title="internal/article/get.go"
package article

import (
    "context"
    "database/sql"

    "github.com/cockroachdb/errors"
    "github.com/uptrace/bun"
)

// Get 读取一篇已发布的文章。草稿对外就像不存在一样。
type Get struct{ db *bun.DB }

func NewGet(db *bun.DB) *Get { return &Get{db: db} }

func (a *Get) HTTP(ctx context.Context, in *IDInput) (*ArticleOutput, error) {
    item := new(Article)
    err := a.db.NewSelect().Model(item).Where("id = ? AND published_at IS NOT NULL", in.ID).Scan(ctx)
    if errors.Is(err, sql.ErrNoRows) {
        return nil, ErrNotFound.New()
    }
    if err != nil {
        return nil, errors.Wrap(err, "load article")
    }
    return &ArticleOutput{Body: item}, nil
}
```

新建时记录作者。`internal/article/create.go`：

```go title="internal/article/create.go"
package article

import (
    "context"
    "time"

    "github.com/cockroachdb/errors"
    "github.com/google/uuid"
    "github.com/uptrace/bun"

    "github.com/go-tenon/tenon/database"
    "github.com/go-tenon/tenon/errorx"
    "github.com/go-tenon/tenon/identity"
)

// Create 新建文章，新文章是草稿。
type Create struct{ db *bun.DB }

func NewCreate(db *bun.DB) *Create { return &Create{db: db} }

// Handle 以 sub 为作者新建一篇草稿，slug 已被使用时返回 ErrSlugTaken。
func (a *Create) Handle(ctx context.Context, sub identity.Subject, c Changes) (*Article, error) {
    now := time.Now().UTC()
    item := &Article{
        ID: uuid.Must(uuid.NewV7()), AuthorID: sub.ID, Title: c.Title, Slug: c.Slug, Content: c.Content,
        CreatedAt: now, UpdatedAt: now,
    }
    _, err := a.db.NewInsert().Model(item).Exec(ctx)
    if database.IsUniqueViolation(err, "articles_slug_key") {
        return nil, ErrSlugTaken.Wrap(err, errorx.Public("slug", c.Slug))
    }
    return item, errors.Wrap(err, "insert article")
}

type CreateInput struct {
    Body Changes
}

func (a *Create) HTTP(ctx context.Context, in *CreateInput) (*ArticleOutput, error) {
    sub, _ := identity.FromContext(ctx)
    item, err := a.Handle(ctx, sub, in.Body)
    if err != nil {
        return nil, err
    }
    return &ArticleOutput{Body: item}, nil
}
```

修改和删除前用 `findOwn` 检查作者。`internal/article/update.go`：

```go title="internal/article/update.go"
package article

import (
    "context"
    "time"

    "github.com/google/uuid"
    "github.com/uptrace/bun"

    "github.com/go-tenon/tenon/database"
    "github.com/go-tenon/tenon/errorx"
    "github.com/go-tenon/tenon/identity"
)

// Update 修改文章，只有作者能修改。
type Update struct{ db *bun.DB }

func NewUpdate(db *bun.DB) *Update { return &Update{db: db} }

func (a *Update) Handle(ctx context.Context, sub identity.Subject, id uuid.UUID, c Changes) (*Article, error) {
    var item *Article
    err := database.RunInTx(ctx, a.db, nil, func(ctx context.Context, tx database.Tx) error {
        var err error
        if item, err = findOwn(ctx, tx, sub, id); err != nil {
            return err
        }
        item.Title, item.Slug, item.Content, item.UpdatedAt = c.Title, c.Slug, c.Content, time.Now().UTC()
        _, err = tx.NewUpdate().Model(item).Column("title", "slug", "content", "updated_at").WherePK().Exec(ctx)
        if database.IsUniqueViolation(err, "articles_slug_key") {
            return ErrSlugTaken.Wrap(err, errorx.Public("slug", c.Slug))
        }
        return err
    })
    if err != nil {
        return nil, err
    }
    return item, nil
}

type UpdateInput struct {
    ID   uuid.UUID `path:"id"`
    Body Changes
}

func (a *Update) HTTP(ctx context.Context, in *UpdateInput) (*ArticleOutput, error) {
    sub, _ := identity.FromContext(ctx)
    item, err := a.Handle(ctx, sub, in.ID, in.Body)
    if err != nil {
        return nil, err
    }
    return &ArticleOutput{Body: item}, nil
}
```

`internal/article/delete.go`：

```go title="internal/article/delete.go"
package article

import (
    "context"

    "github.com/google/uuid"
    "github.com/uptrace/bun"

    "github.com/go-tenon/tenon/database"
    "github.com/go-tenon/tenon/identity"
)

// Delete 删除文章，只有作者能删除。
type Delete struct{ db *bun.DB }

func NewDelete(db *bun.DB) *Delete { return &Delete{db: db} }

func (a *Delete) Handle(ctx context.Context, sub identity.Subject, id uuid.UUID) error {
    return database.RunInTx(ctx, a.db, nil, func(ctx context.Context, tx database.Tx) error {
        item, err := findOwn(ctx, tx, sub, id)
        if err != nil {
            return err
        }
        _, err = tx.NewDelete().Model(item).WherePK().Exec(ctx)
        return err
    })
}

func (a *Delete) HTTP(ctx context.Context, in *IDInput) (*struct{}, error) {
    sub, _ := identity.FromContext(ctx)
    return nil, a.Handle(ctx, sub, in.ID)
}
```

让我们新建 `internal/article/publish.go`，发布草稿：

```go title="internal/article/publish.go"
package article

import (
    "context"
    "time"

    "github.com/google/uuid"
    "github.com/uptrace/bun"

    "github.com/go-tenon/tenon/database"
    "github.com/go-tenon/tenon/identity"
)

// Publish 发布草稿：只有作者能发布，每篇只发布一次。
type Publish struct{ db *bun.DB }

func NewPublish(db *bun.DB) *Publish { return &Publish{db: db} }

func (a *Publish) Handle(ctx context.Context, sub identity.Subject, id uuid.UUID) (*Article, error) {
    var item *Article
    err := database.RunInTx(ctx, a.db, nil, func(ctx context.Context, tx database.Tx) error {
        var err error
        if item, err = findOwn(ctx, tx, sub, id); err != nil {
            return err
        }
        if item.PublishedAt != nil {
            return ErrAlreadyPublished.New()
        }
        now := time.Now().UTC()
        item.PublishedAt, item.UpdatedAt = &now, now
        _, err = tx.NewUpdate().Model(item).Column("published_at", "updated_at").WherePK().Exec(ctx)
        return err
    })
    if err != nil {
        return nil, err
    }
    return item, nil
}

func (a *Publish) HTTP(ctx context.Context, in *IDInput) (*ArticleOutput, error) {
    sub, _ := identity.FromContext(ctx)
    item, err := a.Handle(ctx, sub, in.ID)
    if err != nil {
        return nil, err
    }
    return &ArticleOutput{Body: item}, nil
}
```

在 `internal/article/routes.go` 中注册新的两个接口：

```go title="internal/article/routes.go"
package article

import (
    "net/http"

    "github.com/danielgtaylor/huma/v2"
    "github.com/uptrace/bun"

    "github.com/go-tenon/tenon/httpx/rest"
)

// Register 注册文章的接口：读已发布的文章是公开的，其余要先登录。
func Register(api *rest.API, requireUser func(*huma.Operation), db *bun.DB) {
    route := func(access func(*huma.Operation), id, method, path, summary string) huma.Operation {
        op := huma.Operation{OperationID: id, Method: method, Path: path, Summary: summary}
        access(&op)
        return op
    }
    public := rest.Public()
    create := route(requireUser, "article-create", http.MethodPost, "/articles", "新建草稿")
    create.DefaultStatus = http.StatusCreated

    rest.Register(api, route(public, "article-list", http.MethodGet, "/articles", "已发布的文章"), NewList(db).HTTP)
    rest.Register(api, route(public, "article-get", http.MethodGet, "/articles/{id}", "读取已发布的文章"), NewGet(db).HTTP)
    rest.Register(api, route(requireUser, "article-mine", http.MethodGet, "/me/articles", "我的文章，包括草稿"), NewMine(db).HTTP)
    rest.Register(api, create, NewCreate(db).HTTP)
    rest.Register(api, route(requireUser, "article-update", http.MethodPut, "/articles/{id}", "修改文章"), NewUpdate(db).HTTP)
    rest.Register(api, route(requireUser, "article-delete", http.MethodDelete, "/articles/{id}", "删除文章"), NewDelete(db).HTTP)
    rest.Register(api, route(requireUser, "article-publish", http.MethodPost, "/articles/{id}/publish", "发布草稿"), NewPublish(db).HTTP)
}
```

别人的文章返回 `403`：

```json
{"title":"Forbidden","status":403,"detail":"只有作者能修改这篇文章","code":"article.forbidden"}
```

<a id="frontend"></a>
## 前端

首页 `frontend/src/ArticleList.tsx` 只列出已发布的文章，点标题阅读全文：

```tsx title="frontend/src/ArticleList.tsx"
import { useQuery } from "@tanstack/react-query"
import type { Article } from "./api/api"
import { api } from "./client"

// ArticleList 是首页：已发布的文章，所有人都能看到。
export function ArticleList({ onOpen }: { onOpen: (a: Article) => void }) {
  const articles = useQuery({ queryKey: ["article", "list"], queryFn: () => api.article.list() })

  if (articles.isPending) return <p>加载中…</p>
  if (articles.error) return <p className="error">{articles.error.message}</p>
  if (articles.data.items.length === 0) return <p>还没有发布的文章。</p>
  return (
    <ul className="articles">
      {articles.data.items.map((a) => (
        <li key={a.id}>
          <h2>
            <a onClick={() => onOpen(a)}>{a.title}</a>
          </h2>
          <time>{new Date(a.published_at!).toLocaleDateString()}</time>
          <p>{a.content.slice(0, 120)}</p>
        </li>
      ))}
    </ul>
  )
}
```

接着，新建 `frontend/src/ArticleView.tsx` 显示一篇文章：

```tsx title="frontend/src/ArticleView.tsx"
import type { Article } from "./api/api"

export function ArticleView({ article }: { article: Article }) {
  return (
    <article>
      <h2>{article.title}</h2>
      {article.published_at && <time>{new Date(article.published_at).toLocaleString()}</time>}
      <p className="content">{article.content}</p>
    </article>
  )
}
```

接着，新建 `frontend/src/MyArticles.tsx`，作者在这里编辑、发布、删除：

```tsx title="frontend/src/MyArticles.tsx"
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import type { Article } from "./api/api"
import { api } from "./client"

// MyArticles 是当前作者的全部文章，包括草稿：编辑、发布、删除。
export function MyArticles({ onEdit }: { onEdit: (a: Article) => void }) {
  const queryClient = useQueryClient()
  const articles = useQuery({ queryKey: ["article", "mine"], queryFn: () => api.article.mine() })
  // 修改后让所有 ["article", …] 查询失效：我的文章和首页都会重新获取
  const refresh = () => queryClient.invalidateQueries({ queryKey: ["article"] })
  const publish = useMutation({ mutationFn: (a: Article) => api.article.publish(a.id), onSuccess: refresh })
  const remove = useMutation({ mutationFn: (a: Article) => api.article.delete(a.id), onSuccess: refresh })

  if (articles.isPending) return <p>加载中…</p>
  if (articles.error) return <p className="error">{articles.error.message}</p>
  if (articles.data.items.length === 0) return <p>你还没有写过文章。</p>
  return (
    <ul className="articles">
      {articles.data.items.map((a) => (
        <li key={a.id}>
          <h2>{a.title}</h2>
          <p>{a.published_at ? `发布于 ${new Date(a.published_at).toLocaleString()}` : "草稿"}</p>
          <button onClick={() => onEdit(a)}>编辑</button>
          {!a.published_at && <button onClick={() => publish.mutate(a)}>发布</button>}
          <button onClick={() => confirm(`删除“${a.title}”？`) && remove.mutate(a)}>删除</button>
        </li>
      ))}
    </ul>
  )
}
```

在 `frontend/src/App.tsx` 中加上阅读和“我的文章”：

```tsx title="frontend/src/App.tsx"
import { useState } from "react"
import { useMutation } from "@tanstack/react-query"
import type { Article } from "./api/api"
import { ArticleEditor } from "./ArticleEditor"
import { ArticleList } from "./ArticleList"
import { ArticleView } from "./ArticleView"
import { api, queryClient } from "./client"
import { Login } from "./Login"
import { MyArticles } from "./MyArticles"
import { useMe } from "./session"

// 页面上同时只显示一样东西。
type View =
  | { name: "home" }
  | { name: "read"; article: Article }
  | { name: "mine" }
  | { name: "edit"; article?: Article }
  | { name: "login" }

export function App() {
  const me = useMe()
  const [view, setView] = useState<View>({ name: "home" })
  const logout = useMutation({
    mutationFn: () => api.auth.logout(),
    onSettled: () => {
      api.setAccessToken("")
      queryClient.setQueryData(["me"], null)
      setView({ name: "home" })
    },
  })

  return (
    <main>
      <header>
        <h1 onClick={() => setView({ name: "home" })}>博客</h1>
        {me.data && (
          <nav>
            {me.data.name}
            <button onClick={() => setView({ name: "mine" })}>我的文章</button>
            <button onClick={() => setView({ name: "edit" })}>写文章</button>
            <button onClick={() => logout.mutate()}>退出</button>
          </nav>
        )}
        {me.data === null && view.name !== "login" && (
          <button onClick={() => setView({ name: "login" })}>登录</button>
        )}
      </header>
      {view.name === "home" && <ArticleList onOpen={(article) => setView({ name: "read", article })} />}
      {view.name === "read" && <ArticleView article={view.article} />}
      {view.name === "mine" && <MyArticles onEdit={(article) => setView({ name: "edit", article })} />}
      {view.name === "edit" && <ArticleEditor article={view.article} onDone={() => setView({ name: "mine" })} />}
      {view.name === "login" && <Login onDone={() => setView({ name: "home" })} />}
    </main>
  )
}
```

`frontend/src/app.css` 加几条样式：

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

h1,
.articles a {
  cursor: pointer;
}

nav {
  display: flex;
  align-items: center;
  gap: 0.5rem;
}

time {
  color: #888;
}

.content {
  white-space: pre-wrap;
}
```

登录后写一篇文章，它出现在“我的文章”中，标着草稿，首页还看不到。点“发布”，回到首页就有了。再用 `user create` 创建第二个作者，登录后它看不到第一个作者的草稿，也改不了第一个作者的文章。

恭喜你完成了入门教程！回顾一下，你已经用到了一个常规项目需要的大部分东西：配置与装配、数据库与迁移、接口与校验、业务错误与本地化、React 页面与生成的客户端、登录与授权。

接下来，你可以按需阅读各个参考页：例如用[缓存](../libraries/cache.md)加快首页，用[队列](../libraries/queue.md)在发布后给订阅者发邮件，用[定时任务](../libraries/scheduling.md)定时发布文章，最后参考[部署](../installation/deployment.md)把博客上线。

深入了解：[授权](../security/authorization.md)、[数据库事务](../database/quick-start.md#transactions)。
