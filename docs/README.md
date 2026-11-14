# Tenon 文档

Tenon 是一个用于构建 HTTP API 的 Go 应用框架。登录后使用的界面是调用 API 的单页应用或 App，官网、帮助中心这类公开页面可以在服务端渲染。它为配置、路由、验证、错误处理、日志、认证、数据库和迁移提供一套统一的约定，同时让你直接使用 chi、Huma、slog、Bun、goose、Cobra 等社区组件的原生能力。

第一次使用 Tenon，先读[安装](installation/installing.md)，然后跟着[入门教程](tutorial/README.md)做一个博客。之后按需查阅下面的章节，每一页都可以单独阅读。

## 入门

- [安装](installation/README.md)
    - [安装](installation/installing.md)
    - [开发工具](installation/tooling.md)
    - [部署](installation/deployment.md)
- [入门教程](tutorial/README.md)
    - [创建项目](tutorial/01-project.md)
    - [基础设施与装配](tutorial/02-infrastructure.md)
    - [数据库与迁移](tutorial/03-database.md)
    - [文章接口](tutorial/04-api.md)
    - [文章页面](tutorial/05-pages.md)
    - [作者登录](tutorial/06-login.md)
    - [草稿与发布](tutorial/07-drafts.md)

## 概览与通用主题

- [概览](concepts/README.md)
    - [目录结构](concepts/structure.md)
    - [请求生命周期](concepts/lifecycle.md)
    - [依赖装配](concepts/dependencies.md)
    - [业务动作](concepts/actions.md)
- [通用主题](general/README.md)
    - [配置](general/configuration.md)
    - [错误处理](general/errors.md)
    - [日志](general/logging.md)
    - [可观测性](general/telemetry.md)
    - [本地化](general/localization.md)
    - [命令行](general/commands.md)

## 处理请求

- [请求处理](http/README.md)
    - [路由](http/routing.md)
    - [中间件](http/middleware.md)
    - [请求](http/requests.md)
    - [验证](http/validation.md)
- [构建响应](responses/README.md)
    - [响应](responses/responses.md)
    - [客户端代码](responses/clients.md)
    - [单页应用](responses/spa.md)
    - [服务端推送](responses/sse.md)
    - [视图](responses/views.md)
    - [表单](responses/forms.md)
    - [前端资源](responses/assets.md)
    - [界面组件](responses/components.md)
    - [文档页](responses/doc-pages.md)

## 数据

- [数据库](database/README.md)
    - [快速入门](database/quick-start.md)
    - [迁移](database/migrations.md)

## 库与功能

- [库参考](libraries/README.md)
    - [缓存](libraries/cache.md)
    - [原子锁](libraries/locks.md)
    - [限流](libraries/rate-limiting.md)
    - [队列](libraries/queue.md)
    - [定时任务](libraries/scheduling.md)
    - [广播](libraries/broadcasting.md)
    - [实时频道](libraries/realtime.md)
    - [文件存储](libraries/filesystem.md)
    - [文件的常见用法与上传接口](libraries/file-uploads.md)
    - [邮件](libraries/mail.md)
    - [短信](libraries/sms.md)
    - [生成 PDF](libraries/pdf.md)
    - [出站请求](libraries/outbound.md)
    - [Redis](libraries/redis.md)
- [应用功能](features/README.md)
    - [通知](features/notifications.md)
    - [移动推送](features/push.md)
    - [操作日志](features/activity.md)
    - [功能开关](features/flags.md)
    - [设置](features/settings.md)
    - [后台操作](features/operations.md)
    - [导入与导出](features/import-export.md)
    - [后台导入与导出](features/background-import-export.md)
    - [Webhook](features/webhooks.md)
    - [全文搜索](features/search.md)
- [安全](security/README.md)
    - [认证](security/authentication.md)
    - [登录与注册](security/login.md)
    - [API 令牌](security/api-tokens.md)
    - [第三方登录](security/social-login.md)
    - [企业单点登录](security/enterprise-sso.md)
    - [OAuth 授权服务器](security/oauth-server.md)
    - [多套账号](security/account-examples.md)
    - [多租户](security/tenancy.md)
    - [授权](security/authorization.md)
    - [哈希](security/hashing.md)
    - [加密与签名链接](security/encryption.md)
- [AI](ai/README.md)
    - [AI 助手](ai/chat.md)
    - [MCP 服务](ai/mcp.md)
    - [文档转换与切分](ai/documents.md)
    - [向量检索](ai/vector.md)

## 进阶

- [测试](testing/README.md)
    - [快速入门](testing/overview.md)
    - [数据库与业务动作](testing/database.md)
    - [HTTP 与命令](testing/http.md)

文档与同一版本的代码对应。框架依赖的精确版本以仓库中的 [go.mod](../go.mod) 为准。
