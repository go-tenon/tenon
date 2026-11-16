# 运维

应用上线以后，还会遇到这些事：发布新版本时不能中断服务；偶尔必须停机维护；实例多了以后数据库连接不够用；队列要监控；数据要备份，并且要确认备份能恢复。这一页依次介绍。

阅读前需要了解：[部署](deployment.md)。读到队列一节之前，最好先了解[队列](../libraries/queue.md)是怎样工作的。

- [不停机升级](#zero-downtime-upgrades)
    - [新旧进程同时运行](#upgrade-overlap)
    - [限制](#upgrade-limits)
- [维护窗口](#maintenance-windows)
    - [在反向代理上返回 503](#maintenance-at-the-proxy)
    - [维护步骤](#maintenance-steps)
    - [只停部分功能时](#without-downtime)
- [数据库连接不够用：PgBouncer](#pgbouncer)
- [队列](#queue)
    - [监控积压](#queue-monitoring)
    - [主备切换](#queue-failover)
    - [重建复制槽](#queue-recovery)
- [备份与恢复](#backups)
    - [数据库](#database-backups)
    - [文件](#file-backups)
    - [恢复之后](#after-restoring)
    - [恢复演练](#restore-drills)
- [升级依赖](#upgrading)

<a id="zero-downtime-upgrades"></a>
## 不停机升级

最简单的升级方式是先停止旧版本，再启动新版本。但在停止和启动之间，用户的请求会失败。不停机升级的思路是：先把新版本启动好，确认它能工作，再让旧版本处理完手上的请求后退出，监听的端口始终有人在服务。

**用 systemd 运行时**，执行 `systemctl reload`（[发布新版本](deployment.md#systemd-releases)的脚本就是这样做的）：

1. 正在运行的旧进程，用磁盘上新的二进制启动一个新进程，把监听的端口交给它；
2. 新进程打开数据库、装配好全部部分，然后报告“已经就绪”；
3. 这时旧进程才开始平滑停止：处理完正在处理的请求、队列任务和定时任务后退出。

如果新进程在报告就绪之前退出，或者一分钟内没有就绪，它就会被结束，旧进程继续服务。配置写错、数据库连不上、路由冲突、定时任务的表达式写错，都属于这种情况，用户感觉不到。

这项功能由 `command.Daemon` 提供，只在按[用 systemd 运行](deployment.md#systemd)配置的服务中启用；在开发环境和容器中，程序的行为不变。如果上一次升级的旧进程还没有退出，`reload` 会先等它退出，再开始这一次升级；systemd 等待 `reload` 的时间受 `TimeoutStartSec`（默认 90 秒）限制。

**用容器运行时**，镜像是不可变的，升级就是换一个新容器。要不中断服务，让部署平台先启动新容器，等它的[就绪检查](deployment.md#health-checks)（`/readyz`）通过，再停止旧容器。例如 [Kamal](https://kamal-deploy.org)、Docker Swarm 的 `update_config.order: start-first`、Kubernetes 的 `maxUnavailable: 0`。

<a id="upgrade-overlap"></a>
### 新旧进程同时运行

新进程就绪之后，旧进程还要一段时间才能处理完手上的工作：请求最多 `http.shutdown_timeout`，队列任务最多 `queue.shutdown_timeout`，进行中的 [AI 回答](../ai/chat-advanced.md#deployment)可能更久。这段时间里，新旧两个版本同时在运行，就像临时多部署了一个实例：

| 部分 | 升级时 | 需要你注意的 |
| --- | --- | --- |
| HTTP 服务 | 新的连接由新进程处理，旧进程处理完已有的请求 | 无 |
| 队列 worker | 新进程开始领取任务；旧进程停止领取，等待处理中的任务，超时的任务交给新进程重新执行 | 任务的处理函数要能安全地重复执行，见[队列](../libraries/queue.md) |
| 调度器 | 新旧调度器短暂同时运行，锁保证每次触发只执行一次 | 无 |
| 服务端推送 | 浏览器自动重连到新进程 | 无 |
| AI 回答 | 旧进程等进行中的回答说完 | 配置了 `chat.Cluster` 时，浏览器重连后继续接收；否则回答说完后保存在对话中 |

此外还有两点：

- **数据库的表结构要同时兼容新旧版本**，因为迁移在升级之前就执行了，旧版本也在用新的表结构，见[零停机变更](../database/migrations.md#zero-downtime-changes)。
- **数据库连接数会翻倍。** 同时运行期间，两个进程各有一个连接池，`max_conns` 的两倍应当在数据库的连接上限之内。

<a id="upgrade-limits"></a>
### 限制

- `reload` 会重新读取配置文件，但新进程继承旧进程的环境变量。修改了 `/etc/blog/env` 或 unit 文件之后，要执行 `systemctl restart`，这会短暂地中断服务。
- 新进程在报告就绪之后才崩溃时，systemd 按 `Restart=` 用磁盘上的新二进制重启它，运行的仍然是新版本。
- 旧进程退出之前，`systemctl status` 显示的主进程仍然是旧进程。
- 机器重启、内核升级仍然会中断服务。要在这些时候也持续服务，部署多台机器，放在负载均衡后面。

<a id="maintenance-windows"></a>
## 维护窗口

按上文发布时，服务一直在线。少数情况下确实需要停一段时间：

- 数据库本身要维护，例如升级大版本、迁移到新的实例；
- 一次表结构的变更无法拆成[新旧版本都兼容](../database/migrations.md#zero-downtime-changes)的几步；
- 出了故障，需要先挡住所有请求。

这时数据库往往不可用，程序甚至根本没有运行，所以“正在维护”这个状态不能放在程序中，而要放在程序前面的反向代理上：由代理直接告诉用户“正在维护”，同时放行内部人员，程序停着也照样生效。

<a id="maintenance-at-the-proxy"></a>
### 在反向代理上返回 503

下面的 nginx 配置在每个请求上检查一个标记文件：文件存在就返回 `503 Service Unavailable`，办公网络的地址照常访问：

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

开始和结束维护，就是创建和删除这个文件，nginx 立即生效：

```bash
sudo touch /etc/nginx/blog.maintenance  # 开始维护
sudo rm /etc/nginx/blog.maintenance     # 结束维护
```

- **响应的格式和程序的[错误响应](../general/errors.md#what-clients-see)相同。** 前端看到 `code` 为 `maintenance` 时显示维护提示，按 `Retry-After` 的秒数稍后重试。
- **只拦截 nginx 自己返回的 `503`。** 程序返回的 `503`（例如停机时拒绝新的 AI 回答）原样传给用户。
- **放行按用户的地址判断。** nginx 前面还有负载均衡时，先用 [realip](https://nginx.org/en/docs/http/ngx_http_realip_module.html) 模块还原用户的地址，否则所有请求看起来都来自负载均衡。
- **有多台 nginx 时**，每一台都要创建标记文件。使用云负载均衡时，改用它自带的固定响应规则，例如 AWS ALB 的 `fixed-response`。

<a id="maintenance-steps"></a>
### 维护步骤

以[用 systemd 运行](deployment.md#systemd)的部署为例，维护数据库并发布一个不兼容的版本：

```bash
sudo touch /etc/nginx/blog.maintenance
sudo systemctl stop 'blog@*'

# 维护数据库；换上新二进制并执行迁移，见“发布新版本”

sudo systemctl start blog@serve blog@work blog@schedule
curl -fsS https://example.com/readyz  # 在办公网络中检查
sudo rm /etc/nginx/blog.maintenance
```

- **先挡住请求，再停止程序。** 顺序反过来的话，程序停止后、维护生效前的请求会得到 `502`。
- **worker 和调度器也要停止。** 它们直接连接数据库，维护期间会不断出错；不兼容的变更期间，还可能用旧代码处理新数据。停止时 worker 会等处理中的任务完成，没执行的任务留在队列中，恢复后继续执行。
- **停止期间错过的定时任务会等下一次触发**，见[失败与停止](../libraries/scheduling.md#failures-and-shutdown)。每天只执行一次的任务，恢复后需要[手动执行](../libraries/scheduling.md#running-manually)一次。
- **检查通过后再结束维护。** 标记文件还在时，办公网络已经能访问新版本，确认无误再删除文件。

使用 Docker Compose 时，`docker compose stop` 和 `docker compose up -d` 分别对应上面的停止和启动。

<a id="without-downtime"></a>
### 只停部分功能时

很多时候只需要挡住一部分功能，其余照常服务：

| 情况 | 做法 |
| --- | --- |
| 某个功能出了问题 | 把它的[功能开关](../features/flags.md#evaluation-order)对所有人关闭，立即生效 |
| 维护期间只允许读 | 安装一个拒绝写请求的中间件，见[全局中间件](../http/middleware.md#chi-middleware)中的例子 |
| 只暂停后台任务 | 分开运行时只停止 `work` 或 `schedule`，任务留在队列中，恢复后继续执行 |

<a id="pgbouncer"></a>
## 数据库连接不够用：PgBouncer

每个进程都有一个数据库连接池，长期占着若干个数据库连接。实例和进程多了以后，所有连接池的 `max_conns` 加起来，可能超过 PostgreSQL 能承受的连接数。

[PgBouncer](https://www.pgbouncer.org) 是一个放在程序和数据库之间的连接池代理。用它的 transaction 模式时，程序的一个连接只在执行事务期间占用数据库的一个连接，事务结束就还回去，几千个程序连接可以共用几十个数据库连接：

```ini
; pgbouncer.ini
[pgbouncer]
pool_mode = transaction
default_pool_size = 50          ; 每个数据库、每个用户到 PostgreSQL 的连接数
max_client_conn = 2000          ; 各实例的 max_conns 之和不超过它
max_prepared_statements = 200   ; pgx 默认使用命名的预编译语句，需要 PgBouncer 1.21 以上
```

把配置中的 `database.dsn` 改为 PgBouncer 的地址。有两处必须直接连接 PostgreSQL，因为它们需要在一个连接上保持状态，transaction 模式下会失效：

- **迁移**：goose 在一个连接上加锁，防止多个实例同时迁移。迁移命令使用一个直连的地址，例如在配置中单独写一个 `migrate_database` 段。
- **队列的复制连接**：设置 `queue.replication_dsn`，见[队列](#queue)。

PgBouncer 只是让连接可以共用，数据库的处理能力仍然由数据库本身决定，`default_pool_size` 要按数据库的 CPU 和磁盘来设置。程序自己的连接池用满时，请求按 [`acquire_timeout`](../database/quick-start.md#pool-exhaustion) 很快失败；PgBouncer 到数据库的连接用满时，语句在 PgBouncer 中排队，最长等待时间由它的 `query_wait_timeout` 限制（例如 `5`，单位秒）。

<a id="queue"></a>
## 队列

[队列](../libraries/queue.md)的任务和业务数据写在同一个数据库事务中，事务提交了任务才会发出，不会出现“数据保存了，任务却丢了”的情况。它的实现依赖 PostgreSQL 的两个机制：

- **WAL**（预写日志）：数据库把每一次修改先写进 WAL，再写入数据文件。
- **逻辑复制槽**：一个“读取 WAL 的书签”。worker 通过复制槽依次读出新的任务，转发给 NSQ；没有读走的部分，数据库会一直保留。

所以数据库需要这些设置：

| 设置 | 值 | 说明 |
| --- | --- | --- |
| 版本 | 17 以上 | 复制槽可以随主备切换同步 |
| `wal_level` | `logical` | 修改后需要重启数据库 |
| 用户属性 | `REPLICATION` | 执行迁移和 worker 的复制连接都需要 |
| `max_slot_wal_keep_size` | 例如 `10GB` | 复制槽最多保留的 WAL，超过后槽失效；不设置时可能写满磁盘 |
| `wal_sender_timeout` | `10s` | 负责转发的 worker 失联后多久释放复制槽，由待命的 worker 接手 |
| `max_replication_slots`、`max_wal_senders` | 默认的 10 足够 | 每个数据库一个槽、一个复制连接 |

数据库连接经过 [PgBouncer](#pgbouncer) 时，要配置 `queue.replication_dsn` 直接连接 PostgreSQL：复制协议不能经过 PgBouncer。

<a id="queue-monitoring"></a>
### 监控积压

复制槽保留着 worker 还没读走的 WAL。worker 全部停止、或者 nsqd 不可用时，没读走的部分会一直增长，直到超过 `max_slot_wal_keep_size`，槽失效。

日常可以用 `queue status` 命令查看，或者用 worker 报告的[指标](../general/telemetry.md#queue)设置告警：所有实例的 `queue.relay.active` 之和为 0（没有 worker 在转发），或者 `queue.relay.lag` 超过 `max_slot_wal_keep_size` 的一半。

worker 全部停止时，就没有人报告指标了，所以还要在数据库这一侧监控。复制槽的名字是 `queue.SlotName(数据库名)`：

```sql
SELECT slot_name, active, wal_status,
       pg_wal_lsn_diff(pg_current_wal_lsn(), confirmed_flush_lsn) AS lag_bytes
FROM pg_replication_slots WHERE slot_name LIKE 'mygoravel_queue_%';
```

`active` 为 `false` 说明没有 worker 在转发；`wal_status` 变成 `unreserved` 说明槽即将失效。长时间运行的事务也会让 WAL 无法释放。

<a id="queue-failover"></a>
### 主备切换

复制槽默认只存在于主库上。主库故障、切换到备库后，新的主库上没有复制槽，worker 会拒绝启动。

要让复制槽随主备切换保留，在备库上设置 `sync_replication_slots = on`、`hot_standby_feedback = on`（`primary_conninfo` 中要包含 `dbname`），并在主库的 `synchronized_standby_slots` 中列出备库的物理复制槽，见 [PostgreSQL 文档](https://www.postgresql.org/docs/current/logicaldecoding-explanation.html)。没有配置时，按下一节重建。

<a id="queue-recovery"></a>
### 重建复制槽

复制槽丢失（例如恢复了备份、主备切换时没有同步）或失效（`wal_status` 为 `lost`）时，worker 拒绝启动。用 `queue slot recreate` 删除失效的槽，用同样的名字重建：

```bash
./bin/api queue slot recreate
```

复制槽还能用的时候，这个命令拒绝执行，因为重建会丢弃其中还没转发的任务。确实需要时加上 `--force`。

从丢失到重建期间发出的任务会随之丢失。从业务数据中找出没有完成的工作重新发出，例如状态停在“发送中”的记录。

<a id="backups"></a>
## 备份与恢复

应用的数据保存在两个地方：数据库，以及[文件存储](../libraries/filesystem.md)使用的磁盘或对象存储，两者都要备份。其他地方的数据丢了可以重新生成：Memcached 中的缓存、Redis 中的锁和限流计数会重新生成，Qdrant 中的[向量](../ai/vector.md)可以根据数据库中的数据重建。

Tenon 不提供备份命令：只有数据库自己的备份机制能持续备份、恢复到任意时间点；在程序中定时导出，数据量大时很慢，还会丢掉两次导出之间写入的数据。

<a id="database-backups"></a>
### 数据库

使用云厂商的托管数据库（RDS、Cloud SQL 等）时，开启自动备份和时间点恢复（PITR，可以恢复到过去任意一秒的状态）。自己搭建数据库时，用 [pgBackRest](https://pgbackrest.org) 或 [WAL-G](https://github.com/wal-g/wal-g) 持续归档 WAL。

数据库很小时，用 `pg_dump` 定时导出也可以，但只能恢复到导出的那一刻。`pg_dump` 的大版本要等于或高于数据库服务器的大版本。

<a id="file-backups"></a>
### 文件

文件存储在 `files` 表中记录文件，文件的内容保存在磁盘或对象存储上。调用 `Delete` 删除文件后，[清理任务](../libraries/filesystem.md#cleanup)几分钟内就会删除对应的对象。问题在于：把数据库恢复到较早的时间点后，这段时间里删除的文件又出现在 `files` 表中，对象却已经没有了。所以文件也要能恢复到过去：

- **S3 等对象存储**：为存储桶开启[版本控制](https://docs.aws.amazon.com/AmazonS3/latest/userguide/Versioning.html)。对象被删除时只是加上一个删除标记，删掉这个标记就能找回。再配置生命周期规则，让旧版本在一段时间后过期，这段时间不短于数据库备份的保留期。MinIO 等兼容 S3 的存储同样支持版本控制。
- **本地磁盘**：用 [restic](https://restic.net) 等工具定时备份 `disks.local.root` 目录，并保留历史快照。先备份数据库，再备份文件，两次备份的间隔尽量短。恢复后发现有记录找不到对应的文件时，到更早的快照中找回。

<a id="after-restoring"></a>
### 恢复之后

数据库回到了过去，但已经发出去的邮件、已经调用过的外部接口不会撤回。恢复后要处理这几件事：

- **重建队列的复制槽。** 复制槽不在备份中，恢复后 worker 因为找不到它而拒绝启动，按[重建复制槽](#queue-recovery)处理。备份时还没转发的任务会丢失，从业务数据中找出没有完成的工作重新发出；备份之后执行过的任务，恢复后可能再执行一次，所以任务的处理函数要能安全地重复执行。
- **向量索引比数据库新。** Qdrant 中还有备份之后才写入的文档，检索可能返回数据库中已经不存在的内容。逐个[重建向量索引](../ai/vector.md#reindexing)。
- **撤销过的凭据又能用了。** 备份之后撤销的 [API 令牌](../security/api-tokens.md#revoking-tokens)，恢复后会重新生效。如果这段时间里有用户被封禁或者改过密码，恢复后对这些用户再调用一次 `RevokeSubject`。无法逐个确认时，删除 `api_tokens` 表中 `kind = 'login'` 的行，让所有人重新登录；这段时间里撤销过的个人访问令牌需要再撤销一次。

<a id="restore-drills"></a>
### 恢复演练

只有真的恢复过，才能确定备份是可用的。定期把备份恢复到一个临时数据库，再用同一个二进制连上去检查：

```bash
./bin/api migrate status --config=config/app.yaml,config/restore.yaml
```

`config/restore.yaml` 只把数据库地址改为临时库。确认迁移的版本与预期一致后，再抽查几张业务表中最新的数据。每次演练都记下恢复用了多长时间：真的出事时，停服大约也要这么久。

<a id="upgrading"></a>
## 升级依赖

升级 Tenon 或其他依赖之后，至少运行一遍测试，并开启竞态检测：

```bash
go test ./...
go test -race ./...
```

`-race` 检查多个 goroutine 同时读写同一个变量的问题，这类问题平时很难发现。框架需要的 Go 版本以它的 [go.mod](../../go.mod) 为准，框架自己的数据库集成测试使用 PostgreSQL 18。你的应用额外引入的组件应当固定版本，并有各自的集成测试。

如果在 CI 中启用了[已发布迁移检查](../database/migrations.md#checking-in-ci)，它还会确认升级过程中没有意外修改已经发布的迁移。
