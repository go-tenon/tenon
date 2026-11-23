# 目录结构

一个 Tenon 应用的代码放在哪个目录、文件和类型怎样命名，以及框架的各个包属于哪一层、文档在哪一页。

路由、命令和队列任务都在代码中显式注册，框架不按目录发现代码。`tenon init` 生成的项目和[入门教程](../tutorial/01-project.md)按这一页的结构组织。

<a id="application-layout"></a>
## 应用的目录

```text
blog/
├── go.mod
├── tenon.json             开发工具的配置，可以没有
├── package.json           Bun workspace 与 dev、build、generate 脚本
├── AGENTS.md              项目说明和代码约定，AI 编程助手读它
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
├── locales/               翻译文件：服务端的错误提示、前端的界面文字
├── apps/                  前端，每个一个目录，例如 web、admin
├── packages/client/       生成的 TypeScript 客户端
├── tenon/                 框架同步的文档（tenon/docs）和前端包（tenon/js），不手改
└── .tenon/                tenon dev 运行期生成的临时文件，不提交
```

新项目只有 `config`、`cmd`、一个业务包、`locales/web.zh-CN.yaml`、`apps/web` 和 `packages/client`。其余目录在用到时创建：接上数据库时加 `compose.yaml`、`migrations/`，装配代码从 `main.go` 移到 `internal/bootstrap/`；写错误提示时在 `locales/` 中加 `zh-CN.yaml` 和 `embed.go`；加后台时加 `apps/admin/`。

<a id="the-cmd-directory"></a>
### cmd

