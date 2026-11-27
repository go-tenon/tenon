# 运维

上线后的运维：不停机升级、维护窗口、PgBouncer、队列的数据库设置和监控、备份与恢复、升级依赖。第一次部署见[部署](deployment.md)。

包：`command`、`queue`、`queue/queuecmd`。

<a id="zero-downtime-upgrades"></a>
## 不停机升级

使用 `tenon deploy` 或手动执行 `systemctl reload`（见[部署](deployment.md#tenon-deploy)）：

1. 旧进程用磁盘上的新二进制启动新进程，把监听的端口交给它；
2. 新进程打开数据库、装配好全部部分，等 SSR 子进程等延后就绪项启动完成且 HTTP 开始接收连接后报告就绪（`d.DeferReady` + `httpx.OnReady`）；
3. 旧进程平滑停止（`httpx.SkipDrainDelay(d.HandedOver)` 跳过负载均衡摘流等待），处理完手上的请求、任务后退出。

- 新进程在就绪之前退出或一分钟内没有就绪时被结束，旧进程继续服务，状态文字以 `upgrade failed` 开头。配置写错、数据库连不上、路由冲突都属于这种情况。
- 由 `serve` 中的 `command.Daemon` 实现，只在 unit 设置了 `Type=notify-reload` 和 `RuntimeDirectory=` 时启用，见[用 systemd 运行](deployment.md#systemd)。
- 上一次升级的旧进程还没退出时，`reload` 先等它退出；systemd 等待 `reload` 最多 `TimeoutStartSec`（默认 90 秒）。

<a id="upgrade-overlap"></a>
### 新旧进程同时运行

新进程就绪后，旧进程还要处理完手上的工作（最多 `http.shutdown_timeout`、`queue.shutdown_timeout`，进行中的 [AI 回答](../ai/chat.md#deployment)可能更久），这段时间两个版本同时运行：

| 部分 | 升级时 | 要求 |
| --- | --- | --- |
| HTTP 服务 | 新连接由新进程处理，旧进程处理完已有的请求 | 无 |
| 队列 worker | 旧进程停止领取，超时的任务由新进程重新执行 | 处理函数能安全地重复执行，见[队列](../background/queue.md) |
| 调度器 | 短暂同时运行，锁保证每次触发只执行一次 | 无 |
| 服务端推送 | 浏览器自动重连到新进程 | 无 |
| AI 回答 | 旧进程等回答说完 | 配置了 `chat.Cluster` 时浏览器重连后继续接收；否则说完后保存在对话中 |

- 表结构同时兼容新旧版本：迁移在升级之前执行，旧版本也运行在新表结构上，见[零停机变更](../database/migrations.md#zero-downtime-changes)。
- 数据库连接数翻倍：`max_conns` 的两倍要在数据库的连接上限之内。

<a id="upgrade-limits"></a>
### 限制

- 新进程继承旧进程的环境变量，只重新读取配置文件。改了 `/etc/blog/env` 或 unit 文件后执行 `systemctl restart`，服务短暂中断。
- 旧进程退出之前，`systemctl status` 显示的主进程仍是旧进程。
- 机器重启、内核升级仍会中断服务；要持续服务，部署多台机器放在负载均衡后面。

<a id="maintenance-windows"></a>
## 维护窗口

数据库升级大版本、表结构变更无法拆成[兼容的几步](../database/migrations.md#zero-downtime-changes)、故障时要挡住所有请求，这些情况需要停机。这时程序可能没有运行，“正在维护”由反向代理返回，同时放行内部人员。

<a id="maintenance-at-the-proxy"></a>
### 在反向代理上返回 503

nginx 检查标记文件，存在时返回 `503`，办公网络照常访问：

```nginx
geo $maintenance_bypass {
    default        0;
    203.0.113.0/24 1;  # 办公网络
}

server {
    # listen、ssl 等配置略

    set $maintenance 0;
    if (-f /etc/nginx/blog.maintenance) { set $maintenance 1; }
    if ($maintenance_bypass) { set $maintenance 0; }

    error_page 503 @maintenance;

    location / {
        if ($maintenance) { return 503; }
        proxy_pass http://blog;
        proxy_set_header Host $http_host;
    }

    location @maintenance {
        default_type application/problem+json;
        add_header Retry-After 1800 always;
        return 503 '{"title":"Service Unavailable","status":503,"code":"maintenance","detail":"系统维护中，请稍后再试"}';
    }
}
```

- `sudo touch /etc/nginx/blog.maintenance` 开始维护，`sudo rm` 结束，立即生效。
- 响应格式与程序的[错误响应](../general/errors.md#response-format)相同，前端按 `code` 为 `maintenance` 显示维护提示，按 `Retry-After` 重试。
- nginx 前面还有负载均衡时，先用 [realip](https://nginx.org/en/docs/http/ngx_http_realip_module.html) 还原用户的地址。有多台 nginx 时每台都创建标记文件；云负载均衡用它的固定响应规则。

<a id="maintenance-steps"></a>
### 维护步骤

```bash
sudo touch /etc/nginx/blog.maintenance
sudo systemctl stop 'blog@*'

# 维护数据库；换上新二进制并执行迁移

sudo systemctl start blog@http blog@worker blog@scheduler
curl -fsS https://example.com/readyz  # 在办公网络中检查
sudo rm /etc/nginx/blog.maintenance
```

- 先挡住请求再停止程序：顺序反过来时中间的请求得到 `502`。配置了 `maintenance_file` 时可用 `go tool tenon deploy maintenance on` / `off` 开关维护页。
- worker 和调度器也停止：维护期间它们不断出错，还可能用旧代码处理新数据。没执行的任务留在队列中。
- 错过的定时任务等下一次触发；每天只执行一次的任务恢复后[手动执行](../background/scheduling.md#running-manually)。

<a id="without-downtime"></a>
### 只停部分功能

| 情况 | 做法 |
| --- | --- |
| 某个功能出了问题 | 把它的[功能开关](../features/flags.md#evaluation-order)对所有人关闭 |
| 维护期间只允许读 | 安装拒绝写请求的中间件，见[全局中间件](../http/middleware.md#chi-middleware) |
| 只暂停后台任务 | 分开运行时只停止 worker 或调度器的进程 |

<a id="pgbouncer"></a>
## 数据库连接不够用：PgBouncer

所有实例的 `max_conns` 之和超过 PostgreSQL 的连接上限时，用 [PgBouncer](https://www.pgbouncer.org) 的 transaction 模式：

```ini
[pgbouncer]
pool_mode = transaction
default_pool_size = 50          ; 每个数据库、每个用户到 PostgreSQL 的连接数，按数据库的 CPU 和磁盘设置
max_client_conn = 2000          ; 不小于各实例的 max_conns 之和
max_prepared_statements = 200   ; pgx 默认使用命名的预编译语句，需要 PgBouncer 1.21 以上
```

`database.dsn` 改为 PgBouncer 的地址。只有**迁移**需要直连 PostgreSQL：它在一个连接上持有会话级的锁，`migratecmd.New` 的 `dbConfig` 返回直连的数据库配置（例如配置中单独的 `migrate_database` 段）。队列的 `queue_outbox` 完全兼容 PgBouncer 的 transaction 模式，不需要额外的直连配置。

PgBouncer 的连接用满时语句排队，最长等 `query_wait_timeout`（例如 `5` 秒）。

<a id="queue"></a>
## 队列：监控与运维

[队列](../background/queue.md)的 worker 从 PostgreSQL 的 `queue_outbox` 表批量领取到期任务并转发到 NATS JetStream，延迟任务保留在 `queue_outbox` 中直到执行时间。

<a id="queue-monitoring"></a>
### 监控积压

worker 全部停止或 NATS 不可用时，待转发任务留在 `queue_outbox` 表中（不影响数据库 WAL 回收，也不会写爆磁盘）。

- `queue status`（[queue 命令](../background/queue.md#stats-command)）显示 `outbox pending`、`outbox deferred` 和 `oldest pending`。
- 用[指标](../general/telemetry.md#queue)告警：`queue.outbox.pending` 持续增长，或 `queue.job.wait` 异常升高。
- worker 全部停止时也可以直接查库：

```sql
SELECT count(*) FILTER (WHERE available_at <= now()) AS pending,
       count(*) FILTER (WHERE available_at > now())  AS deferred,
       min(available_at) FILTER (WHERE available_at <= now()) AS oldest_pending
FROM queue_outbox;
```

<a id="queue-jetstream"></a>
### JetStream 中的任务

转发之后任务保存在 JetStream 的 stream 中直到处理完成；失败任务由 JetStream 原生延迟重投（`NakWithDelay`）。

- `queue.replicas`：NATS 集群中设为 3，一台服务器损坏时任务不丢。单机（`1`）的磁盘损坏时未处理的在途任务会丢失，开启云盘的自动快照。
- stream 不设 `max_age`，容量上限配合 `discard: new`：`max_age` 和 `discard: old` 都会删除任务，worker 启动时发现就拒绝启动。`discard: new` 时容量满后任务留在 `queue_outbox` 中，有空间后再转发。

<a id="backups"></a>
## 备份与恢复

备份数据库和[文件存储](../libraries/filesystem.md)。`queue_outbox` 中的待转发任务和延迟任务随数据库自动备份；JetStream 中的在途任务靠[多副本](#queue-jetstream)保护；Redis 的缓存、NATS 中的在线成员会重新生成，Qdrant 中的[向量](../ai/vector.md)可以从数据库重建。Tenon 不提供备份命令，用数据库自己的备份机制。

<a id="database-backups"></a>
### 数据库

- 托管数据库（RDS、Cloud SQL 等）开启自动备份和时间点恢复（PITR）。
- 自建数据库用 [pgBackRest](https://pgbackrest.org) 或 [WAL-G](https://github.com/wal-g/wal-g) 持续归档 WAL。
- 很小的数据库可以用 `pg_dump` 定时导出，只能恢复到导出的那一刻；`pg_dump` 的大版本不低于服务器的。

<a id="file-backups"></a>
### 文件

`Delete` 之后，[清理任务](../libraries/filesystem.md#cleanup)几分钟内删除对象。数据库恢复到较早的时间点后，这段时间删除的文件又出现在 `files` 表中，所以文件也要能恢复到过去：

- **S3 等对象存储**：开启[版本控制](https://docs.aws.amazon.com/AmazonS3/latest/userguide/Versioning.html)，用生命周期规则让旧版本过期，保留时间不短于数据库备份的保留期。
- **本地磁盘**：用 [restic](https://restic.net) 等工具定时备份 `disks.local.root` 并保留历史快照。先备份数据库再备份文件，间隔尽量短。

<a id="after-restoring"></a>
### 恢复之后

- **检查队列任务**：备份时留在 `queue_outbox` 中的待转发和延迟任务随库恢复并继续转发；备份之后执行过的任务可能再执行一次。
- **重建向量索引**：Qdrant 中有备份之后写入的文档，逐个[重建向量索引](../ai/vector.md#reindexing)。
- **再撤销一次凭据**：备份之后撤销的 [API 令牌](../security/api-tokens.md#revoking-tokens)恢复后又能用。对这段时间封禁或改过密码的用户再调用 `RevokeSubject`；无法逐个确认时删除 `api_tokens` 中 `kind = 'login'` 的行，让所有人重新登录。
- 已经发出的邮件、调用过的外部接口不会撤回。

<a id="restore-drills"></a>
### 恢复演练

定期把备份恢复到临时数据库，用同一个二进制检查，`config/restore.yaml` 只把数据库地址改为临时库：

```bash
./bin/blog migrate status --config=config/app.yaml,config/restore.yaml
```

确认迁移的版本符合预期，抽查几张业务表中最新的数据，记下恢复用的时间。

<a id="upgrading"></a>
## 升级框架与依赖

业务项目升级 Tenon 版本按以下顺序执行：

```bash
go get github.com/go-tenon/tenon@vX.Y.Z && go mod tidy
go run ./cmd/blog migrate publish   # 复制框架新增的迁移
go run ./cmd/blog migrate verify    # 确认框架迁移无漏发、无漂移
bun run agents                      # 同步 tenon/docs、AGENTS.md 托管区块、技能和 MCP 配置
bun run generate                    # 同步 tenon/js，重新生成客户端和界面文字目录
bun run check                       # 含 deps 依赖版本检查；有前端 peerDependencies 提示时先升级并 bun install
bun run typecheck && go test ./...
```

- 改动涉及并发时再运行 `go test -race ./...`。
- 框架需要的 Go 版本以它的 [go.mod](../../go.mod) 为准，最低要求 PostgreSQL 18 与 NATS 2.15。
- CI 中启用了[已发布迁移检查](../database/migrations.md#checking-in-ci)和 `migrate verify` 时，分别确认未改写历史迁移且未漏发框架新增迁移。

<a id="agent-troubleshooting"></a>
## 线上排障与只读诊断

让 AI 助手排查线上问题时，按从外到内、最小权限的三层只读观测面逐级排查，不把生产 `deploy/targets/*` 密钥或高权限 SSH 交给日常排障会话：

1. **外置可观测平台（首选）**：生产开启 [telemetry](../general/telemetry.md#setup) 把日志、指标和链路推送到 OpenObserve，助手通过只读观测 MCP 按 `X-Request-ID` 或时间窗口定位绝大多数报错与慢请求，完全不触碰生产主机。
2. **应用只读运维 MCP（查库、队列、Redis、NATS）**：在应用内挂载 [`mcpx/opsmcp`](../ai/mcp.md#ops-mcp) 到 `/ops/mcp`，给助手签发仅含 `ops:read` 权限的短期个人访问令牌。数据库侧只需两行 SQL 创建通用只读角色（`GRANT pg_read_all_data`），所有查询强制在只读事务、超时与敏感列脱敏护栏下执行。
3. **低权限主机观测（进程宕机兜底）**：在 `deploy/targets/<target>/target.yaml`（或 `deploy/app.yaml` 的 `defaults`）中配置 `observe_user: observe` 并运行 `tenon deploy setup`，使 `tenon deploy status`、`logs`、`releases` 与 `tenon mcp` 的部署观测工具走无 `sudo` 的低权限用户；把 `deploy/targets/` 放在外部私有目录并通过 `TENON_DEPLOY_TARGETS` 指定。

- **变更线上走受控流程**：助手通过上述只读面定位根因后，在本地编写回归测试与修复代码提交 PR，由人工确认后走 `tenon deploy` 发布，或由人工审核后执行一次性订正命令。
