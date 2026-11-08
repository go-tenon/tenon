# tools

为多个 Go 产品提供可复用的基础组件和功能模块：

| 包 | 内容 |
|---|---|
| `config` | YAML 配置加载，环境变量引用，`Secret` 打码 |
| `logger` | 基于 slog 的日志初始化与 context 字段透传 |
| `support` | 社区库缺少的通用工具（加密、密码哈希、随机 token、脱敏、集合） |
| `httpx` | chi 中间件（RequestID、RealIP、AccessLog、Recover、SecureHeaders）与 Server 生命周期 |
| `httpx/auth` | Session 与 Bearer 认证的 Principal 约定、登录/退出、页面登录拦截 |
| `httpx/rest` | huma 的 5xx 错误日志、按 OpenAPI security 鉴权、单文件 action 约定 |
| `httpx/web` | templ 渲染、HTMX 辅助、带版本号的静态资源、内置 htmx |

本仓库不替产品组装：中间件、错误处理等都由产品在 main 中显式挂载，各包文档给出推荐写法。

社区已有成熟组件时，产品直接集成，不在本仓库额外封装：

- HTTP 路由：[chi](https://github.com/go-chi/chi)，CORS 用 [go-chi/cors](https://github.com/go-chi/cors)，限流用 [go-chi/httprate](https://github.com/go-chi/httprate)，CSRF 用标准库 `http.CrossOriginProtection`
- REST API：[huma](https://github.com/danielgtaylor/huma)（`adapters/humachi`）
- Session：[scs](https://github.com/alexedwards/scs)
- 页面模板：[templ](https://github.com/a-h/templ)，产品在 go.mod 中用 `tool` 指令固定 templ 命令版本，与运行时库一致
- 表单解码与校验：[go-playground/form](https://github.com/go-playground/form)、[go-playground/validator](https://github.com/go-playground/validator)
- HTTP Client：[Resty](https://github.com/go-resty/resty)
- 错误处理：[cockroachdb/errors](https://github.com/cockroachdb/errors)（包装错误用 `errors.Wrap`，`fmt.Errorf` 会丢失日志中的调用栈）
- 缓存：[gocache](https://github.com/eko/gocache)
- 数据库：Postgres，使用 [pgx](https://github.com/jackc/pgx) 直连
- 分布式锁：Postgres advisory lock（优先事务级 `pg_try_advisory_xact_lock`，事务结束自动释放）；已引入 Redis 的项目可选 [bsm/redislock](https://github.com/bsm/redislock)

使用示例见 [examples/service](./examples/service)：一个包含 REST API、HTMX 页面、登录的完整服务，
运行 `go run ./examples/service -config examples/service/config.yaml` 后打开 http://localhost:8080（admin / admin）。

运行测试：`go test ./...`。
