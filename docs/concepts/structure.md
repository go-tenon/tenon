# 目录结构

这一页介绍一个 Tenon 应用的代码放在哪里：每个目录放什么，哪些文件由工具生成、哪些由你编写，以及框架的各个包分别做什么。读完之后，你在其他文档中看到一段代码，就知道它应该放在项目的哪个位置。

Tenon 不会按目录自动发现代码：路由、命令和队列任务都在代码中明确地注册，所以目录怎样组织由你决定。这一页介绍的是一种**推荐**的结构，`tenon init` 生成的项目和[入门教程](../tutorial/README.md)都按它组织。你可以从它开始，再按团队的习惯调整。

阅读前需要了解：[脚手架](../installation/scaffolding.md)。

- [从小项目开始](#starting-small)
- [默认目录](#application-layout)
    - [cmd](#the-cmd-directory)
    - [internal/bootstrap](#the-bootstrap-directory)
    - [业务包](#business-packages)
    - [migrations](#the-migrations-directory)
    - [locales](#the-locales-directory)
    - [apps 和 packages](#the-frontend-directory)
- [新增一个业务模块](#adding-a-module)
- [命名约定](#naming-conventions)
- [常见问题](#troubleshooting)
- [框架包一览](#framework-packages)
    - [基础](#foundation)
    - [基础设施](#infrastructure)
    - [入口](#entry)
    - [功能](#features)

<a id="starting-small"></a>
## 从小项目开始

`tenon init` 生成的项目只有几个目录：

```text
todo/
├── config/app.yaml        配置
├── cmd/todo/main.go       程序的入口
├── internal/todo/         一个业务模块
├── apps/web/              前端
└── packages/client/       生成的 TypeScript 客户端
```

刚开始时，所有的装配代码都在 `cmd/todo/main.go` 中。随着应用变大，会依次出现新的目录：

| 什么时候 | 新增 | 见 |
| --- | --- | --- |
| 接上数据库 | `compose.yaml`、`migrations/`，装配代码从 `main.go` 移到 `internal/bootstrap/` | [教程第 2、3 章](../tutorial/02-infrastructure.md) |
| 写业务错误的提示 | `locales/` | [教程第 4 章](../tutorial/04-api.md#messages) |
| 加上登录 | `internal/auth/`、`internal/user/`，以及保存密钥的 `.env` | [教程第 6 章](../tutorial/06-login.md) |
| 加上后台 | `apps/admin/` | [教程第 8 章](../tutorial/08-admin.md) |

不需要一开始就建好所有目录，等到真正有代码要放时再创建。

<a id="application-layout"></a>
## 默认目录

一个功能完整的应用通常是这样的：

```text
blog/
├── go.mod
├── tenon.json             开发工具的配置，可以没有
├── package.json           Bun workspace 与 dev、build、generate 脚本
├── AGENTS.md              项目说明和代码约定，AI 编程助手读它，见“AI 编程助手”一页
├── compose.yaml           本机开发用的 PostgreSQL、NATS 等
├── .env                   本机的环境变量，不提交
├── config/
│   ├── app.yaml           配置
│   └── local.yaml         只在本机开发时叠加的配置，可以没有
├── cmd/blog/main.go       程序入口
├── internal/
│   ├── bootstrap/         装配：配置结构、打开资源、运行的部分、命令
│   ├── auth/              令牌校验
│   ├── article/           业务包，每个业务动作一个文件
│   └── user/
├── migrations/            SQL 迁移和 embed.go
├── locales/               翻译文件和 embed.go
├── apps/                  前端，每个一个目录，例如 web、admin
├── packages/client/       生成的 TypeScript 客户端
└── .tenon/                tenon 生成的内容，不提交
```

为什么业务代码放在 `internal` 下？这是 Go 的规则：`internal` 目录中的包只能被同一个 module 中的代码导入，其他项目无法导入它们。应用的代码本来就不是给别人用的库，放在 `internal` 下，以后修改时不用担心影响别人。

下面依次介绍每个目录的作用。

<a id="the-cmd-directory"></a>
### cmd

`cmd` 存放应用的入口，目录名就是程序的名字：`cmd/blog` 构建出 `bin/blog`，部署时运行 `./bin/blog serve`。`tenon init` 用项目的名字创建这个目录，所以部署后的进程名、链路中的服务名（OpenTelemetry 的 `service.name` 默认取程序的文件名）都是项目自己的名字，同一台机器上的几个项目不会混在一起。入口文件应当尽量短小：它只组装根命令并执行，具体做什么都交给 `bootstrap`，见[命令行](../general/commands.md#declaring-the-cli)。

<a id="the-bootstrap-directory"></a>
### internal/bootstrap

`bootstrap` 包负责把应用“组装”起来：定义配置结构，打开数据库等资源，创建 HTTP 服务，注册路由和命令。在 Tenon 中，这些都是普通的函数调用，没有任何隐藏的魔法，详见[依赖装配](dependencies.md)。它通常包含这几个文件：

| 文件 | 内容 |
| --- | --- |
| `config.go` | 应用的配置结构体 |
| `app.go` | `App`：各个进程部分共用的资源；`Open` 打开它们，`Parts` 列出常驻进程的运行部分 |
| `http.go` | 中间件、路由注册，HTTP 服务这个部分 |
| `commands.go` | 需要数据库等资源的命令 |

RPC 服务、队列 worker、定时任务调度器等其他运行部分也放在这里，每个一个文件，例如 `rpc.go`、`background.go`。每个部分只调用各业务包的登记函数（见下一节），不关心业务包里有哪些动作。

<a id="business-packages"></a>
### 业务包

每个业务模块是 `internal` 下的一个包，包名用单数，例如 `article`。包里的代码按[业务动作](actions.md)组织：每个动作一个文件，包含它的依赖、业务规则和各个入口方法。一个业务包通常是这样的：

| 文件 | 内容 |
| --- | --- |
| `article.go` | 模型、业务错误、几个动作共用的查询与授权函数 |
| `create.go`、`publish.go`…… | 一个业务动作一个文件 |
| `routes.go` | `RegisterRoutes`：本模块的 HTTP 路由，路径、方法、访问策略 |
| `rpc.go` | `RegisterRPC`：本模块的 [RPC 端点](../rpc/endpoints.md) |
| `jobs.go` | `RegisterJobs`：本模块的[队列任务](actions.md#as-jobs)的处理函数 |
| `tasks.go` | `RegisterTasks`：本模块的[定时任务](actions.md#as-scheduled-tasks) |
| `commands.go` | `Commands`：本模块的命令 |

模块用到哪几种入口，就有哪几个文件。`bootstrap` 中的各个部分调用这些登记函数：HTTP 服务调用 `RegisterRoutes`，RPC 服务调用 `RegisterRPC`，worker 调用 `RegisterJobs`，调度器调用 `RegisterTasks`。模块不知道自己运行在哪个进程中：`serve` 运行全部部分，`serve worker` 只运行 worker，同一个 `RegisterJobs` 在两种进程中都会被调用。

你会发现模块里没有一个包揽所有操作的 `Service`：动作多了，只是文件多了，每个文件仍然只关心一件事。当动作多到一个目录难以浏览时，按子领域拆成新的包，例如把评论拆到 `internal/comment`。

<a id="the-migrations-directory"></a>
### migrations

`migrations` 存放 SQL 迁移文件。它们由 `embed.go` 嵌入程序，部署时用 `migrate up` 执行，详见[迁移](../database/migrations.md)。

<a id="the-locales-directory"></a>
### locales

如果应用需要多语言的提示文案，把 `en.yaml`、`zh-CN.yaml` 等翻译文件放在这里，详见[本地化](../general/localization.md)。

<a id="the-frontend-directory"></a>
### apps 和 packages

`apps` 中每个目录是一个前端，例如用户端 `apps/web`、运营后台 `apps/admin`，目录名是前端的名称。`tenon build` 把每个前端构建好，放进 `apps/dist`，`apps/apps.go` 把它嵌入 Go 程序，由 Go 托管运行，所以部署时仍然只有一个二进制文件；只有前端需要服务端渲染时，程序中才带 Bun，见[托管前端与服务端渲染](../frontend/hosting.md)。用 [tenon add frontend](../installation/tooling.md#add-frontend) 加入新的前端。

`packages` 中是多个前端共用的包。`packages/client` 是共用的客户端包，前端用 `@<作用域>/client/<名称>` 导入它，见[前端与 API](../installation/tooling.md#conventions)。

> [!NOTE]
> `packages/client/src` 中每个 API 一个目录，是生成的 TypeScript 客户端，每次修改接口后都会重新生成，见[客户端代码](../http/clients.md)。

<a id="adding-a-module"></a>
## 新增一个业务模块

以给博客加上评论为例，看看一个新模块会涉及哪些目录：

1. **表结构**：`go run ./cmd/blog migrate make create_comments_table`，在 `migrations/` 中写建表语句。
2. **业务代码**：新建 `internal/comment/`。`comment.go` 放模型和业务错误，`create.go`、`delete.go` 等每个动作一个文件，见[业务动作](actions.md)。
3. **路由**：在 `internal/comment/routes.go` 中写 `RegisterRoutes` 函数，注册本模块的接口。
4. **装配**：在 `internal/bootstrap/http.go` 的路由中调用 `comment.RegisterRoutes`，把需要的依赖（例如数据库）传进去，见[依赖装配](dependencies.md)。
5. **错误提示**：在 `locales/zh-CN.yaml` 中加上新错误码的提示。
6. **前端**：开发环境重新生成客户端后，在 `apps/web/src/routes/` 中写页面，用 `api.comment.create()` 调用接口。

除了第 4 步要改 `bootstrap`，其他改动都在新模块自己的目录中。这就是按业务分包的好处：一个模块的代码集中在一起，删除这个功能时，删掉这个目录和 `bootstrap` 中的一行即可。

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
| 模块的登记函数 | `Register` + 入口 | `RegisterRoutes`、`RegisterRPC`、`RegisterJobs`、`RegisterTasks` |
| 运行的部分 | 小写的入口名 | `http`、`rpc`、`worker`、`scheduler` |
| 输入、输出 | 动作名 + `Input` / `Output` | `CreateInput` |
| 资源路径 | 复数 | `/articles`、`/articles/{id}` |
| JSON 字段 | snake_case | `author_id` |
| OperationID | `资源-动作` | `article-create` |
| 业务错误码 | `资源.原因` | `article.not_found` |
| 缩写 | 全大写 | `ID`、`HTTP`、`URL`、`TTL` |

<a id="package-suffix"></a>
包名与调用方通常要在同一个文件中同时导入的包同名时，加后缀 `x`；其他时候用本来的名字。两个同名的包出现在同一个文件中时，必须给其中一个起别名，读代码的人还要分辨哪个是哪个，后缀省去了这一步。框架中带后缀的包都是这种情况：

- `errorx`：调用方同时用 `errors.Is`、`errors.As`；
- `httpx`：调用方同时用 `net/http`；
- `mcpx`：调用方同时用官方 SDK 的 `mcp`；
- `natsx`：调用方同时用 `nats.go` 的 `nats`。

同名的包只在包的内部用到时不加后缀，在包内给它起别名即可。例如 `mail` 在内部导入 `net/mail`，`filesystem/s3` 在内部导入 AWS SDK 的 `s3`，调用方只导入框架的包，所以它们仍叫 `mail`、`s3`。

<a id="troubleshooting"></a>
## 常见问题

**编译报错 `import cycle not allowed`**

两个包互相导入了对方。常见的情况是：`article` 导入了 `user`，`user` 又导入了 `article`。把两边都要用的部分移到其中一个包中，或者移到一个新的、更底层的包中；也可以让一方通过接口或函数参数拿到它需要的东西，不再直接导入对方，见[依赖装配](dependencies.md)。

**其他项目导入不了我的包，报错 `use of internal package ... not allowed`**

`internal` 下的包只能在本 module 中导入。确实要共享的代码，移到 `internal` 之外的目录。

**新加的业务模块的接口没有出现在接口文档中**

还没有在 `internal/bootstrap/http.go` 中调用它的 `RegisterRoutes`。Tenon 不会自动发现包，每个模块都要在装配时注册。

<a id="framework-packages"></a>
## 框架包一览

这一节是参考资料，不需要一次读完，用到某个功能时再回来查。

Tenon 的每个包都可以单独使用，按需引入即可。顶层的包分为四层，每个包只能导入同层或更低层的包。例如“基础”层的 `errorx` 不会导入“基础设施”层的 `database`，所以你在任何地方使用 `errorx` 都不会意外地引入数据库：

```text
功能        apitoken  login  oauthserver  authz  notification  push  activity  feature  settings  operation  privacy  privacy/export  webhook  search  vector  sheet/imports  sheet/exports  ai
入口        httpx  web  realtime  mcpx  natsrpc  command  clientgen
基础设施    database  natsx  queue  schedule  cache  lock  ratelimit  captcha  broadcast  filesystem  imgproxy  mail  sms  pdf  outbound  sheet  document  contract  form
基础        errorx  identity  config  cleanup  logging  telemetry  health  doctor  i18n  enum  page  hashing  crypt  acquire
```

这条规则由架构测试 `internal/checks/archtest` 保证，新增的导入违反分层时测试会失败。名字以 `test` 结尾的子包（`dbtest`、`queuetest`、`mailtest` 等）是测试用的替身和工具。

<a id="foundation"></a>
### 基础

位于最底层，只导入同层的包，可以在任何地方使用。

| 包 | 用途 | 文档 |
| --- | --- | --- |
| `acquire` | 在 context 中传递等待共享资源（如数据库连接）的时限 | [连接用满时](../database/quick-start.md#pool-exhaustion) |
| `cleanup` | 按逆序关闭入口打开的资源 | [关闭资源](dependencies.md#closing-resources) |
| `config` | 读取 YAML、展开环境变量、校验 | [配置](../general/configuration.md) |
| `crypt` | 用应用密钥加密、签名链接 | [加密与签名链接](../security/encryption.md) |
| `enum` | 带显示文字的枚举：取值、校验和按语言翻译的显示文字 | [带显示文字的枚举](../http/validation.md#enums) |
| `errorx` | 带稳定错误码和分类的业务错误 | [错误处理](../general/errors.md) |
| `hashing` | 密码哈希 | [哈希](../security/hashing.md) |
| `health` | 检查依赖是否可用，供就绪检查使用 | [健康检查](../http/lifecycle.md#health-checks) |
| `doctor` | 检查运行环境的版本、设置和权限，给出修改方法；`doctorcmd` 是 `doctor` 命令 | [自检](../installation/doctor.md) |
| `i18n` | 组合 go-i18n 消息目录 | [本地化](../general/localization.md) |
| `identity` | 已认证的主体，在 context 中传递 | [认证](../security/authentication.md) |
| `logging` | 构造 slog 日志器，传递请求关联字段 | [日志](../general/logging.md) |
| `page` | 分页参数与响应结构 | [分页](../http/responses.md#pagination) |
| `telemetry` | 用 OTLP 推送日志、指标和链路 | [可观测性](../general/telemetry.md) |

<a id="infrastructure"></a>
### 基础设施

与入口无关的能力：数据库、队列、文件这类外部资源，以及各入口共用的契约和格式。HTTP 处理器、队列任务和命令行都可以使用。

| 包 | 用途 | 文档 |
| --- | --- | --- |
| `database` | PostgreSQL 连接池、事务与提交后回调、约束错误 | [数据库](../database/quick-start.md) |
| `database/migrate` | goose 迁移器，新建、发布迁移 | [迁移](../database/migrations.md) |
| `natsx` | 按框架的约定连接 NATS，队列、广播和在线成员共用这条连接 | [NATS](../libraries/nats.md) |
| `queue` | NATS JetStream 任务队列，数据库发件箱，重试与失败任务 | [队列](../background/queue.md) |
| `schedule` | 按 cron 表达式定时执行，多实例去重 | [定时任务](../background/scheduling.md) |
| `cache` | Memcached 上的先读缓存再加载 | [缓存](../libraries/cache.md) |
| `lock` | 带过期时间的原子锁，`lock/pglock` 是 PostgreSQL 存储 | [原子锁](../libraries/locks.md) |
| `ratelimit` | 按键限流，`ratelimit/pglimit` 是 PostgreSQL 限流器 | [限流](../libraries/rate-limiting.md) |
| `broadcast` | 实例之间广播消息，`broadcast/natsbroadcast` 是 NATS 后端 | [广播](../libraries/broadcasting.md) |
| `filesystem` | `files` 表、上传、公开地址；`local`、`s3` 两种磁盘 | [文件存储](../libraries/filesystem.md) |
| `imgproxy` | 签名的 imgproxy 地址，按需缩放图片 | [图片缩略图](../libraries/file-uploads.md#thumbnails) |
| `mail` | 邮件；`smtp`、`resend`、`ses` 发送，`logmail` 用于开发 | [邮件](../libraries/mail.md) |
| `sms` | 短信；`aliyun`、`tencent` 发送，`logsms` 用于开发 | [短信](../libraries/sms.md) |
| `pdf` | 把前端打印模板渲染的 HTML 生成为 PDF，`pdf/gotenberg` 生成 | [生成 PDF](../libraries/pdf.md) |
| `outbound` | 只能访问公网地址的 HTTP 客户端 | [出站请求](../libraries/outbound.md) |
| `captcha` | 服务端校验 Turnstile、hCaptcha | [人机验证](../security/login-security.md#captcha) |
| `sheet` | 读写 CSV 和 Excel，按结构体解码并验证每一行 | [导入与导出](../features/import-export.md) |
| `document` | `convert` 把文件转为 Markdown，`split` 切分 Markdown | [文档转换与切分](../ai/documents.md) |
| `contract` | 接口契约中与入口无关的部分：按 Huma 标签生成 JSON Schema 并校验、枚举的命名类型、验证错误的规则和译文，HTTP、RPC、MCP 共用 | [验证](../http/validation.md) |
| `form` | 按 Huma 标签转换、验证文本字段，表格导入使用 | [导入与导出](../features/import-export.md) |

<a id="entry"></a>
### 入口

请求进入应用的方式。

| 包 | 用途 | 文档 |
| --- | --- | --- |
| `httpx` | 中间件、平滑停机、HTTP 就绪检查、会话 cookie | [中间件](../http/middleware.md)、[HTTP 服务的生命周期](../http/lifecycle.md) |
| `httpx/rest` | 基于 Huma 的路由注册、错误转换与 OpenAPI | [路由](../http/routing.md) |
| `clientgen` | 从路由生成 TypeScript 与 Go 客户端 | [客户端代码](../http/clients.md) |
| `frontend` | 托管前端：按挂载路径返回页面和静态文件，服务端渲染的页面经 unix socket 交给 Bun 渲染 | [托管前端与服务端渲染](../frontend/hosting.md) |
| `realtime` | 嵌入 Centrifuge 的 WebSocket 频道 | [实时频道](../libraries/realtime.md) |
| `mcpx` | 把动作作为 MCP 工具提供给 AI 客户端 | [MCP 服务](../ai/mcp.md) |
| `natsrpc` | 把动作作为 RPC 端点，其他服务经由 NATS 同步调用 | [提供和调用端点](../rpc/endpoints.md) |
| `command` | 根命令、`Bind`、`Daemon`、退出码；`httpcmd`、`migratecmd` 等内置命令 | [命令行](../general/commands.md) |
| `queue/queueapi` | 队列的运维接口 | [运维接口](../background/queue-monitoring.md#queue-api) |
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
| `privacy` | 注销账号：冷静期，在一个事务中删除各处的个人数据 | [注销账号](../features/privacy.md) |
| `privacy/export` | 导出个人数据：后台打包成 zip，由本人下载 | [导出个人数据](../features/data-export.md) |
| `sheet/imports`、`sheet/exports` | 后台导入与导出表格 | [导入与导出](../features/import-export.md) |
| `webhook` | 按 Standard Webhooks 推送事件 | [Webhook](../features/webhooks.md) |
| `search` | PostgreSQL 全文搜索 | [全文搜索](../features/search.md) |
| `vector` | Qdrant 向量检索 | [向量检索](../ai/vector.md) |
| `ai`、`ai/chat` | 模型能力接口；对话式 agent | [AI 助手](../ai/chat.md) |
