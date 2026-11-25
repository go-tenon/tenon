# 8. 后台：作者与角色

这一章给博客加上运营后台：管理员创建角色（一组权限）并分配给作者，作者能做什么由他的角色决定。作者和角色在 Go 中声明为资源，`tenon generate` 把声明生成到前端，后台的页面按声明渲染。

包：`authz`、`httpx/rest`、`@go-tenon/ui`。

<a id="permissions"></a>
## 权限与角色

列出博客的全部权限。新建 `internal/auth/permissions.go`：

```go title="internal/auth/permissions.go"
package auth

import (
    "github.com/danielgtaylor/huma/v2"

    "github.com/go-tenon/tenon/authz"
    "github.com/go-tenon/tenon/contract"
    "github.com/go-tenon/tenon/enum"
)

// Permission 是一个权限，例如 users:manage。
type Permission string

const (
    UsersManage Permission = "users:manage"
    RolesManage Permission = "roles:manage"
)

// Permissions 是全部权限，源语言默认显示文字写在定义处。
var Permissions = enum.New("permission",
    enum.Item(UsersManage, "管理作者"),
    enum.Item(RolesManage, "管理角色"),
)

// Schema 让 OpenAPI 中的 Permission 只接受上面的取值：角色表单中是带显示文字的多选。
func (Permission) Schema(r huma.Registry) *huma.Schema { return contract.EnumSchema(r, Permissions) }

// Catalog 是权限目录：authz 只接受其中的权限，它也写入 OpenAPI 文档。
var Catalog = authz.Catalog{
    {Name: string(UsersManage), Description: "管理作者"},
    {Name: string(RolesManage), Description: "管理角色"},
}
```

