# 4. 文章接口

这一章为文章写五个接口：列表、读取、新建、修改、删除，并用它们替换模板的待办接口。业务逻辑写成[业务动作](../concepts/actions.md)，HTTP 接口只是调用它的一种方式。

<a id="model"></a>
## 模型

文章是 `internal` 下的一个包 `internal/article`。新建 `internal/article/article.go`，放模型、业务错误、提交的内容和按 ID 读取文章的函数：

```go title="internal/article/article.go"
// Package article 是博客的文章：模型、业务错误和各个动作，每个动作一个文件。
package article

import (
    "context"
    "database/sql"

    "github.com/cockroachdb/errors"
    "github.com/google/uuid"
    "github.com/uptrace/bun"

    "github.com/go-tenon/tenon/database"
    "github.com/go-tenon/tenon/errorx"
)

// Article 是一篇文章。bun 标签对应 articles 表的列，json 标签是接口返回的字段。
type Article struct {
    bun.BaseModel `bun:"table:articles"`

    ID uuid.UUID `bun:"id,pk" json:"id"`
    database.Timestamps
    Title   string `bun:"title,notnull" json:"title"`
    Slug    string `bun:"slug,notnull" json:"slug"`
    Content string `bun:"content,notnull" json:"content"`
}

// 业务错误：错误码返回给客户端，第三个参数是给用户看的默认提示模板。
var (
    ErrNotFound  = errorx.Define(errorx.NotFound, "article.not_found", "文章不存在")
    ErrSlugTaken = errorx.Define(errorx.Conflict, "article.slug_taken", "slug“{{.slug}}”已被使用")
)

// Changes 是新建和修改文章时提交的内容，标签就是校验规则。
type Changes struct {
    Title   string `json:"title" minLength:"1" maxLength:"200"`
    Slug    string `json:"slug" minLength:"1" maxLength:"100" pattern:"^[a-z0-9]+(-[a-z0-9]+)*$" patternDescription:"小写字母、数字和连字符"`
    Content string `json:"content" maxLength:"100000"`
}

// find 按 ID 读取文章，db 可以是连接池，也可以是事务。
func find(ctx context.Context, db bun.IDB, id uuid.UUID) (*Article, error) {
    item := new(Article)
    err := db.NewSelect().Model(item).Where("id = ?", id).Scan(ctx)
    if errors.Is(err, sql.ErrNoRows) {
        return nil, ErrNotFound.New()
    }
    return item, errors.Wrap(err, "load article")
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

- `Article` 既是 [Bun](https://bun.uptrace.dev) 读写 `articles` 表的模型，也直接作为接口返回的 JSON。嵌入的 `database.Timestamps` 是 `created_at`、`updated_at` 两列，插入和修改时由 Bun 的钩子填上时间，见[自动维护时间戳](../database/database.md#timestamps)。
- `errorx.Define` 的第一个参数决定 HTTP 状态码（`NotFound` 是 404，`Conflict` 是 409），第二个是返回给客户端的错误码，见[错误处理](../general/errors.md)。
- `Changes` 的标签是校验规则。不符合时接口返回 `422`，列出每个字段的问题，动作不会被调用，见[请求与校验](../http/requests.md)。
- `find` 把 `sql.ErrNoRows` 转换为 `ErrNotFound`。参数是 `bun.IDB`，连接池和事务都能传入。
- `path:"id"` 表示 `ID` 来自路径中的 `{id}`，不是合法的 UUID 时请求直接被拒绝。

<a id="actions"></a>
## 动作

每个动作一个文件：结构体保存依赖，构造函数传入依赖，`HTTP` 方法处理请求，见[依赖装配](../concepts/dependencies.md)。新建 `internal/article/list.go`：

```go title="internal/article/list.go"
package article

import (
    "context"

    "github.com/uptrace/bun"

    "github.com/go-tenon/tenon/page"
)

// List 按创建时间倒序分页列出文章。
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
        OrderExpr("created_at DESC, id DESC").
        Limit(in.Limit()).Offset(in.Offset()).
        ScanAndCount(ctx)
    if err != nil {
        return nil, err
    }
    return &ListOutput{Body: page.NewOffset(items, in.OffsetParams, int64(total))}, nil
}
```

- 嵌入 `page.OffsetParams` 后接口接受 `?page=2&per_page=20`，默认第 1 页、每页 20 条。
- `ScanAndCount` 一次读出这一页和总数，`page.Offset[Article]` 返回 `items` 和 `total`。
- 排序加上 `id DESC`，创建时间相同时分页顺序也是稳定的。

新建 `internal/article/get.go`：

```go title="internal/article/get.go"
package article

import (
    "context"

    "github.com/uptrace/bun"
)

// Get 读取一篇文章。
type Get struct{ db *bun.DB }

func NewGet(db *bun.DB) *Get { return &Get{db: db} }

