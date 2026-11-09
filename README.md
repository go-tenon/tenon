# go-kit

正式产品的半成品工具包：基础设施、装配层与可复用的业务模块。新产品复制 [starter](./starter)，
`main.go` 只有十几行，产品代码集中在配置、迁移、路由表与自己的业务上。

## 分层

```
产品（main / 路由表 / 页面 / 自己的业务）
   │             │
   ▼             ▼
  app         modules/*      两者互不依赖
   │             │
   ▼             ▼
基础设施：config logger database httpx/* mail messaging storage support/*
```

- **基础设施**：互相独立、可单独使用，不含业务；由 `internal/archtest` 检查不依赖上层。
- **app**：装配层。读取配置、初始化日志、连接数据库、执行迁移、组装全局中间件与 huma、
  创建邮件发送器、文件存储、消息总线与定时任务，提供 cobra 子命令（serve、migrate、openapi、systemd）、
  优雅停机与 systemd 通知。
- **modules**：可复用的业务模块。模块不提供页面，也不自己注册路由：
  它提供服务、迁移、`rest.Endpoint` 与消息处理函数，由产品在路由表中显式挂载。

| 包 | 内容 |
|---|---|
| `app` | 装配层：`Main`、`New`、子命令、迁移发布 |
| `modules/account` | 账号：注册、登录（JWT access + 轮换的 refresh token，网页用 Cookie 会话）、找回与修改密码、登录设备管理、头像、邮箱验证、两步验证（TOTP + 恢复码）、API Key（可限定 scope）、第三方登录（GitHub、Google）、安全事件回调 |
| `modules/file` | 文件：上传（按内容识别类型、大小与类型限制）、经应用转发的下载与权限检查、S3 客户端直传、未被引用文件的清理 |
| `modules/export` | 表格导出：列定义 + 行迭代器，流式写出 CSV（防公式注入）或 XLSX；数据量大时异步导出（后台生成、完成后下载） |
| `modules/audit` | 审计日志：记录操作者、IP、request_id，账号事件自动记录，按条件查询与定期清理 |
| `modules/notification` | 站内通知：发送（可在业务事务中）、列表、未读数、标记已读 |
| `modules/setting` | 系统设置：产品定义的类型化设置组，JSON 保存，新增字段不需要迁移 |
| `config` | YAML 配置加载，环境变量引用，`Secret` 打码 |
| `logger` | 基于 slog 的日志初始化与 context 字段透传 |
| `database` | Postgres（bun + pgx + goose）：连接池、UUIDv7 主键与时间戳的公共模型、带锁的迁移、模块迁移发布、advisory lock 选主 |
| `database/dbtest` | 集成测试用的临时数据库 |
| `httpx` | chi 中间件（RequestID、RealIP、AccessLog、Recover、SecureHeaders）与 Server 生命周期 |
| `httpx/auth` | Session 与 Bearer 认证的 Principal 约定、登录/退出、页面登录拦截 |
| `httpx/rest` | huma 的 5xx 错误日志、按 OpenAPI security 鉴权、action 约定、模块提供的 `Endpoint`、按 IP 限流 |
| `httpx/web` | templ 渲染、HTMX 辅助、带版本号的静态资源、内置 htmx |
| `mail` | 发送邮件：log（开发）、SMTP、Resend 三种实现；`mail/mailtest` 供测试 |
| `messaging` | 基于 Watermill + Postgres 的异步消息：事务内发布、自动重试、poison 队列 |
| `storage` | 文件存储：本地目录或 S3 兼容存储（AWS S3、R2、OSS、COS、MinIO），S3 支持预签名直传 |
| `support` | 社区库缺少的通用工具（加密、密码哈希、随机 token、脱敏、集合） |

## 迁移

产品只有一个迁移目录。go-kit 模块的迁移随包发布，由 `go run . migrate publish` 复制到产品目录后
与产品迁移一起提交、执行（类似 Laravel 的 `vendor:publish`）。命名与表结构约定见 `database` 包文档。

## 页面

