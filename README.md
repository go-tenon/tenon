# Tenon

Tenon 是一个用 Go 构建后端服务的框架：业务逻辑写成 action，HTTP 接口、服务端页面、队列任务、定时任务、命令行和 MCP 工具都是调用它的入口。HTTP API 是目前最完整的入口：网页用 React、Mantine 和 TanStack Start 编写，由 Go 托管的 Bun 进程渲染，和 App 一样调用同一组 API；官网、文档这类公开页面在服务端渲染或构建时预渲染。它为配置、路由、验证、错误处理、日志、认证、数据库、迁移和命令行提供一套统一的约定，同时让你直接使用社区组件的原生能力：chi 与 Huma、slog 与 slog-multi / slog-formatter / slog-chi、Bun ORM 与 pgx、goose、Cobra、go-i18n 和 cockroachdb/errors。

- **依赖一目了然**：没有服务容器和全局状态，所有依赖都通过普通的构造函数显式传递。
- **一个动作一个文件**：业务逻辑写成 action，HTTP 接口、队列任务、定时任务、命令行和 MCP 工具都是它的入口方法。
- **约定开箱即用**：请求 ID、统一的错误响应格式、强制的路由访问策略声明。
- **按需引入**：每个包都可以单独使用，最小的 API 只用路由相关的包，只处理队列和定时任务的后台服务不需要 HTTP 相关的包。
- **接口文档自动生成**：OpenAPI 文档随代码生成，并可离线导出。

## 快速开始

```bash
go get github.com/go-tenon/tenon@main
```

需要 Go 1.27.4 或更高版本。基础设施是固定的一套，每个组件只做最擅长的事：PostgreSQL 保存业务数据以及锁和限流计数，[Memcached](docs/libraries/cache.md) 做缓存，[NATS](docs/libraries/nats.md) 做任务队列、广播和在线成员。框架的包分为基础、基础设施、入口、功能四层，每个包都可以单独使用，见[框架包一览](docs/concepts/structure.md#framework-packages)。从[安装](docs/installation/installing.md)开始，`tenon init` 一条命令生成能直接运行的 Go + React 项目。

## 文档

完整的文档目录见 [docs/README.md](docs/README.md)。常用章节：

| 你想做的事 | 文档 |
| --- | --- |
| 从零做出一个完整的应用 | [入门教程](docs/tutorial/README.md) |
| 运行第一个应用 | [安装](docs/installation/installing.md) |
| 一条命令开发、构建前端和应用 | [开发工具](docs/installation/tooling.md) |
| 读取配置与密钥 | [配置](docs/general/configuration.md) |
| 组织项目代码 | [目录结构](docs/concepts/structure.md) · [依赖装配](docs/concepts/dependencies.md) · [业务动作](docs/concepts/actions.md) |
| 编写接口 | [路由](docs/http/routing.md) · [请求](docs/http/requests.md) · [验证](docs/http/validation.md) · [响应](docs/http/responses.md) · [认证](docs/security/authentication.md) |
| 为前端和 App 生成接口类型 | [客户端代码](docs/http/clients.md) |
| 编写网页并打进二进制 | [服务端渲染](docs/frontend/ssr.md) · [TanStack Start 的接入](docs/frontend/start.md) · [界面组件](docs/frontend/ui.md) |
| 推送实时消息 | [服务端推送](docs/http/sse.md) · [广播](docs/libraries/broadcasting.md) · [实时频道](docs/libraries/realtime.md) |
| 查看全部路由 | [routes 命令](docs/general/commands.md#routes-command) |
| 处理错误与日志 | [错误处理](docs/general/errors.md) · [日志](docs/general/logging.md) |
| 指标、链路与推送 | [可观测性](docs/general/telemetry.md) |
| 在后台执行任务 | [队列](docs/background/queue.md) · [定时任务](docs/background/scheduling.md) · [单独部署后台服务](docs/general/commands.md#running-together) |
| 防止任务重复执行 | [原子锁](docs/libraries/locks.md) |
| 上传和管理文件 | [文件存储](docs/libraries/filesystem.md) · [上传接口](docs/libraries/file-uploads.md) |
| 发送邮件 | [邮件](docs/libraries/mail.md) |
| 发送短信验证码 | [短信](docs/libraries/sms.md) |
| 生成发票、报表等 PDF | [生成 PDF](docs/libraries/pdf.md) |
| 请求用户填写的地址 | [出站请求](docs/libraries/outbound.md) |
| 给用户发站内通知 | [通知](docs/features/notifications.md) |
| 给 App 发推送通知 | [移动推送](docs/features/push.md) |
| 记录谁改了什么 | [操作日志](docs/features/activity.md) |
| 让用户注销账号、删除个人数据 | [注销账号](docs/features/privacy.md) |
| 让用户导出自己的全部数据 | [导出个人数据](docs/features/data-export.md) |
| 逐步放量、紧急关闭新功能 | [功能开关](docs/features/flags.md) |
| 让管理员修改站点名称、注册开关等设置 | [设置](docs/features/settings.md) |
| 导入导出 Excel 和 CSV | [导入与导出](docs/features/import-export.md) |
| 批量处理并显示进度 | [后台操作](docs/features/operations.md) |
| 向租户推送事件 | [Webhook](docs/features/webhooks.md) |
| 按关键词搜索文章、商品 | [全文搜索](docs/features/search.md) |
| 构建 AI 助手 | [AI 助手](docs/ai/chat.md) · [服务端推送](docs/http/sse.md) |
| 让 Claude、Cursor 调用业务操作 | [MCP 服务](docs/ai/mcp.md) · [OAuth 授权服务器](docs/security/oauth-server.md) |
| 让 AI 引用 PDF、Word 等文档 | [文档转换与切分](docs/ai/documents.md) · [向量检索](docs/ai/vector.md) |
| 保护接口 | [认证](docs/security/authentication.md) · [登录与注册](docs/security/login.md) · [授权](docs/security/authorization.md) · [哈希](docs/security/hashing.md) · [加密与签名链接](docs/security/encryption.md) · [限流](docs/libraries/rate-limiting.md) · [人机验证](docs/security/login-security.md#captcha) |
| 使用数据库 | [数据库](docs/database/quick-start.md) · [迁移](docs/database/migrations.md) |
| 测试与上线 | [测试](docs/testing/overview.md) · [部署](docs/installation/deployment.md) |
