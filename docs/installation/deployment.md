# 部署

把应用构建成一个可执行文件，通过 `tenon deploy` 与 systemd 在 Linux 上以原生二进制方式部署。Docker 仅用于开发和测试期启动基础设施依赖。上线后的升级、维护和备份见[运维](operations.md)。

包：`command`、`command/httpcmd`、`command/migratecmd`。

- 每次发布按顺序执行：构建 → `config check` → `migrate up` → `doctor` → 启动或热重载服务。迁移在新版本启动与全量自检之前执行：新代码用到的表和列要先就绪。
- 发布期间新旧版本同时运行，每次的表结构变化都要让两个版本都能运行，见[零停机变更](../database/migrations.md#zero-downtime-changes)。

<a id="building"></a>
## 构建

```bash
bun run generate   # 生成的客户端提交在仓库中，改了接口要重新生成
bun run build      # 即 go tool tenon build，输出 bin/<入口目录名>，例如 bin/blog
```

`tenon build` 构建 `apps` 中的前端，再用 `go build -trimpath` 编译应用，见[开发工具](tooling.md#build)。没有前端的项目直接 `go build -trimpath -o bin/blog ./cmd/blog`。迁移、翻译和前端产物都嵌入在二进制中，服务器上只需要二进制、`config` 目录和环境变量文件。

有服务端渲染的前端（前端 `package.json` 中 `"tenon": {"ssr": true}`）时，运行的机器必须是 Linux（amd64 或 arm64）并带 glibc：SSR 程序由 Bun 编译、从内存（memfd）运行，Alpine（musl）上不能运行，见[托管前端与服务端渲染](../frontend/hosting.md)。在 macOS 上或为另一种架构构建时用 `bun run build --arch amd64`（或 `arm64`）。

<a id="tenon-deploy"></a>
## 使用 `tenon deploy` 自动发布

在项目根目录生成部署配置并初始化目标服务器：

```bash
go tool tenon add deploy --user deploy --host 203.0.113.10
go tool tenon deploy setup
cp .env.example .env.production                      # 填写数据库、NATS、APP_KEY 等密钥
go tool tenon deploy env push .env.production
go tool tenon deploy
```

- `tenon deploy setup` 检查目标机为带 systemd 的 Linux（含 SSR 时检查 glibc），建系统用户和目录，写入 `<应用>@.service`、角色级 drop-in 覆盖文件并 `daemon-reload`，同时在 `/usr/local/bin/<应用>` 安装运维入口（自动切到服务用户、工作目录、环境变量与 `APP_CONFIG`，登录服务器后可在任意目录直接运行 `<应用> doctor` 等子命令）。
- `tenon deploy` 在 `arch` 留空时通过 SSH 执行 `uname -m` 自动探测全部目标主机架构（`amd64` 或 `arm64`，多机校验一致）并本地交叉编译（在 CI 中可传 `--binary` 跳过构建），在主节点加发布锁，全集群并行上传二进制与配置文件并校验 SHA-256，在主迁移节点依次执行 `config check`、`migrate up` 与 `doctor`，随后按波次切换并清理旧版本。
- 常用运维命令：`rollback [version]`（回退二进制与配置）、`status [--role http]`（集群表格状态）、`logs [--role http] [-F] [--since 15m] [--contains ERR]`（多机实时日志汇流）、`run -- doctor`（以服务用户和环境变量执行一次性命令）、`maintenance on|off`（默认仅开关入口主机维护页）。
- **AI 助手与人类同等优先**：所有 `tenon deploy` 子命令均支持 `--format json` 结构化输出且全程非交互；只读观测与发布预演同步作为 MCP 工具暴露（`tenon_deploy_info`、`tenon_deploy_status`、`tenon_deploy_logs`、`tenon_deploy_plan`），见 [MCP 服务](ai-assistants.md#mcp-servers)。

<a id="multi-host-roles"></a>
### 多服务与多机拓扑：`roles`

单机环境在 `deploy.yaml` 中直接写 `hosts: [203.0.113.10]`（运行 `all`）；多服务多台服务器改用 `roles` 按角色声明拓扑：

```yaml
app: blog
keep_releases: 5

environments:
  production:
    user: deploy
    migrate_host: 10.0.1.21           # 可选；未写时默认选首个非入口节点
    roles:
      http:
        hosts: [10.0.1.11, 10.0.1.12, 10.0.1.13]
        max_parallel: 2               # 金丝雀通过后其余节点每批最多同时切 2 台
        stop_timeout: 30s
      worker:
        hosts: [10.0.1.21, 10.0.1.22]
        stop_timeout: 120s            # 自动生成 blog@worker.service.d/override.conf
        env:
          GOMAXPROCS: "4"
      scheduler:
        hosts: [10.0.1.21]            # 同机兼多角色自动归并，只上传和切换一次
```

- **分波次金丝雀滚动**：先升级后端节点（`rpc`、`worker`、`scheduler`），再升级入口节点（`http`、`all`）；每组超过 1 台时先单独切换首台金丝雀并校验 `StatusText` 与本机 `health`，通过后再按 `max_parallel` 滚动其余节点，任一台失败立即熔断并输出最近日志。
- **声明式 unit 对账**：切换每台主机时自动停用并禁用已不在期望列表中的旧 `<应用>@*.service`（例如从 `all` 拆分为 `http` 与 `worker` 时先停 `blog@all.service`），避免端口冲突和孤儿进程。

<a id="deploy-config-files"></a>
### 指定生产配置文件：`config_files`

在 `deploy.yaml` 的环境中用 `config_files` 声明该环境加载的本地配置文件清单（与 `config_dir` 互斥），将生产配置（例如加入 `.gitignore` 的 `config/prod.yaml`）随发布一起同步。支持独立的单一生产配置文件（其余项使用框架默认值），也支持多文件按顺序叠加：

```yaml
environments:
  production:
    user: deploy
    config_files:
      - config/prod.yaml            # 单一独立生产配置；需要叠加基础配置时写 [config/app.yaml, config/prod.yaml]
    hosts: [203.0.113.10]
```

- **精确同步与权限隔离**：`tenon deploy` 在构建前校验清单中的每个文件存在，仅打包清单内文件上传到远端 `releases/<version>/config/`，目录与文件权限分别设为 `0750` / `0640`（属主 `root:<service_user>`），避免把其他环境的配置传到目标机。
- **自动注入 `--config`**：systemd 模板 unit 的 `ExecStart` 以及 `config check`、`doctor`、`migrate up`、`health`、`tenon deploy run` 自动携带清单中的 `--config` 参数，无需在 `/etc/<应用>/env` 中设置 `APP_CONFIG`。
- **零停机生效与随版本回滚**：修改配置文件内容后执行 `tenon deploy` 即可经热重载零停机生效，执行 `tenon deploy rollback` 时配置与二进制一起回退；变更 `config_files` 文件列表本身后需重启对应 unit 使新的 `ExecStart` 生效。

<a id="release-process"></a>
## 第一次发布与手动步骤

不使用 `tenon deploy` 时，按下面的顺序在部署目录（`config` 所在的目录）手动执行，默认加载 `config/app.yaml`。

- 与开发环境不同的值在 `config/app.yaml` 中写成 `${DATABASE_URL}` 这样的环境变量，由 systemd 的 `EnvironmentFile` 提供，密码和密钥不进仓库。
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
- 迁移只在发布时执行一次，不写进 systemd 的 `ExecStartPre`：进程每次崩溃重启都会再执行，分开运行时几个实例同时执行。

<a id="starting-the-server"></a>
### 启动服务

```bash
./bin/blog serve
```

`serve` 在一个进程中运行全部部分（HTTP 服务、队列 worker、调度器等），见[serve 命令](../general/commands.md#serve-command)。正式部署交给 [systemd](#systemd)，由它重启崩溃的进程、停止时发送 `SIGTERM`。

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

`tenon deploy setup` 会自动生成下面的目录和 unit；手动配置时以 `/opt/blog` 为例，程序用专门的系统用户运行：

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
### 手动发布新版本

不使用 `tenon deploy` 时，把新的二进制 `blog` 传到服务器后执行：

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

若目标平台强制要求容器运行，把 `tenon build` 生成的二进制和 `config` 放进带 glibc（有 SSR 前端时）或静态基础镜像，以 JSON 数组形式的 `ENTRYPOINT` 启动并预留不短于平滑停止时间的终止宽限期，再用 `health` 命令作存活检查即可。

<a id="health-checks"></a>
## 健康检查

`/livez` 只检查进程能否响应；`/readyz` 还检查必需的依赖，进程开始停止后返回 `503`。路由的写法见[健康检查](../concepts/lifecycle.md#health-checks)。

- **systemd**：进程非 0 退出时按 `Restart=` 重启，`Type=notify-reload` 告诉它何时就绪；发布脚本和 `tenon deploy` 可运行 `health` 命令（[`httpcmd.Health`](../general/commands.md#health-command)）请求本机 `/livez` 确认新进程响应正常。
- **反向代理**（Caddy、Traefik、HAProxy、Nginx）主动请求 `/readyz`，不把请求转给未就绪或正在停止的实例。
- 健康检查只针对运行 HTTP 服务的实例：worker、调度器、RPC 进程没有 `/livez`。它们出错时以非 0 状态退出，交给重启策略；卡住的 worker 用[队列积压的监控](operations.md#queue-monitoring)发现。

<a id="graceful-shutdown"></a>
## 平滑停止

收到 `SIGTERM` 后，程序停止接收新请求，等处理中的请求、队列任务和定时任务完成，最后关闭数据库，见[应用生命周期](../concepts/lifecycle.md#graceful-shutdown)。

- 信号要到达程序：用包装脚本启动时最后一行写 `exec ./bin/blog serve`。
- systemd 的 `TimeoutStopSec` 要比程序的最长等待时间长：程序最多等 `http.drain_delay + http.shutdown_timeout`（默认 `0s + 10s`）、`queue.shutdown_timeout`（默认 30 秒）、`schedule.shutdown_timeout`（默认 30 秒）中最长的一项，使用 [AI 对话](../ai/chat.md#deployment)时再加上回答说完的时间。

超时被强制结束时，没有完成的队列任务在其他实例上重新执行，错过的定时任务等下一次触发。

<a id="reverse-proxies"></a>
## 反向代理

- 在 `http.client_ip.trusted_proxies` 中填写代理的网段，程序才能读出用户的真实地址，见[可信代理](../http/middleware.md#trusted-proxies)。
- 保留原始的 `Host`（包括端口）：nginx 写 `proxy_set_header Host $http_host;`。`$host` 丢掉端口，生成的链接和[实时频道](../libraries/realtime.md)的来源检查会出错。
- 全站 HTTPS 后在代理上加 HSTS：`add_header Strict-Transport-Security "max-age=31536000" always;`。
- 代理到后端的 `keepalive_timeout` 设为短于后端的 `http.idle_timeout`（默认 `1m`，例如代理设 `30s`），降低高并发热重载时复用已关闭空闲连接触发 502 的概率。
- [服务端推送](../http/sse.md#deployment)和流式输出需要代理启用 HTTP/2 并关闭响应缓冲。
- 前面没有代理时，安装 [httpx.Compress](../http/middleware.md#compression) 压缩响应。

<a id="nginx-config"></a>
### Nginx 配置示例

```nginx
upstream blog_backend {
    server 127.0.0.1:8080 max_fails=3 fail_timeout=10s;
    # 同机反代（127.0.0.1）使用短连接；跨机内网反代按需开启连接池：
    # keepalive 64;
    # keepalive_timeout 30s; # 短于后端 http.idle_timeout（默认 1m）
}

server {
    listen 443 ssl;
    http2 on;
    server_name example.com;

    ssl_certificate     /etc/letsencrypt/live/example.com/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/example.com/privkey.pem;
    add_header Strict-Transport-Security "max-age=31536000" always;

    client_max_body_size 32m; # 与 http.body_limit 或上传上限一致

    location / {
        proxy_pass http://blog_backend;
        proxy_http_version 1.1;
        proxy_set_header Connection "close"; # 跨机开启 keepalive 时改为 ""
        proxy_set_header Host $http_host;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_set_header X-Request-ID $request_id;

        proxy_buffering off;       # 流式输出与 SSE 实时透传
        proxy_read_timeout 300s;
        proxy_next_upstream error timeout invalid_header http_502 http_503 http_504;
    }
}
```

- 代理与后端在同一台机器时，应用配置 `http.client_ip.trusted_proxies: ["127.0.0.1/32"]`；本机回环直连省去空闲连接池，热重载对非幂等 `POST` 请求也零 502。
- 启用 WebSocket（[实时频道](../libraries/realtime.md)）时，在对应 `location` 设置 `proxy_set_header Upgrade $http_upgrade; proxy_set_header Connection "upgrade";`。
- 需要维护窗口时叠加 [503 维护页规则](operations.md#maintenance-at-the-proxy)。

<a id="caddy-config"></a>
### Caddy 配置示例

```caddyfile
example.com {
    header Strict-Transport-Security "max-age=31536000"
    encode zstd gzip

    reverse_proxy 127.0.0.1:8080 {
        # 多机部署时列出多个后端地址：reverse_proxy 10.0.1.11:8080 10.0.1.12:8080
        health_uri /readyz
        health_interval 5s
        health_timeout 2s
        flush_interval -1 # 流式输出与 SSE 实时刷新

        header_up Host {http.request.hostport}
        header_up X-Forwarded-Proto {scheme}

        transport http {
            keepalive 30s # 短于后端 http.idle_timeout（默认 1m）
            keepalive_idle_conns 64
        }
    }
}
```

- Caddy 自动申请并续期 HTTPS 证书、默认开启 HTTP/2 与 HTTP/3，并自动透传 `X-Forwarded-For` 与 WebSocket 升级头。
- 多机部署时结合应用配置 `http.drain_delay: 8s`（大于 `health_interval`），节点停机前先通过 `/readyz` 返回 `503` 让 Caddy 摘流，再关闭监听。

<a id="logging"></a>
## 日志

日志写到标准错误，由 journald 收集（可用 `tenon deploy logs` 或 `journalctl -u 'blog@*' -f` 查看）；标准错误不是终端时为 JSON 格式，见[日志格式](../general/logging.md#formats)。用户反馈问题时让他提供响应头 `X-Request-ID`，按它查这次请求的全部日志，见[按请求 ID 查日志](../general/logging.md#finding-a-request)。推送到观测平台见[可观测性](../general/telemetry.md#setup)。
