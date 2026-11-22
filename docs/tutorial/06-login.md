# 6. 作者登录

作者用邮箱和密码登录，登录之后才能写文章。这一章是单账号后台登录的完整例子：作者表、建号命令、登录和退出接口、登录页。

包：`login`、`apitoken`、`httpx`、`httpx/rest`、`command`、`@go-tenon/client`、`@go-tenon/ui`。

<a id="how-it-works"></a>
## 怎么登录

- `login` 在框架的 `auth_*` 表中保存邮箱和密码哈希，校验密码，限制输错的次数；应用的 `users` 表只有业务字段。
- 密码正确后 `apitoken` 签发 token，登录接口把它写进 `HttpOnly` 的会话 cookie，浏览器之后的请求自动带上它，页面上的代码不接触 token。用 cookie 认证的修改请求必须来自博客自己的页面，否则返回 `403`。

<a id="configuration"></a>
## 配置

`login` 用应用密钥签名验证码等数据。生成密钥写进 `.env`（不提交，生产环境用环境变量 `APP_KEY`）：

```bash
echo "APP_KEY=$(openssl rand -base64 32)" >> .env
```

在 `config/app.yaml` 中加上密钥：

```yaml title="config/app.yaml"
# 应用的配置。${VAR:-默认值} 读取环境变量，生产环境用环境变量覆盖开发时的默认值。
http:
  addr: ":8080"

database:
  dsn: ${DATABASE_URL:-postgres://blog:blog@localhost:5432/blog?sslmode=disable}

crypt:
  key: ${APP_KEY}
```

`internal/bootstrap/config.go` 中加上对应的配置段，`hashing` 和 `token` 用默认值：

```go title="internal/bootstrap/config.go"
// Package bootstrap 装配应用：读取配置，打开数据库等资源，把它们交给 HTTP 服务和各个业务模块。
package bootstrap

import (
    "github.com/go-tenon/tenon/apitoken"
    "github.com/go-tenon/tenon/crypt"
    "github.com/go-tenon/tenon/database"
    "github.com/go-tenon/tenon/hashing"
    "github.com/go-tenon/tenon/httpx"
)

// Config 是应用的配置：config/app.yaml。
type Config struct {
    HTTP       httpx.Config      `yaml:"http"`
    Database   database.Config   `yaml:"database"`
    Crypt      crypt.Config      `yaml:"crypt"` // 应用密钥，login 用它签名验证码等数据
    Hashing    hashing.Config    `yaml:"hashing"`
    Token      apitoken.Config   `yaml:"token"`
    DevServers map[string]string `yaml:"dev_servers"` // 各前端开发服务器的地址，只在 tenon dev 时有值
}
```

<a id="tables"></a>
## 数据表

`login`、`apitoken` 和输错次数计数（`ratelimit/pglimit`）的迁移由框架提供，`migrate publish` 把它们复制到 `migrations` 中，和自己的迁移一样提交、执行。在 `cmd/blog/main.go` 中加上 `publish` 子命令和 `bootstrap.Commands()`：

```go title="cmd/blog/main.go"
// Command blog 是博客的入口。
package main

import (
    "github.com/spf13/cobra"

    "github.com/go-tenon/tenon/apitoken"
    "github.com/go-tenon/tenon/command"
    "github.com/go-tenon/tenon/command/httpcmd"
    "github.com/go-tenon/tenon/command/migratecmd"
    "github.com/go-tenon/tenon/database"
    "github.com/go-tenon/tenon/httpx"
    "github.com/go-tenon/tenon/login"
    "github.com/go-tenon/tenon/ratelimit/pglimit"

    "example.com/blog/internal/bootstrap"
    "example.com/blog/migrations"
)

func main() {
    root := command.Root("blog", "博客")
    root.AddCommand(
        command.Serve(bootstrap.Open, bootstrap.Parts),
        command.Config[bootstrap.Config](),
        httpcmd.Health(func(cfg bootstrap.Config) httpx.Config { return cfg.HTTP }),
        migrateCommand(),
    )
    // 需要数据库的命令，例如 user create
    root.AddCommand(bootstrap.Commands()...)
    // openapi、routes、client：导出接口文档、列出路由、生成客户端，go tool tenon 调用它们。
    root.AddCommand(httpcmd.Commands(bootstrap.DocRouter)...)
    command.Execute(root)
}

// migrateCommand 返回 migrate 命令：up、status、down、make，以及发布框架自带的迁移的 publish。
func migrateCommand() *cobra.Command {
    cmd := migratecmd.New(migrations.FS, func(cfg bootstrap.Config) database.Config { return cfg.Database })
    cmd.AddCommand(migratecmd.Publish(
        login.Migrations(),
        apitoken.Migrations(),
        pglimit.Migrations(),
    ))
    return cmd
}
```

