# 业务动作

业务逻辑按业务动作（action）组织：一个用例是一个类型，单独放在一个文件中，例如“发布文章”是 `internal/article/publish.go` 中的 `article.Publish`。这个文件包含它的依赖、业务规则，以及 HTTP 接口、队列任务、命令行等各个入口方法。

- 业务规则只写在 `Handle` 中，入口方法只做转换。
- 构造函数和结构体的字段只保存依赖，执行时才使用它们，见[规则](#conventions)。

<a id="writing-actions"></a>
## 编写动作：结构体、`NewXxx` 和 `Handle`

一个动作由保存依赖的结构体、构造函数和执行业务的 `Handle` 方法组成。`Article`、`ErrNotFound`、`CanEdit` 定义在同一个包的 `article.go` 中，见[模块共用的代码](#shared-code)：

```go
// Publish 发布文章。
type Publish struct {
    db *bun.DB
}

func NewPublish(db *bun.DB) *Publish {
    return &Publish{db: db}
}

// Handle 发布一篇草稿：只有作者能发布，每篇只发布一次。
func (a *Publish) Handle(ctx context.Context, subject identity.Subject, id uuid.UUID) (*Article, error) {
    item := new(Article)
    err := database.RunInTx(ctx, a.db, nil, func(ctx context.Context, tx database.Tx) error {
        err := tx.NewSelect().Model(item).Where("id = ?", id).For("UPDATE").Scan(ctx)
        if errors.Is(err, sql.ErrNoRows) {
            return ErrNotFound.New()
        }
        if err != nil {
            return errors.Wrap(err, "load article")
        }
        if !CanEdit(subject, item) {
            return ErrForbidden.New()
        }
        if item.Status == StatusPublished {
            return ErrAlreadyPublished.New()
        }
        now := time.Now().UTC()
        item.Status, item.PublishedAt = StatusPublished, &now
        _, err = tx.NewUpdate().Model(item).Column("status", "published_at", "updated_at").WherePK().Exec(ctx)
        return errors.Wrap(err, "publish article")
    })
    if err != nil {
        return nil, err
    }
    return item, nil
}
```

- 调用者的身份 `subject` 作为参数显式传入：HTTP 接口从 context 中取出它，队列任务、命令行和测试传入自己构造的身份，见[授权](../security/authorization.md#ownership)。
- 业务错误原样返回，由各个入口转换为状态码或退出码，见[错误处理](../general/errors.md)。

<a id="shared-code"></a>
## 模块共用的代码：`<模块>.go`

几个动作共用的模型、业务错误、提交的数据结构、查询和授权函数放在与包同名的文件中，例如 `article.go`：

```go
var (
    ErrNotFound         = errorx.Define(errorx.NotFound, "article.not_found")
    ErrForbidden        = errorx.Define(errorx.PermissionDenied, "article.forbidden")
    ErrAlreadyPublished = errorx.Define(errorx.Conflict, "article.already_published")
)

// Changes 是创建和修改文章时提交的内容。
type Changes struct {
    Title   string `json:"title" minLength:"1" maxLength:"200"`
    Content string `json:"content"`
}

// CanEdit 报告 subject 能否修改 item，页面也用它决定是否显示编辑按钮。
func CanEdit(subject identity.Subject, item *Article) bool {
    return subject.Can("articles:admin") || item.AuthorID == subject.ID
}
```

- 只在包内共用的查询函数（例如按 ID 读取的 `find`）不导出；页面也要用的授权规则导出，见[授权函数](../security/authorization.md#authorization-functions)。
- 模型见[定义模型](../database/database.md#defining-models)，业务错误见[定义业务错误](../general/errors.md#defining-errors)。

<a id="entry-points"></a>
## 入口方法

每个入口是动作上的一个方法，在模块的登记函数中作为方法值传入。入口方法取出参数和调用者，调用 `Handle`，把结果转换为这个入口的输出。动作在登记时构造一次，之后它的方法被并发调用。

| 入口 | 方法 | 登记在 |
| --- | --- | --- |
| HTTP 接口 | `HTTP(ctx, *Input) (*Output, error)` | `routes.go` 的 `RegisterRoutes` |
| 队列任务 | `Job(ctx, payload) error` | `jobs.go` 的 `RegisterJobs` |
| 定时任务 | `func(ctx) error`，通常是 `Handle` 本身 | `tasks.go` 的 `RegisterTasks` |
| 命令行 | `Command(cmd *cobra.Command, args []string) error` | `commands.go` 的 `Commands` |
| MCP 工具 | `Tool(ctx, Args) (*Out, error)` | `tools.go` |
| RPC | `RPC(ctx, Args) (*Out, error)` | `rpc.go` 的 `RegisterRPC` |

<a id="as-http"></a>
### HTTP 接口：`HTTP` 方法

`ArticleOutput` 和 `toDTO` 是模块的响应结构和转换函数，见[共享响应结构](../http/routing.md#shared-responses)：

```go
type PublishInput struct {
    ID uuid.UUID `path:"id"`
}

// HTTP 处理 POST /articles/{id}/publish。
func (a *Publish) HTTP(ctx context.Context, in *PublishInput) (*ArticleOutput, error) {
    subject, _ := identity.FromContext(ctx)
    item, err := a.Handle(ctx, subject, in.ID)
    if err != nil {
        return nil, err
    }
    return &ArticleOutput{Body: toDTO(item)}, nil
}
```

模块的 `routes.go` 声明路由的 `huma.Operation`（路径、方法、访问策略），用 `rest.Register(api, op, NewPublish(db).HTTP)` 注册，`api` 和 `db` 是 `RegisterRoutes` 的参数，见[模块内注册](../http/routing.md#module-registration)。

<a id="as-jobs"></a>
### 队列任务：`Job` 方法与 `queue.Topic`

`queue.Topic` 把 topic 名称和 payload 类型绑在一起，发布方和处理方共用这一个声明。在 `publish.go` 中：

```go
// PublishAt 是定时发布的任务，payload 是文章 ID。
var PublishAt = queue.Topic[uuid.UUID]("articles.publish")

// system 是后台调用者，CanEdit 允许它发布任何文章。
var system = identity.Subject{Type: identity.System, ID: "system", Permissions: []string{"articles:admin"}}

// Job 处理定时发布的任务。文章已经发布时任务视为完成，重复执行是安全的。
func (a *Publish) Job(ctx context.Context, id uuid.UUID) error {
    _, err := a.Handle(ctx, system, id)
    if errors.Is(err, ErrAlreadyPublished) {
        return nil
    }
    return err
}
```

- `Job` 要能安全地重复执行：失败的任务会重试。
- 其他错误原样返回：`ErrNotFound` 这类业务错误不重试，任务直接转入失败任务表；数据库故障这类系统错误稍后重试，见[失败与重试](../background/queue.md#failures-and-retries)。
- 多租户应用中 payload 要带上租户，`system` 按租户构造，见[后台任务](../security/tenancy.md#tenant-jobs)。

设置定时发布的动作在写入发布时间的事务 `tx` 中发布任务，事务提交后才投递：`PublishAt.Publish(ctx, tx, item.ID, queue.At(publishAt))`。模块的 `jobs.go` 登记处理方法，worker 装配时调用它，见[启动 worker](../background/queue.md#running-workers)：

```go
// RegisterJobs 登记文章模块的队列任务。
func RegisterJobs(w *queue.Worker, db *bun.DB) {
    PublishAt.Handle(w, NewPublish(db).Job)
}
```

<a id="as-scheduled-tasks"></a>
### 定时任务：`schedule.Scheduler.Add`

签名为 `func(ctx context.Context) error` 的 `Handle` 直接登记。例如每分钟发布到期文章的 `PublishScheduled`，在模块的 `tasks.go` 中：

```go
// RegisterTasks 登记文章模块的定时任务。
func RegisterTasks(s *schedule.Scheduler, db *bun.DB) {
    s.Add("articles:publish-scheduled", "* * * * *", NewPublishScheduled(db).Handle,
        schedule.Describe("发布到了预定时间的文章"))
}
```

调度器装配时调用 `RegisterTasks`，见[定时任务](../background/scheduling.md#defining-tasks)。

<a id="as-commands"></a>
### 命令行：`Command` 方法

`Command` 的签名是 Cobra 的 `RunE`：解析 `args`，以[队列任务](#as-jobs)一节中声明的后台调用者 `system` 调用 `Handle`，把结果写到 `cmd.OutOrStdout()`。参数错误返回 `command.AsExitError(command.ExitUsage, err)`；业务错误原样返回，框架按分类转换为[退出码](../general/commands.md#exit-codes)。

模块的 `commands.go` 只声明命令名、参数和选项：

```go
// Commands 声明 article 模块的命令。
func Commands(db *bun.DB) *cobra.Command {
    group := &cobra.Command{Use: "article", Short: "管理文章"}
    group.AddCommand(&cobra.Command{
        Use:   "publish <id>",
        Short: "立即发布一篇文章",
        Args:  command.ExactArgs(1),
        RunE:  NewPublish(db).Command,
    })
    return group
}
```

`bootstrap` 用 `command.Bind` 调用 `Commands`，执行命令时才打开数据库，见[只在执行时装配的命令](dependencies.md#deferred-commands)。

<a id="as-tools"></a>
### MCP 工具：`Tool` 方法

AI 客户端调用的入口，写法与 `HTTP` 相同：从 context 中取出调用者，调用 `Handle`，返回 `*ArticleDTO`。参数类型（例如 `PublishArgs`）的字段标签与 REST 的请求体相同。工具的名称、说明和需要的权限在模块的 `tools.go` 中登记，见 [MCP 服务](../ai/mcp.md)。

<a id="as-rpc"></a>
### RPC：`RPC` 方法

其他服务经 NATS 同步调用的入口，签名和写法与 `Tool` 相同。端点名、需要的权限和是否幂等在模块的 `RegisterRPC` 中声明，见[提供和调用端点](../rpc/endpoints.md)。

<a id="direct-calls"></a>
### 页面、其他动作和脚本：直接调用 `Handle`

其他代码直接调用 `Handle`，例如[填充数据](../database/migrations.md#seeding)的 `seed` 命令中，`db` 是打开的连接池，`alice` 是构造的用户身份：

```go
item, err := article.NewCreate(db).Handle(ctx, alice, changes)
```

一个动作要用另一个动作时，构造函数接收它（例如 `NewImport(create *Create)`），再调用它的 `Handle`，见[导入文章](../features/import-export.md#import-action)。

<a id="follow-ups"></a>
## 同步的后续动作：钩子

其他模块要在一个动作完成后跟着做事时（例如文章发布后积分模块给作者加积分），动作声明一个钩子类型，由 `bootstrap` 把其他模块的函数传进来。`article` 不导入 `points`，发布文章会触发什么都写在 `bootstrap` 中。

发布之前必须先通过的检查（例如内容审核）是动作的依赖：在 `article` 中声明一个小接口，由构造函数接收，见[构造函数注入](dependencies.md#constructor-injection)。

<a id="declaring-hooks"></a>
### 声明钩子

在 `publish.go` 中声明钩子类型，构造函数接收一组钩子，`Handle` 在事务中更新文章之后依次调用它们，任何一个返回错误就返回：

```go
// PublishedHook 在发布文章的事务中执行，此时文章已经更新。返回错误时整个发布回滚。
type PublishedHook func(ctx context.Context, tx database.Tx, item *Article) error

type Publish struct {
    db          *bun.DB
    onPublished []PublishedHook
}

func NewPublish(db *bun.DB, onPublished ...PublishedHook) *Publish {
    return &Publish{db: db, onPublished: onPublished}
}
```

钩子由下游模块提供，`points` 导入 `article`，依赖只有这一个方向。钩子需要依赖时，写成某个类型的方法，传入方法值：

```go
// ArticlePublished 给发布文章的作者加 10 积分。
func ArticlePublished(ctx context.Context, tx database.Tx, item *article.Article) error {
    return add(ctx, tx, item.AuthorID, 10, "article.published")
}
```

<a id="wiring-hooks"></a>
### 在 bootstrap 中连接钩子

`Publish` 在路由、worker 和命令中都会用到，每处都要带上同样的钩子，否则定时发布的文章不加积分。在 `bootstrap` 的 `App` 上写一个工厂方法统一构造它，所有入口共享这一个装配点：

```go
// publishArticle 构造文章发布动作并接好同步钩子，HTTP、worker 和命令共用它。
func (a *App) publishArticle() *article.Publish {
    return article.NewPublish(a.db, points.ArticlePublished)
}
```

各入口统一调用 `a.publishArticle()`（例如 `article.RegisterRoutes(api, auth, a.db, a.publishArticle())`、`article.PublishAt.Handle(w, a.publishArticle().Job)`、`article.Commands(a.publishArticle())`）：新增钩子时只改这一处，而且 `DocRouter` 用零值 `&App{}` 调用时同样安全。测试 `points.ArticlePublished` 时在测试事务中直接调用它，见[测试](../testing/testing.md#testing-actions)。

<a id="what-hooks-do"></a>
### 钩子里做什么

钩子执行时事务还持有文章的行锁，钩子里只做必须和发布一起成功的写入：

| 后续动作 | 写法 |
| --- | --- |
| 必须和发布一起成功，例如加积分 | 在钩子中用 `tx` 写入 |
| 可以稍后执行、失败要重试，例如通知关注者 | 在钩子中用 `tx` 发布[队列任务](../background/queue.md)：`Topic.Publish(ctx, tx, …)`，事务提交后才投递 |
| 回滚时撤销不了的副作用，例如广播、删除缓存 | 在钩子中用 [`tx.AfterCommit`](../database/database.md#after-commit) 登记 |

> [!WARNING]
> 钩子中的写入都使用传入的 `tx`。另开的事务看不到还没有提交的文章，发布回滚时它的写入也会留下来。

<a id="data-objects"></a>
## 请求、响应和表格行的结构体

| 用途 | 写法 |
| --- | --- |
| 校验并读取请求 | 带校验标签的结构体，例如 `Changes`；HTTP 输入类型写成 `Body Changes`，见[请求](../http/requests.md#json-body) |
| 表格导入 | 同一个结构体：`sheet.Decode[article.Changes](ctx, r)`，见[导入与导出](../features/import-export.md) |
| 业务参数 | `Handle` 直接接收 `Changes`，校验标签对它没有影响 |
| 响应 | `ArticleDTO` 和转换函数 `toDTO`，见[共享响应结构](../http/routing.md#shared-responses) |
| 前端类型 | 由导出的 OpenAPI 文档生成，见[客户端代码](../http/clients.md) |

`Changes` 只声明一次，JSON 接口、页面表单和业务代码都使用它，校验规则只有一份。

<a id="conventions"></a>
## 规则

- **构造函数和登记函数只保存依赖。** 生成 OpenAPI 文档、[查看路由](../http/routing.md#listing-routes)和[声明命令](#as-commands)时，动作用零值的依赖构造；构造时使用依赖会 panic（`nil pointer dereference`）。
- **请求的数据只放在参数和局部变量中。** 同一个动作被很多请求同时调用，存进字段的数据会被其他请求读到或覆盖。`go test -race` 能发现这类问题。
- **业务规则用 `Handle` 测试**，入口的认证和校验用 HTTP 测试覆盖，见[测试](../testing/testing.md#testing-actions)。
- 简单的接口可以直接用一个函数作为处理器，见[路由](../http/routing.md#basic-routing)。
