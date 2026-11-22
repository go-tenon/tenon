# 交给客户自托管

应用部署在客户的 Linux 服务器上时，交付单个可执行文件、`config/`、`.env.example` 与 systemd unit 文件（可配合 `tenon deploy` 远程交付）。这一页给出首次启动、升级、备份和排查问题的步骤。

包：`doctor`、`queue/queuecmd`。

<a id="deliverables"></a>
## 交付什么

- **Linux 可执行文件**：按[构建](deployment.md#building)编译对应架构（`amd64` 或 `arm64`）的二进制，内嵌前端静态资源、SSR、迁移和翻译。应用装配好 [`doctor` 命令](doctor.md#doctor-command)、[迁移之前](doctor.md#before-migrating)和[启动之前](doctor.md#before-starting)的检查，以及恢复备份时要用的 [queue 命令](../background/queue.md#stats-command)。
- **`config/` 目录**（不含 `local.yaml`）、**`.env.example`** 与 **`<应用>@.service`**（见[用 systemd 运行](deployment.md#systemd)）。
- 依赖服务：PostgreSQL 17 以上与 NATS JetStream（2.15 以上）由客户侧提供或按下方要求在同一台机器上部署。若客户侧环境强制要求容器运行时，也可将同一二进制与 `config` 打包进基础镜像运行。

<a id="env-file"></a>
## 环境变量文件

`.env.example`，客户复制到 `/etc/acme/env`（权限 `600`，属于 root）后填写：

```bash
# 用户访问应用的地址，生成的链接以它开头
APP_URL=https://acme.example.com
# 应用密钥，用 openssl rand -base64 32 生成；生成后不再修改，加密过的数据要用它解开
APP_KEY=
# 数据库连接串
DATABASE_URL=postgres://acme:change-me@127.0.0.1:5432/acme?sslmode=disable
# NATS JetStream 地址
NATS_URL=nats://127.0.0.1:4222
```

- `APP_KEY` 是[应用密钥](../security/encryption.md#configuration)。`/etc/acme/env` 和数据一起备份：丢了它，加密过的数据无法解开。

<a id="external-postgres"></a>
## 准备 PostgreSQL 与 NATS

交给客户的数据库管理员或运维人员：

- PostgreSQL 17 以上，一个空的数据库；
- `wal_level = logical`，修改后重启数据库（RDS：`rds.logical_replication = 1`）；
- 应用的用户有复制权限：`ALTER ROLE <用户> REPLICATION`（RDS：`GRANT rds_replication TO <用户>`）；
- 建议设置 `max_slot_wal_keep_size = '10GB'` 与 `wal_sender_timeout = '10s'`，防止应用停机后 WAL 积压写满磁盘；
- 应用直连数据库，复制连接不能经过 [PgBouncer](operations.md#pgbouncer)；
- NATS 2.15 以上，开启 JetStream 并持久化存储目录（`nats-server -js -sd /var/lib/nats`）。

<a id="first-start"></a>
## 第一次启动

将二进制、配置放到 `/opt/acme`，安装 `/etc/systemd/system/acme@.service`（或直接运行 `tenon deploy setup` 与 `tenon deploy env push`）：

```bash
sudo systemd-run --wait --pipe --quiet --uid=acme \
    -p EnvironmentFile=/etc/acme/env -p WorkingDirectory=/opt/acme \
    /opt/acme/bin/acme config check
sudo systemd-run --wait --pipe --quiet --uid=acme \
    -p EnvironmentFile=/etc/acme/env -p WorkingDirectory=/opt/acme \
    /opt/acme/bin/acme migrate up
sudo systemctl enable --now acme@all
sudo systemd-run --wait --pipe --quiet --uid=acme \
    -p EnvironmentFile=/etc/acme/env -p WorkingDirectory=/opt/acme \
    /opt/acme/bin/acme doctor
```

`doctor` 全部 `ok` 时部署完成；有 `FAIL` 或 `warn` 时按 `fix:` 修改，见[读懂结果](doctor.md#reading-results)。`8080` 前面放反向代理负责 HTTPS，见[反向代理](deployment.md#reverse-proxies)。

<a id="upgrading"></a>
## 升级

先[备份](#backups)，再按[手动发布新版本](deployment.md#systemd-releases)（或 `tenon deploy`）校验配置、执行 `migrate up` 并 `systemctl reload acme@all` 完成零停机升级。

- 迁移先于服务重载执行：失败时旧进程照常运行，修好后重新执行。
- 版本说明写清楚能否回退：换回旧二进制不撤销新版本迁移对表结构的修改。

<a id="backups"></a>
## 备份与恢复

备份 PostgreSQL、`/opt/acme/storage` 和 `/etc/acme/env`；NATS 和 Redis 不需要备份。

```bash
pg_dump -Fc "$DATABASE_URL" > acme-$(date +%F).dump
tar -czf storage-$(date +%F).tar.gz -C /opt/acme storage
```

两条命令放进服务器的定时任务，结果复制到另一台机器。需要恢复到任意时间点时改用[持续归档](operations.md#database-backups)。

恢复：

```bash
sudo systemctl stop 'acme@*'
dropdb acme && createdb acme
pg_restore -d acme < acme-2026-11-17.dump
sudo systemd-run --wait --pipe --quiet --uid=acme \
    -p EnvironmentFile=/etc/acme/env -p WorkingDirectory=/opt/acme \
    /opt/acme/bin/acme queue slot recreate
sudo systemctl start acme@all
```

复制槽不在备份中，要用 `queue slot recreate` 重建。其他事项见[恢复之后](operations.md#after-restoring)。

<a id="troubleshooting"></a>
## 排查问题

- **服务起不来或反复退出**：启动前自检没有通过，看 `journalctl -u 'acme@*' -n 100 --no-pager`；也可执行 `doctor` 获取带修复提示的报告。
- **`migrate` 报 `wal_level is replica`**：PostgreSQL 没有按[准备 PostgreSQL 与 NATS](#external-postgres)设置并重启。
- **`storage` 检查报 `permission denied`**：确认 `/opt/acme/storage` 属于服务用户 `acme`，且 unit 的 `ReadWritePaths` 包含 `/opt/acme/storage`。
- **磁盘越来越满**：应用长时间停止时复制槽积压 WAL。启动应用后积压被转发、回收；运行 `acme queue status` 查看积压大小，见[监控积压](operations.md#queue-monitoring)。
