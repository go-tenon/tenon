# Tenon 文档

Tenon 是用 Go 构建后端服务的框架。业务逻辑写成[业务动作](concepts/actions.md)，HTTP 接口、网页、队列、定时任务、命令行、MCP 工具和 RPC 都是调用它的入口。框架直接使用社区组件：chi、Huma、slog、Bun、goose、Cobra，前端是 React、Mantine 和 TanStack Start。

文档按主题排列，每页一行。初学者的顺序：[安装](installation/installing.md) → [入门教程](tutorial/01-project.md) → [目录结构](concepts/structure.md)、[业务动作](concepts/actions.md)、[依赖装配](concepts/dependencies.md) → 用到哪一部分读哪一页。

## 入门

- [安装与生成项目](installation/installing.md)：`tenon init`、项目里有什么、构建、CI
- 入门教程，做一个博客：[1 创建项目](tutorial/01-project.md)、[2 基础设施](tutorial/02-infrastructure.md)、[3 数据库](tutorial/03-database.md)、[4 文章接口](tutorial/04-api.md)、[5 页面](tutorial/05-pages.md)、[6 登录](tutorial/06-login.md)、[7 草稿](tutorial/07-drafts.md)、[8 后台](tutorial/08-admin.md)
- [开发工具](installation/tooling.md)：`tenon dev`、`generate`、`build`、`add`、`deploy`
- [AI 编程助手](installation/ai-assistants.md)：AGENTS.md、技能、`tenon/docs`、`tenon mcp`

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

- [队列](background/queue.md)：发布与处理任务、失败重试、失败任务、运维接口
- [定时任务](background/scheduling.md)

## 基础设施

- [缓存](libraries/cache.md)、[原子锁](libraries/locks.md)、[限流](libraries/rate-limiting.md)
- [NATS](libraries/nats.md)、[广播](libraries/broadcasting.md)、[实时频道](libraries/realtime.md)：WebSocket
- [文件存储与上传](libraries/filesystem.md)：本地磁盘、S3、上传接口、客户端直传、缩略图
- [邮件](libraries/mail.md)、[短信](libraries/sms.md)、[生成 PDF](libraries/pdf.md)
- [出站请求](libraries/outbound.md)：调用外部服务，防 SSRF

## 功能

- [通知](features/notifications.md)、[移动推送](features/push.md)、[Webhook](features/webhooks.md)
- [设置](features/settings.md)、[功能开关](features/flags.md)、[已保存视图](features/saved-views.md)、[操作日志](features/activity.md)
- [后台操作](features/operations.md)：带进度的耗时操作
- [导入与导出](features/import-export.md)：Excel、CSV，大文件在后台导入导出
- [全文搜索](features/search.md)
- [注销账号与导出个人数据](features/privacy.md)

## 认证与授权

- [认证](security/authentication.md)：令牌校验、保护路由、多套账号、代为登录
- [登录与注册](security/login.md)：密码、验证码、找回密码、二次验证、Passkey、人机验证
- [API 令牌](security/api-tokens.md)：会话 cookie、个人访问令牌、撤销
- [授权](security/authorization.md)：角色、权限、资源所有权
- [哈希、加密与签名链接](security/encryption.md)
- [第三方登录](security/social-login.md)：网页、App、Sign in with Apple
- [企业单点登录](security/enterprise-sso.md)
- [OAuth 授权服务器](security/oauth-server.md)：第三方应用和自己的原生客户端登录
- [多租户](security/tenancy.md)：租户隔离、运营后台与租户后台

## 前端

- [前端规范](frontend/conventions.md)：技术栈、目录、`@go-tenon/ui` 的分层、颜色
- [搭建前端应用](frontend/app.md)：装配、主题、登录、权限、错误页
- [布局与导航](frontend/layout.md)：侧栏菜单、多标签、租户
- [列表与表单](frontend/lists-forms.md)、[在 React 中调用接口](frontend/client-react.md)
- [界面文字](frontend/i18n.md)：翻译文件、当前语言、组件的文字、检查写死的文字
- [资源与设置页面](frontend/resources.md)：在 Go 中声明增删改查页面
- [后台功能的前端](frontend/features.md)：通知、后台操作、导入导出、队列面板、AI 对话
- [官网与文档站](frontend/site.md)、[托管前端与服务端渲染](frontend/hosting.md)
- [前端观测](frontend/observability.md)

## 客户端

- [客户端](clients/overview.md)：种类、目录、命令
- [原生客户端](clients/native.md)、[网页宿主的约定](clients/host.md)
- [Electron 桌面端](clients/electron.md)：桌面能力、打包、签名、自动更新

## AI

- [AI 对话](ai/chat.md)：模型、对话接口、审批、多实例、媒体与决策
- [MCP 服务](ai/mcp.md)：把业务动作提供给 AI 客户端
- [文档转换与切分](ai/documents.md)、[向量检索](ai/vector.md)

## 部署与运维

- [部署](installation/deployment.md)、[自检](installation/doctor.md)、[运维](installation/operations.md)、[交给客户自托管](installation/self-hosting.md)
