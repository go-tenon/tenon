# 交给客户自托管

有些应用不由你来运行，而是交给客户，部署在客户自己的服务器上，例如企业内部使用的系统。这时运维的是客户：他们不一定熟悉 PostgreSQL 和 NATS，出了问题你也看不到现场。这一页给出一套交给客户的 Docker Compose 部署：一个 `compose.yaml`、一个 `.env`，客户执行一条命令就能启动，升级、备份和排查问题的步骤也都写在这里，可以直接整理进你交给客户的部署说明。

阅读前需要了解：[部署](deployment.md)（特别是[用 Docker 运行](deployment.md#docker)）；[自检](doctor.md)；[运维](operations.md)。

- [交付什么](#deliverables)
- [compose.yaml](#compose-file)
- [.env](#env-file)
- [第一次启动](#first-start)
- [升级](#upgrading)
- [备份与恢复](#backups)
- [使用客户自己的 PostgreSQL](#external-postgres)
- [常见问题](#troubleshooting)

<a id="deliverables"></a>
## 交付什么

交给客户的是三样东西：

| 文件 | 内容 |
| --- | --- |
| 镜像 | 应用的镜像，按[用 Docker 运行](deployment.md#docker)构建，推送到客户能访问的镜像仓库，每个版本一个标签，例如 `registry.example.com/acme:1.4.2` |
| `compose.yaml` | 应用和它依赖的服务，见下一节 |
| `.env.example` | 客户要填写的设置：版本、访问地址、密码和密钥 |

镜像中的应用要装配好[自检](doctor.md)：`doctor` 命令、[迁移之前的检查](doctor.md#before-migrating)和[启动之前的检查](doctor.md#before-starting)。客户的环境出问题时，程序会直接说出哪一项不对、怎样修改，而不是一句难懂的底层报错。还要装配 [queue 命令](../background/queue-monitoring.md#stats-command)，恢复备份之后要用它重建复制槽。

<a id="compose-file"></a>
## compose.yaml

下面是完整的 `compose.yaml`。应用在一个容器中运行 `start`（HTTP 服务、队列 worker 和定时任务在同一个进程中，见[一个进程，还是三个进程](deployment.md#process-forms)），这是单台服务器上最简单的形态：

```yaml
# compose.yaml：复制 .env.example 为 .env 并填写后，执行 docker compose up -d
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
    command: [start]
    ports: ["8080:8080"]
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
      - wal_level=logical             # 队列经由逻辑复制转发任务
      - -c
      - max_slot_wal_keep_size=10GB   # worker 长时间停止时，积压的 WAL 最多占用这么多磁盘
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

逐段看这个文件：

- **`x-app`** 是 `migrate` 和 `app` 共用的设置：同一个镜像、同样的环境变量，都要等 PostgreSQL 和 NATS 就绪之后才启动。环境变量对应应用配置文件中的 `${APP_URL}`、`${DATABASE_URL}` 等，见[环境变量](../general/configuration.md)。应用用服务名 `postgres`、`nats` 访问它们：容器中的 `127.0.0.1` 是容器自己。
- **`migrate`** 执行完迁移就结束。迁移之前先运行[自检](doctor.md#before-migrating)，`wal_level` 不对、数据库用户少了权限时，它不执行任何迁移，直接说明怎样修改。
- **`app`** 要等 `migrate` 成功结束才启动，所以表结构一定是新的。`storage` 卷保存上传的文件；`stop_grace_period` 让正在处理的请求和任务有时间完成，见[平滑停止](deployment.md#graceful-shutdown)。`ports` 把端口映射到服务器上，生产环境通常在前面放一个反向代理负责 HTTPS，见[反向代理](deployment.md#reverse-proxies)。
- **`postgres`** 的三个参数是[队列](../background/queue.md#preparing)需要的设置，作用见[运维](operations.md#queue)。不写 `max_slot_wal_keep_size` 时，应用停止几天后积压的 WAL 可能写满磁盘，这是客户最难自己排查的一类问题。
- **`nats`** 开启 JetStream，任务保存在 `nats` 卷中；健康检查访问 NATS 的监控端口，JetStream 可用时才算就绪。监控端口没有映射到服务器上，只在 compose 的网络中可以访问。

应用用到了其他服务时，按各自的说明加进来：[Memcached](deployment.md#docker)、[Gotenberg](../libraries/pdf.md#deployment)、Qdrant 等，并把它们加入[自检](doctor.md#framework-checks)。Memcached 是可选的：不部署它时，应用不使用缓存。

<a id="env-file"></a>
## .env

`.env.example` 列出客户要填写的值，客户复制为 `.env` 后填写。Docker Compose 自动读取同一目录中的 `.env`，替换 `compose.yaml` 中的 `${...}`：

```bash
# .env：部署的设置，不要提交到代码仓库，也不要发给别人
APP_VERSION=1.4.2
# 用户访问应用的地址，生成的链接以它开头
APP_URL=https://acme.example.com
# 应用密钥：openssl rand -base64 32；生成后不要再改，否则加密的数据无法解开
APP_KEY=
# 数据库密码：openssl rand -hex 24
POSTGRES_PASSWORD=
```

`APP_KEY` 是[应用密钥](../security/encryption.md#configuration)，用来加密数据和签名链接。提醒客户把 `.env` 和数据一起备份：丢了 `APP_KEY`，加密过的数据就再也解不开了。

`POSTGRES_PASSWORD` 只在第一次启动、PostgreSQL 初始化数据目录时生效。之后再修改 `.env` 中的密码，要先在数据库中执行 `ALTER ROLE acme PASSWORD '...'`，否则应用连不上数据库。

<a id="first-start"></a>
## 第一次启动

```bash
cp .env.example .env    # 然后填写 .env
docker compose up -d
docker compose ps       # app 为 running，migrate 为 exited (0)
```

`up -d` 依次启动 PostgreSQL 和 NATS，等它们就绪后执行迁移，最后启动应用。`migrate` 显示 `exited (0)` 是正常的：它执行完迁移就结束了。

启动后运行一遍自检，确认环境都没有问题：

```bash
docker compose run --rm --no-deps app doctor
```

`run` 用 `app` 服务的设置启动一个临时容器执行 `doctor`，环境变量和网络都与 `app` 相同，检查的就是应用实际连接的服务。`--rm` 在命令结束后删除这个容器；`--no-deps` 不启动 `app` 依赖的服务，否则会先执行一次 `migrate`，迁移失败时 `doctor` 就运行不了。全部是 `ok` 时部署完成；有 `FAIL` 或 `warn` 时，按每一项下面的 `fix:` 修改，见[读懂结果](doctor.md#reading-results)。

<a id="upgrading"></a>
## 升级

把 `.env` 中的 `APP_VERSION` 改为新版本，然后：

```bash
docker compose pull
docker compose run --rm migrate
docker compose up -d
```

三条命令依次是：下载新版本的镜像；用新镜像执行迁移；用新镜像替换 `app` 容器（`migrate` 会再执行一次，这时已经没有要执行的迁移）。

迁移要单独先执行：`up -d` 替换容器时，先停止旧的 `app`，再等 `migrate` 完成。迁移在这一步失败的话，旧的已经停了，新的不会启动，应用就不可用了。先单独执行迁移，失败时旧版本照常运行，`run` 直接输出失败的原因；修好之后再执行一遍这两条命令。迁移期间旧版本运行在新的表结构上，所以每个版本的表结构变化要让旧版本也能正常工作，见[零停机变更](../database/migrations.md#zero-downtime-changes)。

替换容器的几秒钟内应用不可用。对客户来说这通常可以接受：在使用人数少的时间升级即可。需要不停机升级时，见[不停机升级](operations.md#zero-downtime-upgrades)。

升级之前先备份，见下一节。版本说明中写清楚这个版本能否回退：新版本的迁移改了表结构时，换回旧镜像不会撤销这些修改。

<a id="backups"></a>
## 备份与恢复

要备份的只有三样东西：PostgreSQL 的数据、`storage` 卷中的文件，以及 `.env`。NATS 中只有还没处理完的任务，Memcached 是缓存，它们都不需要备份，原因见[备份与恢复](operations.md#backups)。

导出数据库：

```bash
docker compose exec -T postgres pg_dump -U acme -Fc acme > acme-$(date +%F).dump
```

`-Fc` 是 `pg_dump` 的压缩格式，用 `pg_restore` 恢复。`-T` 让命令的输出直接写到服务器上的文件，不经过终端。

备份文件：

```bash
docker compose cp app:/app/storage ./storage-$(date +%F)
```

先导出数据库、再备份文件，两次之间的间隔尽量短。把这两条命令放进服务器的定时任务，并把结果复制到另一台机器上。数据量变大、需要恢复到任意时间点时，改用[持续归档](operations.md#database-backups)。

恢复时停止应用，把数据库导入一个新的空库，再启动：

```bash
docker compose stop app
docker compose exec -T postgres dropdb -U acme acme
docker compose exec -T postgres createdb -U acme acme
docker compose exec -T postgres pg_restore -U acme -d acme < acme-2026-11-17.dump
docker compose run --rm --no-deps app queue slot recreate
docker compose up -d
```

队列的复制槽不在备份中，恢复后要用 `queue slot recreate` 重建，否则应用启动之前的自检会报告它不存在。恢复之后还有几件事要处理，见[恢复之后](operations.md#after-restoring)。

<a id="external-postgres"></a>
## 使用客户自己的 PostgreSQL

有的客户要求使用他们已有的数据库，例如云厂商的托管数据库。这时从 `compose.yaml` 中删掉 `postgres` 服务、它的卷和 `x-app` 中对它的 `depends_on`，把 `DATABASE_URL` 改为客户提供的地址，写进 `.env`：

```yaml
x-app: &app
  environment:
    DATABASE_URL: ${DATABASE_URL}
```

把下面的要求交给客户的数据库管理员：

| 要求 | 说明 |
| --- | --- |
| PostgreSQL 17 以上 | |
| `wal_level = logical` | 修改后需要重启数据库。云数据库在控制台的参数设置中修改，例如 RDS 的 `rds.logical_replication = 1` |
| 应用的数据库用户有复制权限 | 自建数据库执行 `ALTER ROLE <用户> REPLICATION`；RDS 执行 `GRANT rds_replication TO <用户>`；其他云数据库见各自的文档 |
| `max_slot_wal_keep_size` | 建议设置，例如 `10GB` |
| 应用的服务器能直接连到数据库 | 复制连接不能经过 PgBouncer 等连接池，见 [PgBouncer](operations.md#pgbouncer) |
| 一个空的数据库 | 应用的表由迁移创建 |

各家云数据库的参数名和授权方式不同，也会随版本变化。不必逐条核对文档：配置好之后运行 `docker compose run --rm migrate`，迁移之前的自检会逐项检查这些要求，不满足时指出是哪一项、怎样修改。

<a id="troubleshooting"></a>
## 常见问题

**`app` 一直在重启，`docker compose ps` 显示 `restarting`**

启动之前的自检没有通过，应用退出后又被 `restart: unless-stopped` 拉起。`docker compose logs app` 中有失败的检查和修改方法。最常见的原因是升级时 `migrate` 没有执行成功：先看 `docker compose logs migrate`。

**`migrate` 报 `doctor.failed`，说 `wal_level is replica`**

PostgreSQL 没有使用 `compose.yaml` 中的参数，通常是客户改过 `postgres` 服务的 `command`，或者用的是自己的数据库。按[使用客户自己的 PostgreSQL](#external-postgres) 设置并重启数据库。

**`doctor` 中 `storage` 失败：`permission denied`**

`storage` 卷的目录不属于运行应用的用户。镜像中预先建好这个目录并交给运行应用的用户，见[用 Docker 运行](deployment.md#docker)。已经创建的卷，用一个临时容器修改它的所有者（distroless 镜像中没有 `chown` 命令，`nonroot` 用户的 ID 是 65532）：

```bash
docker run --rm -v acme_storage:/data alpine chown -R 65532:65532 /data
```

卷名是 compose 的项目名（默认是 `compose.yaml` 所在目录的名字）加上 `_storage`，用 `docker volume ls` 查看。

**服务器的磁盘越来越满**

先检查 PostgreSQL 的 WAL：`docker compose exec postgres du -sh /var/lib/postgresql`。应用长时间停止时，队列的复制槽会积压 WAL，直到 `max_slot_wal_keep_size` 的上限。启动应用后，积压的 WAL 会被转发、回收；`docker compose run --rm --no-deps app queue status` 显示积压的大小，见[监控积压](operations.md#queue-monitoring)。