go-kit 不提供页面。starter 的页面用 [templ](https://github.com/a-h/templ) +
[shadcn-templ](https://github.com/axadrn/shadcn-templ)（组件由其 CLI 复制进产品，归产品所有）+ HTMX，
样式由 Tailwind CSS v4 编译；开发命令见 `starter/Taskfile.yml`。

## 直接使用的社区组件

社区已有成熟组件时，产品直接集成，不在本仓库额外封装：

- HTTP 路由：[chi](https://github.com/go-chi/chi)，CORS 用 [go-chi/cors](https://github.com/go-chi/cors)，限流用 [go-chi/httprate](https://github.com/go-chi/httprate)，CSRF 用标准库 `http.CrossOriginProtection`
- REST API：[huma](https://github.com/danielgtaylor/huma)（`adapters/humachi`）
- Session：[scs](https://github.com/alexedwards/scs)（app 已配置 postgresstore）
- 异步消息：[Watermill](https://github.com/ThreeDotsLabs/watermill)（watermill-sql 的 Postgres 队列），以后需要时可换成 NATS
- 对象存储：[aws-sdk-go-v2](https://github.com/aws/aws-sdk-go-v2)（S3）；Excel：[excelize](https://github.com/xuri/excelize)；图片缩放：`golang.org/x/image/draw`
- 命令行：[cobra](https://github.com/spf13/cobra)（app 已提供根命令，产品可用 `app.Command` 追加子命令）
- 定时任务：[gocron](https://github.com/go-co-op/gocron)（`a.Scheduler`，多实例时由 Postgres advisory lock 选出一个实例执行）
- 两步验证：[pquerna/otp](https://github.com/pquerna/otp)；第三方登录：[x/oauth2](https://pkg.go.dev/golang.org/x/oauth2)
- systemd：[go-systemd](https://github.com/coreos/go-systemd)（Type=notify 就绪通知与看门狗，`systemd` 子命令生成 unit 文件）
- 页面模板：[templ](https://github.com/a-h/templ)，产品在 go.mod 中用 `tool` 指令固定 templ 命令版本，与运行时库一致
- 表单解码与校验：[go-playground/form](https://github.com/go-playground/form)、[go-playground/validator](https://github.com/go-playground/validator)
- HTTP Client：[Resty](https://github.com/go-resty/resty)
- 错误处理：[cockroachdb/errors](https://github.com/cockroachdb/errors)（包装错误用 `errors.Wrap`，`fmt.Errorf` 会丢失日志中的调用栈）
- 缓存：[gocache](https://github.com/eko/gocache)
- 数据库：Postgres 18+，查询用 [bun](https://bun.uptrace.dev)，驱动用 [pgx](https://github.com/jackc/pgx)，迁移用 [goose](https://github.com/pressly/goose)
- 分布式锁：Postgres advisory lock（优先事务级 `pg_try_advisory_xact_lock`，事务结束自动释放）；已引入 Redis 的项目可选 [bsm/redislock](https://github.com/bsm/redislock)

## 运行

starter 需要 Postgres 18+：

```bash
cd starter
DATABASE_URL='postgres://postgres:postgres@localhost:5432/starter?sslmode=disable' go run .
```

打开 http://localhost:8080 注册账号，API 文档在 http://localhost:8080/docs。找回密码、邮箱验证的邮件默认写入日志。
用 `ADMIN_EMAIL` 指定的邮箱注册的账号可以访问管理后台（/admin）；设置 `GITHUB_CLIENT_ID` 与
`GITHUB_CLIENT_SECRET` 后登录页显示 GitHub 登录。

部署到 Linux 服务器：

```bash
./starter systemd > /etc/systemd/system/starter.service   # 环境变量写在 /etc/starter/env
systemctl daemon-reload && systemctl enable --now starter
```

运行测试：`go test ./...`。集成测试需要 Postgres 18+，设置 `DATABASE_TEST_DSN` 后运行，否则跳过（见 `database/dbtest`）；
SMTP 测试需要 Mailpit，见 `mail/mail_test.go`。
