# 自检

自检在部署、升级和排查问题时检查运行环境：依赖的版本和设置、迁移是否执行、文件存储的权限，每个问题附带修改方法和文档。它与[健康检查](../concepts/lifecycle.md#health-checks)分工不同：健康检查每隔几秒确认依赖连得上，自检检查依赖的设置对不对。

包：`doctor`、`doctor/doctorcmd`、`command/migratecmd`。

```text
$ ./bin/blog doctor
queue
  ok    postgres_version
  FAIL  wal_level: wal_level is replica; the queue needs logical
        fix: set wal_level = logical and restart PostgreSQL:
             ...
        see: docs/background/queue.md#preparing
```

<a id="doctor-command"></a>
## 装配 doctor 命令：`doctorcmd.New`

检查项按依赖分组，框架的包各自提供一组，见[框架提供的检查](#framework-checks)。在 `internal/bootstrap` 中把应用用到的几组写成 `App` 的方法，`doctor` 命令和[启动之前的检查](#before-starting)共用它：

```go
// doctor 是应用的自检。
func (a *App) doctor() []doctor.Group {
    return []doctor.Group{
        database.Doctor(a.db),
        migrate.Doctor(a.db, migrations.FS),
        queue.Doctor(a.db, a.nc, a.cfg.Queue),
        filesystem.Doctor(a.disk),
    }
}

func Commands() []*cobra.Command {
    return command.Bind(Open, func(a *App) []*cobra.Command {
        return []*cobra.Command{
            doctorcmd.New(a.doctor()...),
            queuecmd.New(a.db, queuecmd.WithJetStream(a.nc, a.cfg.Queue)),
        }
    })
}
```

- `a.db`、`a.nc` 是 `Open` 打开的数据库和 NATS 连接（见[多个运行部分](../concepts/dependencies.md#running-parts)），`a.disk` 是[文件存储](../libraries/filesystem.md)的磁盘，`migrations.FS` 是[嵌入的迁移文件](../database/migrations.md#embedding-migrations)。用不到的组不写。
- `command.Bind` 执行时才打开资源，见[只在执行时装配的命令](../concepts/dependencies.md#deferred-commands)。`Open` 失败（数据库或 NATS 连不上）时 `doctor` 只输出这个错误，先修好连接再运行。

<a id="reading-results"></a>
## 读懂结果

| 标记 | 含义 |
| --- | --- |
| `ok` | 通过 |
| `warn` | 程序可以运行，但有隐患，例如可能写满磁盘的设置 |
| `FAIL` | 程序在这个环境中不能正常工作，或者检查本身没能完成（例如连接中断） |
| `skip` | 不适用（例如迁移还没执行时不检查复制槽），或者同组前面的检查没能完成 |

- `fix:` 是修改方法，`see:` 是相关的文档。
- 同一组按顺序执行，一项没能完成时同组后面的都跳过；各组并行，每项最多 10 秒。
- 有 `FAIL` 时以退出码 4 结束，只有 `warn` 时以 0 结束。
- `--strict`：`warn` 也以 4 结束，在 CI 中检查目标环境时使用。
- `--format json`：每一项有 `group`、`check`、`status`（`passed`、`warning`、`failed`、`skipped`）、`summary`、`fix`、`doc`，供脚本处理或贴进工单。

<a id="before-migrating"></a>
## 迁移之前检查：`migratecmd.Check`

`wal_level` 不对时，队列的迁移报 `logical decoding requires "wal_level" >= "logical"`。`migratecmd.Check` 让 `migrate up` 在执行迁移之前运行自检，给出修改方法：

```go
migratecmd.New(migrations.FS, dbConfig, migratecmd.Check(func(cfg bootstrap.Config, db *bun.DB) []doctor.Group {
    return []doctor.Group{database.Doctor(db), queue.Doctor(db, nil, cfg.Queue)}
}))
```

- `dbConfig` 是 `migratecmd.New` 原有的参数；函数收到加载好的配置和迁移用的连接。
- 有检查失败时 `migrate up` 不执行任何迁移，以退出码 4 结束，错误 `doctor.failed` 列出每一项问题和修改方法。`migrate status`、`migrate down` 不检查。
- 只放数据库一侧的检查：`queue.Doctor` 的 NATS 连接传 `nil`；不放 `migrate.Doctor`，迁移之前它一定失败。迁移之前 `slot` 自动跳过。
- 两处的 `cfg.Queue`（特别是 `replication_dsn`）要与 `doctor` 命令的相同，否则结果不一致。

<a id="before-starting"></a>
## 启动之前检查：`doctor.Require`

`doctor.Require` 执行检查，把提示写入日志，有失败时返回错误。在 `bootstrap` 中包一层 `Open`，`serve` 用它，环境不对时进程拒绝启动：

```go
// OpenChecked 打开资源，再运行启动之前的检查，serve 命令用它。
func OpenChecked(ctx context.Context, cfg Config, stack *cleanup.Stack) (*App, error) {
    app, err := Open(ctx, cfg, stack)
    if err != nil {
        return nil, err
    }
    return app, doctor.Require(ctx, nil, app.doctor()...) // 失败时以退出码 4 结束
}
```

- `main.go` 中改为 `command.Serve(bootstrap.OpenChecked, bootstrap.Parts)`；一次性命令仍用 `Open`，环境不对时 `doctor` 命令也能运行。
- `Require` 的第二个参数是记录提示的日志器，`nil` 表示 `slog.Default()`。
- 升级后忘了执行迁移时 `migrations` 组失败，进程退出并提示先运行 `migrate up`。数据库比程序新（滚动升级时旧版本在新表结构上重启）时只给提示，见[新旧进程同时运行](operations.md#upgrade-overlap)。

<a id="with-systemd"></a>
## 在生产环境中排查

使用 `tenon deploy` 部署时，直接以真实服务用户、环境变量和工作目录在远端运行自检：

```bash
go tool tenon deploy run -- doctor
```

手动登录服务器排查时用 `systemd-run` 保持与服务完全一致的运行上下文：

```bash
sudo systemd-run --wait --pipe --quiet --uid=blog \
    -p EnvironmentFile=/etc/blog/env -p WorkingDirectory=/opt/blog \
    /opt/blog/bin/blog doctor
```

- `storage` 组报 `permission denied`：检查 `/opt/blog/storage` 是否归服务用户所有，且 unit 的 `ReadWritePaths` 包含该目录，见[用 systemd 运行](deployment.md#systemd)。

交给客户的完整部署见[交给客户自托管](self-hosting.md)。

<a id="custom-checks"></a>
## 编写应用自己的检查

一项检查是返回 `error` 的函数，`a.gateway` 是应用自己的外部模型网关客户端：

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

| `Run` 返回 | 结果 |
| --- | --- |
| `nil` | 通过 |
| `doctor.Fail(summary, fix)` | 失败；`See` 附上文档地址，`fix` 可以有多行 |
| `doctor.Warn(summary, fix)` | 提示 |
| `doctor.Skip(reason)` | 不适用 |
| 其他错误 | 检查没能完成：失败，同组后面的检查跳过 |

`summary` 和 `fix` 原样显示给运维人员，也进入日志和工单，不写密码和密钥。

<a id="admin-page"></a>
## 在后台页面中显示：`doctor.Run`

`doctor.Run` 返回 `doctor.DoctorReport`，它带 JSON 标签，可以直接作为接口的响应体，在 OpenAPI 中名为 `DoctorReport`：

```go
type DoctorOutput struct {
    Body doctor.DoctorReport
}

status := huma.Operation{
    OperationID: "system-doctor",
    Method:      http.MethodGet,
    Path:        "/system/doctor",
    Summary:     "运行环境自检",
}
requireOps(&status)
rest.Register(api, status, func(ctx context.Context, _ *struct{}) (*DoctorOutput, error) {
    return &DoctorOutput{Body: doctor.Run(ctx, a.doctor()...)}, nil
})
```

- `requireOps` 是只允许运维人员访问的[访问策略](../security/authorization.md)，例如要求 `system:read` 权限；`a.doctor()` 见[装配 doctor 命令](#doctor-command)。
- 有检查失败时接口仍返回 `200`，页面按每一项的 `status` 显示。

> [!WARNING]
> 结果中有数据库、存储和邮件服务器的地址和报错。这个接口只给运维人员访问。

<a id="framework-checks"></a>
## 框架提供的检查

| 函数 | 组名 | 检查 |
| --- | --- | --- |
| `database.Doctor(db)` | `postgres` | `connection`；`clock`：本机与数据库的时钟相差超过 1 秒时提示（锁、限流、任务时间都按实例的时钟计算） |
| `migrate.Doctor(db, fsys)` | `migrations` | `up_to_date`：`fsys` 中的迁移都已执行，否则失败；数据库比程序新时提示。不持有迁移锁 |
| `queue.Doctor(db, nc, cfg)` | `queue` | 见[队列的检查](#queue-checks) |
| `filesystem.Doctor(disk)` | `storage` | `read_write`：写入、读回、删除一个键以 `tenon-doctor/` 开头的探测对象。本地磁盘检查目录可写，S3 检查 bucket、地址和凭据 |
| `s3.Doctor(disk, uploadOrigins...)` | `s3` | 见 [S3 的检查](#s3-checks) |
| `smtp.Doctor(mailer)` | `smtp` | `connection`：连接、按 `tls` 升级、登录后断开，不发邮件；区分连不上（很多云服务器禁止外连 25 端口）、TLS 方式与端口不符、证书不受信任、用户名或密码被拒绝 |
| `cache.Doctor(rdb)` | `redis` | `connection`：服务器连得上，否则提示：缓存不可用时直接读数据库 |
| `gotenberg.Doctor(renderer)` | `gotenberg` | `health`：Gotenberg 和 Chromium 在运行；`render`：生成一页 PDF，确认地址和凭据 |
| `vector.Doctor(store, indexes...)` | `vector` | `qdrant`：连得上；每个索引一项（名称是 `Index.Name()`）：换了模型却没有用 `WithPrevious` 传入旧模型时失败；仍在用旧模型、没有在[重建](../ai/vector.md#reindexing)时提示 |

各个 `Doctor` 的参数在检查时才使用，可以在打开资源之前构造。包路径：`database`、`database/migrate`、`queue`、`filesystem`、`filesystem/s3`、`mail/smtp`、`cache`、`pdf/gotenberg`、`vector`。

<a id="queue-checks"></a>
### 队列的检查：`queue.Doctor`

要求的设置和原因见[队列：数据库设置](operations.md#queue)。

| 检查 | 内容 | 不满足时 |
| --- | --- | --- |
| `postgres_version` | PostgreSQL 17 以上 | 失败 |
| `wal_level` | `logical` | 失败 |
| `replication_limits` | `max_replication_slots`、`max_wal_senders` 大于 0 | 失败 |
| `replication_connection` | 用 worker 的方式建立一条复制连接：用户有 `REPLICATION` 属性，`replication_dsn` 可用 | 失败 |
| `wal_keep_size` | 设置了 `max_slot_wal_keep_size` | 提示 |
| `slot` | 复制槽存在、没有失效；快到上限时提示。队列的迁移还没执行时跳过 | 失败 |
| `nats`、`nats_version`、`jetstream` | `nc` 不为 `nil` 时：连接可用，NATS 2.15 以上，开启了 JetStream | 失败 |

每次启动都提示 `max_slot_wal_keep_size is unlimited` 时，给 PostgreSQL 设置上限，例如 `-c max_slot_wal_keep_size=10GB`。

<a id="s3-checks"></a>
### S3 的检查：`s3.Doctor`

`disk` 是 [S3 磁盘](../libraries/filesystem.md#s3-disk)，`uploadOrigins` 是使用[直传](../libraries/filesystem.md#direct-upload)的前端的来源，例如 `https://admin.example.com`；不用直传时不传，不检查 `cors` 和 `conditional_write`。

| 检查 | 内容 | 不满足时 |
| --- | --- | --- |
| `public_url` | 写入探测对象，从 `public_url` 下载，内容一致 | 失败；本机连不上 `public_url` 时只提示 |
| `cors` | 存储桶的 CORS 允许这些来源 `PUT`，允许请求头 `Content-Type`、`If-None-Match`，暴露响应头 `ETag` | 失败；存储没有 CORS 接口或凭据无权读取时跳过 |
| `conditional_write` | 用 `If-None-Match: *` 两次写入同一个对象，第二次被拒绝，保证[已确认的文件不被覆盖](../libraries/filesystem.md#no-overwrite) | 失败 |

`public_url` 可能只有浏览器能访问（例如只在公网解析的 CDN 域名），提示 `cannot reach public_url from this host` 时在浏览器中打开其中一个文件：能打开就不用处理，打不开时把 `public_url` 改为存储桶或 CDN 的公开地址。
