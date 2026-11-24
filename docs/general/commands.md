# 命令行

应用的程序带子命令：`serve` 运行常驻进程，`migrate`、`config` 等是运维命令，应用还可以加自己的命令，例如 `./bin/blog article publish`。命令是原生的 [Cobra](https://github.com/spf13/cobra) `*cobra.Command`，框架负责加载配置、收到停止信号时取消 `ctx`、把错误转换为退出码。

包：`command`、`command/httpcmd`、`command/migratecmd`。

<a id="example"></a>
## 示例

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

```bash
go run . hello        # 打印 config/app.yaml 中的 greeting
go run . config show  # 输出加载后的完整配置
```

`command.Run` 读取 `.env`、加载并校验配置之后才调用函数；进程收到 `SIGINT`、`SIGTERM` 时取消 `ctx`。

<a id="declaring-the-cli"></a>
## 在 `main.go` 中装配命令行

`cmd/<程序名>/main.go` 只组装根命令并执行，装配代码在 `internal/bootstrap` 中：

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

- `bootstrap.Open`、`Parts`、`DocRouter` 见[依赖装配](../concepts/dependencies.md#assembling-the-application)；`bootstrap.Commands` 是需要应用资源的命令，见[用 `command.Bind` 声明需要资源的命令](#bind-commands)。
- `command.Root` 定义所有子命令共用的 `--config` 选项。没有传入时读取环境变量 `APP_CONFIG`，都没有时加载 `config/app.yaml` 和存在时的 `config/local.yaml`，见[叠加多个配置文件](configuration.md#overriding-configuration)。
- `command.Execute` 执行根命令：收到停止信号时取消命令的 context；命令失败时把提示写到标准错误，以对应的[退出码](#exit-codes)结束进程。
- 程序中只有装配了的命令。开发用的 `dev`、`build`、`generate` 由[开发工具](../installation/tooling.md) `go tool tenon` 提供。

| 构造函数 | 创建的命令 |
| --- | --- |
| `command.Serve(open, parts)` | [`serve`](#serve-command) |
| `command.Run(use, short, run)` | 只需要配置的一次性命令 |
| `command.Config[C]()` | [`config check`、`config show`](#config-command) |
| `migratecmd.New(fsys, dbConfig, opts...)` | [`migrate up/status/down/make`](#migrate-command) |
| `httpcmd.Health(http)` | [`health`](#health-command) |
| `httpcmd.Commands(router, opts...)` | [`openapi`](#openapi-command)、[`routes`](#routes-command)、`client`（[生成客户端](../http/clients.md)）；`httpcmd.Translations(locales.FS)` 让生成的校验提示与服务端相同 |
| `rpccmd.New(server)` | `rpc asyncapi`、`rpc endpoints`，见[导出接口描述](../rpc/endpoints.md#exporting) |
| `command.Bind(open, declare)` | 应用声明的需要资源的命令，例如 `article publish` |
| `queuecmd.New(db, opts...)` | `queue status`、`queue failed`，在 `command.Bind` 中声明，见[队列状态](../background/queue.md#stats-command) |
| `schedulecmd.New(tasks, m)` | `schedules`，在 `command.Bind` 中声明，见[列出任务的命令](../background/scheduling.md#schedules-command) |
| `doctorcmd.New(groups...)` | `doctor`，在 `command.Bind` 中声明，见[自检](../installation/doctor.md) |

<a id="custom-commands"></a>
## 自定义命令：`command.LoadConfig`、`command.NoArgs`

任意 Cobra 命令都可以加入根命令。需要配置时调用 `command.LoadConfig`，它读取同一个 `--config` 选项，失败时以 `ExitConfig` 退出。`root` 是 `command.Root` 创建的根命令，`Config` 是应用的根配置结构体：

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

- 位置参数用 `command.NoArgs`、`command.ExactArgs(n)` 检查，参数个数不对时以 `ExitUsage` 退出；Cobra 的同名函数没有这个退出码。
- 选项用 Cobra 原生的 `Flags` 定义。
- `cmd.Context()` 在进程收到停止信号时被取消。
- 执行某个业务动作的命令写成动作的 `Command` 入口，见[从命令行运行动作](../concepts/actions.md#as-commands)。

<a id="bind-commands"></a>
### 用 `command.Bind` 声明需要资源的命令

需要数据库等应用资源的命令用 `command.Bind` 声明，资源在命令执行时才打开。`Open` 与 `serve` 用的是同一个，`App` 是它返回的资源：

```go
// Commands 返回需要 App 的命令。
func Commands() []*cobra.Command {
    return command.Bind(Open, func(a *App) []*cobra.Command {
        return []*cobra.Command{
            article.Commands(a.db),
            queuecmd.New(a.db, queuecmd.WithJetStream(a.nc, a.cfg.Queue)),
        }
    })
}
```

执行时 `Bind` 把解析到的选项复制到重新声明的命令上，用 `cmd.Flags().GetString` 读取和用 `StringVar` 绑定变量都可以。机制见[只在执行时装配的命令](../concepts/dependencies.md#deferred-commands)。

<a id="input-and-output"></a>
## 输入与输出：标准输出、`command.ReadSecret`

标准输出只写命令的结果，诊断信息和日志写到标准错误：`./bin/blog openapi > openapi.json` 这样的重定向才得到干净的结果。日志默认写到标准错误，见[日志](logging.md#creating-a-logger)。

- 自定义命令用 `cmd.OutOrStdout()` 和 `cmd.ErrOrStderr()` 写输出，测试中用 `SetOut`、`SetErr` 把输出重定向到缓冲区，见[命令测试](../testing/testing.md#command-tests)。
- 密码这类值用 `command.ReadSecret` 读取，不作为命令行参数：参数会留在 shell 的历史和进程列表中。

```go
password, err := command.ReadSecret(cmd, "密码：", "再输入一次：")
if err != nil {
    return err // 两次不一致时是 command.ErrSecretMismatch
}
```

标准输入是终端时，`ReadSecret` 关闭回显并显示提示，第三个参数不为空时再读一次确认；标准输入不是终端时（脚本、`docker compose run -T`）读取第一行，例如 `printf '%s\n' "$PASSWORD" | ./bin/blog user create`。

<a id="exit-codes"></a>
## 退出码

部署脚本和 CI 按退出码判断命令是否成功和失败的原因：

| 退出码 | 常量 | 含义 |
| --- | --- | --- |
| 0 | | 成功 |
| 1 | `command.ExitFailure` | 被业务规则拒绝，例如输入无效或资源不存在 |
| 2 | `command.ExitUsage` | 用法错误：未知命令、未知选项、参数个数不对 |
| 3 | `command.ExitConfig` | 配置文件缺失或无效，`.env` 语法错误 |
| 4 | `command.ExitUnavailable` | 外部依赖不可用，部署脚本可以重试 |
| 5 | `command.ExitInternal` | 其他未分类的错误 |

- 用法错误和配置错误由框架标记。其他错误按[业务错误的分类](errors.md#error-kinds)转换：`errorx.Unavailable` 为 4，`errorx.Internal` 和不是业务错误的错误为 5，其余分类为 1。
- `database.Open` 连不上数据库时返回 `database.ErrUnavailable`，它属于 `errorx.Unavailable`。
- 命令直接返回动作的错误。需要别的退出码时用 `command.AsExitError` 标记，`command.ExitCode(err)` 取出退出码：

```go
return command.AsExitError(command.ExitUnavailable, err)
```

<a id="error-messages"></a>
### 错误提示：`command.Languages`

命令失败时，业务错误显示翻译后的提示和错误码，与 HTTP 响应的 `detail` 是同一句话：

```text
这个邮箱已经被使用（user.email_taken）
```

- 系统故障（`errorx.Unavailable`、`errorx.Internal`）和没有译文的错误显示错误本身，方便排查。
- `command.Execute` 默认使用框架内置的中英文消息，默认语言是中文。应用有自己的[翻译文件](localization.md#translation-files)时用 `command.Languages` 交给它，通常与 HTTP 的 `httpx.Languages` 是同一个：

```go
command.Execute(root, command.Languages(i18n.MustNew("zh-CN", locales.FS)))
```

- 命令中用 `i18n.T(cmd.Context(), id, data)` 按同一种语言翻译自己的输出。
- `command.ErrorText(ctx, err)` 返回失败时显示的文字，自己处理错误输出时使用。

<a id="serve-command"></a>
## serve：运行常驻进程（`command.Serve`、`command.Part`）

常驻进程由几个**部分**组成：HTTP 服务、[RPC 服务](../rpc/endpoints.md#running)、[队列 worker](../background/queue.md#running-workers)、[定时任务调度器](../background/scheduling.md#running-the-scheduler)。每个部分是 `App` 的一个方法（见[多个运行部分](../concepts/dependencies.md#running-parts)），在 `bootstrap` 的 `Parts` 中登记：

```go
// Parts 是常驻进程的运行部分：serve 运行全部部分，serve <名字> 只运行其中一个。
func Parts(a *App) []command.Part {
    return []command.Part{
        {Name: "http", Short: "接口、页面和实时频道", Start: a.HTTP},
        {Name: "worker", Short: "处理队列任务", Start: a.Worker},
        {Name: "scheduler", Short: "执行定时任务", Start: a.Scheduler},
        {Name: "realtime", Short: "只承接实时频道的连接", Start: a.Realtime, Standalone: true},
    }
}
```

| 命令 | 运行的部分 |
| --- | --- |
| `serve`、`serve all` | 全部部分，`Standalone` 的除外；`all` 供 systemd 的模板单元（`blog@all`）使用 |
| `serve http` | 只运行 HTTP 服务；`serve worker` 等同理 |

- 小型部署只运行 `serve`；业务量增长后每个部分一个进程，代码和配置不变。多个实例中调度器保证[每次触发只执行一次](../background/scheduling.md#multiple-instances)。
- `Standalone` 的部分只单独运行，例如 [`realtime`](../libraries/realtime.md#separate-process)：运行全部时实时频道已经挂在 HTTP 服务上。
- 构造命令时用零值的 `App` 调用 `Parts`，所以其中只取方法值（`a.HTTP`），不使用依赖。
- 名字写错或给出多个名字时以 `ExitUsage` 退出，`serve --help` 列出全部名字。
- 各部分共用 `Open` 中创建的数据库连接池，[`max_conns`](../database/database.md#configuration) 按 HTTP 请求和 worker 并发数之和估算。

执行过程：

1. 加载配置，调用 `Open` 打开共用的资源，按顺序调用选中部分的 `Start`。任何一个部分装配失败（例如端口被占用）时，没有部分开始运行。
2. 同时运行选中的部分。收到停止信号时各部分平滑停止（HTTP 服务排空请求，worker 和调度器等待执行中的任务），全部返回之后关闭资源。
3. 一个部分出错退出，或者在收到停止信号之前返回（错误中包含 `command.ErrStoppedEarly`）时，其余部分随之停止，进程以非 0 状态退出，交给 systemd、Docker 重启。

<a id="health-command"></a>
## health：存活检查（`httpcmd.Health`）

`health` 请求本机 HTTP 服务的 `/livez`，返回 2xx 时以 0 退出，否则以 `ExitFailure` 退出。用于 Docker Compose 和 Swarm 的健康检查，镜像中不需要 curl，写法见[部署](../installation/deployment.md#health-checks)：

```go
httpcmd.Health(func(cfg bootstrap.Config) httpx.Config { return cfg.HTTP })
```

```bash
./bin/blog health && echo alive
```

- 应用在路由中注册 `/livez`，见[健康检查](../concepts/lifecycle.md#health-checks)。
- 监听地址的主机为空、`0.0.0.0` 或 `::` 时请求 `127.0.0.1`。
- `--timeout` 是等待响应的最长时间，默认 3 秒。

<a id="openapi-command"></a>
## openapi：导出接口文档（`httpcmd.OpenAPI`）

`openapi` 构造应用的路由，用 [`rest.Lookup`](../http/openapi.md#exporting-openapi) 取出其中的 API，把 OpenAPI 文档写到标准输出：

```bash
./bin/blog openapi > openapi.json                         # OpenAPI 3.1
./bin/blog openapi --spec 3.0 > openapi-3.0.json          # 只支持 3.0 的生成器，例如 oapi-codegen
./bin/blog openapi --base-path /api/v2 > openapi-v2.json  # 路由中有多个 API 时选择一个
```

路由函数通常是 `bootstrap.DocRouter`：它用 `config.Default` 的配置和没有打开资源的 `App` 调用 `serve` 也在用的路由函数，所以导出的文档与实际运行的接口一致，命令离线运行。

<a id="routes-command"></a>
## routes：查看全部路由（`httpcmd.Routes`）

`routes` 离线列出应用的全部路由，包括 JSON API、页面和直接注册在 chi 上的路由：

```bash
./bin/blog routes
./bin/blog routes --method post --path /articles
./bin/blog routes --all   # 包括 Huma 的 /docs、/openapi.json 等文档接口
```

```text
METHOD  PATH                    OPERATION ID     ACCESS            SUMMARY
POST    /articles               article-create   token: articles   创建文章
GET     /articles/{id}          article-get      token: articles   获取文章
POST    /articles/{id}/publish  article-publish  token: articles   发布文章
```

- `--path` 按包含的文本匹配，与 `--method` 同时使用时取交集。
- **ACCESS**：`public` 是显式公开；`token` 要求会话 cookie 或 Bearer token，`token: articles` 同时列出所需的权限；`docs` 是 Huma 的文档接口。`unknown` 表示无法从声明确定策略（例如直接注册在 chi 上的健康检查），**不**表示允许匿名访问。
- 响应任意方法的路由（例如单页应用的 `/*`、`Mount("/files", handler)`）方法显示为 `*`。

<a id="migrate-command"></a>
## migrate：执行迁移（`migratecmd.New`）

`migratecmd.New(fsys, dbConfig)` 创建迁移命令。`fsys` 是[嵌入的迁移文件](../database/migrations.md#embedding-migrations)，`dbConfig` 从应用配置中取出数据库配置：

```bash
./bin/blog migrate up       # 执行全部未执行的迁移
./bin/blog migrate status   # 查看每个迁移的状态
./bin/blog migrate down     # 回滚最近一次迁移
go run ./cmd/blog migrate make create_articles_table  # 新建迁移文件，不连接数据库
```

- 数据库连不上时以 `ExitUnavailable` 退出，部署脚本可以据此重试。
- 应用连接多个数据库时，用 `migratecmd.Name` 为其他数据库构造 `migrate <name> …`，见[多个数据库](../database/database.md#multiple-databases)。
- `migratecmd.Check` 让 `migrate up` 在迁移之前运行[自检](../installation/doctor.md)。

需要框架功能自带的迁移（例如 `filesystem.Migrations()`）时，在 `migratecmd.New` 返回的命令上添加 `migratecmd.Publish(sources...)`，再用 `migrate publish` 把尚未发布的 SQL 拷贝到 `--dir`（默认 `migrations`），执行仍用 `migrate up`，见[发布框架迁移](../database/migrations.md#publishing-framework-migrations)：

```bash
go run ./cmd/blog migrate publish --dir migrations
```

<a id="config-command"></a>
## config：检查配置（`command.Config`）

`command.Config[C]()` 创建两个命令，`C` 是应用的根配置结构体：

```bash
./bin/blog config check               # 只加载并校验，不访问网络；发布前运行
./bin/blog config show --format json  # 输出加载后的配置，默认 YAML
```

- `config show` 的输出包括默认值、多个文件合并的结果和展开后的环境变量。
- 配置文件缺失或校验失败时，这两个命令和所有需要配置的命令都以 `ExitConfig` 退出。在项目根目录执行：`config/app.yaml` 和 `.env` 都按工作目录查找。配置的写法见[配置](configuration.md)。

<a id="groups"></a>
## 帮助中的分组：`command.InGroup`

`command.Execute` 把根命令的子命令分组显示在帮助中：运行（`serve`）、运维（`config`、`health`、`migrate`）、应用、开发（`client`、`openapi`、`routes`）。框架的命令已经标好分组，应用的命令默认归入“应用”。放进别的分组时用 `command.InGroup`：

```go
command.InGroup(cmd, command.GroupOps) // 例如把数据回填命令放进“运维”
```
