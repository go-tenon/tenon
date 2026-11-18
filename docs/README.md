# Tenon 文档

Tenon 是一个用 Go 构建后端服务的框架。业务逻辑写成[业务动作](concepts/actions.md)，再从不同的入口调用它：HTTP 接口、服务端页面、队列任务、定时任务、命令行和 MCP 工具。

其中 HTTP API 是目前最完整的入口。用户登录后使用的界面，是调用这些 API 的单页应用（React）或手机 App；官网、帮助中心这类公开页面，也可以由服务端直接生成 HTML。

一个后端应用要处理很多相同的问题：读取配置、定义路由、校验请求、返回错误、记录日志、连接数据库、管理登录。Tenon 为这些问题提供一套统一的写法，同时让你直接使用 Go 社区中成熟的组件，例如 chi（路由）、Huma（接口与 OpenAPI 文档）、slog（日志）、Bun（数据库）、goose（数据库迁移）和 Cobra（命令行）。

这份文档假设你会写 Go 的基本语法，但不要求你有后端框架的经验。用到的后端概念，例如中间件、事务、JWT，第一次出现时都会解释。

## 核心与入口

配置、错误、日志、数据库、缓存、认证与授权这些部分与入口无关，所有入口共用。入口只负责把外部的调用转换为对业务动作的调用，同一个动作可以从多个入口调用，见[从不同入口运行](concepts/actions.md#entry-points)。目前提供这些入口：

| 入口 | 用途 | 文档 |
| --- | --- | --- |
| HTTP 接口 | JSON API、OpenAPI 文档、生成的客户端 | [HTTP 接口](http/README.md) |
| 网页 | React + Mantine + TanStack Start，由 Go 托管的 Bun 进程渲染，调用同一组 HTTP 接口 | [前端](frontend/README.md) |
| 后台任务 | 队列 worker 与定时任务调度器，也可以单独部署为没有 HTTP 接口的服务 | [后台任务](background/README.md) |
| 命令行 | 运维和一次性的操作 | [命令行](general/commands.md) |
| MCP 工具 | 供 Claude、Cursor 等 AI 客户端调用 | [MCP 服务](ai/mcp.md) |
| 实时频道 | WebSocket，把消息推给在线的用户 | [实时频道](libraries/realtime.md) |
| RPC | 其他服务经由 NATS 同步调用，调用方可以用任何语言 | [RPC](rpc/README.md) |

单页应用和手机 App 是 HTTP 接口的调用方，见[前端](frontend/README.md)。

## 怎样阅读

文档按学习顺序排列，建议从上往下读：

1. **入门**：安装 Tenon，跟着教程做出一个能登录、能发布文章的博客。做完教程，你已经接触了大部分常用功能。
2. **基础**：分别深入讲解教程中用到的每一部分。写自己的项目时，遇到哪一部分就读哪一页。
3. 之后的章节按需阅读：需要缓存、队列、文件上传等功能时读“常用功能”，需要登录和权限时读“安全”，以此类推。

每一页的开头会写出需要先读哪些页面。遇到不熟悉的 Go 写法，例如 `context`、错误的包装、结构体标签，可以先看[Go 知识准备](concepts/go-primer.md)。

## 第一阶段：入门

- [安装](installation/installing.md)：安装 Go 和 Tenon，写出第一个 API
- [脚手架](installation/scaffolding.md)：用 `tenon init` 生成一个完整的项目
- [入门教程](tutorial/README.md)：从零做出一个博客
    1. [创建项目](tutorial/01-project.md)
    2. [基础设施与装配](tutorial/02-infrastructure.md)
    3. [数据库与迁移](tutorial/03-database.md)
    4. [文章接口](tutorial/04-api.md)
    5. [文章页面](tutorial/05-pages.md)
    6. [作者登录](tutorial/06-login.md)
    7. [草稿与发布](tutorial/07-drafts.md)
    8. [后台：作者与角色](tutorial/08-admin.md)

## 第二阶段：基础

先了解一个 Tenon 项目由哪些部分组成，运行时经历哪些阶段：

- [Go 知识准备](concepts/go-primer.md)：文档中常见、入门教程不一定讲到的 Go 写法
- [目录结构](concepts/structure.md)：项目中的目录各放什么
- [应用生命周期](concepts/lifecycle.md)：启动、运行的部分、一次调用的 context、平滑停止与健康检查

然后逐一深入。先是每个应用都会用到、与入口无关的部分：

- [配置](general/configuration.md)：配置文件、环境变量和多套环境
- [错误处理](general/errors.md)：定义业务错误，返回给客户端合适的状态码和提示
- [日志](general/logging.md)：记录日志，在日志中找到一次请求的全部记录
- [本地化](general/localization.md)：中英文等多种语言的提示
- [数据库](database/quick-start.md)：连接数据库，定义模型，查询和事务
- [迁移](database/migrations.md)：用迁移文件管理表结构的变化
- [命令行](general/commands.md)：`serve`、`migrate` 以及你自己的命令

再是 HTTP 接口：

- [HTTP 服务的生命周期](http/lifecycle.md)：一个请求从进入到返回经过哪些环节，排空与就绪检查
- [路由](http/routing.md)：把一个地址交给一个处理函数
- [接口文档](http/openapi.md)：自动生成的 OpenAPI 文档，说明与示例，API 版本
- [请求](http/requests.md)：读取路径参数、查询参数、请求头和请求体
- [验证](http/validation.md)：在处理请求之前检查输入
- [响应](http/responses.md)：返回 JSON、状态码、分页和文件
- [中间件](http/middleware.md)：在每个请求前后做的通用工作，例如请求 ID、超时
- [可选的中间件](http/optional-middleware.md)：限流、跨域、Basic 认证、App 最低版本、响应压缩

最后是测试：

- [测试](testing/README.md)：测试业务代码、数据库和接口
    - [快速入门](testing/overview.md)
    - [数据库与业务动作](testing/database.md)
    - [HTTP 与命令](testing/http.md)

## 第三阶段：组织代码

项目变大以后，代码怎样分层、依赖怎样传递，接口怎样交给前端使用：

- [依赖装配](concepts/dependencies.md)：在入口创建数据库连接等依赖，再传给业务代码
- [业务动作](concepts/actions.md)：一个用例写成一个类型，HTTP、命令行和队列都能调用它
- [客户端协议](http/protocol.md)：前端和 App 调用接口时遵守的约定
- [客户端代码](http/clients.md)：由接口生成 TypeScript 客户端
- [在 React 中调用接口](frontend/client-react.md)：TanStack Query、校验规则、权限、枚举与分页
- [开发工具](installation/tooling.md)：`tenon dev`、`tenon generate`、`tenon build`

## 第四阶段：前端与页面

网页用 React、Mantine 和 TanStack Start 编写，由 Go 托管，调用 HTTP 接口：

- [搭建前端应用](frontend/app.md)：创建应用、样式预设、登录、权限、错误页和新版本提示
- [布局与导航](frontend/layout.md)：`FrameLayout` 和 `RailLayout` 两个布局、侧栏菜单、顶栏、多标签和租户
- [列表与表单](frontend/lists-forms.md)：地址中的列表状态、表格、按生成的规则校验的表单
- [资源与设置页面](frontend/resources.md)：在 Go 中声明增删改查和设置的页面，界面按声明渲染
- [后台功能](frontend/features.md)：通知、后台操作、操作记录、导入导出、队列面板和 AI 对话
- [官网与文档站](frontend/site.md)：服务端渲染与预渲染、官网的布局、首页的占位和文档页
- [托管前端与服务端渲染](frontend/hosting.md)：前端嵌入 Go 程序，Go 是唯一的入口；只在需要服务端渲染时才带 Bun
- [TanStack Start 的接入](frontend/start.md)：服务端渲染与浏览器调用同一组接口、预渲染和编译
- [前端规范](frontend/conventions.md)：前端用什么技术、目录怎样组织、代码怎样写
- [服务端推送](http/sse.md)：服务端主动把进度、通知推给浏览器

网页以外的客户端：

- [客户端](clients/README.md)：桌面端、App 有哪几类，放在项目的哪里，怎样生成和运行
- [原生客户端](clients/native.md)：安装在设备上的客户端在系统浏览器中登录、保存 token、上报版本
- [Electron 桌面端](clients/electron.md)：把前端做成桌面端，托盘、通知、开机自启和深度链接
- [打包、签名与更新](clients/electron-release.md)：为三个平台打包、签名，自动更新和强制升级
- [网页宿主的约定](clients/host.md)：宿主要做到的事和页面能调用的 `tenonHost`

## 第五阶段：常用功能

后台任务：

- [队列](background/queue.md)：把耗时的工作放到后台执行
- [队列：失败任务与监控](background/queue-monitoring.md)：失败任务的重试、队列的状态与运维接口
- [定时任务](background/scheduling.md)：每天、每小时执行的工作

存储与通信：

- [缓存](libraries/cache.md)：把查询结果暂存起来，减少数据库的压力
- [NATS](libraries/nats.md)：连接 NATS，广播和在线成员使用它
- [原子锁](libraries/locks.md)：同一时间只让一个进程做某件事
- [限流](libraries/rate-limiting.md)：限制一段时间内的请求次数
- [广播](libraries/broadcasting.md)：在多个进程之间传递消息
- [实时频道](libraries/realtime.md)：把消息推给在线的用户
- [文件存储](libraries/filesystem.md)：本地磁盘和对象存储
- [文件上传](libraries/file-uploads.md)：上传接口和常见用法
- [客户端直传](libraries/direct-uploads.md)：大文件直接上传到 S3
- [邮件](libraries/mail.md)、[邮件服务商](libraries/mail-providers.md)、[短信](libraries/sms.md)、[生成 PDF](libraries/pdf.md)
- [出站请求](libraries/outbound.md)：安全地调用其他服务的接口

应用中常见的功能：

- [通知](features/notifications.md)：站内通知、邮件和其他渠道
- [移动推送](features/push.md)：推送到手机
- [设置](features/settings.md)：应用和用户的设置
- [功能开关](features/flags.md)：按用户逐步开放新功能
- [操作日志](features/activity.md)：记录谁在什么时候做了什么
- [后台操作](features/operations.md)：导入、批量处理这类耗时的操作，带进度
- [导入与导出](features/import-export.md)：Excel 和 CSV
- [后台导入](features/background-imports.md)、[后台导出](features/background-exports.md)：大文件在后台处理
- [Webhook](features/webhooks.md)：把事件通知给其他系统
- [全文搜索](features/search.md)：按关键词搜索
- [注销账号](features/privacy.md)：冷静期后删除用户的个人数据
- [导出个人数据](features/data-export.md)：用户下载自己的全部数据

## 第六阶段：安全

建议按下面的顺序阅读：先理解认证，再学习登录和授权，最后是更复杂的场景。

- [认证](security/authentication.md)：怎样知道“这个请求是谁发的”
- [登录与注册](security/login.md)：邮箱和密码、验证码、找回密码
- [登录的安全功能](security/login-security.md)：二次验证、Passkey、人机验证
- [API 令牌](security/api-tokens.md)：给程序和第三方使用的令牌
- [授权](security/authorization.md)：角色与权限，“这个人能不能做这件事”
- [哈希](security/hashing.md)：安全地保存密码
- [加密与签名链接](security/encryption.md)：保护敏感数据和链接
- [单账号后台](security/admin-example.md)：一个完整的例子
- [第三方登录](security/social-login.md)：用微信、GitHub 等账号登录
- [在 App 中使用第三方登录](security/social-login-apps.md)：App 登录与 Sign in with Apple
- [企业单点登录](security/enterprise-sso.md)：用企业自己的账号系统登录
- [OAuth 授权服务器](security/oauth-server.md)：让第三方应用代表用户调用你的接口，也负责自己的桌面端、App 在浏览器中登录
- [认证进阶](security/authentication-advanced.md)：多套账号、多租户与代为登录
- [多租户](security/tenancy.md)：一套系统同时服务多个客户
- [运营后台与多租户](security/saas-example.md)：一个完整的例子

## 第七阶段：进阶

- AI：
    - [AI 助手](ai/chat.md)
    - [AI 助手进阶](ai/chat-advanced.md)
    - [MCP 服务](ai/mcp.md)
    - [文档转换与切分](ai/documents.md)
    - [向量检索](ai/vector.md)
- [可观测性](general/telemetry.md)：指标、链路和日志推送到观测平台
- [日志进阶](general/logging-advanced.md)：处理链、多个输出、脱敏与测试日志
- [前端观测](general/frontend-observability.md)：浏览器中的错误、页面和链路
- [数据库进阶](database/advanced.md)：慢查询、软删除、隔离级别、只读副本、多个数据库
- [Go 客户端](http/go-client.md)：其他 Go 服务和工具调用你的 API
- [提供和调用端点](rpc/endpoints.md)：其他服务经由 NATS 调用你的业务动作
- [RPC 协议](rpc/protocol.md)：其他服务经由 NATS 调用业务动作的线路约定
- [部署](installation/deployment.md)：构建、配置和上线
- [自检](installation/doctor.md)：检查运行环境，给出修改方法
- [交给客户自托管](installation/self-hosting.md)：交给客户用 Docker Compose 运行
- [运维](installation/operations.md)：不停机升级、维护窗口、备份与恢复

文档与同一版本的代码对应。框架依赖的精确版本以仓库中的 [go.mod](../go.mod) 为准。