- `Catalog` 是[权限目录](../security/authorization.md#permission-catalog)：角色包含目录以外的权限时返回 422。
- `Permission` 是[枚举](../http/requests.md#enums)，角色表单中显示为多选。

角色和分配保存在 [`authz`](../security/authorization.md#roles) 的表中。在 `cmd/blog/main.go` 的 `migrate publish` 中加上 `authz.Migrations()`：

```go title="cmd/blog/main.go"
// Command blog 是博客的入口。
package main

import (
    "github.com/spf13/cobra"

    "github.com/go-tenon/tenon/apitoken"
    "github.com/go-tenon/tenon/authz"
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
        authz.Migrations(),
    ))
    return cmd
}
```

```bash
go mod tidy
go run ./cmd/blog migrate publish
go run ./cmd/blog migrate up
```

<a id="roles"></a>
## 角色的接口

角色的接口放在新的模块 `internal/role` 中，直接调用 `authz.Store`。新建 `internal/role/role.go`：

```go title="internal/role/role.go"
// Package role 是角色的管理接口：角色是一组权限，分配给作者。数据保存在 authz 的表中。
package role

import (
    "context"
    "net/http"

    "github.com/danielgtaylor/huma/v2"

    "github.com/go-tenon/tenon/authz"
    "github.com/go-tenon/tenon/httpx/rest"
    "github.com/go-tenon/tenon/page"

    "example.com/blog/internal/auth"
)

// Role 是返回给客户端的角色。
type Role struct {
    ID          string            `json:"id"`
    Name        string            `json:"name"`
    Description string            `json:"description"`
    Permissions []auth.Permission `json:"permissions"`
}

// Changes 是创建和修改角色时提交的内容。
type Changes struct {
    Name        string            `json:"name" minLength:"1" maxLength:"50"`
    Description string            `json:"description" maxLength:"200" required:"false"`
    Permissions []auth.Permission `json:"permissions" minItems:"1"`
}

type ListInput struct {
    page.OffsetParams
}

type ListOutput struct {
    Body page.Offset[Role]
}

type CreateInput struct {
    Body Changes
}

type UpdateInput struct {
    ID   string `path:"id"`
    Body Changes
}

type IDInput struct {
    ID string `path:"id"`
}

type RoleOutput struct {
    Body Role
}

type handlers struct {
    roles *authz.Store
}

func toRole(r authz.Role) Role {
    perms := make([]auth.Permission, len(r.Permissions))
    for i, p := range r.Permissions {
        perms[i] = auth.Permission(p)
    }
    return Role{ID: r.ID, Name: r.Name, Description: r.Description, Permissions: perms}
}

func (c Changes) role(id string) authz.Role {
    perms := make([]string, len(c.Permissions))
    for i, p := range c.Permissions {
        perms[i] = string(p)
    }
    return authz.Role{ID: id, Name: c.Name, Description: c.Description, Permissions: perms}
}

// list 返回全部角色。角色不多，在内存中分页。
func (h *handlers) list(ctx context.Context, in *ListInput) (*ListOutput, error) {
    all, err := h.roles.Roles(ctx, "")
    if err != nil {
        return nil, err
    }
    start := min(int(in.Offset()), len(all))
    end := min(start+in.PerPage, len(all))
    items := make([]Role, 0, end-start)
    for _, r := range all[start:end] {
        items = append(items, toRole(r))
    }
    return &ListOutput{Body: page.NewOffset(items, in.OffsetParams, int64(len(all)))}, nil
}

func (h *handlers) create(ctx context.Context, in *CreateInput) (*RoleOutput, error) {
    r, err := h.roles.CreateRole(ctx, in.Body.role(""))
    if err != nil {
        return nil, err
    }
    return &RoleOutput{Body: toRole(r)}, nil
}

func (h *handlers) update(ctx context.Context, in *UpdateInput) (*RoleOutput, error) {
    r, err := h.roles.UpdateRole(ctx, in.Body.role(in.ID), "")
    if err != nil {
        return nil, err
    }
    return &RoleOutput{Body: toRole(r)}, nil
}

// delete 删除角色，同时撤销它的分配。
func (h *handlers) delete(ctx context.Context, in *IDInput) (*struct{}, error) {
    return nil, h.roles.DeleteRole(ctx, in.ID, "")
}

// RegisterRoutes 注册角色的接口，并声明角色的后台页面。require 要求 roles:manage 权限。
func RegisterRoutes(api *rest.API, require func(*huma.Operation), roles *authz.Store) {
    h := &handlers{roles: roles}
    op := func(id, method, path, summary string) huma.Operation {
        o := huma.Operation{OperationID: id, Method: method, Path: path, Summary: summary}
        require(&o)
        return o
    }
    rest.Register(api, op("role-list", http.MethodGet, "/roles", "角色列表"), h.list)
    create := op("role-create", http.MethodPost, "/roles", "创建角色")
    create.DefaultStatus = http.StatusCreated
    rest.Register(api, create, h.create)
    rest.Register(api, op("role-update", http.MethodPut, "/roles/{id}", "修改角色"), h.update)
    rest.Register(api, op("role-delete", http.MethodDelete, "/roles/{id}", "删除角色"), h.delete)

    // 角色的后台页面：列、表单字段的类型和校验规则都来自上面的接口，标题与字段显示名直接写在声明中。
    api.Resource(rest.Resource{
        Name: "role", Title: "角色", List: "role-list", Create: "role-create", Update: "role-update", Delete: "role-delete",
        Columns: []string{"name", "description", "permissions"},
        Labels:  map[string]string{"name": "名称", "description": "说明", "permissions": "权限"},
    })
}
```

- `api.Resource` 把这几个接口声明为[资源](../frontend/resources.md)，只写列表的列；表单字段和校验规则来自请求体 `Changes`，每个操作的权限来自注册时的声明。
- `Roles`、`UpdateRole`、`DeleteRole` 的最后一个参数是租户，`""` 表示不按租户限制。
- `authz` 的错误（例如已有同名角色）自带翻译，直接返回。

<a id="users"></a>
## 作者的接口

后台列出作者，修改作者的名字和角色。新建 `internal/user/admin.go`：

```go title="internal/user/admin.go"
package user

import (
    "context"
    "slices"
    "time"

    "github.com/uptrace/bun"

    "github.com/go-tenon/tenon/authz"
    "github.com/go-tenon/tenon/page"
)

// Author 是后台列表中的作者：Roles 是角色的 ID。
type Author struct {
    ID        string    `json:"id"`
    Name      string    `json:"name"`
    Roles     []string  `json:"roles"`
    CreatedAt time.Time `json:"created_at"`
}

type ListInput struct {
    page.OffsetParams
    Name string `query:"name" doc:"名字包含的文字"`
}

type ListOutput struct {
    Body page.Offset[Author]
}

type UpdateInput struct {
    ID   string `path:"id"`
    Body struct {
        Name  string   `json:"name" minLength:"1" maxLength:"50"`
        Roles []string `json:"roles"`
    }
}

type AuthorOutput struct {
    Body Author
}

// Admin 是后台管理作者的动作：列出作者，修改名字和角色。
type Admin struct {
    db    *bun.DB
    roles *authz.Store
}

func NewAdmin(db *bun.DB, roles *authz.Store) *Admin { return &Admin{db: db, roles: roles} }

func (a *Admin) author(ctx context.Context, u *User) (Author, error) {
    roles, err := a.roles.RolesOf(ctx, u.Ref(), "")
    if err != nil {
        return Author{}, err
    }
    ids := make([]string, len(roles))
    for i, r := range roles {
        ids[i] = r.ID
    }
    return Author{ID: u.ID, Name: u.Name, Roles: ids, CreatedAt: u.CreatedAt}, nil
}

// List 按创建时间列出作者，name 不为空时只列出名字包含它的作者。
func (a *Admin) List(ctx context.Context, in *ListInput) (*ListOutput, error) {
    var users []User
    q := a.db.NewSelect().Model(&users).Order("created_at").Limit(in.Limit()).Offset(in.Offset())
    if in.Name != "" {
        q = q.Where("name ILIKE ?", "%"+in.Name+"%")
    }
    total, err := q.ScanAndCount(ctx)
    if err != nil {
        return nil, err
    }
    items := make([]Author, 0, len(users))
    for i := range users {
        author, err := a.author(ctx, &users[i])
        if err != nil {
            return nil, err
        }
        items = append(items, author)
    }
    return &ListOutput{Body: page.NewOffset(items, in.OffsetParams, int64(total))}, nil
}

// Update 修改作者的名字和角色：分配新加的角色，撤销去掉的角色。权限在下一个请求生效。
func (a *Admin) Update(ctx context.Context, in *UpdateInput) (*AuthorOutput, error) {
    u, err := find(ctx, a.db, in.ID)
    if err != nil {
        return nil, err
    }
    u.Name = in.Body.Name
    if _, err := a.db.NewUpdate().Model(u).Column("name", "updated_at").WherePK().Exec(ctx); err != nil {
        return nil, err
    }
    current, err := a.author(ctx, u)
    if err != nil {
        return nil, err
    }
    for _, id := range in.Body.Roles {
        if !slices.Contains(current.Roles, id) {
            if err := a.roles.Assign(ctx, u.Ref(), "", id); err != nil {
                return nil, err
            }
        }
    }
    for _, id := range current.Roles {
        if !slices.Contains(in.Body.Roles, id) {
            if err := a.roles.Unassign(ctx, u.Ref(), "", id); err != nil {
                return nil, err
            }
        }
    }
    current.Name, current.Roles = u.Name, in.Body.Roles
    return &AuthorOutput{Body: current}, nil
}
```

权限在每个请求中按作者当前的角色加载，修改角色在下一个请求生效，作者不用重新登录。

后台按当前作者的权限显示菜单和按钮。修改 `internal/user/me.go`，返回中加上权限：

```go title="internal/user/me.go"
package user

import (
    "context"
    "time"

    "github.com/uptrace/bun"

    "github.com/go-tenon/tenon/identity"
)

// Me 返回当前登录的作者，以及他的权限：后台据此显示菜单和按钮。
type Me struct{ db *bun.DB }

func NewMe(db *bun.DB) *Me { return &Me{db: db} }

type Profile struct {
    ID          string    `json:"id"`
    Name        string    `json:"name"`
    CreatedAt   time.Time `json:"created_at"`
    Permissions []string  `json:"permissions"`
}

type ProfileOutput struct {
    Body Profile
}

func (a *Me) HTTP(ctx context.Context, _ *struct{}) (*ProfileOutput, error) {
    sub, _ := identity.FromContext(ctx)
    u, err := find(ctx, a.db, sub.ID)
    if err != nil {
        return nil, err
    }
    return &ProfileOutput{Body: Profile{ID: u.ID, Name: u.Name, CreatedAt: u.CreatedAt, Permissions: sub.Permissions}}, nil
}
```

在 `internal/user/routes.go` 中注册后台的接口，并把作者声明为资源。作者仍然用命令创建，资源中只有列表和修改：

```go title="internal/user/routes.go"
package user

import (
    "net/http"

    "github.com/danielgtaylor/huma/v2"
    "github.com/uptrace/bun"

    "github.com/go-tenon/tenon/authz"
    "github.com/go-tenon/tenon/httpx/rest"
)

// RegisterRoutes 注册作者的接口。requireUser 要求登录，requireManage 还要求 users:manage 权限。
func RegisterRoutes(api *rest.API, requireUser, requireManage func(*huma.Operation), db *bun.DB, roles *authz.Store) {
    me := huma.Operation{OperationID: "user-me", Method: http.MethodGet, Path: "/me", Summary: "当前登录的作者"}
    requireUser(&me)
    rest.Register(api, me, NewMe(db).HTTP)

    admin := NewAdmin(db, roles)
    list := huma.Operation{OperationID: "user-list", Method: http.MethodGet, Path: "/users", Summary: "作者列表"}
    requireManage(&list)
    rest.Register(api, list, admin.List)
    update := huma.Operation{OperationID: "user-update", Method: http.MethodPut, Path: "/users/{id}", Summary: "修改作者"}
    requireManage(&update)
    rest.Register(api, update, admin.Update)

    // 作者的后台页面：作者用命令创建，后台只列出和修改。
    api.Resource(rest.Resource{
        Name: "user", Title: "作者", List: "user-list", Update: "user-update",
        Columns: []string{"name", "roles", "created_at"}, Filters: []string{"name"},
        Labels:  map[string]string{"name": "名字", "roles": "角色", "created_at": "注册时间"},
    })
}
```

第一个管理员用命令设置。新建 `internal/user/admin_command.go`：

```go title="internal/user/admin_command.go"
package user

import (
    "fmt"
    "slices"

    "github.com/spf13/cobra"

    "github.com/go-tenon/tenon/authz"
    "github.com/go-tenon/tenon/login"

    "example.com/blog/internal/auth"
)

// adminRole 是 user admin 创建的角色，拥有全部权限。
const adminRole = "管理员"

// MakeAdmin 把作者设为管理员：没有“管理员”角色时先创建它，然后分配给这个作者。
func MakeAdmin(logins *login.Realm, roles *authz.Store) func(*cobra.Command, []string) error {
    return func(cmd *cobra.Command, _ []string) error {
        ctx := cmd.Context()
        email, _ := cmd.Flags().GetString("email")
        ref, err := logins.Lookup(ctx, login.Email(email))
        if err != nil {
            return err
        }
        all, err := roles.Roles(ctx, "")
        if err != nil {
            return err
        }
        i := slices.IndexFunc(all, func(r authz.Role) bool { return r.Name == adminRole })
        var role authz.Role
        if i >= 0 {
            role = all[i]
        } else {
            perms := make([]string, 0, len(auth.Catalog))
            for _, p := range auth.Catalog {
                perms = append(perms, p.Name)
            }
            if role, err = roles.CreateRole(ctx, authz.Role{Name: adminRole, Description: "全部权限", Permissions: perms}); err != nil {
                return err
            }
        }
        if err := roles.Assign(ctx, ref, "", role.ID); err != nil {
            return err
        }
        _, err = fmt.Fprintln(cmd.OutOrStdout(), email, "现在是管理员")
        return err
    }
}
```

在 `internal/user/commands.go` 中加上这个命令：

```go title="internal/user/commands.go"
package user

import (
    "github.com/spf13/cobra"
    "github.com/uptrace/bun"

    "github.com/go-tenon/tenon/authz"
    "github.com/go-tenon/tenon/command"
    "github.com/go-tenon/tenon/login"
)

// Commands 声明 user 模块的命令。
func Commands(db *bun.DB, logins *login.Realm, roles *authz.Store) *cobra.Command {
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
    admin := &cobra.Command{
        Use:   "admin",
        Short: "把作者设为管理员",
        Args:  command.NoArgs,
        RunE:  MakeAdmin(logins, roles),
    }
    admin.Flags().String("email", "", "作者登录用的邮箱")
    _ = admin.MarkFlagRequired("email")
    group.AddCommand(create, admin)
    return group
}
```

<a id="wiring"></a>
## 装配

在 `internal/bootstrap/app.go` 中创建 `authz.Store` 保存到 `App` 中，`internal/bootstrap/commands.go` 把它交给作者的命令：

```go title="internal/bootstrap/app.go"
package bootstrap

import (
    "context"
    "log/slog"
    "time"

    "github.com/uptrace/bun"

    "github.com/go-tenon/tenon/apitoken"
    "github.com/go-tenon/tenon/authz"
    "github.com/go-tenon/tenon/cleanup"
    "github.com/go-tenon/tenon/command"
    "github.com/go-tenon/tenon/crypt"
    "github.com/go-tenon/tenon/database"
    "github.com/go-tenon/tenon/hashing"
    "github.com/go-tenon/tenon/logging"
    "github.com/go-tenon/tenon/login"
    "github.com/go-tenon/tenon/ratelimit"
    "github.com/go-tenon/tenon/ratelimit/pglimit"

    "example.com/blog/internal/auth"
    "example.com/blog/internal/user"
)

// App 是各个运行部分共用的资源，每个进程只装配一次。
type App struct {
    cfg Config
    db  *bun.DB
    // users 校验作者的邮箱和密码，tokens 签发和校验登录凭据。
    users  *login.Realm
    tokens *apitoken.Store
    // roles 是作者的角色和权限。
    roles *authz.Store
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

    return &App{
        cfg: cfg, db: db,
        users: users, tokens: tokens,
        roles: authz.NewStore(db, auth.Catalog),
    }, nil
}

// Parts 是常驻进程的运行部分：serve 运行全部部分，serve <名字> 只运行其中一个。
func Parts(a *App) []command.Part {
    return []command.Part{
        {Name: "http", Short: "接口和页面", Start: a.HTTP},
    }
}
```

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
        return []*cobra.Command{user.Commands(a.db, a.users, a.roles)}
    })
}
```

在 `internal/bootstrap/http.go` 中，作者的 Resolver 换成 `a.roles.Resolve`，后台的接口用带权限的 `rest.RequireToken`，缺少权限时返回 `403`：

```go title="internal/bootstrap/http.go"
package bootstrap

