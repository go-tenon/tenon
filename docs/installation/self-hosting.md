# 交给客户自托管

应用部署在客户的服务器上时，交付镜像、`compose.yaml` 和 `.env.example`，客户执行一条命令启动。这一页给出这两个文件和首次启动、升级、备份、排查问题的步骤。

包：`doctor`、`queue/queuecmd`。

<a id="deliverables"></a>
## 交付什么

- **镜像**：按[用 Docker 构建镜像](deployment.md#docker)构建，推送到客户能访问的仓库，每个版本一个标签，例如 `registry.example.com/acme:1.4.2`。应用装配好 [`doctor` 命令](doctor.md#doctor-command)、[迁移之前](doctor.md#before-migrating)和[启动之前](doctor.md#before-starting)的检查，以及恢复备份时要用的 [queue 命令](../background/queue.md#stats-command)。
- **`compose.yaml`** 和 **`.env.example`**：见下面两节。

<a id="compose-file"></a>
## compose.yaml

应用在一个容器中运行 `serve`，连同 PostgreSQL 和 NATS：

```yaml
x-app: &app
  image: registry.example.com/acme:${APP_VERSION}
  environment:
    APP_URL: ${APP_URL}
    APP_KEY: ${APP_KEY}
    DATABASE_URL: postgres://acme:${POSTGRES_PASSWORD}@postgres:5432/acme?sslmode=disable
    NATS_URL: nats://nats:4222
  depends_on:
    postgres:
      condition: service_healthy
    nats:
      condition: service_healthy

services:
  migrate:
    <<: *app
    command: [migrate, up]

  app:
    <<: *app
    command: [serve]
    ports: ["8080:8080"]
    healthcheck:
      test: [CMD, /app/acme, health]
    volumes: [storage:/app/storage]
    restart: unless-stopped
    stop_grace_period: 45s
    depends_on:
      migrate:
        condition: service_completed_successfully

  postgres:
    image: postgres:18
    command:
      - postgres
      - -c
      - wal_level=logical
      - -c
      - max_slot_wal_keep_size=10GB
      - -c
      - wal_sender_timeout=10s
    environment:
      POSTGRES_USER: acme
      POSTGRES_PASSWORD: ${POSTGRES_PASSWORD}
    volumes: [postgres:/var/lib/postgresql]
    restart: unless-stopped
    healthcheck:
      test: [CMD, pg_isready, -U, acme, -h, 127.0.0.1]
      interval: 2s
      retries: 30

  nats:
    image: nats:2.15-alpine
    command: [nats-server, -js, -sd, /data, -m, "8222"]
    volumes: [nats:/data]
    restart: unless-stopped
    healthcheck:
      test: [CMD, wget, -qO-, "http://127.0.0.1:8222/healthz?js-enabled-only=true"]
      interval: 2s
      retries: 30

volumes:
  postgres:
  nats:
  storage:
```

- `postgres` 的三个参数是[队列](operations.md#queue)需要的设置。不设 `max_slot_wal_keep_size` 时，应用停止几天后积压的 WAL 可能写满磁盘。
- `migrate` 在迁移之前运行自检，环境不对时不执行迁移，直接给出修改方法。
- `8080` 前面放反向代理负责 HTTPS，见[反向代理](deployment.md#reverse-proxies)。用到 Memcached、[Gotenberg](../libraries/pdf.md#deployment)、Qdrant 时加服务，并加入[自检](doctor.md#framework-checks)。

<a id="env-file"></a>
## .env

`.env.example`，客户复制为 `.env` 后填写，Docker Compose 用它替换 `compose.yaml` 中的 `${...}`：

```bash
APP_VERSION=1.4.2
# 用户访问应用的地址，生成的链接以它开头
APP_URL=https://acme.example.com
# 应用密钥，用 openssl rand -base64 32 生成；生成后不再修改，加密过的数据要用它解开
APP_KEY=
# 数据库密码，用 openssl rand -hex 24 生成
POSTGRES_PASSWORD=
```

- `APP_KEY` 是[应用密钥](../security/encryption.md#configuration)。`.env` 和数据一起备份：丢了它，加密过的数据无法解开。
- `POSTGRES_PASSWORD` 只在第一次初始化数据目录时生效。之后改密码，先在数据库中执行 `ALTER ROLE acme PASSWORD '...'`，再改 `.env`。

<a id="first-start"></a>
## 第一次启动

```bash
cp .env.example .env    # 然后填写 .env
docker compose up -d
docker compose ps       # app 为 running，migrate 为 exited (0)
docker compose run --rm --no-deps app doctor
```

`doctor` 全部 `ok` 时部署完成；有 `FAIL` 或 `warn` 时按 `fix:` 修改，见[读懂结果](doctor.md#reading-results)。`--no-deps` 不再执行 `migrate`。

<a id="upgrading"></a>
## 升级

先[备份](#backups)，再把 `.env` 中的 `APP_VERSION` 改为新版本：

```bash
docker compose pull
docker compose run --rm migrate
docker compose up -d
```

- 迁移先单独执行：失败时旧版本照常运行，修好后再执行这两条命令。
- 替换容器的几秒内应用不可用，在使用人数少的时间升级。
- 版本说明写清楚能否回退：换回旧镜像不撤销新版本迁移对表结构的修改。

<a id="backups"></a>
## 备份与恢复

备份 PostgreSQL、`storage` 卷和 `.env`；NATS 和 Memcached 不需要备份。

```bash
docker compose exec -T postgres pg_dump -U acme -Fc acme > acme-$(date +%F).dump
docker compose cp app:/app/storage ./storage-$(date +%F)
```

两条命令放进服务器的定时任务，结果复制到另一台机器。需要恢复到任意时间点时改用[持续归档](operations.md#database-backups)。

恢复：

```bash
docker compose stop app
docker compose exec -T postgres dropdb -U acme acme
docker compose exec -T postgres createdb -U acme acme
docker compose exec -T postgres pg_restore -U acme -d acme < acme-2026-11-17.dump
docker compose run --rm --no-deps app queue slot recreate
docker compose up -d
```

复制槽不在备份中，要用 `queue slot recreate` 重建。其他事项见[恢复之后](operations.md#after-restoring)。

<a id="external-postgres"></a>
## 使用客户自己的 PostgreSQL

从 `compose.yaml` 中删掉 `postgres` 服务、它的卷和 `x-app` 中对它的 `depends_on`，`x-app` 的 `DATABASE_URL` 改为 `${DATABASE_URL}`，写进 `.env`。交给客户的数据库管理员：

- PostgreSQL 17 以上，一个空的数据库；
- `wal_level = logical`，修改后重启数据库（RDS：`rds.logical_replication = 1`）；
- 应用的用户有复制权限：`ALTER ROLE <用户> REPLICATION`（RDS：`GRANT rds_replication TO <用户>`）；
- 建议设置 `max_slot_wal_keep_size`，例如 `10GB`；
- 应用直连数据库，复制连接不能经过 [PgBouncer](operations.md#pgbouncer)。

配置好后运行 `docker compose run --rm migrate`，迁移之前的自检逐项检查这些要求。

<a id="troubleshooting"></a>
## 排查问题

- **`app` 一直 `restarting`**：启动之前的自检没有通过，看 `docker compose logs app`；升级后多半是 `migrate` 失败，看 `docker compose logs migrate`。
- **`migrate` 报 `wal_level is replica`**：PostgreSQL 没有用 `compose.yaml` 中的参数，按[使用客户自己的 PostgreSQL](#external-postgres) 设置并重启数据库。
- **`storage` 检查报 `permission denied`**：卷不属于 `nonroot`（ID 65532）。卷名是 compose 的项目名加 `_storage`（`docker volume ls` 查看），修改所有者：

  ```bash
  docker run --rm -v acme_storage:/data alpine chown -R 65532:65532 /data
  ```

- **磁盘越来越满**：应用长时间停止时复制槽积压 WAL。启动应用后积压被转发、回收；`docker compose run --rm --no-deps app queue status` 显示积压的大小，见[监控积压](operations.md#queue-monitoring)。
