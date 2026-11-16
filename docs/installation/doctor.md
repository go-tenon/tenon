# 自检

部署出问题时，看到的往往是底层组件的报错：PostgreSQL 的 `wal_level` 不对，队列的迁移报 `logical decoding requires "wal_level" >= "logical"`；数据库用户少了 `REPLICATION` 属性，worker 报复制连接失败；升级后忘了执行迁移，某个接口报“列不存在”。这些报错只说出了现象，没有说该改哪里。

自检把这些情况在启动之前一次查出来。它读取依赖的版本和设置、迁移的状态、文件存储的权限，每一个问题都附带修改方法和文档：

```text
$ ./bin/api doctor
queue
  ok    postgres_version
  FAIL  wal_level: wal_level is replica; the queue needs logical
        fix: set wal_level = logical and restart PostgreSQL:
             Docker: command: [postgres, -c, wal_level=logical]
             managed databases: enable logical replication in the parameter group (RDS: rds.logical_replication = 1)
        see: docs/background/queue.md#preparing
  ...
```

它与[健康检查](../http/lifecycle.md#health-checks)分工不同：健康检查每隔几秒运行一次，只确认依赖连得上；自检在部署、升级和排查问题时运行，检查依赖的设置对不对。应用交给别人部署（例如客户自己用 Docker Compose 运行）时，它尤其有用：出问题的人不一定熟悉 PostgreSQL 和 NATS，自检告诉他具体改哪一项。

阅读前需要了解：[部署](deployment.md)；[命令行](../general/commands.md)；[依赖装配](../concepts/dependencies.md)。

- [装配 doctor 命令](#doctor-command)
- [读懂结果](#reading-results)
- [迁移之前检查](#before-migrating)
- [启动之前检查](#before-starting)
- [在 Docker Compose 中使用](#with-compose)
- [编写应用自己的检查](#custom-checks)
- [框架提供的检查](#framework-checks)
- [常见问题](#troubleshooting)
- [API 参考](#api-reference)

<a id="doctor-command"></a>
## 装配 doctor 命令

检查项按依赖分组，框架的包各自提供一组：`database.Doctor` 检查 PostgreSQL，`migrate.Doctor` 检查迁移，`queue.Doctor` 检查队列，`filesystem.Doctor` 检查文件存储。在 `bootstrap` 中把应用用到的几组放在一起，写成 `App` 的方法：

```go
// doctor 是应用的自检，doctor 命令和启动之前的检查共用。
func (a *App) doctor() []doctor.Group {
    return []doctor.Group{
        database.Doctor(a.db),
        migrate.Doctor(a.db, migrations.FS),
        queue.Doctor(a.db, a.nc, a.cfg.Queue),
        filesystem.Doctor(a.disk),
    }
}
```

`a.db`、`a.nc` 是[打开资源](../concepts/dependencies.md#running-parts)时得到的数据库和 NATS 连接，`a.disk` 是[文件存储](../libraries/filesystem.md)的磁盘，`migrations.FS` 是[嵌入的迁移文件](../database/migrations.md#embedding-migrations)。用不到的组不写，例如没有队列的应用去掉 `queue.Doctor`。

然后在需要 `App` 的命令中加上 `doctorcmd.New`：

```go
func Commands() []*cobra.Command {
    return command.Bind(open, func(a *App) []*cobra.Command {
        return []*cobra.Command{
            doctorcmd.New(a.doctor()...),
            queuecmd.New(a.db, queuecmd.WithJetStream(a.nc, a.cfg.Queue)),
        }
    })
}
```

`command.Bind` 先用一个空的 `App` 声明命令，执行时才打开资源、再声明一次，见[只在执行时装配的命令](../concepts/dependencies.md#deferred-commands)。各个 `Doctor` 只保存传入的依赖，检查时才使用它们，所以用空的 `App` 声明也没有问题。

运行它：

```bash
./bin/api doctor
```

<a id="reading-results"></a>
## 读懂结果

每一项检查的结果是下面四种之一：

| 标记 | 结果 | 含义 |
| --- | --- | --- |
| `ok` | 通过 | |
| `warn` | 提示 | 程序可以运行，但存在隐患，例如可能写满磁盘的设置 |
| `FAIL` | 失败 | 程序在这种环境中不能正常工作；或者检查本身没能完成，例如连接中断 |
| `skip` | 跳过 | 这一项不适用，例如迁移还没执行时不检查复制槽；或者同组前面的检查没能完成 |

问题下面的 `fix:` 是修改方法，`see:` 是相关的文档。最后一行是统计。

同一组的检查按顺序执行，前面的是后面的前提：连不上数据库时，同组后面的检查都会跳过，免得满屏都是同一个原因的错误。不同的组同时执行，每一项最多 10 秒。

有检查失败时，命令以退出码 4（`ExitUnavailable`）结束，只有提示时以 0 结束，部署脚本可以据此决定是否继续。两个选项：

- `--strict`：提示也算失败。在 CI 中检查目标环境时使用，不放过任何隐患。
- `--format json`：输出 JSON，每一项有 `group`、`check`、`status`，以及 `summary`、`fix`、`doc`。脚本处理或贴到工单中时使用。

<a id="before-migrating"></a>
## 迁移之前检查

迁移是部署的第一步，环境不对时它往往第一个失败，而且报错难懂：`wal_level` 不是 `logical` 时，队列的迁移报的是 `logical decoding requires "wal_level" >= "logical"`。`migratecmd.Check` 让 `migrate up` 在执行迁移之前先运行自检：

```go
migratecmd.New(migrations.FS, dbConfig, migratecmd.Check(func(cfg bootstrap.Config, db *bun.DB) []doctor.Group {
    return []doctor.Group{database.Doctor(db), queue.Doctor(db, nil, cfg.Queue)}
}))
```

`dbConfig` 与原来相同；函数收到加载好的配置和迁移用的连接，返回要执行的检查。有检查失败时，`migrate up` 不执行任何迁移，以退出码 4 结束，错误中列出每一项问题和修改方法：

```text
doctor.failed: 2 check(s) failed
  queue/wal_level: wal_level is replica; the queue needs logical
    fix: set wal_level = logical and restart PostgreSQL:
         ...
  queue/replication_connection: the database user cannot open a replication connection: permission denied to start WAL sender
    fix: grant it the REPLICATION attribute: ALTER ROLE <user> REPLICATION
         (RDS: GRANT rds_replication TO <user>)
```

这里只放数据库一侧的检查：`queue.Doctor` 的 NATS 连接传 `nil`，不检查 NATS；也不放 `migrate.Doctor`，迁移还没执行，它一定失败。`queue.Doctor` 的复制槽由队列的迁移创建，迁移之前这一项自动跳过。`migrate status`、`migrate down` 不运行这些检查。

<a id="before-starting"></a>
## 启动之前检查

`doctor.Require` 执行检查，把提示写入日志，有检查失败时返回错误。在 `run` 中打开资源之后、启动各个部分之前调用它，环境不对时进程拒绝启动：

```go
app, err := open(ctx, cfg, &stack)
if err != nil {
    return err
}
if err := doctor.Require(ctx, nil, app.doctor()...); err != nil {
    return err // 以退出码 4 结束，错误中有修改方法
}
```

`run` 见[在入口中组装应用](../concepts/dependencies.md#assembling-the-application)。第二个参数是记录提示的日志器，`nil` 表示 `slog.Default()`。这一步通常不到一秒：每一项都是一两条查询，复制连接建立后立即关闭。

最常见的收获是“忘了执行迁移”：升级镜像后直接启动，`migrations` 组失败，进程退出并提示先运行 `migrate up`，而不是带着旧的表结构运行，等到某个请求报“列不存在”。

> [!NOTE]
> 滚动升级时，旧版本的实例可能在新的表结构上重新启动，这时 `migrations` 组给出的是提示（数据库比程序新），不会阻止启动，见[新旧进程同时运行](operations.md#upgrade-overlap)。

<a id="with-compose"></a>
## 在 Docker Compose 中使用

[Docker 部署](deployment.md#docker)中的镜像基于 distroless，里面没有 shell，不能写 `doctor && migrate up` 这样的命令。所以检查要装配在命令内部：`migrate` 服务照常运行 `[migrate, up]`，迁移之前的检查由 `migratecmd.Check` 完成；`serve`、`start` 启动之前的检查由 `doctor.Require` 完成。

排查问题时，在同一个镜像中单独运行 `doctor`：

```bash
docker compose run --rm serve doctor
```

`--rm` 在命令结束后删除这个临时容器。它使用 `serve` 服务的环境变量和网络，与正式运行时连接的是同一组服务。

<a id="custom-checks"></a>
## 编写应用自己的检查

应用自己的依赖也可以加进来，例如一个外部的模型网关。一项检查是一个返回 `error` 的函数：

```go
func (a *App) doctor() []doctor.Group {
    return []doctor.Group{
        database.Doctor(a.db),
        {Name: "gateway", Checks: []doctor.Check{
            {Name: "api_key", Run: func(ctx context.Context) error {
                status, err := a.gateway.Ping(ctx)
                if err != nil {
                    return err // 没能完成检查：失败，同组后面的检查跳过
                }
                if status == http.StatusUnauthorized {
                    return doctor.Fail("the gateway rejected the API key", "set GATEWAY_API_KEY to a valid key").
                        See("https://gateway.example.com/docs/keys")
                }
                return nil
            }},
        }},
    }
}
```

`Run` 的返回值决定结果：

- `nil`：通过；
- `doctor.Fail(summary, fix)`：失败。`summary` 说明哪里不对，`fix` 说明怎样修改，可以有多行；`See` 附上文档地址；
- `doctor.Warn(summary, fix)`：提示；
- `doctor.Skip(reason)`：这一项不适用，例如前提不满足；
- 其他错误：检查没能完成，结果为失败，同组后面的检查跳过。

`summary` 和 `fix` 会原样显示给运维人员，也会出现在日志和工单中，不要包含密码和密钥。

<a id="framework-checks"></a>
## 框架提供的检查

**`database.Doctor(db)`**，组名 `postgres`：

| 检查 | 内容 | 不满足时 |
| --- | --- | --- |
| `connection` | 连接可用 | 失败 |
| `clock` | 本机时钟与数据库时钟相差不超过 1 秒 | 提示 |

框架把 Go 算出的时间写进 SQL：锁的过期、限流的窗口、任务的执行时间都按应用实例的时钟计算。每个实例都与数据库对时，它们之间也就一致，所以 `clock` 用数据库作为共同的参照。

**`migrate.Doctor(db, fsys)`**，组名 `migrations`：`up_to_date` 确认 `fsys` 中的迁移都已执行，否则失败；数据库的版本比程序新时提示。它不持有迁移锁，迁移正在执行时也不会等待。

**`queue.Doctor(db, nc, cfg)`**，组名 `queue`，要求见[队列的准备](../background/queue.md#preparing)和[运维](operations.md#queue)：

| 检查 | 内容 | 不满足时 |
| --- | --- | --- |
| `postgres_version` | PostgreSQL 17 以上 | 失败 |
| `wal_level` | `logical` | 失败 |
| `replication_limits` | `max_replication_slots`、`max_wal_senders` 大于 0 | 失败 |
| `replication_connection` | 用 worker 的方式建立一条复制连接：用户有 `REPLICATION` 属性，`replication_dsn` 可用 | 失败 |
| `wal_keep_size` | 设置了 `max_slot_wal_keep_size` | 提示 |
| `slot` | 复制槽存在，没有失效；快要超过上限时提示。迁移之前跳过 | 失败 |
| `nats`、`nats_version`、`jetstream` | `nc` 不为 `nil` 时：连接可用，NATS 2.15 以上，开启了 JetStream | 失败 |

**`filesystem.Doctor(disk)`**，组名 `storage`：`read_write` 写入一个很小的探测对象（键以 `tenon-doctor/` 开头），读回比较内容，再删除它。本地磁盘检查目录可写，S3 检查 bucket、地址和凭据。

<a id="troubleshooting"></a>
## 常见问题

**`doctor` 命令只输出了一行“连不上数据库”的错误，没有列出检查结果**

`command.Bind` 在执行命令之前先运行 `open` 打开资源，数据库或 NATS 连不上时 `open` 就失败了，还没轮到自检。这时的错误已经说明了原因（地址、认证或网络），先让 `open` 成功，再运行 `doctor` 查看其他设置。

**`migrate up` 报 `doctor.failed`，但 `doctor` 命令没有失败**

两边执行的检查不同：`migratecmd.Check` 中的检查是单独写的。确认两处用的 `cfg.Queue` 相同，特别是 `replication_dsn`。

**每次启动都打印 `max_slot_wal_keep_size is unlimited` 的提示**

这是在提醒：所有 worker 停止时，复制槽积压的 WAL 会一直增长直到写满磁盘。给 PostgreSQL 设置上限即可，例如在 compose 中写 `command: [postgres, -c, wal_level=logical, -c, max_slot_wal_keep_size=10GB]`，见[运维](operations.md#queue)。

**`storage` 组在 Docker 中失败：`cannot write to the storage: permission denied`**

挂载的卷属于 root，程序以 `nonroot` 用户运行，没有写权限。按[用 Docker 运行](deployment.md#docker)中的做法，在镜像中预先建好目录并交给 `nonroot`。

<a id="api-reference"></a>
## API 参考

### Check、Group

```go
type Check struct {
    Name string
    Run  func(ctx context.Context) error
}

type Group struct {
    Name   string
    Checks []Check
}
```

一项检查和一组检查。组内按顺序执行，各组并行；每项最多 `doctor.CheckTimeout`（10 秒），panic 按失败处理。

### Fail、Warn、Skip、Problem

```go
func Fail(summary, fix string) *Problem
func Warn(summary, fix string) *Problem
func (p *Problem) See(doc string) *Problem
func Skip(reason string) error
```

`Problem` 是查到的问题，有 `Summary`、`Fix`、`Doc` 和 `Warning` 四个字段。被包装过的 `*Problem` 同样有效。

### Run、Report

```go
func Run(ctx context.Context, groups ...Group) Report
func (r Report) Count(s Status) int
func (r Report) Err() error
func (r Report) WriteText(w io.Writer) error
```

`Run` 执行全部检查，`Report.Results` 按传入的顺序列出每一项的 `Result`（`Group`、`Check`、`Status`、`Summary`、`Fix`、`Doc`）。`Status` 是 `doctor.Passed`、`Warning`、`Failed`、`Skipped` 之一。`Err` 在有失败时返回 `doctor.ErrFailed` 分类的错误（`errorx.Unavailable`，命令以退出码 4 结束），只有提示时返回 `nil`。`WriteText` 写出 `doctor` 命令的文本格式。

### Require

```go
func Require(ctx context.Context, logger *slog.Logger, groups ...Group) error
```

执行检查，把提示写入 `logger`（`nil` 表示 `slog.Default()`），返回 `Report.Err()`。

### doctorcmd.New、migratecmd.Check

```go
func doctorcmd.New(groups ...doctor.Group) *cobra.Command
func migratecmd.Check[C any](groups func(cfg C, db *bun.DB) []doctor.Group) migratecmd.Option
```

`doctorcmd.New` 返回 `doctor` 命令，选项 `--format text|json` 和 `--strict`。`migratecmd.Check` 让 `migrate up` 在迁移之前运行检查，`C` 是应用的配置结构，与 `migratecmd.New` 的相同。

### 各包的检查组

```go
func database.Doctor(db *bun.DB) doctor.Group
func migrate.Doctor(db *bun.DB, fsys fs.FS) doctor.Group
func queue.Doctor(db *bun.DB, nc *nats.Conn, cfg queue.Config) doctor.Group
func filesystem.Doctor(disk filesystem.Disk) doctor.Group
```

检查项见[框架提供的检查](#framework-checks)。参数在检查时才使用，可以在打开资源之前构造。