import (
    "context"
    "net/http"

    "github.com/go-chi/chi/v5"

    "github.com/go-tenon/tenon/command"
    "github.com/go-tenon/tenon/command/httpcmd"
    "github.com/go-tenon/tenon/config"
    "github.com/go-tenon/tenon/frontend"
    "github.com/go-tenon/tenon/health"
    "github.com/go-tenon/tenon/httpx"
    "github.com/go-tenon/tenon/httpx/rest"
    "github.com/go-tenon/tenon/identity"

    "example.com/blog/apps"
    "example.com/blog/internal/article"
    "example.com/blog/internal/auth"
    "example.com/blog/internal/role"
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
            return httpx.Serve(ctx, a.cfg.HTTP, ln, router, httpcmd.ServeOptions(d)...)
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
        // Accounts 声明接口接受哪些账号的登录凭据：博客只有作者一种，每个请求按作者当前的角色加载权限。
        // Permissions 是权限目录，它写入 OpenAPI，生成的客户端据此得到权限常量。
        api := rest.New(r, rest.Options{Title: "Blog API", Version: "1.0.0", BasePath: "/api",
            Accounts: identity.Accounts{user.Type: a.roles.Resolve}, Permissions: auth.Catalog})
        // requireUser 要求请求带着作者的登录凭据：浏览器的会话 cookie，或者 Authorization: Bearer <token>
        requireUser := rest.RequireToken(api, a.tokens.Validate)
        // 后台的接口还要求权限，缺少时返回 403。
        requireUsers := rest.RequireToken(api, a.tokens.Validate, string(auth.UsersManage))
        requireRoles := rest.RequireToken(api, a.tokens.Validate, string(auth.RolesManage))
        auth.RegisterRoutes(api, requireUser, a.users, a.tokens, a.cfg.Token.Lifetime)
        user.RegisterRoutes(api, requireUser, requireUsers, a.db, a.roles)
        role.RegisterRoutes(api, requireRoles, a.roles)
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

把小明设为管理员：

```bash
go run ./cmd/blog user admin --email ming@example.com
```

它创建拥有全部权限的“管理员”角色，分配给小明。

<a id="admin"></a>
## 后台

后台是挂在 `/admin/` 的独立前端。用 `frame` 布局（顶栏、侧栏菜单和多标签）新建它，默认样式预设是紧凑的 `compact`，见[布局与导航](../frontend/layout.md)和[样式预设与主题](../frontend/app.md#presets)：

```bash
go tool tenon add frontend admin --layout frame
```

它在 `apps/admin` 中生成登录页、`FrameLayout` 布局和首页，只在浏览器中渲染（`apps/admin/package.json` 中 `"ssr": false`）。`internal/bootstrap/http.go` 不用改。重新生成客户端：

```bash
go tool tenon generate
```

`packages/client/src/api/resources.ts` 中多了 `user` 和 `role` 两个资源。新建 `apps/admin/src/resources.ts`，把它们和生成的客户端接在一起：

```ts title="apps/admin/src/resources.ts"
import { resourceModel } from "@go-tenon/ui"
import { createClient } from "@blog/client/api"
import { createKit } from "@blog/client/api/kit"
import { apiFetch } from "@go-tenon/start"
import { session } from "./auth"

// api 是生成的客户端。登录凭据是浏览器自动带上的会话 cookie；受保护的接口返回 401 时，会话随之变为未登录。
export const api = createClient({ baseURL: "/api", fetch: apiFetch, session })

// kit 是生成的客户端和元数据：查询选项、校验规则、验证提示、枚举的显示文字和资源。
// scope 让查询的缓存按登录的账号区分；字段名、枚举和验证提示按简体中文显示。
const kit = createKit(api, { scope: session.scope, language: "zh-CN" })
export const users = resourceModel(kit.resources.user, kit)
export const roles = resourceModel(kit.resources.role, kit)
```

角色的页面只有声明。新建 `apps/admin/src/routes/_app/roles.tsx`：

```tsx title="apps/admin/src/routes/_app/roles.tsx"
import { createFileRoute } from "@tanstack/react-router"
import { ResourcePage } from "@go-tenon/ui"
import { roles } from "../../resources"

export const Route = createFileRoute("/_app/roles")({
  staticData: { title: "角色", icon: "safety", permission: "roles:manage" },
  validateSearch: roles.listSearch,
  component: () => <ResourcePage model={roles} route={Route} form="modal" />,
})
```

- `staticData.title` 直接写菜单标题。
- `ResourcePage` 按资源渲染列表、表单和删除确认。
- `form="modal"` 在对话框中填写表单；默认是单独的页面，要另加两个路由，见[资源页面](../frontend/resources.md#admin)。
- 没有 `roles:manage` 的作者看不到这个菜单。

作者的页面只覆盖角色字段：接口中是角色的 ID，页面上显示和选择角色的名称。新建 `apps/admin/src/routes/_app/users.tsx`：

```tsx title="apps/admin/src/routes/_app/users.tsx"
import { Badge, Group, MultiSelect } from "@mantine/core"
import { useQuery } from "@tanstack/react-query"
import { createFileRoute } from "@tanstack/react-router"
import { type FieldInput, ResourcePage } from "@go-tenon/ui"
import { api, users } from "../../resources"

export const Route = createFileRoute("/_app/users")({
  staticData: { title: "作者", icon: "team", permission: "users:manage" },
  validateSearch: users.listSearch,
  component: Users,
})

// 全部角色：角色那一列显示名称，表单中从角色里选择。
function useRoles() {
  return useQuery({ queryKey: ["roles", "all"], queryFn: () => api.role.list({ per_page: 100 }) })
}

function RoleBadges({ ids }: { ids: string[] }) {
  const all = useRoles().data?.items ?? []
  return (
    <Group gap={4}>
      {ids.map((id) => (
        <Badge key={id} variant="light">
          {all.find((r) => r.id === id)?.name ?? id}
        </Badge>
      ))}
    </Group>
  )
}

const RoleSelect: FieldInput = ({ field, label, value, onChange, error, disabled }) => {
  const all = useRoles().data?.items ?? []
  return (
    <MultiSelect
      label={label}
      required={field.required}
      error={error}
      disabled={disabled}
      value={(value as string[]) ?? []}
      onChange={onChange}
      data={all.map((r) => ({ value: r.id, label: r.name }))}
    />
  )
}

// 作者的页面按 Go 中的声明渲染，只覆盖角色这个字段：接口中是角色的 ID，页面上显示和选择名称。
function Users() {
  return (
    <ResourcePage
      model={users}
      route={Route}
      render={{ roles: (ids) => <RoleBadges ids={(ids as string[]) ?? []} /> }}
      inputs={{ roles: RoleSelect }}
      form="modal"
    />
  )
}
```

运行 `go tool tenon dev`，打开 http://localhost:8080/admin/，用小明登录：

1. 在“角色”中新建“编辑”角色，权限选“管理角色”；
2. 在“作者”中把小红的角色改为“编辑”；
3. 用小红登录：侧栏中有“角色”，没有“作者”；请求 `/api/users` 返回 `403`。

- 侧栏中没有菜单：当前作者没有任何权限，用 `user admin --email` 设为管理员。
- 页面上显示 `user.fields.name` 这样的键：翻译文件缺少它，补上后运行 `go tool tenon generate`。
- 修改角色后，刷新页面菜单才更新。