```bash
go mod tidy
go run ./cmd/blog migrate publish
```

再为作者建一张表：

```bash
go run ./cmd/blog migrate make create_users_table
```

```sql title="migrations/2026_11_13_080009_create_users_table.sql"
-- +goose Up
CREATE TABLE users (
    id         uuid PRIMARY KEY,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    name       text NOT NULL
);
COMMENT ON TABLE users IS '博客作者账号';
COMMENT ON COLUMN users.id IS '主键';
COMMENT ON COLUMN users.created_at IS '创建时间';
COMMENT ON COLUMN users.updated_at IS '更新时间';
COMMENT ON COLUMN users.name IS '作者姓名';

-- +goose Down
DROP TABLE users;
```

<a id="users"></a>
## 作者账号

新建 `internal/user/user.go`。`Type` 是这套账号的类型，`Ref` 是作者交给 `login`、`apitoken` 时用的引用：

```go title="internal/user/user.go"
// Package user 是博客的作者账号。邮箱和密码保存在 login 的表中，users 表只有业务字段。
package user

import (
    "context"
    "database/sql"

    "github.com/cockroachdb/errors"
    "github.com/google/uuid"
    "github.com/uptrace/bun"

    "github.com/go-tenon/tenon/database"
    "github.com/go-tenon/tenon/errorx"
    "github.com/go-tenon/tenon/identity"
)

// Type 是作者这套账号体系，登录凭据中的主体都是这个类型。
const Type identity.SubjectType = "user"

var ErrNotFound = errorx.Define(errorx.NotFound, "user.not_found", "作者不存在")

// User 是一个作者。
type User struct {
    bun.BaseModel `bun:"table:users"`

    ID string `bun:"id,pk" json:"id"`
    database.Timestamps
    Name string `bun:"name,notnull" json:"name"`
}

// Ref 是这个作者在 login、apitoken 中的引用。
func (u *User) Ref() identity.Ref { return identity.Ref{Type: Type, ID: u.ID} }

func newID() string { return uuid.Must(uuid.NewV7()).String() }

func find(ctx context.Context, db bun.IDB, id string) (*User, error) {
    u := new(User)
    err := db.NewSelect().Model(u).Where("id = ?", id).Scan(ctx)
    if errors.Is(err, sql.ErrNoRows) {
        return nil, ErrNotFound.New()
    }
    return u, errors.Wrap(err, "load user")
}
```

博客不开放注册，作者用命令创建。新建 `internal/user/create.go`，`Handle` 在一个事务中写入 `users` 表和 `login` 的表，失败时整体回滚：

```go title="internal/user/create.go"
package user

import (
    "context"
    "fmt"

    "github.com/spf13/cobra"
    "github.com/uptrace/bun"

    "github.com/go-tenon/tenon/database"
    "github.com/go-tenon/tenon/login"
)

// Create 创建作者账号：users 表中的一行，加上登录用的邮箱和密码。
type Create struct {
    db     *bun.DB
    logins *login.Realm
}

func NewCreate(db *bun.DB, logins *login.Realm) *Create { return &Create{db: db, logins: logins} }

// Handle 在一个事务中创建账号、写入邮箱和密码，任何一步失败时整体回滚。
// 邮箱已被使用时返回 login.ErrTaken，密码不符合要求时返回 login.ErrWeakPassword。
func (a *Create) Handle(ctx context.Context, name, email, password string) (*User, error) {
    u := &User{ID: newID(), Name: name}
    err := database.RunInTx(ctx, a.db, nil, func(ctx context.Context, tx database.Tx) error {
        if _, err := tx.NewInsert().Model(u).Exec(ctx); err != nil {
            return err
        }
        // 管理员建号，邮箱直接标为已验证
        if err := a.logins.Attach(ctx, tx, u.Ref(), login.Email(email), true); err != nil {
            return err
        }
        return a.logins.SetPassword(ctx, tx, u.Ref(), password)
    })
    if err != nil {
        return nil, err
    }
    return u, nil
}

// Command 处理 user create --name 名字 --email 邮箱 --password 密码。
func (a *Create) Command(cmd *cobra.Command, _ []string) error {
    name, _ := cmd.Flags().GetString("name")
    email, _ := cmd.Flags().GetString("email")
    password, _ := cmd.Flags().GetString("password")
    u, err := a.Handle(cmd.Context(), name, email, password)
    if err != nil {
        return err
    }
    _, err = fmt.Fprintln(cmd.OutOrStdout(), "已创建作者", u.Name, u.ID)
    return err
}
```

