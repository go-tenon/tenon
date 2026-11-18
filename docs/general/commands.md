# 命令行

一个 API 应用除了启动 HTTP 服务，通常还要执行一些运维任务：检查配置、执行数据库迁移、导出接口文档、查看路由，或者手动执行一次业务操作，例如立即发布某篇文章。这些都写成程序的**子命令**，例如 `./bin/blog serve`、`./bin/blog migrate up`。

`command` 包基于 [Cobra](https://github.com/spf13/cobra) 提供这些命令。Cobra 是 Go 中最常用的命令行库：一个命令是一个 `*cobra.Command` 结构体，写明命令名、说明和执行的函数；命令可以嵌套，组成 `migrate up` 这样的多级命令。`command` 包在 Cobra 之上加了几件事：加载配置、收到停止信号时取消 ctx、把错误转换为退出码。

阅读前需要了解：[配置](configuration.md)；[context](../concepts/go-primer.md#context)。

- [示例用法](#example-usage)
- [装配命令行](#declaring-the-cli)
- [自定义命令](#custom-commands)
- [输入与输出](#input-and-output)
- [退出码](#exit-codes)
- [内置命令](#built-in-commands)
    - [serve：运行常驻进程](#serve-command)
    - [health 命令](#health-command)
    - [openapi 命令](#openapi-command)
    - [routes 命令](#routes-command)
    - [migrate 命令](#migrate-command)
    - [config 命令](#config-command)
- [帮助中的分组](#groups)
- [常见问题](#troubleshooting)
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
    root := command.Root("blog", "示例")
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

`command.Root` 创建根命令，然后用 Cobra 原生的 `AddCommand` 装配命令。一个 API 应用的 `cmd/blog/main.go`：

```go
func main() {
    root := command.Root("blog", "Blog API")
    root.AddCommand(
        command.Serve(bootstrap.Open, bootstrap.Parts),
        migratecmd.New(migrations.FS, func(cfg bootstrap.Config) database.Config { return cfg.Database }),
        command.Config[bootstrap.Config](),
        httpcmd.Health(func(cfg bootstrap.Config) httpx.Config { return cfg.HTTP }),
    )
    root.AddCommand(httpcmd.Commands(bootstrap.DocRouter)...)
    root.AddCommand(bootstrap.Commands()...)
    command.Execute(root)
}
```

`bootstrap` 中的 `Open`、`Parts`、`DocRouter` 见[依赖装配](../concepts/dependencies.md#assembling-the-application)和[入门教程](../tutorial/02-infrastructure.md#entrypoint)，`bootstrap.Commands` 是需要数据库的命令，见[自定义命令](#custom-commands)。

`command.Execute` 执行根命令。收到 `SIGINT` 或 `SIGTERM` 时，它会取消命令的 context；命令失败时，它把错误写到标准错误，并以对应的[退出码](#exit-codes)结束进程。

`command.Root` 在根命令上定义了 `--config` 选项，所有子命令共用它。它可以传入多次，后面的文件覆盖前面的；没有传入时读取环境变量 `APP_CONFIG`（格式相同，用逗号分隔），它也没有设置时是 `config/app.yaml`，见[叠加多个配置文件](configuration.md#overriding-configuration)。需要配置的命令由框架负责读取项目根目录的 [.env](configuration.md#env-files)、按 `--config` 加载配置并校验，你的代码只会拿到校验通过的配置。配置结构见[配置](configuration.md#loading-configuration)，类型参数通常可以从你传入的函数推断出来。

| 构造函数 | 创建的命令 | 需要配置 |
| --- | --- | --- |
| `command.Serve(open, parts)` | `serve`：运行常驻进程，见[serve：运行常驻进程](#serve-command) | 是 |
| `command.Run(use, short, run)` | 任意名称，只需要配置的一次性任务，例如发送一次报表 | 是 |
| `command.Config[C]()` | `config check`、`config show` | 是 |
| `migratecmd.New(fsys, dbConfig, opts...)` | `migrate up`、`migrate status`、`migrate down`、`migrate make`；`migratecmd.Name` 构造其他数据库的命令，见[多个数据库](../database/advanced.md#multiple-databases) | 是 |
| `migratecmd.Publish(sources...)` | 显式挂载的 `migrate publish` | 否 |
| `httpcmd.Health(http)` | `health`：检查本机的 HTTP 服务是否存活，见[health 命令](#health-command) | 是 |
| `httpcmd.Commands(router, opts...)` | `openapi`、`routes`、`client`（生成 TypeScript 或 Go 客户端，见[客户端代码](../http/clients.md)）；`httpcmd.Translations(locales.FS)` 传入应用的翻译文件，生成的验证提示与服务端相同。也可以用 `httpcmd.OpenAPI`、`httpcmd.Routes`、`clientgen.Command` 单独装配 | 否 |
| `rpccmd.New(server)` | `rpc asyncapi`（输出 RPC 端点的 AsyncAPI 文档）、`rpc endpoints`（列出端点），见[导出接口描述](../rpc/endpoints.md#exporting) | 否 |
| `command.Bind(Open, declare)` | 应用声明的命令，例如 `article publish`，见[从命令行运行动作](../concepts/actions.md#as-commands) | 是 |
| `queuecmd.New(db, opts...)` | `queue status`、`queue failed`、`queue slot recreate`，在 `command.Bind` 中声明；`queuecmd.WithJetStream(nc, cfg)` 让 `queue status` 同时显示 JetStream 中的任务，见[队列状态](../background/queue-monitoring.md#stats-command) | 是 |
| `schedulecmd.New(tasks, m)` | `schedules`，在 `command.Bind` 中声明，见[列出任务的命令](../background/scheduling.md#schedules-command) | 是 |
| `doctorcmd.New(groups...)` | `doctor`，在 `command.Bind` 中声明；`migratecmd.Check` 让 `migrate up` 在迁移之前运行同样的检查，见[自检](../installation/doctor.md) | 是 |

开发时用的 `dev`、`build`、`generate` 不在这里，它们由[开发工具](../installation/tooling.md) `go tool tenon` 提供。命令行中只有你装配了的命令：不提供 HTTP API 的程序，例如一个只消费消息的常驻进程，`Parts` 中只登记消费消息的部分，再装配 `command.Serve` 和 `migrate` 即可。

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

需要应用资源的命令用 `command.Bind` 声明，执行时才装配：

```go
// bootstrap.Commands 返回需要 App 的命令。
func Commands() []*cobra.Command {
    return command.Bind(Open, func(a *App) []*cobra.Command {
        return []*cobra.Command{
            article.Commands(a.db),
            queuecmd.New(a.db, queuecmd.WithJetStream(a.nc, a.cfg.Queue)),
        }
    })
}
```

`Open` 与 `serve` 用的是同一个，见[只在执行时装配的命令](../concepts/dependencies.md#deferred-commands)。执行某个业务动作的命令写成动作的 `Command` 入口，见[从命令行运行动作](../concepts/actions.md#as-commands)。

<a id="input-and-output"></a>
## 输入与输出

请遵循这个约定：**标准输出只用于命令结果，诊断信息写入标准错误**。内置命令都是这样做的，因此 `./bin/blog openapi > openapi.json` 之类的重定向可以得到干净的结果；日志默认也写到标准错误（见[日志](logging.md#creating-a-logger)）。

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

<a id="built-in-commands"></a>
## 内置命令

<a id="serve-command"></a>
### serve：运行常驻进程

一个完整的应用通常有几个一直运行的**部分**：HTTP 服务、[RPC 服务](../rpc/endpoints.md#running)、[队列 worker](../background/queue.md#running-workers)、[定时任务调度器](../background/scheduling.md#running-the-scheduler)。每个部分是 `App` 的一个方法，在 `bootstrap` 的 `Parts` 中登记，`command.Serve` 用它们构造 `serve` 命令：

```go
// Parts 是常驻进程的运行部分：serve 运行全部部分，serve <名字> 只运行其中一个。
func Parts(a *App) []command.Part {
    return []command.Part{
        {Name: "http", Short: "接口、页面和实时频道", Start: a.HTTP},
        {Name: "rpc", Short: "RPC 端点", Start: a.RPC},
        {Name: "worker", Short: "处理队列任务", Start: a.Worker},
        {Name: "scheduler", Short: "执行定时任务", Start: a.Scheduler},
        {Name: "realtime", Short: "只承接实时频道的连接", Start: a.Realtime, Standalone: true},
    }
}
```

`a.HTTP` 这些方法见[多个运行部分](../concepts/dependencies.md#running-parts)。进程只有两种形态：

| 命令 | 运行的部分 |
| --- | --- |
| `serve` | 全部部分，`Standalone` 的除外 |
| `serve all` | 与 `serve` 相同，供 systemd 的模板单元（`blog@all`）使用 |
| `serve http` | 只运行 HTTP 服务；`serve worker`、`serve scheduler` 等同理 |

小型部署只运行 `serve`：一个进程、一份配置、一套资源。业务量增长后改为每个部分一个进程，各自按负载增减实例，代码和配置保持原样。`serve` 也可以部署多个实例：worker 竞争领取任务，调度器通过锁保证[每次触发只执行一次](../background/scheduling.md#multiple-instances)。

`Standalone` 的部分只单独运行。例如 `realtime` 只承接实时频道的连接：`serve` 运行全部时，实时频道已经挂在 HTTP 服务的 `/realtime` 上，不需要再多开一个端口，见[单独部署实时连接](../libraries/realtime.md#separate-process)。

`serve` 执行时：

- 先加载配置、调用 `Open` 打开共用的资源，再按顺序调用每个选中部分的 `Start`。一个部分装配失败（例如端口被占用）时，在任何部分开始运行之前就返回错误，进程以非 0 状态退出。
- 全部装配成功后同时运行这些部分（内部是 `Daemon.Run`）。收到停止信号时，各部分同时平滑停止：HTTP 服务排空请求，worker 停止领取并等待处理中的任务，调度器停止触发并等待执行中的任务。全部返回之后才关闭数据库。
- 一个部分运行时出错退出，其余部分随之停止，进程以非 0 状态退出。
- 一个部分在收到停止信号之前就正常返回，同样视为异常，错误中包含 `command.ErrStoppedEarly`，交给 systemd、Docker 等重启。

`serve --help` 列出全部部分的名字和说明，名字写错或者给出多个名字时以 `ExitUsage` 退出。构造命令行时还没有打开任何资源，`Parts` 收到的是零值的 `App`，所以其中只取方法值（`a.HTTP`），不使用依赖。

各部分共用 `Open` 中创建的数据库连接池，HTTP 请求和 worker 的并发任务都会占用连接，设置 [`max_conns`](../database/quick-start.md#configuration) 时要按两者之和估算。

<a id="health-command"></a>
### health 命令

`httpcmd.Health` 创建 `health` 命令：加载配置，请求本机 HTTP 服务的 `/livez`，返回 2xx 时以 0 退出，否则以 `ExitFailure` 退出。它的参数从应用配置中取出 HTTP 服务的配置；监听地址的主机为空、`0.0.0.0` 或 `::` 时请求 `127.0.0.1`：

```go
httpcmd.Health(func(cfg bootstrap.Config) httpx.Config { return cfg.HTTP })
```

```bash
./bin/blog health && echo alive
```

它用于 Docker Compose 和 Swarm 的健康检查，镜像中不需要 curl，写法见[部署](../installation/deployment.md#health-checks)。应用要在路由中注册 `/livez`，见[健康检查](../http/lifecycle.md#health-checks)。`--timeout` 是等待响应的最长时间，默认 3 秒。

<a id="openapi-command"></a>
### openapi 命令

`httpcmd.OpenAPI(router)` 调用 `router` 构造应用的路由，用 [`rest.Lookup`](../http/openapi.md#exporting-openapi) 取出其中的 API，把 OpenAPI 文档输出到标准输出：

```bash
./bin/blog openapi > openapi.json
./bin/blog openapi --spec 3.0 > openapi-3.0.json
```

默认输出 OpenAPI 3.1；有的客户端生成器只支持 3.0，例如 oapi-codegen，这时加上 `--spec 3.0`。路由中有[多个 API](../http/openapi.md#api-versioning) 时，用 `--base-path` 选择其中一个，例如 `--base-path /api/v2`。

`bootstrap.DocRouter` 用默认配置和没有打开任何资源的 `App` 调用 `serve` 也在用的路由函数（见[依赖装配](../concepts/dependencies.md#assembling-the-application)），因此导出的文档与实际运行的接口始终一致。构造路由只登记处理器和 token 校验器，所以这个命令离线运行，只需要源代码。

<a id="routes-command"></a>
### routes 命令

当业务模块变多时，你可以用 `routes` 查看整个应用的路由，包括 JSON API 和挂载的页面。`httpcmd.Routes` 的参数是构造完整路由的函数，例如[入门教程](../tutorial/02-infrastructure.md#assembling)中的 `bootstrap.DocRouter`。它离线运行：

```bash
./bin/blog routes
./bin/blog routes --method post --path /articles
./bin/blog routes --all
```

`--method` 不区分大小写，`--path` 按路径包含的文本匹配，两者同时使用时取交集。用 `Handle`、`Mount` 注册、响应任意方法的路由（例如单页应用的 `/*`）合并为一行，方法显示为 `*`，按方法过滤时总是列出。结果按路径、HTTP 方法排序，例如 `--path /articles` 会输出：

```text
METHOD  PATH                    OPERATION ID     ACCESS            SUMMARY
POST    /articles               article-create   token: articles   创建文章
GET     /articles/{id}          article-get      token: articles   获取文章
POST    /articles/{id}/publish  article-publish  token: articles   发布文章
```

各列的含义：

- **ACCESS** 展示路由声明的访问策略：`public` 表示显式公开，`token` 表示 JSON API 要求 token（会话 cookie 或 Bearer），`token: articles` 同时列出所需的权限，`docs` 表示 Huma 的文档端点。`unknown` 表示无法从声明确定策略（例如直接注册在 chi 上的健康检查），**不**表示允许匿名访问；
- **OPERATION ID**、**SUMMARY** 来自 `huma.Operation`，页面路由没有，显示为 `-`。摘要中的换行会合并为空格。

Huma 自动注册的 `/docs`、`/openapi.json`、`/schemas/{schema}` 等文档端点默认不显示，加上 `--all` 才会列出。列表也包含直接注册在 chi 上的原生路由和挂载的子路由；挂载的不透明 `http.Handler`（例如 `Mount("/files", handler)`）显示为一行 `* /files/*`，无法展开其内部路由。

<a id="migrate-command"></a>
### migrate 命令

`migratecmd.New(fsys, dbConfig)` 创建四个迁移命令。`fsys` 是[嵌入的迁移文件](../database/migrations.md#embedding-migrations)，`dbConfig` 从应用配置中取出数据库配置：

```bash
./bin/blog migrate up       # 执行全部未执行的迁移
./bin/blog migrate status   # 查看每个迁移的状态
./bin/blog migrate down     # 回滚最近一次迁移
go run ./cmd/blog migrate make create_articles_table  # 新建迁移文件
```

`up` 和 `down` 每完成一个迁移输出一行，`status` 输出一张表：

```text
VERSION         STATE    APPLIED AT           FILE
20261111090000  applied  2026-11-11 09:12:03  2026_11_11_090000_create_articles_table.sql
20261112100000  pending  -                    2026_11_12_100000_create_article_tags_table.sql
```

`make` 只生成迁移文件的骨架；其他命令会加载配置、连接数据库，并在结束后关闭连接。数据库连不上时以 `ExitUnavailable` 退出，部署脚本可以据此重试。迁移文件的编写与嵌入见[迁移](../database/migrations.md)。应用连接多个数据库时，用 `migratecmd.Name` 为其他数据库构造 `migrate <name> …` 命令，见[多个数据库](../database/advanced.md#multiple-databases)。

需要文件上传等功能自带的 SQL 时，可在 `migratecmd.New` 返回的命令上显式添加 `migratecmd.Publish(sources...)`。其中 `sources` 是应用选择的功能包的迁移，例如 `filesystem.Migrations()`。注册后可以执行：

```bash
go run ./cmd/blog migrate publish --dir migrations
```

这个命令只将尚未发布的 SQL 拷贝到应用目录，执行迁移仍用 `migrate up`。`--dir` 默认是 `migrations`；每个新增文件输出一行，目录中已有的迁移保持原样。注册代码、空项目首次发布方式及升级流程见[发布框架迁移](../database/migrations.md#publishing-framework-migrations)。

<a id="config-command"></a>
### config 命令

`command.Config[C]()` 创建两个检查配置的命令，`C` 是应用的配置结构：

- `config check` 只加载并校验配置，不访问网络。发布前运行它，可以尽早发现缺失的环境变量或写错的字段；
- `config show` 输出加载后的配置，包括默认值和展开后的环境变量。默认输出 YAML，`--format json` 输出 JSON。

```bash
./bin/blog config check
./bin/blog config show --format json
```

配置文件缺失或校验失败时，这两个命令以及所有需要读取配置的命令都以 `ExitConfig` 退出。配置的读取与校验见[配置](configuration.md)。

<a id="groups"></a>
## 帮助中的分组

`command.Execute` 把根命令的子命令分组显示在帮助中：

```text
运行：
  serve       运行常驻进程：不带参数时运行全部部分，带参数时只运行这一个部分
运维：
  config      配置工具
  health      检查本机的 HTTP 服务是否存活，供 Docker 的健康检查使用
  migrate     数据库迁移
应用：
  user        作者账号
开发：
  client      生成 Go 或 TypeScript 客户端
  openapi     输出 OpenAPI 文档
  routes      查看应用的全部路由
```

框架的命令已经标好了分组；应用自己的命令没有标注时归入“应用”。要放进别的分组，用 `command.InGroup`，例如 `command.InGroup(cmd, command.GroupOps)` 把一个数据回填命令放进“运维”。

<a id="troubleshooting"></a>
## 常见问题

**报错 `unknown command "xxx" for "blog"`，退出码 2**

这个命令没有装配到根命令上。检查 `main.go` 中的 `root.AddCommand`；需要数据库的命令是否加进了 `bootstrap.Commands()`。

**命令以退出码 3 结束**

配置文件缺失或无效。用 `config check` 查看具体的原因；确认在项目根目录执行（`config/app.yaml` 和 `.env` 都按工作目录查找）。

**`serve` 报错 `unknown part "xxx"`，退出码 2**

这个名字不在 `Parts` 中。`serve --help` 列出全部部分的名字；`serve` 最多接受一个名字，要运行全部部分时不带参数。

**`./bin/blog openapi > openapi.json` 得到的文件中混入了日志**

有代码把诊断信息写到了标准输出。日志和诊断信息写到标准错误，见[输入与输出](#input-and-output)。

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

### Serve、Part

```go
func Serve[C, D any](open func(ctx context.Context, cfg C, stack *cleanup.Stack) (*D, error), parts func(*D) []Part) *cobra.Command

type Part struct {
    Name       string
    Short      string
    Standalone bool
    Start      func(d *Daemon) (run func(context.Context) error, err error)
}

const AllParts = "all"
```

- **参数**
    - `open`：打开各部分共用的资源，把关闭操作登记到 `stack`，与 `Bind` 的相同
    - `parts`：返回全部部分；构造命令时用零值的 `D` 调用，只用来列出名字和说明
- **返回**：`serve` 命令

`Part` 的 `Name` 是 `serve` 中的名字，`Short` 出现在 `serve --help` 中，`Standalone` 的部分只单独运行；`Start` 装配这个部分并用 `d.Listen` 取得监听端口，返回一直运行到 `ctx` 被取消的函数。名字为空、重复、含空白或者是 `all`（`command.AllParts`），以及 `Start` 为 `nil` 时 panic。行为见[serve：运行常驻进程](#serve-command)。

### Run

```go
func Run[C any](use, short string, run func(ctx context.Context, cfg C) error) *cobra.Command
```

- **参数**
    - `use`：命令名，例如 `report`
    - `short`：一句话说明
    - `run`：命令要做的事，收到加载并校验后的配置
- **返回**：命令

适合只需要配置的一次性任务。`run` 的 `ctx` 在进程收到 `SIGINT`、`SIGTERM` 时被取消。

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

在自定义命令中读取 `.env`，再按 `--config` 或环境变量 `APP_CONFIG` 加载配置，见[自定义命令](#custom-commands)。

### Bind

```go
func Bind[C, D any](open func(ctx context.Context, cfg C, stack *cleanup.Stack) (*D, error), declare func(*D) []*cobra.Command) []*cobra.Command
```

依赖推迟到执行时装配的命令，见[只在执行时装配的命令](../concepts/dependencies.md#deferred-commands)。执行时 `Bind` 把解析到的选项复制到重新声明的命令上，用 `cmd.Flags().GetString` 读取和用 `StringVar` 绑定变量都可以。

### InGroup

```go
func InGroup(cmd *cobra.Command, id string) *cobra.Command

const (
    GroupRun = "run"
    GroupOps = "ops"
    GroupApp = "app"
    GroupDev = "dev"
)
```

把 `cmd` 标注为分组 `id`，返回 `cmd`。`Execute` 按标注把根命令的子命令分组显示在帮助中，见[帮助中的分组](#groups)。

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
func Commands(router func() chi.Routes, opts ...Option) []*cobra.Command
func Translations(sources ...fs.FS) Option
func OpenAPI(router func() chi.Routes) *cobra.Command
func Routes(router func() chi.Routes) *cobra.Command
```

`Commands` 返回 `openapi`、`routes` 和 `client` 三个命令；`Translations` 传入应用的翻译文件，`client` 命令生成的验证提示和显示文字与服务端相同。`router` 构造应用的全部路由，通常用默认配置和 `nil` 依赖调用 `serve` 也在用的路由函数，例如 `func() chi.Routes { return routes(config.Default[Config](), nil) }`；三个命令执行时才调用它。

```go
func Health[C any](http func(C) httpx.Config) *cobra.Command
const LivePath = "/livez"
```

`Health` 返回 `health` 命令，请求本机 HTTP 服务的 `LivePath`，见[health 命令](#health-command)。

### migratecmd

包 `github.com/go-tenon/tenon/command/migratecmd`。

```go
func New[C any](fsys fs.FS, dbConfig func(cfg C) database.Config, opts ...Option) *cobra.Command
func Name(name string) Option
func Publish(sources ...fs.FS) *cobra.Command
func Make() *cobra.Command
```

`New` 返回 `migrate` 及其 `up`、`status`、`down`、`make` 子命令；`Name` 为另一个数据库构造 `migrate <name> …`；`Publish` 返回 `migrate publish`；`Make` 单独返回 `migrate make`，它离线运行。
