# 命令行

一个 API 应用除了启动 HTTP 服务，通常还要执行一些运维任务：检查配置、执行数据库迁移、导出接口文档、查看路由。`command` 包基于 [Cobra](https://github.com/spf13/cobra) 提供这些命令。

每个命令都是一个独立的 `*cobra.Command`，应用按需把它们装配到根命令上，用不到的命令不会出现。

- [示例用法](#example-usage)
- [装配命令行](#declaring-the-cli)
- [内置命令](#built-in-commands)
    - [Run：serve 与常驻进程](#run-command)
    - [在一个进程中运行多个部分](#running-together)
    - [openapi 命令](#openapi-command)
    - [routes 命令](#routes-command)
    - [migrate 命令](#migrate-command)
    - [config 命令](#config-command)
- [自定义命令](#custom-commands)
- [输入与输出](#input-and-output)
- [退出码](#exit-codes)
- [API 参考](#api-reference)

<a id="example-usage"></a>
## 示例用法

下面是一个只有两个命令的完整程序：

```go
package main

import (
    "context"
    "fmt"

    "github.com/go-tenon/tenon/command"
)

type Config struct {
    Greeting string `yaml:"greeting"`
}

func main() {
    root := command.Root("api", "示例")
    root.AddCommand(
        command.Run("hello", "打个招呼", func(ctx context.Context, cfg Config) error {
            fmt.Println(cfg.Greeting)
            return nil
        }),
        command.Config[Config](),
    )
    command.Execute(root)
}
```

运行它：

```bash
go run . hello --config config/app.yaml
go run . config show
```

`hello` 打印配置文件中的 `greeting`。`command.Run` 替你做了几件事：读取 `.env`，加载并校验 `--config` 指定的配置，然后才调用你的函数；进程收到 `SIGINT`、`SIGTERM` 时，它会取消 `ctx`。`config show` 则输出加载后的完整配置。

<a id="declaring-the-cli"></a>
## 装配命令行

`command.Root` 创建根命令，然后用 Cobra 原生的 `AddCommand` 装配命令。一个 API 应用的 `cmd/api/main.go`：

```go
func main() {
    root := command.Root("api", "Blog API")
    root.AddCommand(
        command.Run("serve", "启动 HTTP 服务", bootstrap.Serve),
        command.Run("work", "处理队列任务", bootstrap.Work),
        command.Run("schedule", "执行定时任务", bootstrap.Schedule),
        command.Run("start", "在一个进程中运行 HTTP 服务、队列和定时任务", bootstrap.Start),
        migratecmd.New(migrations.FS, func(cfg bootstrap.Config) database.Config { return cfg.Database }),
        command.Config[bootstrap.Config](),
    )
    root.AddCommand(httpcmd.Commands(bootstrap.DocRouter)...)
    root.AddCommand(bootstrap.Commands()...)
    command.Execute(root)
}
```

`bootstrap` 中的 `Serve`、`Start`、`DocRouter` 见[依赖装配](../concepts/dependencies.md#assembling-the-application)和[入门教程](../tutorial/02-infrastructure.md#entrypoint)，`bootstrap.Commands` 是需要数据库的命令，见[自定义命令](#custom-commands)。

`command.Execute` 执行根命令。收到 `SIGINT` 或 `SIGTERM` 时，它会取消命令的 context；命令失败时，它把错误写到标准错误，并以对应的[退出码](#exit-codes)结束进程。

`command.Root` 在根命令上定义了 `--config` 选项，所有子命令共用它。它可以传入多次，后面的文件覆盖前面的，默认是 `config/app.yaml`，见[按环境覆盖配置](configuration.md#overriding-configuration)。需要配置的命令由框架负责读取项目根目录的 [.env](configuration.md#env-files)、按 `--config` 加载配置并校验，你的代码只会拿到校验通过的配置。配置结构见[配置](configuration.md#loading-configuration)，类型参数通常可以从你传入的函数推断出来。

| 构造函数 | 创建的命令 | 需要配置 |
| --- | --- | --- |
| `command.Run(use, short, run)` | 任意名称，例如 `serve`、`worker` | 是 |
| `command.Config[C]()` | `config check`、`config show` | 是 |
| `migratecmd.New(fsys, dbConfig, opts...)` | `migrate up`、`migrate status`、`migrate down`、`migrate make`；`migratecmd.Name` 构造其他数据库的命令，见[多个数据库](../database/quick-start.md#multiple-databases) | 是 |
| `migratecmd.Publish(sources...)` | 显式挂载的 `migrate publish` | 否 |
| `httpcmd.Commands(router)` | `openapi`、`routes`、`client`（生成 TypeScript 或 Go 客户端，见[客户端代码](../responses/clients.md)）；也可以用 `httpcmd.OpenAPI`、`httpcmd.Routes`、`clientgen.Command` 单独装配 | 否 |
| `command.Bind(open, declare)` | 应用声明的命令，例如 `article publish`，见[从命令行运行动作](../concepts/actions.md#as-commands) | 是 |
| `queuecmd.New(db)` | `queue status`、`queue failed`、`queue slot recreate`，在 `command.Bind` 中声明，见[队列状态](../libraries/queue.md#stats-command) | 是 |
| `schedulecmd.New(tasks, m)` | `schedules`，在 `command.Bind` 中声明，见[列出任务的命令](../libraries/scheduling.md#schedules-command) | 是 |

迁移命令和 HTTP 命令分别放在 `command/migratecmd`、`command/httpcmd` 两个子包中。开发时用的 `dev`、`build`、`generate` 不在应用中，由[开发工具](../installation/tooling.md) `go tool tenon` 提供。`command` 本身不依赖 HTTP 和数据库组件，只导入它的应用不会链接 Huma、pgx 和 goose。

不提供 HTTP API 的应用，例如一个只消费消息队列的常驻进程，只装配它需要的命令即可：

```go
root := command.Root("worker", "订单事件消费者")
root.AddCommand(
    command.Run("run", "消费订单事件", consumer.Run),
    migratecmd.New(migrations.FS, dbConfig),
)
```

<a id="built-in-commands"></a>
## 内置命令

<a id="run-command"></a>
### Run：serve 与常驻进程

`command.Run` 创建一个先加载配置、再调用 `run(ctx, cfg)` 的命令。`ctx` 会在进程收到停止信号时被取消，常驻进程据此退出。`bootstrap.Serve` 装配应用并启动 HTTP 服务，见[依赖装配](../concepts/dependencies.md#assembling-the-application)，HTTP 服务会据此[平滑停止](../concepts/lifecycle.md)：

```bash
./bin/api serve                                                 # 使用 config/app.yaml
./bin/api serve --config config/app.yaml --config config/prod.yaml
```

`Run` 不限于常驻进程，任何“读取配置然后做一件事”的命令都可以用它创建，例如数据回填、发送一次报表。

<a id="running-together"></a>
### 在一个进程中运行多个部分

一个完整的应用通常有三个常驻部分：HTTP 服务、[队列 worker](../libraries/queue.md#running-workers) 和[定时任务调度器](../libraries/scheduling.md#running-the-scheduler)，分别是 `App` 的 `HTTP`、`Worker` 和 `Scheduler` 方法（见[依赖装配](../concepts/dependencies.md#assembling-the-application)）。每个命令只是选择运行哪些部分：

```go
func Serve(ctx context.Context, cfg Config) error {
    return run(ctx, cfg, (*App).HTTP)
}

func Work(ctx context.Context, cfg Config) error {
    return run(ctx, cfg, (*App).Worker)
}

func Schedule(ctx context.Context, cfg Config) error {
    return run(ctx, cfg, (*App).Scheduler)
}

func Start(ctx context.Context, cfg Config) error {
    return run(ctx, cfg, (*App).HTTP, (*App).Worker, (*App).Scheduler)
}
```

| 命令 | 运行的部分 |
| --- | --- |
| `serve` | HTTP 服务 |
| `work` | 队列 worker |
| `schedule` | 定时任务调度器 |
| `start` | 以上三者，在同一个进程中 |
| `realtime` | 只承接[实时频道](../libraries/realtime.md)的连接，可选，见[单独部署实时连接](../libraries/realtime.md#separate-process) |

小型部署只运行 `start`：一个进程、一份配置、一套资源。业务量增长后改为分别部署 `serve`、`work` 和 `schedule`，各自按负载增减实例，代码不用改。`start` 也可以部署多个实例：worker 竞争领取任务，调度器通过锁保证[每次触发只执行一次](../libraries/scheduling.md#multiple-instances)。

`run` 先装配全部部分，再用 `d.Run` 同时运行它们（内部是 `command.Concurrently`）：

- 一个部分装配失败（例如端口被占用）时，`run` 直接返回错误，哪个部分都不会开始运行，进程以非 0 状态退出。
- 收到停止信号时，各部分同时平滑停止：HTTP 服务排空请求，worker 停止领取并等待处理中的任务，调度器不再触发并等待执行中的任务。全部返回之后才关闭数据库。
- 一个部分运行时出错退出，其余部分随之停止，进程以非 0 状态退出。
- 一个部分在收到停止信号之前就正常返回，同样视为异常，错误中包含 `command.ErrStoppedEarly`，交给 systemd、Kubernetes 等重启。

各部分共用 `open` 中创建的数据库连接池，HTTP 请求和 worker 的并发任务都会占用连接，设置 [`max_conns`](../database/quick-start.md#configuration) 时要按两者之和估算。

<a id="openapi-command"></a>
### openapi 命令

`httpcmd.OpenAPI(router)` 调用 `router` 构造应用的路由，用 [`rest.Lookup`](../http/routing.md#exporting-openapi) 取出其中的 API，把 OpenAPI 文档输出到标准输出：

```bash
./bin/api openapi > openapi.json
./bin/api openapi --spec 3.0 > openapi-3.0.json
```

默认输出 OpenAPI 3.1；有的客户端生成器只支持 3.0，例如 oapi-codegen，这时加上 `--spec 3.0`。路由中有[多个 API](../http/routing.md#api-versioning) 时，用 `--base-path` 选择其中一个，例如 `--base-path /api/v2`。

`bootstrap.DocRouter` 用默认配置和没有打开任何资源的 `App` 调用 `serve` 也在用的路由函数（见[依赖装配](../concepts/dependencies.md#assembling-the-application)），因此导出的文档与实际运行的接口始终一致。构造路由不会调用处理器，也不会调用 token 校验器，所以这个命令不读取配置文件，也不连接数据库。

<a id="routes-command"></a>
### routes 命令

当业务模块变多时，你可以用 `routes` 查看整个应用的路由，包括 JSON API 和[页面](../responses/views.md#page-routes)，不必逐个打开模块中的 `routes.go`。`httpcmd.Routes` 的参数是构造完整路由的函数，例如[入门教程](../tutorial/02-infrastructure.md#assembling)中的 `bootstrap.DocRouter`。它不需要配置文件，也不连接数据库：

```bash
./bin/api routes
./bin/api routes --method post --path /articles
./bin/api routes --all
```

`--method` 不区分大小写，`--path` 按路径包含的文本匹配，两者同时使用时取交集。用 `Handle`、`Mount` 注册、响应任意方法的路由（例如单页应用的 `/*`）合并为一行，方法显示为 `*`，按方法过滤时总是列出。结果按路径、HTTP 方法排序，例如 `--path /articles` 会输出：

```text
METHOD  PATH                    OPERATION ID     ACCESS            SUMMARY
POST    /articles               article-create   bearer: articles  创建文章
GET     /articles/{id}          article-get      bearer: articles  获取文章
POST    /articles/{id}/publish  article-publish  bearer: articles  发布文章
```

各列的含义：

- **ACCESS** 展示路由声明的访问策略：`public` 表示显式公开，`bearer` 表示 JSON API 要求 Bearer token，`bearer: articles` 同时列出所需的权限，`docs` 表示 Huma 的文档端点。`unknown` 表示无法从声明确定策略（例如直接注册在 chi 上的健康检查），**不**表示允许匿名访问；
- **OPERATION ID**、**SUMMARY** 来自 `huma.Operation`，页面路由没有，显示为 `-`。摘要中的换行会合并为空格。

Huma 自动注册的 `/docs`、`/openapi.json`、`/schemas/{schema}` 等文档端点默认不显示，加上 `--all` 才会列出。列表也包含直接注册在 chi 上的原生路由和挂载的子路由；挂载的不透明 `http.Handler`（例如 `Mount("/files", handler)`）显示为一行 `* /files/*`，无法展开其内部路由。

<a id="migrate-command"></a>
### migrate 命令

`migratecmd.New(fsys, dbConfig)` 创建四个迁移命令。`fsys` 是[嵌入的迁移文件](../database/migrations.md#embedding-migrations)，`dbConfig` 从应用配置中取出数据库配置：

```bash
./bin/api migrate up       # 执行全部未执行的迁移
./bin/api migrate status   # 查看每个迁移的状态
./bin/api migrate down     # 回滚最近一次迁移
go run ./cmd/api migrate make create_articles_table  # 新建迁移文件
```

`up` 和 `down` 每完成一个迁移输出一行，`status` 输出一张表：

```text
VERSION         STATE    APPLIED AT           FILE
20261111090000  applied  2026-11-11 09:12:03  20261111090000_create_articles_table.sql
20261112100000  pending  -                    20261112100000_create_article_tags_table.sql
```

`make` 生成迁移文件的骨架，不加载配置、不连接数据库；其他命令会加载配置、连接数据库，并在结束后关闭连接。数据库连不上时以 `ExitUnavailable` 退出，部署脚本可以据此重试。迁移文件的编写与嵌入见[迁移](../database/migrations.md)。应用连接多个数据库时，用 `migratecmd.Name` 为其他数据库构造 `migrate <name> …` 命令，见[多个数据库](../database/quick-start.md#multiple-databases)。

需要文件上传等功能自带的 SQL 时，可在 `migratecmd.New` 返回的命令上显式添加 `migratecmd.Publish(sources...)`。其中 `sources` 是应用选择的 `[]migrate.Source`，例如 `migrate.Source{Name: "filesystem", FS: filesystem.Migrations()}`。注册后可以执行：

```bash
go run ./cmd/api migrate publish --dir migrations
```

这个命令只将尚未发布的 SQL 拷贝到应用目录，不加载配置、不连接数据库、不执行迁移。`--dir` 默认是 `migrations`；每个新增文件输出一行，重复执行不覆盖已有来源。注册代码、空项目首次发布方式及升级流程见[发布框架迁移](../database/migrations.md#publishing-framework-migrations)。

<a id="config-command"></a>
### config 命令

`command.Config[C]()` 创建两个检查配置的命令，`C` 是应用的配置结构：

- `config check` 只加载并校验配置，不访问网络。发布前运行它，可以尽早发现缺失的环境变量或写错的字段；
- `config show` 输出加载后的配置，包括默认值和展开后的环境变量。默认输出 YAML，`--format json` 输出 JSON。

```bash
./bin/api config check --config config/app.yaml,config/prod.yaml
./bin/api config show --format json
```

配置文件缺失或校验失败时，这两个命令以及所有需要读取配置的命令都以 `ExitConfig` 退出。配置的读取与校验见[配置](configuration.md)。

<a id="custom-commands"></a>
## 自定义命令

根命令和内置命令都是原生的 `*cobra.Command`，任意 Cobra 命令都可以一起装配。需要读取配置时，调用 `command.LoadConfig`，它使用同一个 `--config` 选项，失败时同样带 `ExitConfig` 退出码。不接受位置参数的命令，用 `command.NoArgs` 代替 `cobra.NoArgs`，多余参数就会以 `ExitUsage` 失败：

```go
root.AddCommand(&cobra.Command{
    Use:   "ping-db",
    Short: "检查数据库能否连通",
    Args:  command.NoArgs,
    RunE: func(cmd *cobra.Command, _ []string) error {
        cfg, err := command.LoadConfig[Config](cmd)
        if err != nil {
            return err
        }
        db, err := database.Open(cmd.Context(), cfg.Database)
        if err != nil {
            return err // 连不上时是 database.ErrUnavailable，以 ExitUnavailable 退出
        }
        return db.Close()
    },
})
```

选项使用 Cobra 原生的 `Flags` 定义。命令的 context 通过 `cmd.Context()` 获取，进程收到停止信号时它会被取消。需要恰好 n 个位置参数时，用 `command.ExactArgs(n)`，参数个数不对同样以 `ExitUsage` 失败。

需要应用资源的命令不必这样手动加载配置和打开数据库，用 `command.Bind` 声明，执行时才装配：

```go
// bootstrap.Commands 返回需要 App 的命令。
func Commands() []*cobra.Command {
    return command.Bind(open, func(a *App) []*cobra.Command {
        return []*cobra.Command{
            article.Commands(a.db),
            queuecmd.New(a.db),
        }
    })
}
```

`open` 与 `serve` 用的是同一个，见[只在执行时装配的命令](../concepts/dependencies.md#deferred-commands)。执行某个业务动作的命令写成动作的 `Command` 入口，见[从命令行运行动作](../concepts/actions.md#as-commands)。

<a id="input-and-output"></a>
## 输入与输出

请遵循这个约定：**标准输出只用于命令结果，诊断信息写入标准错误**。内置命令都是这样做的，因此 `./bin/api openapi > openapi.json` 之类的重定向可以得到干净的结果；日志默认也写到标准错误（见[日志](logging.md#creating-a-logger)）。

自定义命令中，用 `cmd.OutOrStdout()` 和 `cmd.ErrOrStderr()` 写输出。这样在测试中可以用 Cobra 的 `SetOut`、`SetErr` 把输出重定向到缓冲区，见[命令测试](../testing/http.md#command-tests)。

<a id="exit-codes"></a>
## 退出码

部署脚本和 CI 通常根据退出码判断命令是否成功，以及失败的原因：

| 退出码 | 常量 | 含义 |
| --- | --- | --- |
| 0 | | 成功 |
| 1 | `command.ExitFailure` | 操作被业务规则拒绝，例如输入无效或资源不存在 |
| 2 | `command.ExitUsage` | 用法错误，例如未知命令、未知选项或多余参数 |
| 3 | `command.ExitConfig` | 配置文件缺失或无效 |
| 4 | `command.ExitUnavailable` | 外部依赖不可用 |
| 5 | `command.ExitInternal` | 其他未分类的错误 |

用法错误和配置错误由框架自动标记；`database.Open` 连不上数据库时返回 `database.ErrUnavailable`，它属于 `errorx.Unavailable` 分类。其他错误按[业务错误](errors.md#error-kinds)的分类转换：

| 分类 | 退出码 |
| --- | --- |
| `errorx.Unavailable` | `ExitUnavailable`（4） |
| `errorx.Internal` 及非业务错误 | `ExitInternal`（5） |
| 其余分类 | `ExitFailure`（1） |

因此，命令直接返回动作的错误即可。需要覆盖默认结果时，用 `command.AsExitError` 为错误标记退出码，`command.ExitCode` 则从错误中取出退出码：

```go
return command.AsExitError(command.ExitUnavailable, err)
```

<a id="api-reference"></a>
## API 参考

包 `github.com/go-tenon/tenon/command`。

### Root、Execute

```go
func Root(name, short string) *cobra.Command
func Execute(root *cobra.Command)
```

- **参数**
    - `name`：程序名，出现在帮助信息中
    - `short`：一句话说明
- **返回**：带 `--config` 选项的根命令

未知命令、未知选项和多余参数都标记为 `ExitUsage`。`Execute` 执行根命令：收到停止信号时取消命令的 context；命令失败时把错误写到标准错误，并以 `ExitCode(err)` 退出进程。

### Run

```go
func Run[C any](use, short string, run func(ctx context.Context, cfg C) error) *cobra.Command
```

- **参数**
    - `use`：命令名，例如 `serve`
    - `short`：一句话说明
    - `run`：命令要做的事，收到加载并校验后的配置
- **返回**：命令

`run` 的 `ctx` 在进程收到 `SIGINT`、`SIGTERM` 时被取消，常驻进程据此退出。

### Config

```go
func Config[C any]() *cobra.Command
```

- **返回**：`config` 命令，包含 `check` 和 `show` 两个子命令

`C` 是应用的根配置结构体。

### LoadConfig

```go
func LoadConfig[C any](cmd *cobra.Command) (C, error)
```

- **参数**
    - `cmd`：当前执行的命令，用来读取 `--config` 选项
- **返回**：加载并校验后的配置；失败时返回的错误带 `ExitConfig` 退出码

在自定义命令中读取 `.env`，再按 `--config` 加载配置，见[自定义命令](#custom-commands)。

### Bind

```go
func Bind[C, D any](open func(ctx context.Context, cfg C, stack *cleanup.Stack) (*D, error), declare func(*D) []*cobra.Command) []*cobra.Command
```

依赖推迟到执行时装配的命令，见[只在执行时装配的命令](../concepts/dependencies.md#deferred-commands)。

### NoArgs、ExactArgs

```go
func NoArgs(cmd *cobra.Command, args []string) error
func ExactArgs(n int) cobra.PositionalArgs
```

与 Cobra 的同名函数相同，参数个数不对时以 `ExitUsage` 失败。

### AsExitError、ExitCode

```go
func AsExitError(code int, err error) error
func ExitCode(err error) int
```

`AsExitError` 为错误标记一个退出码，覆盖默认的规则；`ExitCode` 取出错误对应的退出码。规则见[退出码](#exit-codes)。

### Daemon、Concurrently

见[依赖装配的 API 参考](../concepts/dependencies.md#api-reference)。

### httpcmd

包 `github.com/go-tenon/tenon/command/httpcmd`。

```go
func Commands(router func() chi.Routes) []*cobra.Command
func OpenAPI(router func() chi.Routes) *cobra.Command
func Routes(router func() chi.Routes) *cobra.Command
```

`Commands` 返回 `openapi`、`routes` 和 `client` 三个命令。`router` 构造应用的全部路由，通常用默认配置和 `nil` 依赖调用 `serve` 也在用的路由函数，例如 `func() chi.Routes { return routes(config.Default[Config](), nil) }`；三个命令执行时才调用它。

### migratecmd

包 `github.com/go-tenon/tenon/command/migratecmd`。

```go
func New[C any](fsys fs.FS, dbConfig func(cfg C) database.Config, opts ...Option) *cobra.Command
func Name(name string) Option
func Publish(sources ...migrate.Source) *cobra.Command
func Make() *cobra.Command
```

`New` 返回 `migrate` 及其 `up`、`status`、`down`、`make` 子命令；`Name` 为另一个数据库构造 `migrate <name> …`；`Publish` 返回 `migrate publish`；`Make` 单独返回 `migrate make`，不需要配置。