新建 `internal/user/commands.go` 声明命令：

```go title="internal/user/commands.go"
package user

import (
    "github.com/spf13/cobra"
    "github.com/uptrace/bun"

    "github.com/go-tenon/tenon/command"
    "github.com/go-tenon/tenon/login"
)

// Commands 声明 user 模块的命令。
func Commands(db *bun.DB, logins *login.Realm) *cobra.Command {
    group := &cobra.Command{Use: "user", Short: "管理作者账号"}
    create := &cobra.Command{
        Use:   "create",
        Short: "创建作者",
        Args:  command.NoArgs,
        RunE:  NewCreate(db, logins).Command,
    }
    create.Flags().String("name", "", "名字")
    create.Flags().String("email", "", "登录用的邮箱")
    create.Flags().String("password", "", "密码，至少 8 个字符")
    for _, f := range []string{"name", "email", "password"} {
        _ = create.MarkFlagRequired(f)
    }
    group.AddCommand(create)
    return group
}
```

`command.Bind` 先用零值的 `App` 声明命令，执行其中一个时才加载配置、调用 `Open`，结束后关闭资源。新建 `internal/bootstrap/commands.go`：

```go title="internal/bootstrap/commands.go"
package bootstrap

import (
    "github.com/spf13/cobra"

    "github.com/go-tenon/tenon/command"

    "example.com/blog/internal/user"
)

// Commands 返回需要数据库等资源的命令。声明时 App 是零值，执行其中一个命令时才调用 Open 装配。
func Commands() []*cobra.Command {
    return command.Bind(Open, func(a *App) []*cobra.Command {
        return []*cobra.Command{user.Commands(a.db, a.users)}
    })
}
```

新建 `internal/user/me.go` 和 `internal/user/routes.go`，返回当前作者。`requireUser` 校验 token 后把登录者放进 ctx，`identity.FromContext` 取出它：

```go title="internal/user/me.go"
package user

import (
    "context"

    "github.com/uptrace/bun"

    "github.com/go-tenon/tenon/identity"
)

// Me 返回当前登录的作者。
type Me struct{ db *bun.DB }

func NewMe(db *bun.DB) *Me { return &Me{db: db} }

type UserOutput struct {
    Body *User
}

func (a *Me) HTTP(ctx context.Context, _ *struct{}) (*UserOutput, error) {
    sub, _ := identity.FromContext(ctx)
    u, err := find(ctx, a.db, sub.ID)
    if err != nil {
        return nil, err
    }
    return &UserOutput{Body: u}, nil
}
```

```go title="internal/user/routes.go"
package user

import (
    "net/http"

    "github.com/danielgtaylor/huma/v2"
    "github.com/uptrace/bun"

    "github.com/go-tenon/tenon/httpx/rest"
)

// RegisterRoutes 注册作者的接口。
func RegisterRoutes(api *rest.API, requireUser func(*huma.Operation), db *bun.DB) {
    op := huma.Operation{OperationID: "user-me", Method: http.MethodGet, Path: "/me", Summary: "当前登录的作者"}
    requireUser(&op)
    rest.Register(api, op, NewMe(db).HTTP)
}
```

<a id="auth"></a>
## 登录接口

新建 `internal/auth/auth.go`，提供登录和退出两个接口：

