## 生产部署（Ubuntu + 原生二进制 + systemd + Nginx）

### 前提条件

- **应用服务器**：Ubuntu（systemd），本地可通过 SSH 登录目标用户并具备免密 `sudo` 权限，已安装 Nginx。
- **基础设施**：按项目实际依赖准备外部服务（例如 PostgreSQL 18+ 放行应用服务器 IP 白名单，NATS 2.15+ 开启 JetStream `-js` 并放通 `4222` 端口）。
- **本地构建环境**：已安装与 [go.mod](go.mod) 一致的 Go 和 Bun。

### 1. 准备部署目录与实例配置

首次生成多实例部署骨架（`deploy/app.yaml` 与 `deploy/example/` 提交到仓库，`deploy/targets/` 整体在 `.gitignore` 中，不提交客户主机地址与密钥）：

```bash
go tool tenon add deploy --user <ssh用户> --host <服务器IP>
```

每套独立部署实例放在 `deploy/targets/<目标名>/` 下（新增合作方或环境时复制 `deploy/example/` 即可，也可通过环境变量 `TENON_DEPLOY_TARGETS` 指向外部私有运维仓库）。每个目标包含：

- `target.yaml`：声明该实例的主机/角色与远端 `instance` 名（留空默认 `{{app}}`；合作方需要独立名称时可改为任意标识，systemd unit、用户、目录、二进制与运维命令均按 `instance` 隔离）。
- `config.yaml`：该实例的独立生产配置（同目录存在时自动以 `0640` 权限同步并传入 `--config`），未显式填写的超时、健康探针过滤（`/livez`、`/readyz`）等项均由框架默认值补全：

```yaml
http:
  addr: "127.0.0.1:8080"                # 仅监听本机回环，由同机 Nginx 反向代理转发
  public_url: "https://example.com"     # 应用对外的访问地址，按需填写
  client_ip:
    trusted_proxies:
      - "127.0.0.1/32"                  # 同机 Nginx 反代网段，用于还原客户端真实 IP
  access_log:
    disabled: true                      # 反向代理已记录访问日志时可关闭应用访问日志；也可改为 logs.levels.http: warn 只记 4xx/5xx

# 按项目实际配置结构填写依赖项（仅保留项目 Config 中声明的字段；密码含 @ 等特殊字符需 URL 编码，例如 @ 写为 %40）
# logs:
#   level: info
# telemetry:
#   service: {{app}}
#   endpoint: https://o2.example.com/api/default
#   headers:
#     Authorization: Basic <base64(user:password)>
#     stream-name: {{app}}                # OpenObserve 目标流名（logs/traces 共用，同名按类型隔离）
#   browser:
#     site: https://o2.example.com
#     client_token: <openobserve-rum-client-token>
#     organization: default
#     environment: production
# database:
#   dsn: postgres://<db_user>:<db_pass>@<db_host>:5432/<db_name>?sslmode=disable
# crypt:
#   key: "<openssl rand -base64 32 生成的密钥>"
# nats:
#   url: nats://<nats_user>:<nats_pass>@<nats_host>:4222
# filesystem:
#   disks:
#     local:
#       root: /opt/{{app}}/storage/files   # 生产本地磁盘使用绝对路径，目录归属服务用户
```

本地校验配置与预演发布计划：

```bash
go run ./cmd/{{app}} config check --config deploy/targets/prod/config.yaml
go tool tenon deploy -t prod --dry-run
```

### 2. 初始化目标服务器

首次部署运行一次 `setup`，自动检查环境、创建 `<instance>` 系统用户与 `/opt/<instance>` 目录，写入 `/etc/systemd/system/<instance>@.service`，并在 `/usr/local/bin/<instance>` 安装运维入口：

```bash
go tool tenon deploy setup -t prod
```

### 3. 打包、上传并启动服务

直接运行 `go tool tenon deploy`，自动校验 Git 工作区干净、并发探测目标机架构（`amd64` 或 `arm64`）、带前端指纹缓存交叉编译（自动注入 Git Tag 版本到 `--version` 与 OTel `service.version`）、内容寻址去重后经 gzip 流式上传、全机并行 `config check`、主节点 `migrate up` 与 `doctor`，最后按波次零停机热重载（unit 或 drop-in 变更时自动滚动重启）。存在多个目标时显式传 `-t`（支持一次构建、按序串行发多个实例）：

