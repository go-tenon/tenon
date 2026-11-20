# 部署

把应用构建成一个可执行文件，用 systemd、Docker Compose 或 Docker Swarm 运行。上线后的升级、维护和备份见[运维](operations.md)。

包：`command`、`command/httpcmd`、`command/migratecmd`。

- 每次发布按顺序执行：构建 → `config check` → `migrate up` → 启动或升级服务。迁移在新版本启动之前执行：新代码用到的表和列要先存在。
- 发布期间新旧版本同时运行，每次的表结构变化都要让两个版本都能运行，见[零停机变更](../database/migrations.md#zero-downtime-changes)。

<a id="building"></a>
## 构建

```bash
bun run generate   # 生成的客户端提交在仓库中，改了接口要重新生成
bun run build      # 即 go tool tenon build，输出 bin/<入口目录名>，例如 bin/blog
```

`tenon build` 构建 `apps` 中的前端，再用 `go build -trimpath` 编译应用，见[开发工具](tooling.md#build)。没有前端的项目直接 `go build -trimpath -o bin/blog ./cmd/blog`。迁移、翻译和前端产物都嵌入在二进制中，服务器上只需要二进制和 `config` 目录。

有服务端渲染的前端（前端 `package.json` 中 `"tenon": {"ssr": true}`）时，运行的机器必须是 Linux（amd64 或 arm64）并带 glibc：SSR 程序由 Bun 编译、从内存（memfd）运行，Alpine（musl）上不能运行，见[托管前端与服务端渲染](../frontend/hosting.md)。在 macOS 上或为另一种架构构建时用 `bun run build --arch amd64`（或 `arm64`）。

<a id="release-process"></a>
## 第一次发布

命令在部署目录（`config` 所在的目录）中执行，默认加载 `config/app.yaml`。

- 与开发环境不同的值在 `config/app.yaml` 中写成 `${DATABASE_URL}` 这样的环境变量，由 systemd 的 `EnvironmentFile`、Docker 的 `environment` 或工作目录中的 `.env` 提供，密码和密钥不进仓库。
- 部署目录中不放 `config/local.yaml`：它存在时叠加在 `config/app.yaml` 之后。要叠加别的文件用 `--config` 或 `APP_CONFIG`，见[叠加多个配置文件](../general/configuration.md#overriding-configuration)。

<a id="checking-configuration"></a>
### 检查配置

```bash
./bin/blog config check     # 只校验配置，不连数据库；缺环境变量、字段写错时以退出码 3 结束
./bin/blog migrate status   # 列出将要执行的迁移；连不上数据库时以退出码 4 结束
./bin/blog doctor           # 检查 PostgreSQL、队列、文件存储的设置和权限，见“自检”
```

`doctor` 要先在应用中装配，见[自检](doctor.md)。

<a id="running-migrations"></a>
### 执行迁移

```bash
./bin/blog migrate up
```

- 迁移加锁，多台服务器同时执行时依次进行，同一个迁移只执行一次。
- 迁移只在发布时执行一次，不写进 systemd 的 `ExecStartPre` 或容器的启动命令：进程每次崩溃重启都会再执行，分开运行时几个实例同时执行。

<a id="starting-the-server"></a>
### 启动服务

```bash
./bin/blog serve
```

`serve` 在一个进程中运行全部部分（HTTP 服务、队列 worker、调度器等），见[serve 命令](../general/commands.md#serve-command)。正式部署交给 [systemd](#systemd) 或 [Docker Compose](#docker-compose)，由它们重启崩溃的进程、停止时发送 `SIGTERM`。

<a id="process-forms"></a>
## 一个进程，还是每个部分一个进程

| 命令 | 适合 |
| --- | --- |
| `serve`（即 `serve all`） | 刚上线、规模不大：一个进程，部署和监控最简单 |
| `serve http`、`serve worker`、`serve scheduler`、`serve rpc`…… | 任务多，或耗时的任务拖慢了接口：各部分按负载增减实例 |

部分的名字是 `bootstrap` 的 `Parts` 中登记的名字，`serve --help` 列出全部。代码和配置相同，先用 `serve`，需要时再分开。

- 每个部分都可以运行多个实例：HTTP 服务放在负载均衡后面；一个任务只被一个 worker 领取；调度器用锁保证每次触发只执行一次，见[多个实例](../background/scheduling.md#multiple-instances)。
- 只有 HTTP 服务需要健康检查，见[健康检查](#health-checks)。
- 所有实例的 `max_conns` 之和不超过数据库的最大连接数，见[连接池](../database/database.md#pool-exhaustion)；不够时用 [PgBouncer](operations.md#pgbouncer)。

<a id="systemd"></a>
## 用 systemd 运行

部署目录中有 `bin/blog`、`config` 和保存上传文件的 `storage`，下文以 `/opt/blog` 为例。程序用专门的系统用户运行：

```bash
sudo useradd --system --no-create-home --shell /usr/sbin/nologin blog
sudo install -d -o blog -g blog /opt/blog/storage
```

`/etc/blog/env`（权限 `600`，属于 root）每行一个变量，例如 `DATABASE_URL=postgres://...`。

`/etc/systemd/system/blog@.service`：

```ini
[Unit]
Description=blog %i
After=network-online.target
Wants=network-online.target

[Service]
Type=notify-reload
NotifyAccess=all
RuntimeDirectory=blog/%i
PIDFile=/run/blog/%i/daemon.pid
User=blog
WorkingDirectory=/opt/blog
EnvironmentFile=/etc/blog/env
ExecStart=/opt/blog/bin/blog serve %i
Restart=on-failure
RestartSec=5s
RestartPreventExitStatus=2 3
TimeoutStopSec=45s
NoNewPrivileges=yes
ProtectSystem=strict
ReadWritePaths=/opt/blog/storage
PrivateTmp=yes

[Install]
WantedBy=multi-user.target
```

```bash
sudo systemctl daemon-reload
sudo systemctl enable --now blog@all                               # 一起运行
sudo systemctl enable --now blog@http blog@worker blog@scheduler   # 或者分开运行
journalctl -u 'blog@*' -f
```

- `%i` 是 `@` 后面的部分名，`all` 运行全部。改为分开运行时先 `systemctl disable --now blog@all`。
- `Type=notify-reload`、`NotifyAccess`、`RuntimeDirectory`、`PIDFile` 启用就绪通知和 `systemctl reload` 的[不停机升级](operations.md#zero-downtime-upgrades)，由 `command.Daemon` 实现。
- `RestartPreventExitStatus=2 3`：用法错误和配置错误不重启，`systemctl status` 显示原因。数据库连不上时以 4 退出，每 5 秒重试。
- `TimeoutStopSec` 要比程序的[平滑停止](#graceful-shutdown)时间长。程序要写其他目录时加进 `ReadWritePaths`。

<a id="systemd-releases"></a>
### 发布新版本

把新的二进制 `blog` 传到服务器后执行：

```bash
set -e
run() {
    sudo systemd-run --wait --pipe --quiet --uid=blog \
        -p EnvironmentFile=/etc/blog/env -p WorkingDirectory=/opt/blog "$@"
}

sudo install -m 755 blog /opt/blog/bin/blog.new
run /opt/blog/bin/blog.new config check
sudo mv /opt/blog/bin/blog.new /opt/blog/bin/blog
run /opt/blog/bin/blog migrate up

for unit in $(systemctl list-units 'blog@*' --state=active --plain --no-legend | awk '{print $1}'); do
    sudo systemctl reload "$unit"
    if [[ $(systemctl show -p StatusText --value "$unit") == "upgrade failed"* ]]; then
        systemctl status "$unit" --no-pager
        exit 1
    fi
done
```

- `run` 用服务的用户、环境变量和工作目录执行一次性命令。
- 新二进制先写到 `blog.new` 再 `mv`：直接覆盖正在运行的文件报错 `Text file busy`。
- 新版本启动失败时旧版本继续运行，`reload` 仍返回成功，所以检查状态文字是否以 `upgrade failed` 开头。

<a id="docker"></a>
## 用 Docker 构建镜像

`Dockerfile`：

```dockerfile
FROM golang:1.27 AS build
COPY --from=oven/bun:1.4.4 /usr/local/bin/bun /usr/local/bin/bun
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go tool tenon build -o /out/blog && mkdir /out/storage

FROM gcr.io/distroless/base-debian13:nonroot
WORKDIR /app
COPY --from=build /out/blog /app/blog
COPY config /app/config
COPY --from=build --chown=nonroot:nonroot /out/storage /app/storage
ENTRYPOINT ["/app/blog"]
CMD ["serve"]
```

- `ENTRYPOINT` 写成 JSON 数组，程序是 1 号进程，能收到 `docker stop` 的 `SIGTERM`。写成 shell 形式时信号发给 shell，程序到超时被强制结束。
- `distroless/base` 带 glibc，SSR 程序需要；没有服务端渲染的前端时可以用 `distroless/static`。
- `storage` 预先建好并交给 `nonroot`，新挂载的卷继承这个所有者，程序才能写入。文件存放在 S3 时不需要它。
- `HEALTHCHECK` 不写进 Dockerfile：同一个镜像也运行没有 HTTP 服务的 worker 和调度器。

<a id="docker-compose"></a>
## 用 Docker Compose 运行

`compose.yaml`，`APP_VERSION`、`DATABASE_URL` 写在同目录的 `.env` 中：

```yaml
x-app: &app
  image: blog:${APP_VERSION}
  environment:
    APP_URL: https://example.com
    DATABASE_URL: ${DATABASE_URL}
    NATS_URL: nats://nats:4222
  volumes: [storage:/app/storage]
  restart: unless-stopped
  stop_grace_period: 45s
  depends_on:
    migrate:
      condition: service_completed_successfully

services:
  migrate:
    image: blog:${APP_VERSION}
    environment:
      DATABASE_URL: ${DATABASE_URL}
    command: [migrate, up]
  http:
    <<: *app
    command: [serve, http]
    ports: ["8080:8080"]
    healthcheck:
      test: [CMD, /app/blog, health]
  worker:
    <<: *app
    command: [serve, worker]
  scheduler:
    <<: *app
    command: [serve, scheduler]
  nats:
    image: nats:2.15-alpine
    command: [nats-server, -js, -sd, /data]
    volumes: [nats:/data]

volumes:
  storage:
  nats:
```

- `stop_grace_period` 必须写：Docker 默认只等 10 秒，短于队列和调度器默认的 30 秒。
- 应用用服务名访问依赖，配置文件写 `nats.url: ${NATS_URL:-nats://127.0.0.1:4222}`，见[连接 NATS](../libraries/nats.md#connecting)。用到 [Memcached](../libraries/cache.md#config-servers)、[Gotenberg](../libraries/pdf.md#deployment) 时按各自的说明加服务。
- 一起运行时把 `http`、`worker`、`scheduler` 换成一个 `command: [serve]` 的服务，健康检查写在它上面。
- 连 PostgreSQL 一起运行的版本见[交给客户自托管](self-hosting.md)。

<a id="compose-releases"></a>
### 发布新版本

改 `.env` 中的 `APP_VERSION`，然后：

```bash
docker compose pull
docker compose run --rm migrate
docker compose up -d
```

迁移先单独执行：`up -d` 先停旧容器再等 `migrate`，迁移失败时没有版本在运行；单独执行失败时旧版本照常运行。替换容器的几秒内服务不可用，不停机替换用 [Docker Swarm](#docker-swarm)。

<a id="docker-swarm"></a>
## 用 Docker Swarm 运行

用 compose 文件部署（`docker stack deploy -c compose.yaml blog`），HTTP 服务加上 `deploy`：

```yaml
  http:
    <<: *app
    command: [serve, http]
    ports: ["8080:8080"]
    healthcheck:
      test: [CMD, /app/blog, health]
    deploy:
      replicas: 2
      update_config:
        order: start-first
        failure_action: rollback
```

- `order: start-first`：新容器健康后才停止旧容器，升级不中断服务。
- Swarm 忽略 `depends_on`，迁移在 `docker stack deploy` 之前单独执行，例如 `docker run --rm -e DATABASE_URL blog:1.4.2 migrate up`。
- `docker stack deploy` 不读 `.env`：compose 文件中的 `${APP_VERSION}` 等变量要先在 shell 中设置，例如 `APP_VERSION=1.4.2 docker stack deploy -c compose.yaml blog`。

<a id="health-checks"></a>
## 健康检查

`/livez` 只检查进程能否响应；`/readyz` 还检查必需的依赖，进程开始停止后返回 `503`。路由的写法见[健康检查](../concepts/lifecycle.md#health-checks)。

- **systemd** 不用 HTTP 检查：进程非 0 退出时按 `Restart=` 重启，`Type=notify-reload` 告诉它何时就绪。
- **Docker Compose、Swarm** 用 `health` 命令（[`httpcmd.Health`](../general/commands.md#health-command)），它请求本机的 `/livez`，镜像中不需要 curl：`test: [CMD, /app/blog, health]`。Swarm 替换检查失败的容器，滚动升级等新容器健康后再继续。
- **反向代理**（Caddy、Traefik、HAProxy）主动请求 `/readyz`，不把请求转给未就绪或正在停止的实例。

规则：

- Docker 检查 `/livez`：数据库故障时 `/readyz` 失败，Swarm 会反复替换全部容器，重启解决不了数据库的问题。`/livez` 在程序开始处理请求后才成功，足够判断新版本有没有起来。
- 健康检查只写在运行 HTTP 服务的服务上：worker、调度器、RPC 进程没有 `/livez`。它们出错时以非 0 状态退出，交给重启策略；卡住的 worker 用[队列积压的监控](operations.md#queue-monitoring)发现。

<a id="graceful-shutdown"></a>
## 平滑停止

收到 `SIGTERM` 后，程序停止接收新请求，等处理中的请求、队列任务和定时任务完成，最后关闭数据库，见[应用生命周期](../concepts/lifecycle.md#graceful-shutdown)。

- 信号要到达程序：Docker 的 `ENTRYPOINT` 写成 JSON 数组；用 shell 脚本启动时最后一行写 `exec ./bin/blog serve`。
- 平台的等待时间（systemd 的 `TimeoutStopSec`、Docker 的 `stop_grace_period`）要比程序的长：程序最多等 `http.shutdown_timeout`（默认 10 秒）、`queue.shutdown_timeout`（默认 30 秒）、`schedule.shutdown_timeout`（默认 30 秒）中最长的一项，使用 [AI 对话](../ai/chat.md#deployment)时再加上回答说完的时间。

超时被强制结束时，没有完成的队列任务在其他实例上重新执行，错过的定时任务等下一次触发。

<a id="reverse-proxies"></a>
## 反向代理

- 在 `http.client_ip.trusted_proxies` 中填写代理的网段，程序才能读出用户的真实地址，见[可信代理](../http/middleware.md#trusted-proxies)。
- 保留原始的 `Host`（包括端口）：nginx 写 `proxy_set_header Host $http_host;`。`$host` 丢掉端口，生成的链接和[实时频道](../libraries/realtime.md)的来源检查会出错。
- 全站 HTTPS 后在代理上加 HSTS：`add_header Strict-Transport-Security "max-age=31536000" always;`。
- [服务端推送](../http/sse.md#deployment)需要代理启用 HTTP/2。
- 前面没有代理时，安装 [httpx.Compress](../http/middleware.md#compression) 压缩响应。

<a id="logging"></a>
## 日志

日志写到标准错误，由 journald 或 Docker 收集；标准错误不是终端时为 JSON 格式，见[日志格式](../general/logging.md#formats)。用户反馈问题时让他提供响应头 `X-Request-ID`，按它查这次请求的全部日志，见[按请求 ID 查日志](../general/logging.md#finding-a-request)。推送到观测平台见[可观测性](../general/telemetry.md#setup)。