```go title="internal/auth/auth.go"
// Package auth 是登录和退出的接口。
//
// 浏览器的登录凭据是会话 cookie：登录成功后把 token 写进 HttpOnly cookie，之后浏览器随同源请求自动带上，
// 页面上的脚本读不到它。
package auth

import (
    "context"
    "net/http"
    "time"

    "github.com/danielgtaylor/huma/v2"

    "github.com/go-tenon/tenon/apitoken"
    "github.com/go-tenon/tenon/httpx"
    "github.com/go-tenon/tenon/httpx/rest"
    "github.com/go-tenon/tenon/identity"
    "github.com/go-tenon/tenon/login"
)

type LoginInput struct {
    UserAgent string `header:"User-Agent"`
    Body      struct {
        Email    string `json:"email" format:"email"`
        Password string `json:"password" minLength:"1"`
    }
}

// SessionOutput 写入或清除会话 cookie。
type SessionOutput struct {
    SetCookie *http.Cookie `header:"Set-Cookie"`
}

type handlers struct {
    users    *login.Realm
    tokens   *apitoken.Store
    lifetime time.Duration
}

// login 校验邮箱和密码，签发 token 写进会话 cookie。密码错误返回 401；同一个邮箱错太多次返回 429。
func (h *handlers) login(ctx context.Context, in *LoginInput) (*SessionOutput, error) {
    out, err := h.users.Password(ctx, login.Email(in.Body.Email), in.Body.Password)
    if err != nil {
        return nil, err
    }
    token, err := h.tokens.Login(ctx, out.Subject, apitoken.Device{UserAgent: in.UserAgent})
    if err != nil {
        return nil, err
    }
    return &SessionOutput{SetCookie: httpx.SessionCookie{}.Set(token, h.lifetime)}, nil
}

// logout 撤销这次登录，并清除 cookie。
func (h *handlers) logout(ctx context.Context, _ *struct{}) (*SessionOutput, error) {
    sub, _ := identity.FromContext(ctx)
    if err := h.tokens.Revoke(ctx, sub, sub.Credential); err != nil {
        return nil, err
    }
    return &SessionOutput{SetCookie: httpx.SessionCookie{}.Clear()}, nil
}

// RegisterRoutes 注册登录和退出的接口。requireUser 要求请求带着有效的登录凭据；lifetime 是浏览器保存 cookie 的时长。
func RegisterRoutes(api *rest.API, requireUser func(*huma.Operation), users *login.Realm, tokens *apitoken.Store, lifetime time.Duration) {
    h := &handlers{users: users, tokens: tokens, lifetime: lifetime}

    op := huma.Operation{OperationID: "auth-login", Method: http.MethodPost, Path: "/auth/login", Summary: "用邮箱和密码登录", DefaultStatus: http.StatusNoContent}
    rest.Public()(&op)
    rest.SameOrigin(api)(&op)
    rest.Register(api, op, h.login)

    op = huma.Operation{OperationID: "auth-logout", Method: http.MethodPost, Path: "/auth/logout", Summary: "退出登录", DefaultStatus: http.StatusNoContent}
    requireUser(&op)
    rest.Register(api, op, h.logout)
}
```

- 密码错误返回 `401`；同一个邮箱 15 分钟内错 5 次后返回 `429`。
- 退出撤销这次登录的 token，token 立即失效。
- `rest.SameOrigin` 让登录接口只接受博客自己的页面发出的请求，其他网站不能让浏览器登录攻击者的账号。

<a id="assembling"></a>
## 装配

在 `internal/bootstrap/app.go` 的 `Open` 中创建 `login` 和 `apitoken`：