```bash
# 发布单个目标（仅存在唯一目标时可省略 -t；未提交代码时传 --allow-dirty）
go tool tenon deploy -t prod

# 一次构建，按序串行发布多个目标实例（任一目标失败立即熔断）
go tool tenon deploy -t prod -t partner-a

# 仅修改目标 config.yaml 时：零编译、零上传二进制、不执行迁移，秒级热重载生效
go tool tenon deploy config -t prod

# 发布指定 GitHub Release 标签产物（自动校验 checksums.txt）或本地二进制
go tool tenon deploy -t prod --tag v1.0.0
go tool tenon deploy -t prod --binary bin/{{app}}
```

验证服务状态与远端自检：

```bash
go tool tenon deploy status -t prod
go tool tenon deploy releases -t prod
go tool tenon deploy run -t prod -- doctor
```

### 4. 配置 Nginx 反向代理（80 端口模板）

在服务器上创建或更新站点配置（例如 `/etc/nginx/conf.d/{{app}}.conf`），将 80 端口请求转发到本机 `127.0.0.1:8080`：

```nginx
map $http_upgrade $connection_upgrade {
    default upgrade;
    ''      close; # 普通 HTTP 走同机回环短连接（配合热重载避免 502），WebSocket 自动升级
}

upstream {{app}}_backend {
    server 127.0.0.1:8080 max_fails=3 fail_timeout=10s;
}

server {
    listen 80;
    server_name example.com;

    client_max_body_size 32m;

    location / {
        proxy_pass http://{{app}}_backend;
        proxy_http_version 1.1;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection $connection_upgrade;
        proxy_set_header Host $http_host;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_set_header X-Request-ID $request_id;

        proxy_buffering off;       # 流式输出、SSE 与 WebSocket 实时透传
        proxy_read_timeout 300s;
        proxy_send_timeout 300s;
        proxy_next_upstream error timeout invalid_header http_502 http_503 http_504;
    }
}
```

> HTTPS（443 端口、TLS 证书、HTTP/2 与 80→443 重定向）由运维人员按需在 Nginx 或前置负载均衡上配置；浏览器会话 Cookie 带有 `__Host-` 与 `Secure` 属性，外网访问需通过 HTTPS 接入。

校验、重载 Nginx 并验证就绪探针：

```bash
sudo nginx -t && sudo systemctl reload nginx
curl -fsS http://127.0.0.1:8080/readyz
```

### 5. 日常运维与一次性命令

```bash
go tool tenon deploy -t prod               # 完整发布（二进制 + 配置，自动内容去重与 No-op 检测）
go tool tenon deploy config -t prod        # 仅同步修改后的配置并零停机热重载
go tool tenon deploy status -t prod        # 查看当前发布序号、Git 版本、配置摘要与 systemd 状态
go tool tenon deploy releases -t prod      # 查看留存的历史发布清单
go tool tenon deploy logs -t prod -F       # 实时查看远端应用日志
go tool tenon deploy rollback -t prod      # 按激活历史栈回退到上一发布（也可指定序号或 Git Tag）
```

`tenon deploy setup` 已将运维入口安装到 `/usr/local/bin/<instance>`（默认 `/usr/local/bin/{{app}}`），自动挂载服务用户、工作目录、环境变量与默认配置清单（无需手写 `--config`）；登录服务器后可在任意目录直接执行应用子命令，或在本地通过 `ssh -t` 交互执行（例如创建首个后台账号；终端输入密码时关闭回显属正常现象）：

```bash
# 登录服务器后直接运行
{{app}} doctor
{{app}} <子命令>

# 或在本地通过 SSH 交互运行
ssh -t <ssh用户>@<服务器IP> {{app}} <子命令>
```

多实例命名、多机 `roles` 拓扑、维护窗口与其他部署选项见 `tenon/docs/installation/deployment.md`。
