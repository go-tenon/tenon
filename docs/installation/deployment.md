# 部署

这一页讲怎样把应用第一次部署到服务器上：构建出可执行文件，准备配置，执行数据库迁移，让程序在服务器上一直运行，并且在需要停止时平稳地停下。上线以后的升级、维护和备份见[运维](operations.md)。

阅读前需要了解：[脚手架](scaffolding.md)、[配置](../general/configuration.md)、[迁移](../database/migrations.md)。

- [上线需要哪几步](#overview)
- [构建](#building)
- [第一次发布](#release-process)
    - [检查配置](#checking-configuration)
    - [执行迁移](#running-migrations)
    - [启动服务](#starting-the-server)
- [一个进程，还是三个进程](#process-forms)
- [用 systemd 运行](#systemd)
    - [发布新版本](#systemd-releases)
- [用 Docker 运行](#docker)
- [健康检查](#health-checks)
- [平滑停止](#graceful-shutdown)
- [反向代理](#reverse-proxies)
- [日志](#logging)
- [常见问题](#troubleshooting)

<a id="overview"></a>
## 上线需要哪几步

不管用什么方式部署，步骤都是一样的：

1. **构建**：把代码编译成一个可执行文件，前端、迁移文件、翻译文件都打包在其中。
2. **检查配置**：生产环境的配置（数据库地址、密钥等）和开发时不同，先检查有没有缺少或写错。
3. **执行迁移**：让数据库的表结构和新代码一致。
4. **启动服务**：让程序一直运行，崩溃后自动重启，停止时平稳地停下。

前三步每次发布只做一次；第四步交给 systemd、Docker 这类工具，下面分别介绍。

<a id="building"></a>
## 构建

用[脚手架](scaffolding.md)生成的项目，用一条命令构建：

```bash
bun run build
```

它先构建前端，再编译 Go 程序，得到 `bin/api`，见[开发工具](tooling.md#build)。没有前端的项目直接用 `go build`：

```bash
go build -trimpath -o bin/api ./cmd/api
```

`-trimpath` 去掉二进制中你电脑上的文件路径。PostgreSQL 的驱动是纯 Go 写的，可以加上 `CGO_ENABLED=0` 构建一个不依赖系统库的静态二进制，放到任何 Linux 机器上都能运行。

迁移文件、翻译文件、前端页面都通过 [embed](../concepts/go-primer.md#embed) 打包进了二进制，所以部署时只需要两样东西：

- `bin/api` 这个文件；
- `config` 目录。

构建之前确认生成的代码都是最新的：服务端页面用到的 templ、Tailwind 生成的样式表，都提交在代码仓库中，修改源文件后忘了重新生成，二进制中就还是旧的内容。另外，记下每次构建对应的提交，出问题时能准确知道线上运行的是哪个版本。

<a id="release-process"></a>
## 第一次发布

下面的命令在服务器的部署目录（`config` 目录所在的目录）中执行。生产环境的配置由 `config/app.yaml` 和 `config/prod.yaml` 叠加而成，后者只写和开发环境不同的部分，见[按环境覆盖配置](../general/configuration.md#overriding-configuration)。为了简洁，先把配置参数记在一个变量中：

```bash
CONFIG=--config=config/app.yaml,config/prod.yaml
```

<a id="checking-configuration"></a>
### 检查配置

```bash
./bin/api config check $CONFIG
```

它只读取和检查配置文件，不连接数据库，可以在启动服务之前发现缺少的环境变量和写错的字段。

再检查数据库能不能连上，同时看看哪些迁移将要执行：

```bash
./bin/api migrate status $CONFIG
```

数据库连不上时，这条命令以退出码 4 结束。

数据库密码、密钥这类敏感信息不要写在配置文件中。配置文件中写 `${DATABASE_URL}`，真正的值由部署平台的环境变量或密钥管理功能提供，配置文件就可以放心地提交到代码仓库。

<a id="running-migrations"></a>
### 执行迁移

在启动新版本**之前**执行迁移：

```bash
./bin/api migrate up $CONFIG
```

为什么要在启动之前？新代码可能用到新的表和字段，如果先启动服务，第一批请求就会因为表不存在而出错。

迁移执行时会锁住数据库，即使同时有好几台服务器在执行迁移，它们也会排队依次执行，不会把同一个迁移执行两次。

以后的发布中，新旧两个版本会同时运行一小段时间，所以每次的表结构变化都要让新旧版本都能正常工作，见[零停机变更](../database/migrations.md#zero-downtime-changes)。

<a id="starting-the-server"></a>
### 启动服务

```bash
./bin/api start $CONFIG
```

`start` 在一个进程中同时运行三样东西：HTTP 服务、队列的 worker（在后台执行任务）和定时任务的调度器。下一节说明什么时候应该把它们分开运行。

在终端中直接运行时，关闭终端程序就停了，也不会在崩溃后重启。正式的部署交给 [systemd](#systemd) 或 [Docker](#docker)。

<a id="process-forms"></a>
## 一个进程，还是三个进程

按[命令行](../general/commands.md#running-together)一章写好的应用，有三个一直运行的部分：

- **HTTP 服务**（`serve`）：处理接口请求；
- **队列的 worker**（`work`）：在后台执行耗时的任务，例如发送邮件，见[队列](../background/queue.md)；
- **调度器**（`schedule`）：到点执行定时任务，见[定时任务](../background/scheduling.md)。

它们可以在一个进程中一起运行，也可以分开运行。代码完全相同，只是启动的命令不同：

| 方式 | 命令 | 适合 |
| --- | --- | --- |
| 一起运行 | `start` | 刚上线、规模不大：只有一个进程，部署和监控最简单 |
| 分开运行 | `serve`、`work`、`schedule` | 任务很多，或者耗时的任务拖慢了接口：各自按负载增减实例 |

建议从 `start` 开始，确实需要时再分开。分开时只需要把 `start` 换成三组分别运行 `serve`、`work`、`schedule` 的实例；`work` 和 `schedule` 的健康检查见[健康检查](#health-checks)。

每一部分都可以运行多个实例：

- **HTTP 服务**放在负载均衡后面，请求被分摊到各个实例。
- **worker** 从队列中领取任务，一个任务只会被一个实例领取，可以随时增减实例。
- **调度器**用锁保证每个定时任务每次只执行一次，所以多个实例互为备份，前提是它们共用同一个 Redis，见[多个实例](../background/scheduling.md#multiple-instances)。

这些部分共用数据库的连接池。设置连接数上限 `max_conns` 时，要把 HTTP 请求、各队列的并发数和同时执行的定时任务都算进去；所有实例的连接数加起来，不能超过数据库允许的最大连接数，见[连接池](../database/quick-start.md#pool-exhaustion)。

<a id="systemd"></a>
## 用 systemd 运行

在云服务器或物理机上，用 systemd 管理程序。systemd 是大多数 Linux 发行版自带的服务管理器，它负责：开机时启动程序、程序崩溃后重启、收集程序输出的日志，以及停止时给程序发送 `SIGTERM` 信号，让它平稳地停下。

**准备部署目录。** 假设部署目录是 `/opt/blog`，其中有 `bin/api`、`config`，以及保存上传文件的 `storage`。程序用一个专门的系统用户 `blog` 运行，这个用户只能写 `storage` 目录，即使程序被攻破，也改不了其他文件：

```bash
sudo useradd --system --no-create-home --shell /usr/sbin/nologin blog
sudo install -d -o blog -g blog /opt/blog/storage
```

数据库密码等密钥写在 `/etc/blog/env` 中，每行一个，例如 `DATABASE_URL=postgres://...`，权限设为 `600`，只有 root 能读。

**编写服务文件。** systemd 用一个 unit 文件描述怎样运行程序。新建 `/etc/systemd/system/blog@.service`：

```ini
# /etc/systemd/system/blog@.service
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
ExecStart=/opt/blog/bin/api %i --config=config/app.yaml,config/prod.yaml
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

文件名中的 `@` 表示这是一个模板：`%i` 会被替换为启用时写在 `@` 后面的名字，在这里就是要运行的命令。所以一个文件可以运行任何一种[方式](#process-forms)：

```bash
sudo systemctl daemon-reload                                    # 让 systemd 读取新的文件
sudo systemctl enable --now blog@start                          # 一起运行
sudo systemctl enable --now blog@serve blog@work blog@schedule  # 或者分开运行
```

`enable --now` 既立即启动，也设置为开机启动。从一起运行改为分开运行时，先 `sudo systemctl disable --now blog@start`，再启用另外三个。

文件中几组设置的作用：

- **运行身份**：`User` 是运行程序的用户，`WorkingDirectory` 是工作目录（配置文件的相对路径从这里算起），`EnvironmentFile` 读取密钥。
- **崩溃后重启**：`Restart=on-failure` 在程序以非 0 状态退出时重启它，每次间隔 `RestartSec`（5 秒）。数据库暂时连不上时，程序以退出码 4 结束，systemd 每 5 秒重试，直到连上为止。`RestartPreventExitStatus=2 3` 排除了用法错误（2）和配置错误（3）：这两种错误重启多少次都一样，服务直接进入失败状态，用 `systemctl status` 能看到原因。
- **停止**：`TimeoutStopSec` 是发送 `SIGTERM` 后等待程序退出的时间，超时后强制结束。它要比程序的平滑停止时间长，见[平滑停止](#graceful-shutdown)。
- **安全**：`ProtectSystem=strict` 让整个文件系统只读，只有 `ReadWritePaths` 中的目录可写；`PrivateTmp` 给程序一个独立的 `/tmp`；`NoNewPrivileges` 禁止程序获得更多权限。程序需要写其他目录时，把它加到 `ReadWritePaths` 中。
- **就绪与升级**：`Type=notify-reload` 让 systemd 等程序报告“已经就绪”才认为启动完成，并且支持用 `systemctl reload` 不停机地升级；`NotifyAccess`、`RuntimeDirectory`、`PIDFile` 都是为升级准备的。升级的过程见[不停机升级](operations.md#zero-downtime-upgrades)。这几项要求程序的入口使用 `command.Daemon`，见[依赖装配](../concepts/dependencies.md#assembling-the-application)；没有使用时改为 `Type=exec`，并删掉这三行。

程序输出到标准错误的 JSON 日志由 systemd 的 journald 收集，这样查看：

```bash
journalctl -u 'blog@*' -f
```

<a id="systemd-releases"></a>
### 发布新版本

发布新版本时，按[第一次发布](#release-process)的顺序：换上新的二进制，检查配置，执行迁移，再让服务换成新版本。下面的脚本做了这些事：

```bash
set -e
CONFIG=--config=config/app.yaml,config/prod.yaml
run() {
    sudo systemd-run --wait --pipe --quiet --uid=blog \
        -p EnvironmentFile=/etc/blog/env -p WorkingDirectory=/opt/blog "$@" $CONFIG
}

sudo install -m 755 api /opt/blog/bin/api.new
run /opt/blog/bin/api.new config check
sudo mv /opt/blog/bin/api.new /opt/blog/bin/api
run /opt/blog/bin/api migrate up

for unit in $(systemctl list-units 'blog@*' --state=active --plain --no-legend | awk '{print $1}'); do
    sudo systemctl reload "$unit"
    if [[ $(systemctl show -p StatusText --value "$unit") == "upgrade failed"* ]]; then
        systemctl status "$unit" --no-pager
        exit 1
    fi
done
```

逐段解释：

- **`set -e`**：任何一步失败，脚本立即停止，不会带着错误继续发布。
- **`run`**：用 `systemd-run` 执行一次性的命令，它使用和服务相同的用户、环境变量和工作目录，所以检查的结果和服务真正运行时一致。
- **先写到 `api.new`**：用新的二进制检查配置，通过后再改名覆盖。直接覆盖正在运行的二进制会失败，报错 `Text file busy`。
- **迁移只在脚本中执行一次。** 不要把迁移写进 unit 文件的 `ExecStartPre`：那样程序每次崩溃重启都会再执行一次迁移，分开运行时三个实例还会同时执行。
- **`systemctl reload`**：让每个服务不停机地换成新版本。新版本启动失败时，旧版本会继续运行，`reload` 仍然返回成功，所以脚本再检查服务的状态文字，以 `upgrade failed` 开头就是升级失败。

<a id="docker"></a>
## 用 Docker 运行

Docker 把程序和它运行需要的文件打包成一个镜像，在任何装了 Docker 的机器上都能以同样的方式运行。我们的程序是一个静态二进制，所以镜像中只需要这个二进制和 `config` 目录：

```dockerfile
# Dockerfile
FROM golang:1.27 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -o /out/api ./cmd/api && mkdir /out/storage

FROM gcr.io/distroless/static-debian13:nonroot
WORKDIR /app
COPY --from=build /out/api /app/api
COPY config /app/config
COPY --from=build --chown=nonroot:nonroot /out/storage /app/storage
ENTRYPOINT ["/app/api", "--config=config/app.yaml,config/prod.yaml"]
CMD ["start"]
```

这个文件分两段：第一段在 Go 的镜像中编译程序，第二段只把编译结果复制到一个很小的运行镜像中，最终的镜像不包含 Go 工具链和源代码。需要注意的几点：

- **`ENTRYPOINT` 要写成 JSON 数组的形式。** 这样程序就是容器中的第一个进程，能直接收到 `docker stop` 发出的 `SIGTERM`。如果写成 `ENTRYPOINT ./api start`，信号会发给 shell，程序收不到，等待时间一到就被强制结束，正在处理的请求被中断。
- **`--config` 写在 `ENTRYPOINT` 中**，`docker run blog migrate up` 这样的命令也会使用同样的配置。
- **distroless 镜像中带有 CA 证书和时区数据**，访问 HTTPS 接口、按时区执行定时任务都能直接用。
- **`storage` 目录要挂载卷**，否则容器删除后上传的文件也没了。镜像中预先建好这个目录，并交给 `nonroot` 用户，否则新挂载的卷属于 root，程序无法写入。文件存放在 S3 这类对象存储中时不需要它。

用 Docker Compose 分开运行三个部分，并在启动前执行迁移：

```yaml
# compose.yaml
x-app: &app
  image: blog:${APP_VERSION}
  environment:
    APP_URL: https://example.com
    DATABASE_URL: ${DATABASE_URL}
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
  serve:
    <<: *app
    command: [serve]
    ports: ["8080:8080"]
  work:
    <<: *app
    command: [work]
  schedule:
    <<: *app
    command: [schedule]
```

- `migrate` 是一个执行完就结束的服务；其他服务的 `depends_on` 要求它成功结束后才启动，所以迁移一定在服务启动之前完成。
- `x-app` 是三个服务共用的设置，`<<: *app` 把它合并进来。
- **`stop_grace_period` 必须写上。** Docker 默认只等 10 秒就强制结束容器，比队列和调度器默认的平滑停止时间（30 秒）还短。

一起运行时，把三个服务换成一个运行 `start` 的服务即可。

应用用到的其他服务也放进同一个 compose 文件。框架会用到 PostgreSQL、[Redis](../libraries/redis.md)（锁、限流、广播）、[Memcached](../libraries/cache.md)（缓存）和 [NATS](../background/queue.md#preparing)（任务队列）：

```yaml
  redis:
    image: redis:8
    command: [redis-server, --maxmemory-policy, noeviction, --appendonly, "yes"]
    volumes: [redis:/data]
  memcached:
    image: memcached:1.6-alpine
    command: [memcached, -m, "512"]
  nats:
    image: nats:2.15-alpine
    command: [nats-server, -js, -sd, /data, -m, "8222"]
    volumes: [nats:/data]
    ports: ["127.0.0.1:8222:8222"]
# 以及顶层的 volumes: {redis: {}, nats: {}}
```

Redis 要配置 `noeviction`：内存满时报错，而不是悄悄删掉锁和限流计数。NATS 的 `-js` 开启 JetStream，任务保存在 `/data` 中；`8222` 是它的监控端口，只监听本机，通过 SSH 隧道或内网访问。用到[生成 PDF](../libraries/pdf.md#deployment) 等功能时，再按各自的说明加上对应的服务。

容器的升级方式是换一个新容器，见[不停机升级](operations.md#zero-downtime-upgrades)。

<a id="health-checks"></a>
## 健康检查

部署平台和负载均衡器会定期访问程序的两个地址，判断它的状态：

- **存活检查**（例如 `/livez`）：程序还能响应吗？失败时平台会重启程序。所以它只检查程序本身，不检查数据库：数据库出了问题，重启程序也没有用。
- **就绪检查**（例如 `/readyz`）：程序现在能处理请求吗？它检查程序没有在停止，并且数据库等必需的依赖可用。失败时负载均衡暂时不把请求交给这个实例，等依赖恢复后自动重新加入。

这两个地址由应用自己注册，写法见[健康检查](../http/lifecycle.md#health-checks)；再在部署平台中填写对应的路径。

分开运行时，`work` 和 `schedule` 的进程没有 HTTP 服务。在 Kubernetes 这类平台上，给它们设置环境变量 `PROBE_ADDR`，在单独的端口上提供同样的两个路径，见[没有 HTTP 服务的进程](../http/lifecycle.md#probe-listener)。用 systemd 运行时不需要，systemd 通过就绪通知和退出状态管理进程。

<a id="graceful-shutdown"></a>
## 平滑停止

部署平台停止程序时发送 `SIGTERM` 信号。程序收到后开始平滑停止：不再接收新请求，等正在处理的请求完成，等后台任务处理完，最后关闭数据库连接，见[应用生命周期](../concepts/lifecycle.md#graceful-shutdown)。

要让平滑停止生效，有两件事要做对：

**信号要能到达程序。** 在 Docker 中，程序要是容器中的第一个进程，见上文的 `ENTRYPOINT`。用 shell 脚本启动程序时，脚本最后要用 `exec ./bin/api start`，把进程交给程序，否则信号只发给了 shell。

**平台要等得够久。** 平台发出 `SIGTERM` 后只等一段时间，超时后强制结束程序。这段时间要比程序平滑停止所需的时间长：

- 程序等待的时间由三项配置决定：`http.shutdown_timeout`（等待请求）、`queue.shutdown_timeout`（等待后台任务）、`schedule.shutdown_timeout`（等待定时任务）。一起运行时它们同时开始，取最长的一项。
- 使用 [AI 助手](../ai/chat-advanced.md#deployment)时，还要等进行中的回答说完，这段时间要加上去。
- 平台的等待时间（systemd 的 `TimeoutStopSec`、Docker 的 `stop_grace_period`、Kubernetes 的 `terminationGracePeriodSeconds`）应当比上面的时间再长一些，留出关闭数据库等资源的时间。

即使超时被强制结束，也不会丢失工作：没有完成的队列任务会在其他实例上重新执行，错过的定时任务等下一次触发。

<a id="reverse-proxies"></a>
## 反向代理

线上通常不让程序直接面对用户，而是在前面放一个反向代理（例如 nginx）或负载均衡器：由它处理 HTTPS 证书、压缩响应，再把请求转发给程序。这时有几点需要配置：

- **识别用户的真实地址。** 程序看到的请求都来自代理的地址。在 `http.client_ip.trusted_proxies` 中填写代理的网段，程序就会从代理转发的请求头中读出用户的真实地址，见[可信代理](../http/middleware.md#trusted-proxies)。
- **保留原始的 `Host` 请求头**，包括端口。nginx 用 `proxy_set_header Host $http_host;`，不要用 `$host`，它会丢掉端口，程序生成的地址和[实时频道](../libraries/realtime.md)的安全检查都会出错。
- **HTTPS 与 HSTS。** 全站使用 HTTPS 后，在代理上加上 `Strict-Transport-Security` 响应头，例如 nginx 的 `add_header Strict-Transport-Security "max-age=31536000" always;`，浏览器此后只用 HTTPS 访问这个域名。
- **服务端推送**需要在代理上启用 HTTP/2，见[服务端推送](../http/sse.md#deployment)。

程序直接对外提供服务、前面没有代理时，安装 [httpx.Compress](../http/optional-middleware.md#compression) 压缩响应。跨域请求用内置的 [CORS](../http/optional-middleware.md#cors)。限流可以在网关中配置，也可以用内置的[限流](../libraries/rate-limiting.md)。

<a id="logging"></a>
## 日志

程序默认把 JSON 格式的日志输出到标准错误，由 systemd 或 Docker 收集。用户反馈问题时，让他提供响应头 `X-Request-ID` 中的请求 ID，就能在日志中找到这次请求的全部记录，见[日志](../general/logging.md)。要把日志、指标和链路推送到观测平台，见[可观测性](../general/telemetry.md#setup)。

<a id="troubleshooting"></a>
## 常见问题

**`config check` 报错缺少环境变量**

配置文件中引用的 `${...}` 没有设置。检查 `/etc/blog/env` 或部署平台中的环境变量。

**`migrate status` 以退出码 4 结束**

连不上数据库。检查 `DATABASE_URL` 的地址、用户名、密码，以及服务器能否访问数据库的端口。

**覆盖二进制时报错 `Text file busy`**

正在运行的程序占用着这个文件。先写到另一个文件名，再用 `mv` 改名覆盖，见[发布新版本](#systemd-releases)。

**容器停止时正在处理的请求被中断**

信号没有到达程序，或者平台等待的时间太短。检查 `ENTRYPOINT` 是否是 JSON 数组的形式，以及 `stop_grace_period` 的设置，见[平滑停止](#graceful-shutdown)。

**服务启动后反复重启**

用 `systemctl status blog@start` 或 `docker logs` 查看程序退出前的日志。退出码 3 是配置错误，修正配置后再启动。
