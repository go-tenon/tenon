# go-kit 约定

本文是 go-kit 的规则与约束。新增或修改代码前先对照本文；能自动检查的规则由 `internal/archtest` 检查。
标注 **【待确认】** 的条目是尚未定下的取舍。

## 1. 定位

- go-kit 是正式产品的半成品：基础设施、装配层与可复用的业务模块。**go-kit 本身是交付物。**
- `examples/` 是用 go-kit 组装的示例，主要用途是端到端测试 go-kit；面向不同场景的脚手架以后在其中另开。
- 与 go-kit 同一个仓库、同一个 `go.mod`，CI 一起测试。

## 2. 分层与依赖

```
examples/*            产品示例，不受限制
app                   装配层：只依赖基础设施
modules/*             业务模块：只依赖基础设施，模块之间互不依赖
基础设施               config logger i18n database httpx/* mail messaging storage support/*：只依赖基础设施
```

- 模块之间**不能互相 import**。需要协作时：
  - “当前用户”统一由 `httpx/auth` 提供（见 §6.3），不依赖 account；
  - 事件通过回调，由产品（或示例）在装配时连接，例如 account 的事件写入 audit。
- 当前 `modules/audit` 依赖 `modules/account`（`AccountEvent` 适配函数），按本条要移除：
  audit 不再保存摘要文字（见 §5.4），account 事件到审计记录的转换只有几行，放在示例中。**【待确认】**
- 第三方库：优先使用社区成熟、主流的库，直接暴露其类型（如 `*bun.DB`、`gocron.Scheduler`），
  不为了“统一”再包一层。新引入的依赖写进 README 的“直接使用的社区组件”。

## 3. 包的结构

一个业务模块（以 `modules/order` 为例）：

```
modules/order/
	doc.go              包文档：做什么、如何装配（完整示例代码）
	config.go           Config、DefaultConfig
	order.go            Service、New、领域错误、模型
	*.go                按功能拆分的业务逻辑
	endpoints.go        REST 接口（rest.Endpoint）与 DTO
	messages.go         消息 topic、payload 与处理函数（如果有）
	migrations/         0001_create_orders.sql …
	locales/            zh-CN.toml、en.toml
	*_test.go
```

### 3.1 构造

- 模块的主类型叫 `Service`，用 `New` 构造；次要类型用 `NewXxx`（如 `account.NewAvatars`）。
- 依赖（`*bun.DB`、`storage.Storage` 等）作为 `New` 的参数，配置放在最后：

  ```go
  func New(db *bun.DB, store storage.Storage, cfg Config) (*Service, error)
  ```

  配置不可能出错时返回值可以只有 `*Service`。
- 回调不放在 `Config` 中，也不用导出的可变字段，而是在构造后用 `OnXxx` 方法注册（可注册多个，
  按注册顺序调用），必须在开始处理请求之前注册：

  ```go
  users.OnEvent(func(ctx context.Context, e account.Event) { ... })
  jobs.OnFinish(func(ctx context.Context, job *export.Job) { ... })
  ```

### 3.2 配置

- `Config` 的字段带 yaml 标签（snake_case），`DefaultConfig()` 返回全部默认值。
- `New` 中把零值字段补成默认值，产品只写需要修改的配置项也能正常运行。
- 时长用 `time.Duration`（yaml 中写 `15m`、`24h`），密钥用 `config.Secret`（打印时打码）。
- 产品配置结构体内嵌 `app.Config`，模块配置作为命名字段：`Account account.Config \`yaml:"account"\``。

## 4. 错误

### 4.1 包装

- go-kit 统一使用 `github.com/cockroachdb/errors`：`errors.New`、`errors.Wrap`、`errors.Is`、`errors.As`，
  错误带调用栈，日志用 `%+v` 打印。不使用 `fmt.Errorf`（由 archtest 检查）。
- 包装时以包名开头：`errors.Wrap(err, "account: register")`。

### 4.2 领域错误

- 每个模块用导出的哨兵变量定义领域错误，并在定义处登记 HTTP 状态码与错误码：

  ```go
  var ErrEmailTaken = httpx.DefineError(http.StatusConflict, "account.email_taken")
  ```

  错误码同时是多语言消息的 ID（§5），也是 API 响应中给客户端判断用的稳定标识。
- 处理函数（REST 接口与页面）直接返回领域错误，不写 `mapError`：全局的错误处理按登记的信息
  决定状态码，并把错误码翻译为当前语言的提示。
