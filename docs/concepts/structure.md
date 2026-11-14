# 目录结构

要用好 Tenon，先要了解一个应用是怎样组织的：代码放在哪里，哪些文件由框架生成，哪些由你编写。

Tenon 并不强制固定的目录结构，也不会根据目录的位置自动发现任何东西：路由、命令和队列任务都在代码里显式注册。所以，本页介绍的是一种**推荐**的结构。`tenon init` 生成的项目和[入门教程](../tutorial/README.md)都按它组织，你可以从它开始，再按团队的习惯调整。

- [默认目录](#application-layout)
    - [cmd](#the-cmd-directory)
    - [internal/bootstrap](#the-bootstrap-directory)
    - [业务包](#business-packages)
    - [migrations](#the-migrations-directory)
    - [locales](#the-locales-directory)
    - [frontend](#the-frontend-directory)
- [命名约定](#naming-conventions)
- [框架包一览](#framework-packages)
    - [基础](#foundation)
    - [基础设施](#infrastructure)
    - [入口](#entry)
    - [功能](#features)

<a id="application-layout"></a>
## 默认目录

一个完整的应用通常是这样的：

```text
blog/
├── go.mod
├── tenon.json             开发工具的配置，可以没有
├── package.json           前端依赖与 dev、build、generate 脚本
├── vite.config.ts
├── compose.yaml           本机开发用的 PostgreSQL、Redis 等
├── .env                   本机的环境变量，不提交
├── config/
│   ├── app.yaml           配置
│   └── local.yaml         只在本机开发时叠加的配置
├── cmd/api/main.go        程序入口
├── internal/
│   ├── bootstrap/         装配：配置结构、打开资源、HTTP 服务、命令
│   ├── auth/              令牌校验
│   ├── article/           业务包，每个业务动作一个文件
│   └── user/
├── migrations/            SQL 迁移和 embed.go
├── locales/               翻译文件和 embed.go
└── frontend/              单页应用的源码，以及嵌入构建产物的 frontend.go
```

不必一开始就建齐这些目录。一个刚起步的应用只需要 `cmd` 和一个业务包，其他目录等到真正有代码要放时再创建。下面依次介绍每个目录的作用。

<a id="the-cmd-directory"></a>
### cmd

`cmd` 存放应用的进程入口，每个子目录对应一个可执行文件，例如 `cmd/api` 构建出 API 服务。入口文件应当尽量短小：它只组装根命令并执行，具体做什么都交给 `bootstrap`，见[命令行](../general/commands.md#declaring-the-cli)。

<a id="the-bootstrap-directory"></a>
### internal/bootstrap

`bootstrap` 包负责把应用“组装”起来：定义配置结构，打开数据库等资源，创建 HTTP 服务，注册路由和命令。在 Tenon 中，这些都是普通的函数调用，没有任何隐藏的魔法，详见[依赖装配](dependencies.md)。它通常包含这几个文件：

| 文件 | 内容 |
| --- | --- |
| `config.go` | 应用的配置结构体 |
| `app.go` | `App`：各个进程部分共用的资源；`open` 打开它们 |
| `http.go` | 中间件、路由注册，`serve` 命令运行的 HTTP 服务 |
| `commands.go` | 需要数据库等资源的命令 |

队列 worker、定时任务调度器等其他运行部分也放在这里，每个一个文件。

<a id="business-packages"></a>
### 业务包

每个业务模块是 `internal` 下的一个包，包名用单数，例如 `article`。包里的代码按[业务动作](actions.md)组织：每个动作一个文件，包含它的依赖、业务规则和各个入口方法。一个业务包通常是这样的：

| 文件 | 内容 |
| --- | --- |
| `article.go` | 模型、业务错误、几个动作共用的查询与授权函数 |
| `create.go`、`publish.go`…… | 一个业务动作一个文件 |
| `routes.go` | 本模块的路由：路径、方法、访问策略 |
| `commands.go` | 本模块的命令 |

你会发现模块里没有一个包揽所有操作的 `Service`：动作多了，只是文件多了，每个文件仍然只关心一件事。当动作多到一个目录难以浏览时，按子领域拆成新的包，例如把评论拆到 `internal/comment`，而不是在包内再建子目录。

> [!NOTE]
> 服务端渲染的页面放在子包中，例如 `article/articleweb`。templ 组件要导入 `article` 的类型，放在同一个包里会循环导入。

<a id="the-migrations-directory"></a>
### migrations

`migrations` 存放 SQL 迁移文件。它们由 `embed.go` 嵌入程序，部署时用 `migrate up` 执行，详见[迁移](../database/migrations.md)。

<a id="the-locales-directory"></a>
### locales

如果应用需要多语言的提示文案，把 `en.yaml`、`zh-CN.yaml` 等翻译文件放在这里，详见[本地化](../general/localization.md)。

<a id="the-frontend-directory"></a>
### frontend

`frontend` 是单页应用的源码。`frontend.go` 把构建产物嵌入程序，所以部署时仍然只有一个二进制文件，见[单页应用](../responses/spa.md)。

> [!NOTE]
> `frontend/src/api` 中是生成的 TypeScript 客户端，每次修改接口后都会重新生成，请不要手动修改，见[客户端代码](../responses/clients.md)。

<a id="naming-conventions"></a>
## 命名约定

遵循统一的命名约定，团队成员和前端开发者就更容易猜到一样东西叫什么：

| 对象 | 约定 | 示例 |
| --- | --- | --- |
| 业务包 | 单数 | `article` |
| 动作文件 | 动作，snake_case | `create.go`、`publish_scheduled.go` |
| 动作类型 | 动词，业务对象由包名表达 | `article.Create`、`article.Publish` |
| 构造函数 | `New` + 类型名 | `NewCreate` |
| 业务方法 | `Handle` | `(*Publish).Handle` |
| 入口方法 | 入口名 | `(*Publish).HTTP`、`(*Publish).Job`、`(*Publish).Command` |
| 输入、输出 | 动作名 + `Input` / `Output` | `CreateInput` |
| 资源路径 | 复数 | `/articles`、`/articles/{id}` |
| JSON 字段 | snake_case | `author_id` |
| OperationID | `资源-动作` | `article-create` |
| 业务错误码 | `资源.原因` | `article.not_found` |
| 缩写 | 全大写 | `ID`、`HTTP`、`URL`、`TTL` |

<a id="framework-packages"></a>
## 框架包一览

Tenon 的每个包都可以单独使用，按需引入即可。顶层的包分为四层，每个包只能导入同层或更低层的包：

```text
功能        apitoken  login  oauthserver  authz  notification  push  activity  feature  settings  operation  webhook  search  vector  sheet/imports  sheet/exports  ai
入口        httpx  web  realtime  mcpx  command
基础设施    database  queue  schedule  cachex  lock  ratelimit  captcha  broadcast  filesystem  imgproxy  mail  sms  pdf  outbound  sheet  document
基础        errorx  identity  config  cleanup  logging  telemetry  i18n  page  form  hashing  crypt  acquire
```

这条规则由[架构测试](../contributions.md#architecture-tests)保证，新增的导入违反分层时测试会失败。名字以 `test` 结尾的子包（`dbtest`、`queuetest`、`mailtest` 等）是测试用的替身和工具。

<a id="foundation"></a>
### 基础

不依赖数据库、HTTP 和命令行，可以在任何地方使用。

| 包 | 用途 | 文档 |
| --- | --- | --- |
| `acquire` | 在 context 中传递等待共享资源（如数据库连接）的时限 | [连接用满时](../database/quick-start.md#pool-exhaustion) |
| `cleanup` | 按逆序关闭入口打开的资源 | [关闭资源](dependencies.md#closing-resources) |
| `config` | 读取 YAML、展开环境变量、校验 | [配置](../general/configuration.md) |
| `crypt` | 用应用密钥加密、签名链接 | [加密与签名链接](../security/encryption.md) |
| `errorx` | 带稳定错误码和分类的业务错误 | [错误处理](../general/errors.md) |
| `form` | 按 Huma 标签转换、验证文本字段，表单和表格导入共用 | [表单](../responses/forms.md) |
| `hashing` | 密码哈希 | [哈希](../security/hashing.md) |
| `i18n` | 组合 go-i18n 消息目录 | [本地化](../general/localization.md) |
| `identity` | 已认证的主体，在 context 中传递 | [认证](../security/authentication.md) |
| `logging` | 构造 slog 日志器，传递请求关联字段 | [日志](../general/logging.md) |
| `page` | 分页参数与响应结构 | [分页](../responses/responses.md#pagination) |
| `telemetry` | 用 OTLP 推送日志、指标和链路 | [可观测性](../general/telemetry.md) |

<a id="infrastructure"></a>
### 基础设施

与外部资源打交道。HTTP 处理器、队列任务和命令行都可以使用。

| 包 | 用途 | 文档 |
| --- | --- | --- |
| `database` | PostgreSQL 连接池、事务与提交后回调、约束错误 | [数据库](../database/quick-start.md) |
| `database/migrate` | goose 迁移器，新建、发布迁移 | [迁移](../database/migrations.md) |
| `queue` | NSQ 任务队列，数据库发件箱，重试与失败任务 | [队列](../libraries/queue.md) |
| `schedule` | 按 cron 表达式定时执行，多实例去重 | [定时任务](../libraries/scheduling.md) |
| `cachex` | Memcached 上的先读缓存再加载 | [缓存](../libraries/cache.md) |
| `lock` | 带过期时间的原子锁，`lock/redislock` 是 Redis 存储 | [原子锁](../libraries/locks.md) |
| `ratelimit` | 按键限流，`ratelimit/redislimit` 是 Redis 限流器 | [限流](../libraries/rate-limiting.md) |
| `broadcast` | 实例之间广播消息，`broadcast/redisbroadcast` 是 Redis 后端 | [广播](../libraries/broadcasting.md) |
| `filesystem` | `files` 表、上传、公开地址；`local`、`s3` 两种磁盘 | [文件存储](../libraries/filesystem.md) |
| `imgproxy` | 签名的 imgproxy 地址，按需缩放图片 | [图片缩略图](../libraries/file-uploads.md#thumbnails) |
| `mail` | 邮件；`smtp`、`resend`、`ses` 发送，`logmail` 用于开发 | [邮件](../libraries/mail.md) |
| `sms` | 短信；`aliyun`、`tencent` 发送，`logsms` 用于开发 | [短信](../libraries/sms.md) |
| `pdf` | 把 templ 页面生成为 PDF，`pdf/gotenberg` 生成 | [生成 PDF](../libraries/pdf.md) |
| `outbound` | 只能访问公网地址的 HTTP 客户端 | [出站请求](../libraries/outbound.md) |
| `captcha` | 服务端校验 Turnstile、hCaptcha | [人机验证](../security/login.md#captcha) |
| `sheet` | 读写 CSV 和 Excel，按结构体解码并验证每一行 | [导入与导出](../features/import-export.md) |
| `document` | `convert` 把文件转为 Markdown，`split` 切分 Markdown | [文档转换与切分](../ai/documents.md) |

<a id="entry"></a>
### 入口

请求进入应用的方式。

| 包 | 用途 | 文档 |
| --- | --- | --- |
| `httpx` | 中间件、平滑停机、就绪检查、单页应用 | [中间件](../http/middleware.md)、[请求生命周期](lifecycle.md) |
| `httpx/rest` | 基于 Huma 的路由注册、错误转换与 OpenAPI | [路由](../http/routing.md) |
| `httpx/rest/clientgen` | 从路由生成 TypeScript 与 Go 客户端 | [客户端代码](../responses/clients.md) |
| `web` | 服务端渲染的公开页面：组件、错误页、表单、静态文件 | [视图](../responses/views.md) |
| `web/ui`、`web/docs` | 界面组件；Markdown 文档页 | [界面组件](../responses/components.md)、[文档页](../responses/doc-pages.md) |
| `realtime` | 嵌入 Centrifuge 的 WebSocket 频道 | [实时频道](../libraries/realtime.md) |
| `mcpx` | 把动作作为 MCP 工具提供给 AI 客户端 | [MCP 服务](../ai/mcp.md) |
| `command` | 根命令、`Bind`、`Daemon`、退出码；`httpcmd`、`migratecmd` 等内置命令 | [命令行](../general/commands.md) |
| `queue/queueweb` | 队列面板 | [队列面板](../libraries/queue.md#panel) |
| `cmd/tenon` | 开发工具 `go tool tenon` | [开发工具](../installation/tooling.md) |

<a id="features"></a>
### 功能

自带数据表的应用功能，由应用把它们的路由和命令接进来。

| 包 | 用途 | 文档 |
| --- | --- | --- |
| `apitoken` | API 令牌：不透明令牌与 JWT | [API 令牌](../security/api-tokens.md) |
| `login` | 密码、验证码登录与注册，找回密码，二次验证；`oauth`、`passkey` 子包 | [登录与注册](../security/login.md) |
| `oauthserver` | OAuth 2.1 授权服务器，供 MCP 远程连接器使用 | [OAuth 授权服务器](../security/oauth-server.md) |
| `authz` | 角色与权限，按租户分配 | [授权](../security/authorization.md) |
| `notification` | 站内通知 | [通知](../features/notifications.md) |
| `push` | 移动推送，`push/aliyun` | [移动推送](../features/push.md) |
| `activity` | 操作日志 | [操作日志](../features/activity.md) |
| `feature` | 功能开关 | [功能开关](../features/flags.md) |
| `settings` | 运行时可修改的设置 | [设置](../features/settings.md) |
| `operation` | 后台操作的状态与进度 | [后台操作](../features/operations.md) |
| `sheet/imports`、`sheet/exports` | 后台导入与导出表格 | [导入与导出](../features/import-export.md) |
| `webhook` | 按 Standard Webhooks 推送事件 | [Webhook](../features/webhooks.md) |
| `search` | PostgreSQL 全文搜索 | [全文搜索](../features/search.md) |
| `vector` | Qdrant 向量检索 | [向量检索](../ai/vector.md) |
| `ai`、`ai/chat` | 模型能力接口；对话式 agent | [AI 助手](../ai/chat.md) |
