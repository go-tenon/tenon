## 生产部署（Ubuntu + 原生二进制 + systemd + Nginx）

### 前提条件

- **应用服务器**：Ubuntu（systemd），本地可通过 SSH 登录目标用户并具备免密 `sudo` 权限，已安装 Nginx。
- **基础设施**：按项目实际依赖准备外部服务（例如 PostgreSQL 18+ 放行应用服务器 IP 白名单，NATS 2.15+ 开启 JetStream `-js` 并放通 `4222` 端口）。
- **本地构建环境**：已安装与 [go.mod](go.mod) 一致的 Go 和 Bun。

### 1. 准备部署与生产配置

首次生成 `deploy.yaml`（留空 `arch` 时 `tenon deploy` 会自动通过 SSH 探测目标服务器是 `amd64` 还是 `arm64` 并交叉编译）：

```bash
go tool tenon add deploy --user <ssh用户> --host <服务器IP>
```

在 `deploy.yaml` 中将生产配置设为单一独立文件 `config_files: [config/prod.yaml]`，并创建本地 `config/prod.yaml`（已在 `.gitignore` 中，不提交到仓库）。未显式填写的超时、健康探针过滤（`/livez`、`/readyz`）等项均由框架默认值补全，只需填写生产特有配置：

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
# database:
#   dsn: postgres://<db_user>:<db_pass>@<db_host>:5432/<db_name>?sslmode=disable
# crypt:
#   key: "<openssl rand -base64 32 生成的密钥>"
# nats:
#   url: nats://<nats_user>:<nats_pass>@<nats_host>:4222
# filesystem:
#   disks:
#     local:
#       root: /opt/{{app}}/storage/files   # 生产本地磁盘使用绝对路径，目录归属 {{app}} 服务用户
```

本地校验配置与预演发布计划：

```bash
go run ./cmd/{{app}} config check --config config/prod.yaml
go tool tenon deploy --dry-run
```

### 2. 初始化目标服务器

首次部署运行一次 `setup`，自动检查环境、创建 `{{app}}` 系统用户与 `/opt/{{app}}` 目录，写入 `/etc/systemd/system/{{app}}@.service`，并在 `/usr/local/bin/{{app}}` 安装运维入口（后续调整 `deploy.yaml` 中的 `config_files` 列表后需重新执行 `setup` 并重启服务）：

```bash
go tool tenon deploy setup
```

### 3. 打包、上传并启动服务

直接运行 `go tool tenon deploy`，自动通过 SSH 探测目标机架构（`amd64` 或 `arm64`，多机校验一致）、本地交叉编译嵌入前端与迁移文件、上传校验，并依次执行远端 `config check`、`migrate up`、`doctor` 与零停机启动/热重载：

```bash
# 推荐：自动探测架构、交叉编译并发布
go tool tenon deploy

# 可选：手动交叉编译后发布指定二进制（适合 CI 构建与发布分离）
bun run build --arch amd64              # 或 --arch arm64
go tool tenon deploy --binary bin/{{app}}
```

验证服务状态与远端自检：

```bash
go tool tenon deploy status
go tool tenon deploy run -- doctor
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
go tool tenon deploy               # 发布新版本或同步修改后的 config/prod.yaml（零停机热重载）
go tool tenon deploy status        # 查看当前发布版本与 systemd 服务状态
go tool tenon deploy logs -F       # 实时查看远端应用日志
go tool tenon deploy rollback      # 回退二进制与配置到上一版本
```

`tenon deploy setup` 已将运维入口安装到 `/usr/local/bin/{{app}}`，自动挂载服务用户、工作目录、环境变量与默认配置清单（无需手写 `--config`）；登录服务器后可在任意目录直接执行应用子命令，或在本地通过 `ssh -t` 交互执行（例如创建首个后台账号；终端输入密码时关闭回显属正常现象）：

```bash
# 登录服务器后直接运行
{{app}} doctor
{{app}} <子命令>

# 或在本地通过 SSH 交互运行
ssh -t <ssh用户>@<服务器IP> {{app}} <子命令>
```

多机 `roles` 拓扑、维护窗口与其他部署选项见 `tenon/docs/installation/deployment.md`。