```go title="internal/bootstrap/app.go"
package bootstrap

import (
    "context"
    "log/slog"
    "time"

    "github.com/uptrace/bun"

    "github.com/go-tenon/tenon/apitoken"
    "github.com/go-tenon/tenon/cleanup"
    "github.com/go-tenon/tenon/command"
    "github.com/go-tenon/tenon/crypt"
    "github.com/go-tenon/tenon/database"
    "github.com/go-tenon/tenon/hashing"
    "github.com/go-tenon/tenon/logging"
    "github.com/go-tenon/tenon/login"
    "github.com/go-tenon/tenon/ratelimit"
    "github.com/go-tenon/tenon/ratelimit/pglimit"

    "example.com/blog/internal/user"
)

// App 是各个运行部分共用的资源，每个进程只装配一次。
type App struct {
    cfg Config
    db  *bun.DB
    // users 校验作者的邮箱和密码，tokens 签发和校验登录凭据。
    users  *login.Realm
    tokens *apitoken.Store
}

// Open 安装日志器，连接数据库，把关闭操作登记到 stack。
func Open(ctx context.Context, cfg Config, stack *cleanup.Stack) (*App, error) {
    slog.SetDefault(logging.New(logging.Config{}))

    db, err := database.Open(ctx, cfg.Database)
    if err != nil {
        return nil, err
    }
    stack.MustAdd("database", func(context.Context) error { return db.Close() })

    keys := crypt.New(cfg.Crypt)
    // 每个邮箱 15 分钟内最多输错 5 次密码，计数保存在数据库中，所有实例共享。
    every15 := ratelimit.Config{Rate: 5, Interval: 15 * time.Minute, Burst: 5}
    users := login.New(db, login.Options{
        Type:     user.Type,
        Hasher:   hashing.New(cfg.Hashing),
        Crypt:    keys,
        Policy:   login.Policy{PasswordKinds: []string{login.KindEmail}},
        Attempts: pglimit.New(db, "login:attempts", every15),
        Sends:    pglimit.New(db, "login:sends", every15),
    })
    tokens := apitoken.NewStore(db, cfg.Token)

    return &App{cfg: cfg, db: db, users: users, tokens: tokens}, nil
}

// Parts 是常驻进程的运行部分：serve 运行全部部分，serve <名字> 只运行其中一个。
func Parts(a *App) []command.Part {
    return []command.Part{
        {Name: "http", Short: "接口和页面", Start: a.HTTP},
    }
}
```

在 `internal/bootstrap/http.go` 中注册接口。`rest.RequireToken` 返回 `requireUser`，它接受会话 cookie 和 `Authorization: Bearer <token>`：

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
    "github.com/go-tenon/tenon/identity"

    "example.com/blog/apps"
    "example.com/blog/internal/article"
    "example.com/blog/internal/auth"
    "example.com/blog/internal/user"
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
        // Accounts 声明接口接受哪些账号的登录凭据：博客只有作者一种。
        api := rest.New(r, rest.Options{Title: "Blog API", Version: "1.0.0", BasePath: "/api", Accounts: identity.Accounts{user.Type: nil}})
        // requireUser 要求请求带着作者的登录凭据：浏览器的会话 cookie，或者 Authorization: Bearer <token>
        requireUser := rest.RequireToken(api, a.tokens.Validate)
        auth.RegisterRoutes(api, requireUser, a.users, a.tokens, a.cfg.Token.Lifetime)
        user.RegisterRoutes(api, requireUser, a.db)
        article.RegisterRoutes(api, requireUser, a.db)
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

在 `internal/article/routes.go` 中，读文章公开，写文章需要登录：

```go title="internal/article/routes.go"
package article

import (
    "net/http"

    "github.com/danielgtaylor/huma/v2"
    "github.com/uptrace/bun"

    "github.com/go-tenon/tenon/httpx/rest"
)

// RegisterRoutes 注册文章的接口：读文章是公开的，写文章要先登录。
func RegisterRoutes(api *rest.API, requireUser func(*huma.Operation), db *bun.DB) {
    route := func(access func(*huma.Operation), id, method, path, summary string) huma.Operation {
        op := huma.Operation{OperationID: id, Method: method, Path: path, Summary: summary}
        access(&op)
        return op
    }
    public := rest.Public()
    create := route(requireUser, "article-create", http.MethodPost, "/articles", "新建文章")
    create.DefaultStatus = http.StatusCreated

    rest.Register(api, route(public, "article-list", http.MethodGet, "/articles", "文章列表"), NewList(db).HTTP)
    rest.Register(api, route(public, "article-get", http.MethodGet, "/articles/{id}", "读取文章"), NewGet(db).HTTP)
    rest.Register(api, create, NewCreate(db).HTTP)
    rest.Register(api, route(requireUser, "article-update", http.MethodPut, "/articles/{id}", "修改文章"), NewUpdate(db).HTTP)
    rest.Register(api, route(requireUser, "article-delete", http.MethodDelete, "/articles/{id}", "删除文章"), NewDelete(db).HTTP)
}
```

