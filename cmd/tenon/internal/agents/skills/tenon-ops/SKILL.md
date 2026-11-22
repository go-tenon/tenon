---
name: tenon-ops
description: Tenon 应用上线准备、生产部署与运维配置：自检 doctor、日志与可观测性 telemetry、CORS 与反向代理 trusted_proxies、出站 SSRF 防护 outbound，以及用 tenon add deploy 生成容器部署文件。
---

# 上线与运维

上线前按本技能补齐安全、观测、自检与部署配置。动手前先读 `.tenon/docs/installation/deployment.md` 和 `.tenon/docs/installation/doctor.md`。

## 核对清单

1. **出站请求防 SSRF**：请求外部或用户输入/租户填写的 URL（webhook、图片或文件拉取、链接预览）时，使用 `outbound.New(outbound.Options{...})` 构造 `*http.Client`，不裸用 `http.Client` 或 `http.DefaultClient`，见 `.tenon/docs/libraries/outbound.md#creating-clients`。
2. **日志与可观测性**：
   - `Config` 中包含 `Logs logging.Config` 与 `Telemetry telemetry.Config`，入口先调用 `telemetry.Open` 并最早登记 `tel.Shutdown` 到 `cleanup.Stack`，再用 `logging.New(cfg.Logs, logging.Export(tel.Logs))` 安装日志器，见 `.tenon/docs/general/telemetry.md#example-usage` 和 `.tenon/docs/general/logging.md#creating-a-logger`。
   - 把 `tel.Meters`、`tel.Tracers` 传给 `httpx.Telemetry`、`database.WithTelemetry`、`queue.WithTelemetry`、`schedule.WithTelemetry`，见 `.tenon/docs/general/telemetry.md#components`。
3. **反向代理与跨域**：
   - 经 Nginx/Caddy/LB 部署时配置 `http.client_ip.trusted_proxies`，确保日志记录真实客户端 IP，见 `.tenon/docs/http/middleware.md#trusted-proxies`。
   - 需要浏览器跨域调用的 API 路由组安装 `httpx.CORS(cfg.HTTP.CORS)`，按需通过 `ExposeHeaders` 暴露自定义响应头，见 `.tenon/docs/http/middleware.md#cors`。
   - 浏览器登录 Cookie 带有 `__Host-` 和 `Secure`，生产环境经反向代理启用 HTTPS，见 `.tenon/docs/installation/deployment.md#reverse-proxies`。
4. **自检与迁移前置校验**：
   - 登记 `doctorcmd.New(a.doctor()...)` 组合 `database.Doctor`、`migrate.Doctor`、`queue.Doctor` 等，见 `.tenon/docs/installation/doctor.md#doctor-command`。
   - 在 `migratecmd.New` 中传入 `migratecmd.Check`，在 `serve` 的入口用 `doctor.Require`，环境不满足要求时拒绝启动，见 `.tenon/docs/installation/doctor.md#before-migrating` 和 `.tenon/docs/installation/doctor.md#before-starting`。
5. **只连数据库的运维命令**：创建首个管理员等只读/写数据库的命令行，用只打开数据库的 `openDB` 绑定，避免初始化时强依赖 NATS。
6. **生成部署文件**：运行 `go tool tenon add deploy` 生成 `Dockerfile`、`.dockerignore`、`compose.prod.yaml` 和 `.env.example`，按项目是否启用 SSR 自动选择 `distroless/base` 或 `distroless/static`，见 `.tenon/docs/installation/tooling.md#add-deploy` 和 `.tenon/docs/installation/self-hosting.md#deliverables`。

## 发布顺序

构建 → `config check` → `doctor` → `migrate up` → 启动或滚动升级服务，见 `.tenon/docs/installation/deployment.md#release-process`。
