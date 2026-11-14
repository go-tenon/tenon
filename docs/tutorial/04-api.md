# 4. 文章接口

有了文章表，现在可以写接口了。这一章，我们为文章写五个接口：列表、读取、新建、修改和删除。在这个过程中，你会写出第一个业务动作，并看到 Tenon 怎样验证输入、返回业务错误。

- [模型](#model)
- [动作](#actions)
- [注册路由](#routes)
- [错误提示](#messages)
- [试一试](#trying)

<a id="model"></a>
## 模型

一个业务模块是 `internal` 下的一个包。新建 `internal/article/article.go`，放各个动作共用的东西：

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
)

// Article 是一篇文章。bun 标签对应 articles 表的列，json 标签是接口返回的字段。
type Article struct {
    bun.BaseModel `bun:"table:articles"`

    ID        uuid.UUID `bun:"id,pk" json:"id"`
    Title     string    `bun:"title,notnull" json:"title"`
    Slug      string    `bun:"slug,notnull" json:"slug"`
    Content   string    `bun:"content,notnull" json:"content"`
    CreatedAt time.Time `bun:"created_at,notnull" json:"created_at"`
    UpdatedAt time.Time `bun:"updated_at,notnull" json:"updated_at"`
}

// 业务错误：错误码返回给客户端，前端按它显示提示。
var (
    ErrNotFound  = errorx.Define(errorx.NotFound, "article.not_found")
    ErrSlugTaken = errorx.Define(errorx.Conflict, "article.slug_taken")
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

- `Article` 的 `bun` 标签对应数据表的列，`json` 标签是接口返回的字段，同一个结构体两边都用。
- 业务错误用 `errorx.Define` 定义：第一个参数是分类，决定 HTTP 状态码（`NotFound` 是 404，`Conflict` 是 409）；第二个参数是错误码，客户端按它判断发生了什么。
- `Changes` 是提交的内容。字段上的标签就是校验规则，不符合时接口返回 `422`，列出每个字段的问题，处理函数不会被调用。

<a id="actions"></a>
## 动作

每个动作一个文件：一个结构体保存它用到的依赖，`HTTP` 方法处理请求。新建 `internal/article/list.go`：

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

`page.OffsetParams` 让接口接受 `?page=2&per_page=20`，`page.Offset` 是带总数的一页。

`internal/article/get.go`：

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

`internal/article/create.go`：

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
)

// Create 新建文章。
type Create struct{ db *bun.DB }

func NewCreate(db *bun.DB) *Create { return &Create{db: db} }

// Handle 新建一篇文章，slug 已被使用时返回 ErrSlugTaken。
func (a *Create) Handle(ctx context.Context, c Changes) (*Article, error) {
    now := time.Now().UTC()
    item := &Article{
        ID: uuid.Must(uuid.NewV7()), Title: c.Title, Slug: c.Slug, Content: c.Content,
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
    item, err := a.Handle(ctx, in.Body)
    if err != nil {
        return nil, err
    }
    return &ArticleOutput{Body: item}, nil
}
```

新建和修改都可能遇到 slug 重复。不必先查一次再插入，那样并发时仍然可能重复：直接插入，由唯一约束拒绝，再把约束冲突转换为业务错误 `ErrSlugTaken`。`errorx.Public` 附加的参数会出现在给用户看的提示中。

`Create` 把业务逻辑放在 `Handle` 中，`HTTP` 只负责从请求中取出参数、把结果包装成响应。以后的命令行、队列任务和测试都直接调用 `Handle`。

`internal/article/update.go`：

```go title="internal/article/update.go"
package article

import (
    "context"
    "time"

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
    item.Title, item.Slug, item.Content, item.UpdatedAt = c.Title, c.Slug, c.Content, time.Now().UTC()
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

`internal/article/delete.go`：

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

让我们新建 `internal/article/routes.go`，把各个动作注册为接口：

```go title="internal/article/routes.go"
package article

import (
    "net/http"

    "github.com/danielgtaylor/huma/v2"
    "github.com/uptrace/bun"

    "github.com/go-tenon/tenon/httpx/rest"
)

// Register 注册文章的接口。db 为 nil 时只登记路由（生成文档时）。
func Register(api *rest.API, db *bun.DB) {
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

`OperationID` 是接口的名字，生成的客户端按它分组：`article-list` 就是前端的 `api.article.list()`。每个接口都要声明访问策略，现在都是公开的 `rest.Public()`；忘记声明时应用启动就会报错，不会意外地发布一个没有保护的接口。第 6 章加上登录后，写文章的接口改为需要登录。

<a id="messages"></a>
## 错误提示

错误响应中的 `detail` 是给用户看的提示，按错误码从翻译文件中查找。新建 `locales/embed.go` 和 `locales/zh-CN.yaml`：

```go title="locales/embed.go"
// Package locales 嵌入应用的翻译：业务错误码对应给用户看的提示。
package locales

import "embed"

//go:embed *.yaml
var FS embed.FS
```

```yaml title="locales/zh-CN.yaml"
article.not_found: 文章不存在
article.slug_taken: "slug“{{.slug}}”已被使用"
```

最后把 `internal/bootstrap/http.go` 中的待办接口换成文章，并加载翻译：

```go title="internal/bootstrap/http.go"
package bootstrap

import (
    "context"
    "net/http"

    "github.com/go-chi/chi/v5"

    "github.com/go-tenon/tenon/command"
    "github.com/go-tenon/tenon/httpx"
    "github.com/go-tenon/tenon/httpx/rest"
    "github.com/go-tenon/tenon/i18n"

    "example.com/blog/frontend"
    "example.com/blog/internal/article"
    "example.com/blog/locales"
)

// API 是 JSON API 的选项，挂在 /api 下。serve、openapi、client 命令共用它。
var API = rest.Options{Title: "Blog API", Version: "1.0.0", BasePath: "/api"}

// Routes 注册全部接口，serve 和生成文档共用它。
func Routes(api *rest.API, a *App) {
    article.Register(api, a.db)
}

// DocRoutes 以文档模式注册全部接口，openapi 和 client 命令用它。文档模式只登记路由，
// 不调用处理器，所以用一个没有打开任何资源的 App。
func DocRoutes(api *rest.API) { Routes(api, &App{}) }

// DocRouter 在文档模式下构造全部路由，routes 命令用它。
func DocRouter() chi.Routes {
    router := chi.NewRouter()
    router.Route("/api", func(r chi.Router) {
        opts := API
        opts.Mode, opts.Router = rest.ModeOpenAPI, r
        DocRoutes(rest.New(opts))
    })
    return router
}

// HTTP 装配 HTTP 服务：健康检查、/api 下的接口和单页应用。
func (a *App) HTTP(d *command.Daemon) (func(context.Context) error, error) {
    spa, err := httpx.NewSPA(frontend.Dist, httpx.SPAOptions{DevServer: a.cfg.FrontendDevServer})
    if err != nil {
        return nil, err
    }
    languages, err := i18n.New("zh-CN", locales.FS)
    if err != nil {
        return nil, err
    }
    var draining httpx.Draining
    router := chi.NewRouter()
    router.Use(httpx.Defaults(a.cfg.HTTP, nil, languages)...)
    router.Get("/livez", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
    router.Handle("/readyz", httpx.Ready(httpx.ReadyOptions{
        Draining: &draining,
        Checks:   []httpx.Check{{Name: "database", Ping: a.db.PingContext}},
    }))
    router.Route("/api", func(r chi.Router) {
        r.Use(httpx.Timeout(a.cfg.HTTP.Timeout))
        opts := API
        opts.Router = r
        Routes(rest.New(opts), a)
    })
    router.Handle("/*", spa)

    ln, err := d.Listen("tcp", a.cfg.HTTP.Addr)
    if err != nil {
        return nil, err
    }
    return func(ctx context.Context) error {
        return httpx.Serve(ctx, a.cfg.HTTP, ln, router, httpx.ServeOptions{OnDraining: draining.Set})
    }, nil
}
```

删除模板的示例 `internal/todo`。

<a id="trying"></a>
## 试一试

`bun run dev` 运行着的话，应用已经重新编译了。打开 http://localhost:8080/api/docs，或者用 curl：

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

到这里，文章的五个接口都可以用了：提交的数据会被验证，找不到文章时返回带错误码的 `404`，错误提示是中文的。

不过，首页还在调用已经删除的待办接口。下一章，我们把它换成文章页面。

深入了解：[路由](../http/routing.md)、[验证](../http/validation.md)、[响应](../responses/responses.md)、[错误处理](../general/errors.md)、[业务动作](../concepts/actions.md)、[本地化](../general/localization.md)。