`cmd/<程序名>/main.go` 是入口，目录名是程序名：`cmd/blog` 构建出 `bin/blog`，部署时运行 `./bin/blog serve`。进程名和 OpenTelemetry 的 `service.name` 默认取程序名。`main.go` 只组装根命令并执行，装配都在 `bootstrap` 中，见[命令行](../general/commands.md#declaring-the-cli)。

<a id="the-bootstrap-directory"></a>
### internal/bootstrap

`bootstrap` 包把应用装配起来，写法见[依赖装配](dependencies.md#running-parts)：

| 文件 | 内容 |
| --- | --- |
| `config.go` | 应用的配置结构体 |
| `app.go` | `App`：各个运行部分共用的资源；`Open` 打开它们，`Parts` 列出常驻进程的运行部分 |
| `http.go` | 中间件、路由注册，HTTP 服务这个部分 |
| `commands.go` | 需要数据库等资源的命令 |
| `rpc.go`、`background.go` | RPC 服务、队列 worker、定时任务调度器等其他运行部分，每个一个文件 |

每个运行部分只调用各业务包的登记函数，例如 HTTP 服务调用 `article.RegisterRoutes`。

<a id="business-packages"></a>
### 业务包

每个业务模块是 `internal` 下的一个包，包名用单数，例如 `article`。代码按[业务动作](actions.md)组织，一个动作一个文件：

| 文件 | 内容 |
| --- | --- |
| `article.go` | 模型、业务错误、几个动作共用的查询与授权函数 |
| `create.go`、`publish.go`…… | 一个业务动作一个文件 |
| `routes.go` | `RegisterRoutes`：本模块的 HTTP 路由、方法、访问策略 |
| `rpc.go` | `RegisterRPC`：本模块的 [RPC 端点](../rpc/endpoints.md) |
| `jobs.go` | `RegisterJobs`：本模块的[队列任务](actions.md#as-jobs)的处理函数 |
| `tasks.go` | `RegisterTasks`：本模块的[定时任务](actions.md#as-scheduled-tasks) |
| `tools.go` | 本模块的 [MCP 工具](../ai/mcp.md) |
| `commands.go` | `Commands`：本模块的命令 |

模块用到哪几种入口，就有哪几个文件。同一个 `RegisterJobs` 在 `serve` 和 `serve worker` 两种进程中都会被调用，模块不关心自己运行在哪个进程中。动作多到一个目录难以浏览时，按子领域拆成新的包，例如 `internal/comment`。

两个业务包不能互相导入，Go 编译时报 `import cycle not allowed`。下游模块要在上游的动作完成后做事时，用[钩子](actions.md#follow-ups)，依赖只有一个方向。

<a id="the-migrations-directory"></a>
### migrations

SQL 迁移文件，由 `embed.go` 嵌入程序，部署时用 `migrate up` 执行，见[迁移](../database/migrations.md)。

<a id="the-locales-directory"></a>
### locales

`en.yaml`、`zh-CN.yaml` 等是服务端的翻译文件，由 `embed.go` 嵌入程序，见[本地化](../general/localization.md)。`<前端>.<语言>.yaml`（例如 `web.zh-CN.yaml`）是前端的界面文字，`tenon generate` 把它生成为前端的 `src/messages.gen.ts`，见[界面文字](../frontend/i18n.md)。

<a id="the-frontend-directory"></a>
### apps 和 packages

`apps` 中每个目录是一个前端，目录名是前端的名称，例如 `apps/web`、`apps/admin`。`tenon build` 把它们构建到 `apps/dist`，`apps/apps.go` 把它嵌入 Go 程序，部署时只有一个二进制文件，见[托管前端与服务端渲染](../frontend/hosting.md)。用 [tenon add frontend](../installation/tooling.md#add-frontend) 加入新的前端。

`packages` 中是多个前端共用的包。`packages/client/src` 中每个 API 一个目录，是生成的 TypeScript 客户端，修改接口后重新生成，不手动修改，见[客户端代码](../http/clients.md)。前端用 `@<作用域>/client/<名称>` 导入它，见[前端与 API](../installation/tooling.md#conventions)。

<a id="adding-a-module"></a>
## 新增一个业务模块

以评论为例：

1. **表结构**：`go run ./cmd/blog migrate make create_comments_table`，在 `migrations/` 中写建表语句。
2. **业务代码**：新建 `internal/comment/`，`comment.go` 放模型和业务错误，`create.go`、`delete.go` 每个动作一个文件，见[业务动作](actions.md)。
3. **路由**：在 `internal/comment/routes.go` 中写 `RegisterRoutes`。
4. **装配**：在 `internal/bootstrap/http.go` 中调用 `comment.RegisterRoutes`，传入它需要的依赖。没有这一步，接口不会出现在服务和接口文档中：框架不自动发现包。
5. **错误提示**：在 `locales/zh-CN.yaml` 中加上新错误码的提示。
6. **前端**：重新生成客户端，在 `apps/web/src/routes/` 中写页面，用 `api.comment.create()` 调用接口。

除第 4 步外，改动都在新模块的目录中。删除这个功能时，删掉目录和 `bootstrap` 中的一行。

<a id="naming-conventions"></a>
## 命名约定

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
### 包名的后缀 `x`

包名与调用方通常在同一个文件中同时导入的包同名时，加后缀 `x`，调用方不用起别名：

- `errorx`：调用方同时用 `errors.Is`、`errors.As`；
- `httpx`：调用方同时用 `net/http`；
- `mcpx`：调用方同时用官方 SDK 的 `mcp`；
- `natsx`：调用方同时用 `nats.go` 的 `nats`。

同名的包只在包的内部用到时不加后缀，在包内起别名。例如 `mail` 在内部导入 `net/mail`，`filesystem/s3` 在内部导入 AWS SDK 的 `s3`。

<a id="framework-packages"></a>
## 框架包一览

每个包可以单独使用。顶层的包分为四层，每个包只能导入同层或更低层的包，由架构测试 `internal/checks/archtest` 检查：

```text
功能        apitoken  login  oauthserver  authz  notification  push  activity  feature  settings  savedview  operation  privacy  privacy/export  webhook  search  vector  sheet/imports  sheet/exports  ai
入口        httpx  frontend  realtime  mcpx  natsrpc  command  clientgen
基础设施    database  natsx  queue  schedule  cache  lock  ratelimit  captcha  broadcast  filesystem  imgproxy  mail  sms  pdf  outbound  sheet  document  contract  form
基础        errorx  identity  config  cleanup  logging  telemetry  health  doctor  i18n  enum  page  period  hashing  crypt  acquire
```

名字以 `test` 结尾的子包（`dbtest`、`queuetest`、`mailtest` 等）是测试用的替身和工具。函数的签名和说明用 `go doc github.com/go-tenon/tenon/<包>` 查。

<a id="foundation"></a>
### 基础

只导入同层的包，可以在任何地方使用。

| 包 | 用途 | 文档 |
| --- | --- | --- |
| `acquire` | 在 context 中传递等待共享资源（如数据库连接）的时限 | [连接用满时](../database/database.md#pool-exhaustion) |
| `cleanup` | 按逆序关闭入口打开的资源 | [关闭资源](dependencies.md#closing-resources) |
| `config` | 读取 YAML、展开环境变量、校验 | [配置](../general/configuration.md) |
| `crypt` | 用应用密钥加密、签名链接 | [加密与签名链接](../security/encryption.md) |
| `enum` | 带显示文字的枚举：取值、校验和按语言翻译的显示文字 | [带显示文字的枚举](../http/requests.md#enums) |
| `errorx` | 带稳定错误码和分类的业务错误 | [错误处理](../general/errors.md) |
| `hashing` | 密码哈希 | [哈希](../security/encryption.md) |
| `health` | 检查依赖是否可用，供就绪检查使用 | [健康检查](lifecycle.md#health-checks) |
| `doctor` | 检查运行环境的版本、设置和权限，给出修改方法；`doctorcmd` 是 `doctor` 命令 | [自检](../installation/doctor.md) |
| `i18n` | 组合 go-i18n 消息目录 | [本地化](../general/localization.md) |
| `identity` | 已认证的主体，在 context 中传递 | [认证](../security/authentication.md) |
| `logging` | 构造 slog 日志器，传递请求关联字段 | [日志](../general/logging.md) |
| `page` | 分页参数与响应结构 | [分页](../http/responses.md#pagination) |
| `period` | 报表时间窗口参数、时区边界、环比期与时间桶补零 | [时间窗口](../http/responses.md#period) |
| `telemetry` | 用 OTLP 推送日志、指标和链路 | [可观测性](../general/telemetry.md) |

<a id="infrastructure"></a>
### 基础设施

与入口无关的能力：数据库、队列、文件这类外部资源，以及各入口共用的契约和格式。

| 包 | 用途 | 文档 |
| --- | --- | --- |
| `database` | PostgreSQL 连接池、事务与提交后回调、约束错误 | [数据库](../database/database.md) |
| `database/migrate` | goose 迁移器，新建、发布迁移 | [迁移](../database/migrations.md) |
| `natsx` | 按框架的约定连接 NATS，队列、广播和在线成员共用这条连接 | [NATS](../libraries/nats.md) |
| `queue` | NATS JetStream 任务队列，数据库发件箱，重试与失败任务 | [队列](../background/queue.md) |
| `schedule` | 按 cron 表达式定时执行，多实例去重 | [定时任务](../background/scheduling.md) |
| `cache` | Redis 上的先读缓存再加载 | [缓存](../libraries/cache.md) |
| `lock` | 带过期时间的原子锁，`lock/pglock` 是 PostgreSQL 存储 | [原子锁](../libraries/locks.md) |
| `ratelimit` | 按键限流，`ratelimit/pglimit` 是 PostgreSQL 限流器 | [限流](../libraries/rate-limiting.md) |
| `broadcast` | 实例之间广播消息，`broadcast/natsbroadcast` 是 NATS 后端 | [广播](../libraries/broadcasting.md) |
| `filesystem` | `files` 表、上传、公开地址；`local`、`s3` 两种磁盘 | [文件存储](../libraries/filesystem.md) |
| `imgproxy` | 签名的 imgproxy 地址，按需缩放图片 | [图片缩略图](../libraries/filesystem.md#thumbnails) |
| `mail` | 邮件；`smtp`、`resend`、`ses` 发送，`logmail` 用于开发 | [邮件](../libraries/mail.md) |
| `sms` | 短信；`aliyun`、`tencent` 发送，`logsms` 用于开发 | [短信](../libraries/sms.md) |
| `pdf` | 把前端打印模板渲染的 HTML 生成为 PDF，`pdf/gotenberg` 生成 | [生成 PDF](../libraries/pdf.md) |
| `outbound` | 只能访问公网地址的 HTTP 客户端 | [出站请求](../libraries/outbound.md) |
| `captcha` | 服务端校验 Turnstile、hCaptcha | [人机验证](../security/login.md#captcha) |
| `sheet` | 读写 CSV 和 Excel，按结构体解码并校验每一行 | [导入与导出](../features/import-export.md) |
| `document` | `convert` 把文件转为 Markdown，`split` 切分 Markdown | [文档转换与切分](../ai/documents.md) |
| `contract` | 接口契约中与入口无关的部分：JSON Schema 与校验、枚举的命名类型、校验错误的规则和译文，HTTP、RPC、MCP 共用 | [校验](../http/requests.md) |
| `form` | 按 Huma 标签转换、校验文本字段，表格导入使用 | [导入与导出](../features/import-export.md) |

<a id="entry"></a>
### 入口

请求进入应用的方式。

| 包 | 用途 | 文档 |
| --- | --- | --- |
| `httpx` | 中间件、平滑停止、就绪检查、会话 cookie | [中间件](../http/middleware.md)、[应用生命周期](lifecycle.md) |
| `httpx/rest` | 基于 Huma 的路由注册、错误转换与 OpenAPI | [路由](../http/routing.md) |
| `clientgen` | 从路由生成 TypeScript 与 Go 客户端 | [客户端代码](../http/clients.md) |
| `frontend` | 托管前端：按挂载路径返回页面和静态文件，服务端渲染的页面交给 Bun 渲染 | [托管前端与服务端渲染](../frontend/hosting.md) |
| `realtime` | 嵌入 Centrifuge 的 WebSocket 频道 | [实时频道](../libraries/realtime.md) |
| `mcpx` | 把动作作为 MCP 工具提供给 AI 客户端 | [MCP 服务](../ai/mcp.md) |
| `natsrpc` | 把动作作为 RPC 端点，其他服务经由 NATS 同步调用 | [提供和调用端点](../rpc/endpoints.md) |
| `command` | 根命令、`Serve`、`Bind`、`Daemon`、退出码；`httpcmd`、`migratecmd` 等内置命令 | [命令行](../general/commands.md) |
| `queue/queueapi` | 队列的运维接口 | [运维接口](../background/queue.md#queue-api) |
| `cmd/tenon` | 开发工具 `go tool tenon` | [开发工具](../installation/tooling.md) |

<a id="features"></a>
### 功能

自带数据表的应用功能，由应用把它们的路由和命令接进来。

| 包 | 用途 | 文档 |
| --- | --- | --- |
| `apitoken` | API 令牌：不透明令牌与 JWT | [API 令牌](../security/api-tokens.md) |
| `login` | 密码、验证码登录与注册，找回密码，二次验证；`oauth`、`passkey` 子包 | [登录与注册](../security/login.md) |
| `oauthserver` | OAuth 2.1 授权服务器，供原生应用和 MCP 远程连接器使用 | [OAuth 授权服务器](../security/oauth-server.md) |
| `authz` | 角色与权限，按租户分配 | [授权](../security/authorization.md) |
| `notification` | 站内通知 | [通知](../features/notifications.md) |
| `push` | 移动推送，`push/aliyun` | [移动推送](../features/push.md) |
| `activity` | 操作日志 | [操作日志](../features/activity.md) |
| `feature` | 功能开关 | [功能开关](../features/flags.md) |
| `settings` | 运行时可修改的设置 | [设置](../features/settings.md) |
| `savedview` | 已保存的视图与筛选条件（共享与个人） | [已保存视图](../features/saved-views.md) |
| `operation` | 后台操作的状态与进度 | [后台操作](../features/operations.md) |
| `privacy` | 注销账号：冷静期，在一个事务中删除各处的个人数据 | [注销账号](../features/privacy.md) |
| `privacy/export` | 导出个人数据：后台打包成 zip，由本人下载 | [导出个人数据](../features/privacy.md) |
| `sheet/imports`、`sheet/exports` | 后台导入与导出表格 | [导入与导出](../features/import-export.md) |
| `webhook` | 按 Standard Webhooks 推送事件 | [Webhook](../features/webhooks.md) |
| `search` | PostgreSQL 全文搜索 | [全文搜索](../features/search.md) |
| `vector` | Qdrant 向量检索 | [向量检索](../ai/vector.md) |
| `ai`、`ai/chat`、`ai/router` | 模型能力接口；对话式 agent；智能模型路由 | [AI 助手](../ai/chat.md) |