- 未登记的错误一律视为 500，记录日志，响应只返回通用提示。

### 4.3 响应格式

REST 接口的错误响应为 RFC 9457（huma 默认），增加 `code` 字段：

```json
{"status": 409, "title": "Conflict", "detail": "该邮箱已注册", "code": "account.email_taken"}
```

App 根据 `code` 判断错误、根据 `detail` 显示提示。

## 5. 多语言

### 5.1 基础

- 新增基础设施包 `i18n`，基于 [go-i18n](https://github.com/nicksnyder/go-i18n)（CLDR 复数规则、模板参数）。
- go-kit 自带 `zh-CN`（默认、兜底）与 `en` 两种语言。
- 所有面向用户的文字（错误提示、邮件、页面、通知、审计摘要）都通过消息 ID 翻译，代码中不写死。
  OpenAPI 文档（接口摘要、字段说明）是写给开发者看的，使用中文，不翻译。

### 5.2 消息文件

- 每个模块在 `locales/<语言>.toml` 中提供自己的消息，消息 ID 以模块名开头：`account.email_taken`。
- 模块的消息在 import 时自动注册到 `i18n` 的默认目录（类似 database/sql 注册驱动），
  产品用 `i18n.Load(fsys)` 加载自己的消息；后加载的覆盖先加载的，因此产品可以改写 go-kit 的任何提示。
  **【待确认】**：也可以像迁移一样由产品在 `app.Options` 中显式列出，缺点是漏写时提示会静默缺失。

### 5.3 确定语言

- 每个请求的语言由 `i18n.Middleware` 确定，依次取：`lang` Cookie（用户在页面上切换的）→
  `Accept-Language` 头 → 默认语言。响应带 `Content-Language` 头。
- 账号保存用户选择的语言（`users.locale`）。异步发送的内容（邮件、通知）没有请求，使用收件人的 `users.locale`；
  消息 payload 中带上 `Locale` 字段。
- 页面与代码中翻译：`i18n.T(ctx, "account.email_taken")`、`i18n.T(ctx, "export.done", map[string]any{"File": name})`。

### 5.4 保存在数据库中的内容

- go-kit 不在数据库中保存翻译后的文字，保存消息 ID 与参数，显示时翻译：
  - 审计日志：保存 `action` 与 `data`，摘要由消息 `audit.<action>` 翻译。
  - 站内通知：保存 `type` 与 `data`，标题与正文由消息 `notification.<type>.title`、`notification.<type>.body` 翻译。
    **【待确认】**：这要求产品发的每种通知都在消息文件中定义；另一种做法是发送时按收件人语言翻译好再保存，
    简单但切换语言后旧通知不会变。
- 用户输入的内容（笔记、名称等）原样保存，不翻译。

### 5.5 已知限制

huma 的参数校验错误（如“长度不能小于 1”）的字段级详情是英文，由 huma 生成；
顶层的 `detail` 会翻译为“请求参数有误”。后续可以按 huma 的消息模板逐条翻译。

### 5.6 时区 **【待确认】**

数据库统一保存 `timestamptz`。显示时使用服务器时区，还是在账号中保存用户时区（`users.time_zone`）？
建议先只做服务器时区，需要时再加。

## 6. HTTP

### 6.1 接口（Endpoint）

- 模块不注册路由，只提供返回 `rest.Endpoint[I, O]` 的函数，命名为 `<动作>Endpoint`，由产品选择路径挂载。
- `OperationID` 为 `<模块>-<动作>`（如 `account-login`），`Tags` 为模块名。
- 需要登录的接口声明 `Security: rest.RequireAuth()`；修改密码、管理 API Key 等敏感接口不接受 API Key。
- 普通 HTTP 处理函数（下载、跳转等）以 `http.Handler` 返回，命名为 `<动作>Handler`；
  内部用 `web.HandlerFunc`（返回 error）。
- 路径参数用标准库 `r.PathValue("id")`，不用 `chi.URLParam`。

### 6.2 DTO

- 返回给客户端的类型命名为 `<模型>DTO`，转换统一为 Service 的方法 `s.<模型>DTO(m)`（可以用到配置，如生成 URL）。
- JSON 字段 snake_case；ID 为 UUID 字符串；时间为 RFC 3339；可省略的字段用 `omitzero`/`omitempty`。
- 模型（bun 结构体）不直接返回给客户端。

### 6.3 当前用户

- 所有 ID 都是 UUID，`auth.Principal` 的 `UserID()` 改为返回 `uuid.UUID`。
- 任何模块都用 `auth.UserID(ctx) (uuid.UUID, bool)` 取当前用户，不再传入 `CurrentUser` 函数，也不依赖 account。

### 6.4 分页

- 列表统一用游标分页：请求参数 `before`（上一页返回的 `next`）与 `limit`，响应：

  ```json
  {"items": [...], "next": "0193…"}
  ```

  `next` 省略表示没有更多。共用 `rest.PageInput`、`rest.Page[T]`；服务层用 `database.PageParams`，
  按 UUIDv7 主键倒序（即按创建时间倒序）。
- 数据量确定很小的列表（登录设备、API Key）可以不分页，直接返回数组。

## 7. 数据库

- 迁移：模块内按 `0001_create_xxx.sql` 编号，由 `migrate publish` 复制到产品的迁移目录；**已发布的迁移不再修改**，改表结构一律新增迁移。
- 表名：复数 snake_case；与其他模块容易重名的加模块前缀（`user_sessions`、`audit_logs`、`export_jobs`）。
- 主键：`id uuid PRIMARY KEY DEFAULT uuidv7()`，应用中由 `database.Model` 生成。
  `created_at`、`updated_at` 紧跟 `id`；只追加的表只有 `created_at`（`database.CreatedModel`）；
  不是每张表都需要时间戳，不写死 `set_updated_at` 触发器。
- 跨模块引用（如 `user_id`）**不加外键**，同一模块内的表可以加。
- 约束与索引命名：唯一约束 `<表>_<列>_key`（`database.IsUniqueViolation` 依据它判断），索引 `<表>_<列>_idx`。
- 状态等枚举用 `text` + `CHECK`；灵活结构用 `jsonb`；时间一律 `timestamptz`。
- 需要清理的数据提供 `Cleanup`/`Prune` 方法，由产品用定时任务调用；模块不自己启动后台 goroutine。

## 8. 消息、事件与定时任务

- 异步消息 topic 命名 `<模块>_<事件>`（watermill-sql 用 topic 作表名的一部分，不能含点），
  payload 类型以 `Requested` 结尾，JSON 字段 snake_case。
- 发消息与写业务数据在同一事务中（`messaging.PublishTx`），处理函数必须可以重复执行（至少一次投递）。
- 同步事件（回调）类型名 `<模块>.<事件>`（如 `account.login`），用 `OnEvent` 注册。
- 定时任务由产品在 `a.Scheduler` 中注册，多实例只在 leader 上执行；模块只提供要调用的方法。

## 9. 日志

- 使用 `slog` 并传入 context（`slog.InfoContext`），自动带上 `request_id`、`user_id`。
- 键名 snake_case，错误的键名为 `err`；不记录密码、token、密钥。
- 业务已经成功、附带操作失败（审计写入、通知发送）时只记 Warn/Error 日志，不向上返回错误。

## 10. 时间

- Service 内部用不导出的 `s.now()` 取当前时间，测试中可以替换；不在业务代码中直接调用 `time.Now()`。
- 保存到数据库的时间截断到微秒（`database.Model` 已处理）。

## 11. 测试

- 集成测试使用真实的 Postgres 18（`DATABASE_TEST_DSN`），每个测试一个临时库：
  `db := dbtest.New(t, account.Migrations, audit.Migrations)` 建库、发布并执行迁移。
- 外部依赖用进程内替身：S3 用 gofakes3，邮件用 `mailtest.Recorder`，SMTP 用 Mailpit（可选）。
- 优先写外部测试包（`package xxx_test`），只测公开 API；需要替换时钟等内部状态时才用包内测试。
- 每个模块至少有服务层测试与接口测试；`examples/` 有端到端测试，覆盖完整的装配。
- CI（GitHub Actions）跑 `go vet`、`gofmt` 检查与全部测试（含 Postgres 18 服务）。

## 12. 文档与版本

- 代码注释、包文档使用中文。每个包的 `doc.go` 写清楚做什么、不做什么、如何装配（可复制的示例代码）。
- 导出的标识符都有注释。
- 按语义化版本打 tag；`v0.x` 期间允许不兼容的修改，但在 CHANGELOG 中写明迁移方法。