func (a *Get) HTTP(ctx context.Context, in *IDInput) (*ArticleOutput, error) {
    item, err := find(ctx, a.db, in.ID)
    if err != nil {
        return nil, err
    }
    return &ArticleOutput{Body: item}, nil
}
```

新建 `internal/article/create.go`：

```go title="internal/article/create.go"
package article

import (
    "context"

    "github.com/cockroachdb/errors"
    "github.com/google/uuid"
    "github.com/uptrace/bun"

    "github.com/go-tenon/tenon/database"
    "github.com/go-tenon/tenon/errorx"
)

// Create 新建文章。
type Create struct{ db *bun.DB }

func NewCreate(db *bun.DB) *Create { return &Create{db: db} }

// Handle 新建一篇文章，slug 已被使用时返回 ErrSlugTaken。
func (a *Create) Handle(ctx context.Context, c Changes) (*Article, error) {
    item := &Article{ID: uuid.Must(uuid.NewV7()), Title: c.Title, Slug: c.Slug, Content: c.Content}
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
    item, err := a.Handle(ctx, in.Body)
    if err != nil {
        return nil, err
    }
    return &ArticleOutput{Body: item}, nil
}
```

- slug 重复由第 3 章的唯一约束拒绝：先查询再插入的做法在两个请求同时提交时会都插入成功。`database.IsUniqueViolation` 识别约束冲突，转换为 `ErrSlugTaken`。`errorx.Public` 附加的 `slug` 出现在给用户看的提示中。
- 业务逻辑写在 `Handle` 中，参数和返回值与 HTTP 无关，命令行、后台任务和测试直接调用它，第 6 章的命令行建号就这样做。`HTTP` 只取出参数、调用 `Handle`、包装响应。

新建 `internal/article/update.go`：

```go title="internal/article/update.go"
package article

import (
    "context"

    "github.com/cockroachdb/errors"
    "github.com/google/uuid"
    "github.com/uptrace/bun"

    "github.com/go-tenon/tenon/database"
    "github.com/go-tenon/tenon/errorx"
)

// Update 修改文章。
type Update struct{ db *bun.DB }

func NewUpdate(db *bun.DB) *Update { return &Update{db: db} }

func (a *Update) Handle(ctx context.Context, id uuid.UUID, c Changes) (*Article, error) {
    item, err := find(ctx, a.db, id)
    if err != nil {
        return nil, err
    }
    item.Title, item.Slug, item.Content = c.Title, c.Slug, c.Content
    _, err = a.db.NewUpdate().Model(item).Column("title", "slug", "content", "updated_at").WherePK().Exec(ctx)
    if database.IsUniqueViolation(err, "articles_slug_key") {
        return nil, ErrSlugTaken.Wrap(err, errorx.Public("slug", c.Slug))
    }
    return item, errors.Wrap(err, "update article")
}

type UpdateInput struct {
    ID   uuid.UUID `path:"id"`
    Body Changes
}

func (a *Update) HTTP(ctx context.Context, in *UpdateInput) (*ArticleOutput, error) {
    item, err := a.Handle(ctx, in.ID, in.Body)
    if err != nil {
        return nil, err
    }
    return &ArticleOutput{Body: item}, nil
}
```

`Column` 列出要更新的列，其中要有 `updated_at`：钩子改了 `item.UpdatedAt`，列在这里才会写进数据库。

新建 `internal/article/delete.go`，文章不存在时返回 404：

```go title="internal/article/delete.go"
package article

import (
    "context"

    "github.com/uptrace/bun"
)

// Delete 删除文章。
type Delete struct{ db *bun.DB }

func NewDelete(db *bun.DB) *Delete { return &Delete{db: db} }

func (a *Delete) HTTP(ctx context.Context, in *IDInput) (*struct{}, error) {
    res, err := a.db.NewDelete().Model((*Article)(nil)).Where("id = ?", in.ID).Exec(ctx)
    if err != nil {
        return nil, err
    }
    if n, _ := res.RowsAffected(); n == 0 {
        return nil, ErrNotFound.New()
    }
    return nil, nil
}
```

<a id="routes"></a>
## 注册路由

新建 `internal/article/routes.go`：

```go title="internal/article/routes.go"
package article

import (
    "net/http"

    "github.com/danielgtaylor/huma/v2"
    "github.com/uptrace/bun"

    "github.com/go-tenon/tenon/httpx/rest"
)

