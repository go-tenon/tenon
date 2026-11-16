# 6. 作者登录

到目前为止，任何人都可以写文章。这一章，我们让作者用邮箱和密码登录，只有登录之后才能写文章。

**这一章要做的**（内容比较多，分成几步）：

1. 了解登录是怎样工作的；
2. 准备配置：应用密钥；
3. 建作者表，加一个创建作者的命令；
4. 写登录、刷新、退出三个接口，让写文章的接口要求登录；
5. 在前端加上登录页，没有登录时跳到登录页。

阅读前需要了解：[第 5 章](05-pages.md)；HTTP 的 cookie 和 `Authorization` 请求头。

- [怎么登录](#how-it-works)
- [配置](#configuration)
- [数据表](#tables)
- [作者账号](#users)
- [登录接口](#auth)
- [装配](#assembling)
- [前端](#frontend)
- [常见问题](#troubleshooting)
- [小结](#summary)

<a id="how-it-works"></a>
## 怎么登录

登录要解决两个问题：用户第一次登录时，确认他是谁；之后的每个请求，确认它来自一个已经登录的用户。框架用两个包分别处理：

- `login` 回答“这个人是谁”：保存邮箱和密码哈希，校验密码，限制输错的次数。邮箱、密码保存在框架的 `auth_*` 表中，应用的 `users` 表只有名字这样的业务字段。
- `apitoken` 签发登录凭据。浏览器中的单页应用用一对凭据：
  - **access token**，有效期 10 分钟，只保存在页面的内存中，请求时放在 `Authorization: Bearer` 中；
  - **refresh token**，保存在 HttpOnly cookie 中，页面上的脚本读不到它，用来换新的 access token。

几个术语：

- **密码哈希**：数据库中不保存密码本身，而是保存用单向算法计算出的“指纹”。登录时对输入的密码做同样的计算，比较结果。即使数据库泄露，也得不到原始密码。
- **token（令牌）**：登录成功后服务端签发的一段字符串，之后的请求带上它，服务端就知道请求来自谁，不需要每次都发送密码。access token 是 JWT 格式：内容带有服务端的签名，服务端不用查数据库就能验证它。
- **HttpOnly cookie**：浏览器保存、每次请求自动带上的 cookie，并且页面上的 JavaScript 读不到它。

为什么用两个 token？access token 每次请求都要带，放在页面的内存中最方便，但页面上的脚本也能读到它，所以它只有 10 分钟有效期，即使被偷走，很快也会失效。refresh token 有效期长，放在脚本读不到的 HttpOnly cookie 中，只用来换新的 access token。

所以流程是：登录成功后得到一对 token；打开页面时先用 cookie 中的 refresh token 换一个 access token，换到了就是已登录；access token 过期后再换一个。

<a id="configuration"></a>
## 配置

登录需要一个**应用密钥**：签发 token 时用它签名，没有密钥就无法伪造 token。

生成一个随机的密钥写进 `.env`：

```bash
echo "APP_KEY=$(openssl rand -base64 32)" >> .env
```

`.env` 不提交到代码仓库，生产环境用环境变量 `APP_KEY` 设置。密钥泄露后要更换，已经签发的 token 随之失效。在 `config/app.yaml` 中加上密钥：

```yaml title="config/app.yaml"
# 应用的配置。${VAR:-默认值} 读取环境变量，生产环境用环境变量覆盖开发时的默认值。
http:
  addr: ":8080"

database:
  dsn: ${DATABASE_URL:-postgres://blog:blog@localhost:5432/blog?sslmode=disable}

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

// Config 是应用的配置：config/app.yaml。
type Config struct {
    HTTP     httpx.Config    `yaml:"http"`
    Database database.Config `yaml:"database"`
    // Crypt 是应用密钥，用来签发登录凭据。
    Crypt   crypt.Config    `yaml:"crypt"`
    Hashing hashing.Config  `yaml:"hashing"`
    Token   apitoken.Config `yaml:"token"`
    // DevServers 是各前端开发服务器的地址，只在 tenon dev 时有值。
    DevServers map[string]string `yaml:"dev_servers"`
}
```

<a id="tables"></a>
## 数据表

`login` 和 `apitoken` 需要自己的表（保存邮箱、密码哈希、签发过的 token），`login` 还要记录每个邮箱输错密码的次数，错太多次就暂时拒绝登录，防止有人不断猜密码，计数保存在限流的 `rate_limits` 表中。这些表的迁移由框架提供。我们先把这些迁移复制（发布）到 `migrations` 目录中。在 `cmd/api/main.go` 中给 `migrate` 加上 `publish` 子命令；顺便加上 `bootstrap.Commands()`，下文创建作者的命令在其中：

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
    "github.com/go-tenon/tenon/ratelimit/pglimit"

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
    root.AddCommand(httpcmd.Commands(bootstrap.DocRouter)...)
    command.Execute(root)
}

// migrateCommand 返回 migrate 命令：up、status、down、make，以及发布框架自带的迁移的 publish。
func migrateCommand() *cobra.Command {
    cmd := migratecmd.New(migrations.FS, func(cfg bootstrap.Config) database.Config { return cfg.Database })
    cmd.AddCommand(migratecmd.Publish(
        migrate.Source{Name: "login", FS: login.Migrations()},
        migrate.Source{Name: "apitoken", FS: apitoken.Migrations()},
        migrate.Source{Name: "ratelimit", FS: pglimit.Migrations()},
    ))
    return cmd
}
```

```bash
go mod tidy
go run ./cmd/api migrate publish
```

命令把迁移复制到 `migrations` 中，它们和你自己写的迁移一样提交到代码仓库、一起执行。再为作者建一张自己的表：

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

作者是一个新的业务模块 `internal/user`，结构和第 4 章的 `internal/article` 一样。新建 `internal/user/user.go`：

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

- **`Type`** 是这套账号的类型。一个应用可能有几种账号，例如作者和后台管理员，所以登录凭据中记录的是“哪种账号的哪一个”。
- **`Ref`** 返回这个作者的引用，把作者交给 `login`、`apitoken` 时用它。
- 作者的 ID 用 `uuid.NewV7()` 生成，这种 UUID 按时间递增，插入数据库时比完全随机的 UUID 更快。

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

// Handle 在一个事务中创建账号、写入邮箱和密码，任何一步失败时整体回滚。
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

- `Handle` 在一个事务中写入 `users` 表和 `login` 的表：邮箱已被使用、密码太短时，整体回滚，不会留下一个没有邮箱的作者。
- `Command` 是这个动作的命令行入口：从命令行参数中取出名字、邮箱和密码，调用 `Handle`。这就是第 4 章说的，业务逻辑写在 `Handle` 中，HTTP 和命令行都只是调用它的方式。

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

这里有一个先后顺序的问题：命令要用到数据库，但程序启动、声明有哪些命令时，配置还没有加载，数据库还不能打开。`command.Bind` 解决这个问题：它先用空的 `App` 声明命令，等真的执行其中一个命令时，才加载配置、调用 `open` 打开资源，命令结束后关闭资源。新建 `internal/bootstrap/commands.go`：

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

- `identity.FromContext` 从 ctx 中取出这次请求的登录者。登录者是验证 token 时放进 ctx 的，见 [context](../concepts/go-primer.md#context)。
- `requireUser` 是一个保护接口的函数，和 `rest.Public()` 用法一样：被它保护的接口，只有带着有效的 access token 才能调用，所以 `Me` 中一定能取到登录者。它在下文的装配一节中创建。

<a id="auth"></a>
## 登录接口

接着，新建 `internal/auth/auth.go`，提供登录、刷新和退出三个接口：

```go title="internal/auth/auth.go"
// Package auth 是登录、刷新和登出的接口。
//
// 浏览器中的单页应用使用 JWT：access token 只保存在页面的内存中，refresh token 保存在 HttpOnly cookie 中，
// 页面上的脚本读不到它。打开页面时先刷新一次，拿到 access token。
package auth

import (
    "context"
    "net/http"

    "github.com/danielgtaylor/huma/v2"

    "github.com/go-tenon/tenon/apitoken"
    "github.com/go-tenon/tenon/httpx/rest"
    "github.com/go-tenon/tenon/identity"
    "github.com/go-tenon/tenon/login"
)

// refreshCookie 只发给 /api/auth 下的接口。
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
    Body      apitoken.Session
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
    out := &SessionOutput{}
    out.Body, out.SetCookie = refreshCookie.Session(pair)
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

`refreshCookie` 的 `Path` 是 `/api/auth`，浏览器只在请求 `/api/auth` 下的接口时带上这个 cookie，其他接口收不到它，减少泄露的机会。

<a id="assembling"></a>
## 装配

现在把这些连起来。在 `internal/bootstrap/app.go` 的 `open` 中创建 `login` 和 `apitoken`，保存到 `App` 中：

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
    // users 校验作者的邮箱和密码，jwt 签发和校验登录凭据。
    users  *login.Realm
    tokens *apitoken.Store
    jwt    *apitoken.JWT
}

// open 安装日志器，连接数据库，把关闭操作登记到 stack。
func open(ctx context.Context, cfg Config, stack *cleanup.Stack) (*App, error) {
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

    return &App{
        cfg: cfg, db: db,
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

在 `internal/bootstrap/http.go` 中注册新的接口：

```go title="internal/bootstrap/http.go"
package bootstrap

import (
    "context"
    "net/http"

    "github.com/go-chi/chi/v5"

    "github.com/go-tenon/tenon/command"
    "github.com/go-tenon/tenon/config"
    "github.com/go-tenon/tenon/health"
    "github.com/go-tenon/tenon/httpx"
    "github.com/go-tenon/tenon/httpx/rest"
    "github.com/go-tenon/tenon/i18n"
    "github.com/go-tenon/tenon/identity"

    "example.com/blog/apps/web"
    "example.com/blog/internal/article"
    "example.com/blog/internal/auth"
    "example.com/blog/internal/user"
    "example.com/blog/locales"
)

// HTTP 装配 HTTP 服务，serve 命令运行它。
func (a *App) HTTP(d *command.Daemon) (func(context.Context) error, error) {
    var draining httpx.Draining
    router := a.Router(&draining)
    ln, err := d.Listen("tcp", a.cfg.HTTP.Addr)
    if err != nil {
        return nil, err
    }
    return func(ctx context.Context) error {
        return httpx.Serve(ctx, a.cfg.HTTP, ln, router, httpx.OnDraining(draining.Set))
    }, nil
}

// Router 构造应用的全部路由：健康检查、/api 下的接口和单页应用。
// 它只保存 App 的字段、不使用它们，所以 DocRouter 可以用一个没有打开任何资源的 App 调用它。
func (a *App) Router(draining *httpx.Draining) chi.Router {
    router := chi.NewRouter()
    router.Use(httpx.Defaults(a.cfg.HTTP, httpx.Languages(i18n.MustNew("zh-CN", locales.FS)))...)
    router.Get("/livez", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
    router.Handle("/readyz", httpx.Ready(httpx.ReadyOptions{
        Draining: draining,
        Checks: []health.Check{
            {Name: "database", Ping: func(ctx context.Context) error { return a.db.PingContext(ctx) }},
        },
    }))
    router.Route("/api", func(r chi.Router) {
        r.Use(httpx.Timeout(a.cfg.HTTP.Timeout))
        // Accounts 声明接口接受哪些账号的登录凭据：博客只有作者一种。
        api := rest.New(r, rest.Options{Title: "Blog API", Version: "1.0.0", BasePath: "/api", Accounts: identity.Accounts{user.Type: nil}})
        // requireUser 要求请求带着作者的 access token：Authorization: Bearer <token>
        requireUser := rest.RequireBearer(api, a.jwt.Validate)
        auth.Register(api, requireUser, a.users, a.tokens, a.jwt)
        user.Register(api, requireUser, a.db)
        article.Register(api, requireUser, a.db)
    })
    router.Handle("/*", httpx.NewSPA(web.Dist, httpx.SPAOptions{DevServer: a.cfg.DevServers["web"]}))
    return router
}

// DocRouter 用默认配置和没有打开任何资源的 App 构造路由，openapi、routes、client 命令用它。
func DocRouter() chi.Routes {
    return (&App{cfg: config.Default[Config]()}).Router(nil)
}
```

- `rest.Options.Accounts` 声明接口接受哪些账号的 token，现在只有作者。
- `rest.RequireBearer` 返回 `requireUser`：被它保护的接口要求请求头中带有 `Authorization: Bearer <access token>`。它被传给 `user.Register`、`auth.Register` 和 `article.Register`。

在 `internal/article/routes.go` 中，读文章仍然公开，写文章改为需要登录：

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

后端完成了。执行迁移，用新命令创建一个作者：

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

前端要做的事情有：保管 access token，每次请求带上它；打开页面时用 cookie 换 access token；token 过期时再换一个。这些由 `@go-tenon/client` 的**会话**（session）完成：受保护的接口返回 `401` 时，会话用 cookie 换一个新的 access token，然后重试原来的请求，页面上的代码不用关心这些。

把 `apps/web/src/client.ts` 改成下面这样：

```ts title="apps/web/src/client.ts"
import { createSession } from "@go-tenon/client"
import { createClient } from "@blog/client/api"

// 刷新接口靠 cookie 认证，用一个不带 access token 的客户端调用它。
const plain = createClient({ baseURL: "/api" })

// session 保管登录凭据；refresh 用 cookie 换一个新的 access token。
export const session = createSession({ refresh: () => plain.auth.refresh() })

// api 是 tenon generate 生成的客户端（src/api），接口改了以后 tenon dev 会自动重新生成。
// 受保护的接口返回 401 时，它通过 session.auth 换一个新的 access token 再重试；登录、退出时 session 更新它的 token。
export const api = createClient({ baseURL: "/api", auth: session.auth })
session.connect(api)
```

再把会话交给应用。把 `apps/web/src/app.ts` 改成下面这样：

```ts title="apps/web/src/app.ts"
import { createApp } from "@go-tenon/shell"
import { session } from "./client"
import { routeTree } from "./routeTree.gen"

export const app = createApp({ id: "web", routeTree, session })

declare module "@tanstack/react-router" {
  interface Register {
    router: typeof app.router
  }
}
```

应用在打开第一个页面之前恢复登录状态：cookie 还有效时，刷新页面后仍然是登录的。

<a id="login-page"></a>
### 登录页

新建登录页 `apps/web/src/routes/login.tsx`，地址是 `/login`：

```tsx title="apps/web/src/routes/login.tsx"
import { useState, type FormEvent } from "react"
import { useMutation } from "@tanstack/react-query"
import { createFileRoute } from "@tanstack/react-router"
import { isApiError } from "@blog/client/api"
import { app } from "../app"
import { api, session } from "../client"

export const Route = createFileRoute("/login")({
  // ?redirect= 是登录前想去的页面
  validateSearch: (search: Record<string, unknown>): { redirect?: string } => ({
    redirect: typeof search.redirect === "string" ? search.redirect : undefined,
  }),
  component: Login,
})

function Login() {
  const { redirect } = Route.useSearch()
  const [email, setEmail] = useState("")
  const [password, setPassword] = useState("")
  const login = useMutation({
    mutationFn: () => api.auth.login({ email, password }),
    onSuccess: async (body) => {
      await session.signIn(body)
      await app.redirectAfterSignIn(redirect)
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
      <button disabled={login.isPending}>登录</button>
    </form>
  )
}
```

- 登录成功后，`session.signIn` 保存 access token；`app.redirectAfterSignIn` 回到登录前想去的页面（地址中的 `?redirect=`），没有时回到首页。
- 回跳地址只接受本站的相对路径。否则别人可以构造一个 `?redirect=https://evil.example` 的链接，让用户登录后跳到一个仿冒的网站。

<a id="requiring-login"></a>
### 需要登录的页面

写文章和修改文章需要登录。在这两个页面的路由上加上 `beforeLoad: requireAuth`：没有登录时，先跳到登录页，并带上原来的地址。把 `apps/web/src/routes/write.tsx` 改成下面这样：

```tsx title="apps/web/src/routes/write.tsx"
import { createFileRoute, useNavigate } from "@tanstack/react-router"
import { requireAuth } from "@go-tenon/shell"
import { ArticleEditor } from "../ArticleEditor"

export const Route = createFileRoute("/write")({ beforeLoad: requireAuth, component: Write })

function Write() {
  const navigate = useNavigate()
  return <ArticleEditor onDone={() => navigate({ to: "/" })} />
}
```

`apps/web/src/routes/articles/$id.edit.tsx` 中也加上：

```tsx title="apps/web/src/routes/articles/$id.edit.tsx"
import { useQuery } from "@tanstack/react-query"
import { createFileRoute, useNavigate } from "@tanstack/react-router"
import { requireAuth } from "@go-tenon/shell"
import { ArticleEditor } from "../../ArticleEditor"
import { api } from "../../client"

export const Route = createFileRoute("/articles/$id/edit")({ beforeLoad: requireAuth, component: EditArticle })

function EditArticle() {
  const { id } = Route.useParams()
  const navigate = useNavigate()
  const article = useQuery({ queryKey: ["article", id], queryFn: () => api.article.get(id) })

  if (article.isPending) return <p>加载中…</p>
  if (article.error) return <p className="error">{article.error.message}</p>
  return <ArticleEditor article={article.data} onDone={() => navigate({ to: "/" })} />
}
```

页面上的检查只是为了让没有登录的人先去登录，真正的保护仍然在服务端：接口要求登录，没有 token 时返回 `401`。

<a id="header"></a>
### 页头

页头显示当前的作者和退出按钮，没有登录时显示登录链接。把 `apps/web/src/routes/__root.tsx` 改成下面这样：

```tsx title="apps/web/src/routes/__root.tsx"
import { useMutation, useQuery } from "@tanstack/react-query"
import { createRootRouteWithContext, Link, Outlet } from "@tanstack/react-router"
import { type RouterContext, useSignedIn } from "@go-tenon/shell"
import { api, session } from "../client"

export const Route = createRootRouteWithContext<RouterContext>()({ component: Layout })

function Layout() {
  const signedIn = useSignedIn()
  const me = useQuery({ queryKey: ["me"], queryFn: () => api.user.me(), enabled: signedIn })
  const logout = useMutation({ mutationFn: () => api.auth.logout(), onSettled: () => session.signOut() })

  return (
    <main>
      <header>
        <h1>
          <Link to="/">博客</Link>
        </h1>
        {signedIn ? (
          <nav>
            <span>{me.data?.name}</span>
            <Link to="/write">写文章</Link>
            <button onClick={() => logout.mutate()}>退出</button>
          </nav>
        ) : (
          <Link to="/login">登录</Link>
        )}
      </header>
      <Outlet />
    </main>
  )
}
```

- `useSignedIn` 返回是否已登录，登录、退出时页面随之更新。
- 退出时，`session.signOut()` 清除内存中的 access token，退出接口清除 cookie。正在看的页面需要登录时，应用随即跳到登录页。
- 登录、退出时，TanStack Query 中缓存的数据也被清空，上一个作者的数据不会出现在下一个作者的页面上。

最后，首页在没有登录时不显示编辑和删除。把 `apps/web/src/routes/index.tsx` 改成下面这样：

```tsx title="apps/web/src/routes/index.tsx"
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { createFileRoute, Link } from "@tanstack/react-router"
import { useSignedIn } from "@go-tenon/shell"
import type { Article } from "@blog/client/api"
import { api } from "../client"

export const Route = createFileRoute("/")({ component: ArticleList })

function ArticleList() {
  const signedIn = useSignedIn()
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
          {signedIn && (
            <div>
              <Link to="/articles/$id/edit" params={{ id: a.id }}>
                编辑
              </Link>
              <button onClick={() => confirm(`删除“${a.title}”？`) && remove.mutate(a)}>删除</button>
            </div>
          )}
        </li>
      ))}
    </ul>
  )
}
```

打开 http://localhost:8080/write，页面先跳到登录页。用刚才创建的账号登录，回到了写文章的页面。写一篇文章，再刷新页面，仍然是登录状态：页面用 cookie 换到了新的 access token。退出后，编辑按钮消失了。

<a id="troubleshooting"></a>
## 常见问题

**启动时报错缺少 `APP_KEY`**

还没有生成密钥，或者 `.env` 不在项目根目录。按[配置](#configuration)一节生成。

**`user create` 报错密码不符合要求**

默认的规则是：8 到 128 个字符，首尾不能有空格。换一个符合规则的密码。

**登录后刷新页面，又变成未登录**

refresh token 的 cookie 没有带上。检查是否通过 8080 端口访问页面：cookie 只在同源的请求中发送，直接访问前端开发服务器的端口时就没有。

**登录时返回 `429`**

同一个邮箱 15 分钟内输错了 5 次密码，等一会儿再试。

<a id="summary"></a>
## 小结

这一章，你：

- 了解了 access token 和 refresh token 的分工：一个短期有效、放在内存中，一个长期有效、放在脚本读不到的 cookie 中；
- 用 `login` 保存邮箱和密码，并限制输错的次数；
- 写了一个创建作者的命令，它和接口调用同一个 `Handle`；
- 用 `requireUser` 保护了写文章的接口；
- 在前端加上了登录页，需要登录的页面先跳到登录页。

作者现在可以登录了。不过，任何作者都能修改别人的文章，所有文章写完就立即公开。下一章，我们给文章加上作者和草稿。

深入了解：[登录与注册](../security/login.md)、[API 令牌](../security/api-tokens.md)、[认证](../security/authentication.md)、[应用骨架](../frontend/app-shell.md)、[命令行](../general/commands.md)。