执行迁移，创建一个作者：

```bash
go run ./cmd/blog migrate up
go run ./cmd/blog user create --name 小明 --email ming@example.com --password secret123
```

没有登录就新建文章，返回 `401`：

```json
{"title":"Unauthorized","status":401,"detail":"请先认证","code":"auth.unauthenticated"}
```

<a id="frontend"></a>
## 前端

`@go-tenon/client` 的会话（session）记录当前的作者：打开页面时读一次 `/api/me`，登录、退出时更新。把 `apps/web/src/api.ts` 改成下面这样：

```ts title="apps/web/src/api.ts"
// 注意：src/client.ts 和 src/server.ts 是 TanStack Start 约定的入口文件名，API 客户端放在 src/api.ts。
import { createSession } from "@go-tenon/client"
import { apiFetch } from "@go-tenon/start"
import { createClient } from "@blog/client/api"
import { createKit } from "@blog/client/api/kit"

// session 记录当前的作者：打开页面、登录后读取 /api/me，没有登录时它返回 401。
export const session = createSession({ load: async () => ({ account: (await api.user.me()).id }) })

// api 是 tenon generate 生成的客户端（src/api），接口改了以后 tenon dev 会自动重新生成。
// 受保护的接口返回 401 时（登录过期，或者在别处退出了），会话随之变为未登录。
export const api = createClient({ baseURL: "/api", fetch: apiFetch, session })

// kit 中按身份缓存的查询随会话切换，字段名、枚举和验证提示按简体中文显示。
export const kit = createKit(api, { scope: session.scope, language: "zh-CN" })
```

把 `apps/web/src/router.tsx` 改成下面这样，把会话交给应用：

```tsx title="apps/web/src/router.tsx"
import { createApp } from "@go-tenon/ui"
import { cspNonce } from "@go-tenon/start"
import { NotFoundPage, RouteError } from "@go-tenon/ui"
import { session } from "./api"
import { routeTree } from "./routeTree.gen"

// getRouter 创建应用：路由、TanStack Query 和会话。服务端渲染时每个请求调用一次，所以每次都创建新的应用。
export function getRouter() {
  return createApp({
    id: "web",
    routeTree,
    session,
    router: { ssr: { nonce: cspNonce() }, defaultErrorComponent: RouteError, defaultNotFoundComponent: NotFoundPage },
  }).router
}

declare module "@tanstack/react-router" {
  interface Register {
    router: ReturnType<typeof getRouter>
  }
}
```

服务端渲染时不读取身份，页面先按未登录渲染，浏览器接管后再更新。

<a id="login-page"></a>
### 登录页

新建登录页 `apps/web/src/routes/login.tsx`：

```tsx title="apps/web/src/routes/login.tsx"
import { createFileRoute } from "@tanstack/react-router"
import { CenteredAuthLayout, LoginForm } from "@go-tenon/ui"
import { api } from "../api"

export const Route = createFileRoute("/login")({
  // ?redirect= 是登录前想去的页面
  validateSearch: (search: Record<string, unknown>): { redirect?: string } => ({
    redirect: typeof search.redirect === "string" ? search.redirect : undefined,
  }),
  component: Login,
})

function Login() {
  const { redirect } = Route.useSearch()
  return (
    <CenteredAuthLayout brand="博客" title="登录">
      <LoginForm login={(values) => api.auth.login(values)} redirect={redirect} />
    </CenteredAuthLayout>
  )
}
```

`LoginForm` 登录成功后回到 `?redirect=` 中的页面，没有时回到首页。回跳地址只接受本站的相对路径。

<a id="requiring-login"></a>
### 需要登录的页面

路由加上 `beforeLoad: requireAuth`，没有登录时跳到登录页；加上 `ssr: false`，因为服务端渲染时不知道是否已登录。把 `apps/web/src/routes/write.tsx` 改成下面这样：