// RegisterRoutes 注册文章的接口。db 为 nil 时只登记路由（生成文档时）。
func RegisterRoutes(api *rest.API, db *bun.DB) {
    route := func(id, method, path, summary string) huma.Operation {
        op := huma.Operation{OperationID: id, Method: method, Path: path, Summary: summary}
        rest.Public()(&op) // 第 6 章加上登录后改为需要认证
        return op
    }
    create := route("article-create", http.MethodPost, "/articles", "新建文章")
    create.DefaultStatus = http.StatusCreated

    rest.Register(api, route("article-list", http.MethodGet, "/articles", "文章列表"), NewList(db).HTTP)
    rest.Register(api, route("article-get", http.MethodGet, "/articles/{id}", "读取文章"), NewGet(db).HTTP)
    rest.Register(api, create, NewCreate(db).HTTP)
    rest.Register(api, route("article-update", http.MethodPut, "/articles/{id}", "修改文章"), NewUpdate(db).HTTP)
    rest.Register(api, route("article-delete", http.MethodDelete, "/articles/{id}", "删除文章"), NewDelete(db).HTTP)
}
```

- `OperationID` 决定生成的前端客户端中的名字：`article-list` 是 `api.article.list()`。
- 每个接口都要声明访问策略，忘记声明时程序启动就报错。现在都是 `rest.Public()`，第 6 章改为需要登录。

<a id="messages"></a>
## 接入文章路由

响应中的 `detail` 自动使用 `errorx.Define` 中写的默认提示，`{{.slug}}` 替换为 `errorx.Public` 附加的参数，见[本地化](../general/localization.md)。

修改 `internal/bootstrap/http.go`，把待办接口换成文章：

```go title="internal/bootstrap/http.go"
package bootstrap

import (
    "context"
    "net/http"

    "github.com/go-chi/chi/v5"

    "github.com/go-tenon/tenon/command"
    "github.com/go-tenon/tenon/config"
    "github.com/go-tenon/tenon/frontend"
    "github.com/go-tenon/tenon/health"
    "github.com/go-tenon/tenon/httpx"
    "github.com/go-tenon/tenon/httpx/rest"

    "example.com/blog/apps"
    "example.com/blog/internal/article"
)

// HTTP 装配 HTTP 服务。apps 中的前端由 frontend.Host 按挂载路径提供：服务端渲染的前端由
// Go 托管的 SSR 进程渲染，它们渲染时经内部 socket 调用同一组路由。
func (a *App) HTTP(d *command.Daemon) (func(context.Context) error, error) {
    var router chi.Router
    // router 在 pages 创建之后才构造好，SSR 进程调用接口时经这个函数转给它。
    api := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { router.ServeHTTP(w, r) })
    pages, err := frontend.New(apps.Dist, frontend.Options{API: api, DevServers: a.cfg.DevServers})
    if err != nil {
        return nil, err
    }
    router = a.Router(pages.Handler())
    ln, err := d.Listen("tcp", a.cfg.HTTP.Addr)
    if err != nil {
        return nil, err
    }
    return func(ctx context.Context) error {
        return pages.Run(ctx, func(ctx context.Context) error {
            return httpx.Serve(ctx, a.cfg.HTTP, ln, router)
        })
    }, nil
}

// Router 构造应用的全部路由：健康检查、/api 下的接口和页面。
// 它只保存 App 的字段、不使用它们，所以 DocRouter 可以用一个没有打开任何资源的 App 调用它。
func (a *App) Router(pages http.Handler) chi.Router {
    router := chi.NewRouter()
    router.Use(httpx.Defaults(a.cfg.HTTP)...)
    router.Get("/livez", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
    router.Handle("/readyz", httpx.Ready(httpx.ReadyOptions{
        Checks: []health.Check{
            {Name: "database", Ping: func(ctx context.Context) error { return a.db.PingContext(ctx) }},
        },
    }))
    router.Route("/api", func(r chi.Router) {
        r.Use(httpx.Timeout(a.cfg.HTTP.Timeout))
        api := rest.New(r, rest.Options{Title: "Blog API", Version: "1.0.0", BasePath: "/api"})
        article.RegisterRoutes(api, a.db)
    })
    if pages != nil {
        router.Handle("/*", pages)
    }
    return router
}

// DocRouter 用默认配置和没有打开任何资源的 App 构造路由，openapi、routes、client 命令用它。
func DocRouter() chi.Routes {
    return (&App{cfg: config.Default[Config]()}).Router(nil)
}

```

删除模板的示例 `internal/todo`。

<a id="trying"></a>
## 试一试

`bun run dev` 运行着时应用已经重新编译。打开 <http://localhost:8080/api/docs>，或者用 curl：

```bash
curl -X POST localhost:8080/api/articles -H 'Content-Type: application/json' \
  -d '{"title": "你好", "slug": "hello", "content": "第一篇文章"}'
```

```json
{"id":"01a1d366-5a12-74f4-9d93-f37c3900aa79","title":"你好","slug":"hello","content":"第一篇文章","created_at":"…","updated_at":"…"}
```

同样的 slug 再提交一次，返回 `409`：

```json
{"title":"Conflict","status":409,"detail":"slug“hello”已被使用","code":"article.slug_taken"}
```

不合法的内容返回 `422`，每个字段一条提示：

```json
{"title":"Unprocessable Entity","status":422,"detail":"提交的内容有误","errors":[
  {"message":"必须是小写字母、数字和连字符","location":"body.slug","value":"Bad Slug","code":"validation.pattern_description"},
  {"message":"不能为空","location":"body.title","value":"","code":"validation.filled"}]}
```

首页还在调用已删除的待办接口，第 5 章换成文章页面。
