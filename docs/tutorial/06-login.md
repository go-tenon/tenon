# 6. 作者登录

- [怎么登录](#how-it-works)
- [配置](#configuration)
- [数据表](#tables)
- [作者账号](#users)
- [登录接口](#auth)
- [装配](#assembling)
- [前端](#frontend)

这一章让作者用邮箱和密码登录，登录后才能写文章。

<a id="how-it-works"></a>
## 怎么登录

框架的两个包分工：

- `login` 回答“这个人是谁”：保存邮箱和密码哈希，校验密码，限制输错的次数。邮箱、密码保存在框架的 `auth_*` 表中，应用的 `users` 表只有名字这样的业务字段。
- `apitoken` 签发登录凭据。浏览器中的单页应用用一对凭据：
  - **access token**，有效期 10 分钟，只保存在页面的内存中，请求时放在 `Authorization: Bearer` 中；
  - **refresh token**，保存在 HttpOnly cookie 中，页面上的脚本读不到它，用来换新的 access token。

打开页面时先用 cookie 换一个 access token，换到了就是已登录。这样即使页面被注入了脚本，也拿不走长期有效的凭据。

<a id="configuration"></a>
## 配置

`login` 用 Redis 记录输错密码的次数，所有实例共享。签发凭据需要一个应用密钥，生成一个写进 `.env`：

```bash
echo "APP_KEY=$(openssl rand -base64 32)" >> .env
```

`.env` 不提交到版本库，生产环境用环境变量 `APP_KEY` 设置，密钥泄露后要更换。在 `config/app.yaml` 中加上 Redis 和密钥：

```yaml title="config/app.yaml"
# 应用的配置。${VAR:-默认值} 读取环境变量，生产环境用环境变量覆盖开发时的默认值。
http:
  addr: ":8080"

database:
  dsn: ${DATABASE_URL:-postgres://blog:blog@localhost:5432/blog?sslmode=disable}

redis:
  url: ${REDIS_URL:-redis://localhost:6379/0}

crypt:
  key: ${APP_KEY}
```

`internal/bootstrap/config.go` 中加上对应的配置段。`hashing` 是密码哈希的参数，`token` 是登录的有效期，都使用默认值：

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

// Config 是应用的配置：config/app.yaml，开发时再叠加 config/local.yaml。
type Config struct {
    HTTP     httpx.Config    `yaml:"http"`
    Database database.Config `yaml:"database"`
    Redis    RedisConfig     `yaml:"redis"`
    // Crypt 是应用密钥，用来签发登录凭据。
    Crypt   crypt.Config    `yaml:"crypt"`
    Hashing hashing.Config  `yaml:"hashing"`
    Token   apitoken.Config `yaml:"token"`
    // FrontendDevServer 非空时把页面请求转给 Vite 的开发服务器，只在 config/local.yaml 中设置。
    FrontendDevServer string `yaml:"frontend_dev_server"`
}

type RedisConfig struct {
    URL string `yaml:"url"`
}
```

<a id="tables"></a>
## 数据表

`login` 和 `apitoken` 自带迁移，先把它们发布到 `migrations` 目录。在 `cmd/api/main.go` 中给 `migrate` 加上 `publish` 子命令；顺便加上 `bootstrap.Commands()`，下面创建作者的命令在其中：

```go title="cmd/api/main.go"
// Command api 是博客的入口。
package main

import (
    "github.com/spf13/cobra"

    "github.com/go-tenon/tenon/apitoken"
    "github.com/go-tenon/tenon/command"
    "github.com/go-tenon/tenon/command/httpcmd"
    "github.com/go-tenon/tenon/command/migratecmd"
    "github.com/go-tenon/tenon/database"
    "github.com/go-tenon/tenon/database/migrate"
    "github.com/go-tenon/tenon/login"

    "example.com/blog/internal/bootstrap"
    "example.com/blog/migrations"
)

func main() {
    root := command.Root("api", "博客")
    root.AddCommand(
        command.Run("serve", "启动 HTTP 服务", bootstrap.Serve),
        command.Config[bootstrap.Config](),
        migrateCommand(),
    )
    // 需要数据库的命令，例如 user create
    root.AddCommand(bootstrap.Commands()...)
    // openapi、routes、client：导出接口文档、列出路由、生成客户端，go tool tenon 调用它们。
    root.AddCommand(httpcmd.Commands(bootstrap.API, bootstrap.DocRoutes, bootstrap.DocRouter)...)
    command.Execute(root)
}

// migrateCommand 返回 migrate 命令：up、status、down、make，以及发布框架自带的迁移的 publish。
func migrateCommand() *cobra.Command {
    cmd := migratecmd.New(migrations.FS, func(cfg bootstrap.Config) database.Config { return cfg.Database })
    cmd.AddCommand(migratecmd.Publish(
        migrate.Source{Name: "login", FS: login.Migrations()},
        migrate.Source{Name: "apitoken", FS: apitoken.Migrations()},
    ))
    return cmd
}
```

```bash
go mod tidy
go run ./cmd/api migrate publish
```

命令把迁移复制到 `migrations` 中，它们和你自己写的迁移一样提交到版本库、一起执行。再为作者建表：

```bash
go run ./cmd/api migrate make create_users_table
```

```sql title="migrations/20261113080009_create_users_table.sql"
-- +goose Up
CREATE TABLE users (
    id         uuid PRIMARY KEY,
    name       text NOT NULL,
    created_at timestamptz NOT NULL
);

-- +goose Down
DROP TABLE users;
```

<a id="users"></a>
## 作者账号

新建 `internal/user/user.go`：

```go title="internal/user/user.go"
// Package user 是博客的作者账号。邮箱和密码保存在 login 的表中，users 表只有业务字段。
package user

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

// Type 是作者这套账号体系，登录凭据中的主体都是这个类型。
const Type identity.SubjectType = "user"

var ErrNotFound = errorx.Define(errorx.NotFound, "user.not_found")

// User 是一个作者。
type User struct {
    bun.BaseModel `bun:"table:users"`

    ID        string    `bun:"id,pk" json:"id"`
    Name      string    `bun:"name,notnull" json:"name"`
    CreatedAt time.Time `bun:"created_at,notnull" json:"created_at"`
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

`Type` 是这套账号的类型，登录凭据中记录着“哪种账号的哪一个”。`Ref` 把作者交给 `login` 时就用它。

博客不开放注册，作者由管理员用命令创建。新建 `internal/user/create.go`：

```go title="internal/user/create.go"
package user

import (
    "context"
    "fmt"
    "time"

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

// Handle 在一个事务中创建账号、写入邮箱和密码，任何一步失败都不会留下半个账号。
// 邮箱已被使用时返回 login.ErrTaken，密码不符合要求时返回 login.ErrWeakPassword。
func (a *Create) Handle(ctx context.Context, name, email, password string) (*User, error) {
    u := &User{ID: newID(), Name: name, CreatedAt: time.Now().UTC()}
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

账号、邮箱和密码在一个事务中写入，任何一步失败都不会留下半个账号。`Command` 是这个动作的命令行入口。新建 `internal/user/commands.go` 声明命令：

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

命令要用到数据库，而数据库要在加载配置之后才能打开。新建 `internal/bootstrap/commands.go`，`command.Bind` 先用空的 `App` 声明命令，执行时才调用 `open` 装配，命令结束后关闭资源：

```go title="internal/bootstrap/commands.go"
package bootstrap

import (
    "github.com/spf13/cobra"

    "github.com/go-tenon/tenon/command"

    "example.com/blog/internal/user"
)

// Commands 返回需要数据库等资源的命令。声明时 App 是零值，执行其中一个命令时才调用 open 装配。
func Commands() []*cobra.Command {
    return command.Bind(open, func(a *App) []*cobra.Command {
        return []*cobra.Command{user.Commands(a.db, a.users)}
    })
}
```

再加一个接口，返回当前登录的作者。新建 `internal/user/me.go` 和 `internal/user/routes.go`：

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

// Register 注册作者的接口。
func Register(api *rest.API, requireUser func(*huma.Operation), db *bun.DB) {
    op := huma.Operation{OperationID: "user-me", Method: http.MethodGet, Path: "/me", Summary: "当前登录的作者"}
    requireUser(&op)
    rest.Register(api, op, NewMe(db).HTTP)
}
```

`identity.FromContext` 取出这次请求的登录者。`requireUser` 保护的接口只有登录后才能调用，所以这里一定有。

<a id="auth"></a>
## 登录接口

新建 `internal/auth/auth.go`，提供登录、刷新和退出：

```go title="internal/auth/auth.go"
// Package auth 是登录、刷新和登出的接口。
//
// 浏览器中的单页应用使用 JWT：access token 只保存在页面的内存中，refresh token 保存在 HttpOnly cookie 中，
// 页面上的脚本读不到它。打开页面时先刷新一次，拿到 access token。
package auth

import (
    "context"
    "net/http"
    "time"

    "github.com/danielgtaylor/huma/v2"

    "github.com/go-tenon/tenon/apitoken"
    "github.com/go-tenon/tenon/httpx/rest"
    "github.com/go-tenon/tenon/identity"
    "github.com/go-tenon/tenon/login"
)

// refreshCookie 只发给 /api/auth 下的接口，其他请求不带它。
var refreshCookie = apitoken.RefreshCookie{Name: "blog_refresh", Path: "/api/auth"}

type LoginInput struct {
    UserAgent string `header:"User-Agent"`
    Body      struct {
        Email    string `json:"email" format:"email"`
        Password string `json:"password" minLength:"1"`
    }
}

type RefreshInput struct {
    Cookie string `cookie:"blog_refresh"`
}

// SessionOutput 返回 access token，refresh token 写入 cookie。
type SessionOutput struct {
    SetCookie *http.Cookie `header:"Set-Cookie"`
    Body      struct {
        AccessToken string    `json:"access_token"`
        ExpiresAt   time.Time `json:"expires_at"`
    }
}

type LogoutOutput struct {
    SetCookie *http.Cookie `header:"Set-Cookie"`
}

type handlers struct {
    users  *login.Realm
    tokens *apitoken.Store
    jwt    *apitoken.JWT
}

func session(pair apitoken.Pair) *SessionOutput {
    out := &SessionOutput{SetCookie: refreshCookie.Set(pair)}
    out.Body.AccessToken, out.Body.ExpiresAt = pair.Access, pair.ExpiresAt
    return out
}

// login 校验邮箱和密码。密码错误返回 401；同一个邮箱错太多次返回 429。
func (h *handlers) login(ctx context.Context, in *LoginInput) (*SessionOutput, error) {
    out, err := h.users.Password(ctx, login.Email(in.Body.Email), in.Body.Password)
    if err != nil {
        return nil, err
    }
    pair, err := h.jwt.Login(ctx, out.Subject, apitoken.Device{UserAgent: in.UserAgent})
    if err != nil {
        return nil, err
    }
    return session(pair), nil
}

// refresh 用 cookie 中的 refresh token 换一个新的 access token，失败时返回 401，页面显示登录。
func (h *handlers) refresh(ctx context.Context, in *RefreshInput) (*SessionOutput, error) {
    pair, err := h.jwt.Refresh(ctx, in.Cookie)
    if err != nil {
        return nil, err
    }
    return session(pair), nil
}

// logout 撤销这次登录，并清除 cookie。
func (h *handlers) logout(ctx context.Context, _ *struct{}) (*LogoutOutput, error) {
    sub, _ := identity.FromContext(ctx)
    if err := h.tokens.Revoke(ctx, sub, sub.Credential); err != nil {
        return nil, err
    }
    return &LogoutOutput{SetCookie: refreshCookie.Clear()}, nil
}

// Register 注册登录、刷新和登出的接口。requireUser 要求请求带着有效的 access token。
func Register(api *rest.API, requireUser func(*huma.Operation), users *login.Realm, tokens *apitoken.Store, jwt *apitoken.JWT) {
    h := &handlers{users: users, tokens: tokens, jwt: jwt}

    op := huma.Operation{OperationID: "auth-login", Method: http.MethodPost, Path: "/auth/login", Summary: "用邮箱和密码登录"}
    rest.Public()(&op)
    rest.Register(api, op, h.login)

    op = huma.Operation{OperationID: "auth-refresh", Method: http.MethodPost, Path: "/auth/refresh", Summary: "换一个新的 access token"}
    rest.Public()(&op)
    rest.Register(api, op, h.refresh)

    op = huma.Operation{OperationID: "auth-logout", Method: http.MethodPost, Path: "/auth/logout", Summary: "退出登录", DefaultStatus: http.StatusNoContent}
    requireUser(&op)
    rest.Register(api, op, h.logout)
}
```

- 登录：`Password` 校验邮箱和密码，错误时返回 `401`；同一个邮箱 15 分钟内错 5 次后返回 `429`。成功后签发一对凭据，refresh token 写入 cookie。
- 刷新：用 cookie 中的 refresh token 换一对新的，旧的随之作废。
- 退出：撤销这次登录，清除 cookie。

cookie 只发给 `/api/auth` 下的接口，其他请求不带它。

<a id="assembling"></a>
## 装配

在 `internal/bootstrap/app.go` 的 `open` 中连接 Redis，创建 `login` 和 `apitoken`：

```go title="internal/bootstrap/app.go"
package bootstrap

import (
    "context"
    "log/slog"
    "time"

    "github.com/redis/rueidis"
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
    "github.com/go-tenon/tenon/ratelimit/redislimit"

    "example.com/blog/internal/user"
)

// App 是各个运行部分共用的资源，每个进程只装配一次。
type App struct {
    cfg Config
    db  *bun.DB
    rdb rueidis.Client
    // users 校验作者的邮箱和密码，jwt 签发和校验登录凭据。
    users  *login.Realm
    tokens *apitoken.Store
    jwt    *apitoken.JWT
}

// open 安装日志器，连接数据库和 Redis，把关闭操作登记到 stack。
func open(ctx context.Context, cfg Config, stack *cleanup.Stack) (*App, error) {
    slog.SetDefault(logging.New(logging.Config{}))

    db, err := database.Open(ctx, cfg.Database)
    if err != nil {
        return nil, err
    }
    stack.MustAdd("database", func(context.Context) error { return db.Close() })

    opt, err := rueidis.ParseURL(cfg.Redis.URL)
    if err != nil {
        return nil, err
    }
    rdb, err := rueidis.NewClient(opt)
    if err != nil {
        return nil, err
    }
    stack.MustAdd("redis", func(context.Context) error { rdb.Close(); return nil })

    keys := crypt.New(cfg.Crypt)
    // 每个邮箱 15 分钟内最多输错 5 次密码，计数保存在 Redis 中，所有实例共享。
    every15 := ratelimit.Config{Rate: 5, Interval: 15 * time.Minute, Burst: 5}
    users := login.New(db, login.Options{
        Type:     user.Type,
        Hasher:   hashing.New(cfg.Hashing),
        Crypt:    keys,
        Policy:   login.Policy{PasswordKinds: []string{login.KindEmail}},
        Attempts: redislimit.New(rdb, "blog:login:attempts", every15),
        Sends:    redislimit.New(rdb, "blog:login:sends", every15),
    })
    tokens := apitoken.NewStore(db, cfg.Token)

    return &App{
        cfg: cfg, db: db, rdb: rdb,
        users: users, tokens: tokens,
        jwt: apitoken.NewJWT(tokens, apitoken.JWTConfig{Keys: apitoken.HMACKeys(keys)}),
    }, nil
}

// part 是进程中的一个运行部分，例如 HTTP 服务：装配时返回 run，run 一直运行到 ctx 被取消。
type part func(*App, *command.Daemon) (run func(context.Context) error, err error)

// run 装配 App 和 parts，然后在同一个进程中运行 parts，直到收到停止信号，最后关闭资源。
func run(ctx context.Context, cfg Config, parts ...part) (err error) {
    d, err := command.NewDaemon(nil)
    if err != nil {
        return err
    }
    defer d.Stop()

    var stack cleanup.Stack
    defer func() { err = stack.CloseWith(context.Background(), err) }()

    app, err := open(ctx, cfg, &stack)
    if err != nil {
        return err
    }
    runs := make([]func(context.Context) error, len(parts))
    for i, p := range parts {
        if runs[i], err = p(app, d); err != nil {
            return err
        }
    }
    return d.Run(ctx, runs...)
}

// Serve 运行 HTTP 服务，serve 命令调用它。
func Serve(ctx context.Context, cfg Config) error {
    return run(ctx, cfg, (*App).HTTP)
}
```

在 `internal/bootstrap/http.go` 中注册新的接口，`/readyz` 也检查 Redis：

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
    "github.com/go-tenon/tenon/identity"

    "example.com/blog/frontend"
    "example.com/blog/internal/article"
    "example.com/blog/internal/auth"
    "example.com/blog/internal/user"
    "example.com/blog/locales"
)

// API 是 JSON API 的选项，挂在 /api 下。serve、openapi、client 命令共用它。
// Accounts 声明接口接受哪些账号的登录凭据：博客只有作者一种。
var API = rest.Options{Title: "Blog API", Version: "1.0.0", BasePath: "/api", Accounts: identity.Accounts{user.Type: nil}}

// Routes 注册全部接口，serve 和生成文档共用它。
func Routes(api *rest.API, a *App) {
    // requireUser 要求请求带着作者的 access token：Authorization: Bearer <token>
    requireUser := rest.RequireBearer(api, a.jwt.Validate)
    auth.Register(api, requireUser, a.users, a.tokens, a.jwt)
    user.Register(api, requireUser, a.db)
    article.Register(api, requireUser, a.db)
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
        Checks: []httpx.Check{
            {Name: "database", Ping: a.db.PingContext},
            {Name: "redis", Ping: func(ctx context.Context) error { return a.rdb.Do(ctx, a.rdb.B().Ping().Build()).Error() }},
        },
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

`rest.Options.Accounts` 声明接口接受哪些账号的凭据，`rest.RequireBearer` 返回的 `requireUser` 用来保护接口。在 `internal/article/routes.go` 中，读文章仍然公开，写文章改为需要登录：

```go title="internal/article/routes.go"
package article

import (
    "net/http"

    "github.com/danielgtaylor/huma/v2"
    "github.com/uptrace/bun"

    "github.com/go-tenon/tenon/httpx/rest"
)

// Register 注册文章的接口：读文章是公开的，写文章要先登录。
func Register(api *rest.API, requireUser func(*huma.Operation), db *bun.DB) {
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
go run ./cmd/api migrate up
go run ./cmd/api user create --name 小明 --email ming@example.com --password secret123
```

没有登录就新建文章，返回 `401`：

```json
{"title":"Unauthorized","status":401,"detail":"请先认证","code":"auth.unauthenticated"}
```

<a id="frontend"></a>
## 前端

把 `frontend/src/client.ts` 改成下面这样。客户端自己保管 access token，请求时带上它；接口返回 `401` 时调用一次 `refresh` 换新的，再重试原来的请求：

```ts title="frontend/src/client.ts"
import { QueryClient } from "@tanstack/react-query"
import { createClient } from "./api/api"

export const queryClient = new QueryClient()

// 刷新接口靠 cookie 认证，用一个不带 access token 的客户端调用它。
const plain = createClient({ baseURL: "/api" })

// api 是 tenon generate 生成的客户端（src/api），接口改了以后 tenon dev 会自动重新生成。
// access token 只放在内存中；受保护的接口返回 401 时，先用 cookie 换一个新的再重试。
export const api = createClient({
  baseURL: "/api",
  auth: {
    refresh: async () => (await plain.auth.refresh()).access_token,
    // 刷新也失败了：登录已过期或已退出，显示为未登录
    onSignedOut: () => queryClient.setQueryData(["me"], null),
  },
})
```

`queryClient` 移到这里，`frontend/src/main.tsx` 改为使用它：

```tsx title="frontend/src/main.tsx"
import { StrictMode } from "react"
import { createRoot } from "react-dom/client"
import { QueryClientProvider } from "@tanstack/react-query"
import { App } from "./App"
import { queryClient } from "./client"
import "./app.css"

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <QueryClientProvider client={queryClient}>
      <App />
    </QueryClientProvider>
  </StrictMode>,
)
```

新建 `frontend/src/session.ts`，`useMe` 是当前登录的作者，没有登录时为 `null`：

```ts title="frontend/src/session.ts"
import { useQuery } from "@tanstack/react-query"
import { api } from "./client"

// useMe 返回当前登录的作者，未登录时为 null。打开页面时先刷新一次，用 cookie 换 access token。
export function useMe() {
  return useQuery({
    queryKey: ["me"],
    queryFn: async () => ((await api.refresh()) ? api.user.me() : null),
    staleTime: Infinity,
  })
}
```

新建登录表单 `frontend/src/Login.tsx`：

```tsx title="frontend/src/Login.tsx"
import { useState, type FormEvent } from "react"
import { useMutation } from "@tanstack/react-query"
import { isApiError } from "./api/api"
import { api, queryClient } from "./client"

export function Login({ onDone }: { onDone: () => void }) {
  const [email, setEmail] = useState("")
  const [password, setPassword] = useState("")
  const login = useMutation({
    mutationFn: () => api.auth.login({ email, password }),
    onSuccess: async (session) => {
      api.setAccessToken(session.access_token)
      await queryClient.invalidateQueries({ queryKey: ["me"] })
      onDone()
    },
  })

  function submit(e: FormEvent) {
    e.preventDefault()
    login.mutate()
  }
  return (
    <form className="editor" onSubmit={submit}>
      {isApiError(login.error) && <p className="error">{login.error.detail}</p>}
      <label>
        邮箱 <input type="email" value={email} onChange={(e) => setEmail(e.target.value)} autoFocus />
      </label>
      <label>
        密码 <input type="password" value={password} onChange={(e) => setPassword(e.target.value)} />
      </label>
      <div>
        <button disabled={login.isPending}>登录</button>
        <button type="button" onClick={onDone}>
          取消
        </button>
      </div>
    </form>
  )
}
```

在 `frontend/src/App.tsx` 中显示登录、退出按钮，登录后才能写文章：

```tsx title="frontend/src/App.tsx"
import { useState } from "react"
import { useMutation } from "@tanstack/react-query"
import type { Article } from "./api/api"
import { ArticleEditor } from "./ArticleEditor"
import { ArticleList } from "./ArticleList"
import { api, queryClient } from "./client"
import { Login } from "./Login"
import { useMe } from "./session"

// 页面上同时只显示一样东西：文章列表、编辑器或登录表单。
type View = { name: "list" } | { name: "edit"; article?: Article } | { name: "login" }

export function App() {
  const me = useMe()
  const [view, setView] = useState<View>({ name: "list" })
  const logout = useMutation({
    mutationFn: () => api.auth.logout(),
    onSettled: () => {
      api.setAccessToken("")
      queryClient.setQueryData(["me"], null)
    },
  })
  const home = () => setView({ name: "list" })

  return (
    <main>
      <header>
        <h1 onClick={home}>博客</h1>
        {me.data ? (
          <span>
            {me.data.name}
            <button onClick={() => setView({ name: "edit" })}>写文章</button>
            <button onClick={() => logout.mutate()}>退出</button>
          </span>
        ) : (
          me.data === null &&
          view.name !== "login" && <button onClick={() => setView({ name: "login" })}>登录</button>
        )}
      </header>
      {view.name === "list" && (
        <ArticleList onEdit={me.data ? (article) => setView({ name: "edit", article }) : undefined} />
      )}
      {view.name === "edit" && <ArticleEditor article={view.article} onDone={home} />}
      {view.name === "login" && <Login onDone={home} />}
    </main>
  )
}
```

`frontend/src/ArticleList.tsx` 中，没有登录时不显示编辑和删除按钮：

```tsx title="frontend/src/ArticleList.tsx"
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import type { Article } from "./api/api"
import { api } from "./client"

// onEdit 为空时（未登录）不显示编辑和删除按钮。
export function ArticleList({ onEdit }: { onEdit?: (a: Article) => void }) {
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
          {onEdit && (
            <div>
              <button onClick={() => onEdit(a)}>编辑</button>
              <button onClick={() => confirm(`删除“${a.title}”？`) && remove.mutate(a)}>删除</button>
            </div>
          )}
        </li>
      ))}
    </ul>
  )
}
```

打开 http://localhost:8080，用刚才创建的账号登录，写一篇文章。刷新页面，仍然是登录状态：页面用 cookie 换到了新的 access token。退出后，编辑按钮消失了。

现在任何作者都能修改别人的文章，所有文章写完就公开。下一章加上作者和草稿。

深入了解：[登录与注册](../security/login.md)、[API 令牌](../security/api-tokens.md)、[认证](../security/authentication.md)、[命令行](../general/commands.md)。