```tsx title="apps/web/src/routes/write.tsx"
import { Container, Title } from "@mantine/core"
import { createFileRoute, useNavigate } from "@tanstack/react-router"
import { requireAuth } from "@go-tenon/ui"
import { ArticleEditor } from "../ArticleEditor"

export const Route = createFileRoute("/write")({ ssr: false, beforeLoad: requireAuth, component: Write })

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

`apps/web/src/routes/articles/$id.edit.tsx` 中也加上：

```tsx title="apps/web/src/routes/articles/$id.edit.tsx"
import { Container, Text, Title } from "@mantine/core"
import { useQuery } from "@tanstack/react-query"
import { createFileRoute, useNavigate } from "@tanstack/react-router"
import { requireAuth } from "@go-tenon/ui"
import { ArticleEditor } from "../../ArticleEditor"
import { api } from "../../api"

export const Route = createFileRoute("/articles/$id/edit")({ ssr: false, beforeLoad: requireAuth, component: EditArticle })

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

<a id="header"></a>
### 页头

页头显示当前的作者和退出按钮。把 `apps/web/src/routes/__root.tsx` 改成下面这样：

```tsx title="apps/web/src/routes/__root.tsx"
import "@go-tenon/ui/styles.css"
import type { ReactNode } from "react"
import { Button, ColorSchemeScript, Group, Text, mantineHtmlProps } from "@mantine/core"
import { useMutation, useQuery } from "@tanstack/react-query"
import { createRootRouteWithContext, HeadContent, Scripts, useRouter } from "@tanstack/react-router"
import { type RouterContext, useSignedIn } from "@go-tenon/ui"
import { AnchorLink, ButtonLink, SiteLayout, UIProvider } from "@go-tenon/ui"
import { api, session } from "../api"
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
  const signedIn = useSignedIn()
  const me = useQuery({ queryKey: ["me"], queryFn: () => api.user.me(), enabled: signedIn })
  const logout = useMutation({ mutationFn: () => api.auth.logout(), onSettled: () => session.signOut() })
  return (
    <SiteLayout
      brand={
        <AnchorLink to="/" c="inherit" underline="never" fw={700}>
          博客
        </AnchorLink>
      }
      actions={
        signedIn ? (
          <Group gap="sm">
            <Text size="sm">{me.data?.name}</Text>
            <ButtonLink to="/write">写文章</ButtonLink>
            <Button variant="default" onClick={() => logout.mutate()}>
              退出
            </Button>
          </Group>
        ) : (
          <ButtonLink to="/login" variant="default">
            登录
          </ButtonLink>
        )
      }
    />
  )
}
```

`session.signOut()` 清除当前的身份；登录、退出时 TanStack Query 的缓存被清空。

首页在没有登录时不显示编辑和删除。把 `apps/web/src/routes/index.tsx` 改成下面这样：

```tsx title="apps/web/src/routes/index.tsx"
import { Button, Container, Group, Paper, Stack, Text, Title } from "@mantine/core"
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { createFileRoute } from "@tanstack/react-router"
import { ButtonLink, useSignedIn } from "@go-tenon/ui"
import type { Article } from "@blog/client/api"
import { api } from "../api"

export const Route = createFileRoute("/")({ component: ArticleList })

function ArticleList() {
  const signedIn = useSignedIn()
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
            {signedIn && (
              <Group mt="md" gap="xs">
                <ButtonLink to="/articles/$id/edit" params={{ id: a.id }} variant="default" size="xs">
                  编辑
                </ButtonLink>
                <Button variant="subtle" color="red" size="xs" onClick={() => confirm(`删除“${a.title}”？`) && remove.mutate(a)}>
                  删除
                </Button>
              </Group>
            )}
          </Paper>
        ))}
      </Stack>
    </Container>
  )
}
```

打开 <http://localhost:8080/write>，页面跳到登录页；登录后回到写文章的页面，刷新后仍是登录状态。

<a id="troubleshooting"></a>
## 常见问题

- **`user create` 报错密码不符合要求**：默认要求 8 到 128 个字符，首尾没有空白。
- **登录后刷新又变成未登录**：通过 <http://localhost:8080> 访问。会话 cookie 只随同源请求发送，带有 `Secure` 属性，只在 HTTPS 和 `localhost` 上保存。
- **修改文章返回 `403`，错误码是 `http.cross_site`**：请求不是从博客的页面发出的。用 curl 调试时改用 `Authorization: Bearer <token>`。
