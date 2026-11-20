# Tenon 文档

Tenon 是用 Go 构建后端服务的框架。业务逻辑写成[业务动作](concepts/actions.md)，HTTP 接口、网页、队列、定时任务、命令行、MCP 工具和 RPC 都是调用它的入口。框架直接使用社区组件：chi、Huma、slog、Bun、goose、Cobra，前端是 React、Mantine 和 TanStack Start。

文档按主题排列，每页一行。初学者的顺序：[安装](installation/installing.md) → [入门教程](tutorial/01-project.md) → [目录结构](concepts/structure.md)、[业务动作](concepts/actions.md)、[依赖装配](concepts/dependencies.md) → 用到哪一部分读哪一页。

## 入门

- [安装](installation/installing.md)、[脚手架](installation/scaffolding.md)：安装 Tenon，用 `tenon init` 生成项目
- 入门教程，做一个博客：[1 创建项目](tutorial/01-project.md)、[2 基础设施](tutorial/02-infrastructure.md)、[3 数据库](tutorial/03-database.md)、[4 文章接口](tutorial/04-api.md)、[5 页面](tutorial/05-pages.md)、[6 登录](tutorial/06-login.md)、[7 草稿](tutorial/07-drafts.md)、[8 后台](tutorial/08-admin.md)
- [开发工具](installation/tooling.md)：`tenon dev`、`generate`、`build`、`add`
- [AI 编程助手](installation/ai-assistants.md)：技能、文档和 MCP 服务

## 项目结构

- [目录结构](concepts/structure.md)：目录、命名、框架包一览
- [业务动作](concepts/actions.md)：一个用例一个类型，各入口调用它
- [依赖装配](concepts/dependencies.md)：在入口创建依赖，传给业务代码
- [生命周期](concepts/lifecycle.md)：启动、请求的处理、平滑停止、健康检查

## 基础

- [配置](general/configuration.md)、[错误处理](general/errors.md)、[本地化](general/localization.md)、[命令行](general/commands.md)
- [日志](general/logging.md)：请求 ID、处理链、脱敏、测试日志
- [可观测性](general/telemetry.md)：指标、链路、错误追踪
- [数据库](database/database.md)：连接、模型、查询、事务、软删除、只读副本
- [迁移](database/migrations.md)
- [测试](testing/testing.md)：`dbtest`、业务动作、HTTP 接口、命令、控制时间

## HTTP 接口

- [路由](http/routing.md)：注册接口、访问策略
- [请求与校验](http/requests.md)：输入类型、校验规则、校验错误
- [响应](http/responses.md)：JSON、分页、文件
- [中间件](http/middleware.md)：默认中间件、超时、限流、跨域、Basic 认证、App 版本、压缩
- [接口文档](http/openapi.md)：OpenAPI
- [客户端](http/clients.md)：生成 TypeScript、Go 和其他语言的客户端
- [服务端推送](http/sse.md)：SSE 事件流
- [客户端协议](http/protocol.md)：任何语言的客户端遵守的线路约定

## RPC

- [RPC 端点](rpc/endpoints.md)、[RPC 协议](rpc/protocol.md)：其他服务经由 NATS 调用业务动作

## 后台任务

- [队列](background/queue.md)、[失败任务与监控](background/queue-monitoring.md)、[定时任务](background/scheduling.md)

## 基础设施

- [缓存](libraries/cache.md)、[原子锁](libraries/locks.md)、[限流](libraries/rate-limiting.md)
- [NATS](libraries/nats.md)、[广播](libraries/broadcasting.md)、[实时频道](libraries/realtime.md)：WebSocket
- [文件存储](libraries/filesystem.md)、[文件上传](libraries/file-uploads.md)、[客户端直传](libraries/direct-uploads.md)
- [邮件](libraries/mail.md)、[邮件服务商](libraries/mail-providers.md)、[短信](libraries/sms.md)、[生成 PDF](libraries/pdf.md)
- [出站请求](libraries/outbound.md)：调用外部服务，防 SSRF

## 功能

- [通知](features/notifications.md)、[移动推送](features/push.md)、[Webhook](features/webhooks.md)
- [设置](features/settings.md)、[功能开关](features/flags.md)、[操作日志](features/activity.md)
- [后台操作](features/operations.md)：带进度的耗时操作
- [导入与导出](features/import-export.md)、[后台导入](features/background-imports.md)、[后台导出](features/background-exports.md)：Excel、CSV
- [全文搜索](features/search.md)
- [注销账号](features/privacy.md)、[导出个人数据](features/data-export.md)

## 认证与授权

- [认证](security/authentication.md)、[认证进阶](security/authentication-advanced.md)：令牌校验、保护路由、多套账号
- [登录与注册](security/login.md)、[登录的安全功能](security/login-security.md)：密码、验证码、二次验证、Passkey
- [API 令牌](security/api-tokens.md)：会话、个人访问令牌
- [授权](security/authorization.md)：角色、权限、资源所有权
- [哈希](security/hashing.md)、[加密与签名链接](security/encryption.md)
- [第三方登录](security/social-login.md)、[App 中的第三方登录](security/social-login-apps.md)、[企业单点登录](security/enterprise-sso.md)
- [OAuth 授权服务器](security/oauth-server.md)：第三方应用和自己的原生客户端登录
- [多租户](security/tenancy.md)
- 完整的例子：[单账号后台](security/admin-example.md)、[运营后台与多租户](security/saas-example.md)

## 前端

- [前端规范](frontend/conventions.md)、[前端包的分层](frontend/README.md)
- [搭建前端应用](frontend/app.md)、[布局与导航](frontend/layout.md)、[列表与表单](frontend/lists-forms.md)
- [在 React 中调用接口](frontend/client-react.md)：TanStack Query、权限、枚举
- [资源与设置页面](frontend/resources.md)：在 Go 中声明增删改查页面
- [后台功能](frontend/features.md)：通知、操作记录、导入导出、AI 对话的界面
- [官网与文档站](frontend/site.md)、[托管与服务端渲染](frontend/hosting.md)、[TanStack Start 的接入](frontend/start.md)
- [前端观测](frontend/observability.md)

## 客户端

- [客户端](clients/README.md)、[原生客户端](clients/native.md)、[网页宿主的约定](clients/host.md)
- [Electron 桌面端](clients/electron.md)、[打包、签名与更新](clients/electron-release.md)

## AI

- [AI 助手](ai/chat.md)、[AI 助手进阶](ai/chat-advanced.md)
- [MCP 服务](ai/mcp.md)、[文档转换与切分](ai/documents.md)、[向量检索](ai/vector.md)

## 部署与运维

- [部署](installation/deployment.md)、[自检](installation/doctor.md)、[运维](installation/operations.md)、[交给客户自托管](installation/self-hosting.md)
