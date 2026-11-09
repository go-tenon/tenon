# mygoravel 约定

本文是 mygoravel 的规则与约束。新增或修改代码前先对照本文与根目录 `spec.go`；
能自动检查的规则由 `internal/archtest` 执行。

## 1. 定位与第一期范围

- mygoravel 是约定驱动、显式装配的 Go 应用框架；第一期只交付常规 HTTP API 完整路径。
- 框架给出经过验证的默认组合；开发者负责业务模型、业务规则与应用装配。
- 交付物包括库、API 运维命令、可运行参考应用、约定与测试工具。
- 控制台业务应用、页面与会话、RPC、WebSocket、独立 worker/scheduler 均为后期目标，
  不因“未来可能用到”而加入核心依赖或空包。
- 旧实现快照在 `legacy/`（独立 module），不参与第一期主构建；详见 `legacy/README.md`。

## 2. 目标包图与依赖方向

只在存在实际代码时建包：

```
cleanup/           资源清理栈
config/            类型化配置
fault/             与传输无关的业务错误语义
logging/           slog 构建与上下文字段
i18n/              可选 go-i18n 目录辅助
identity/          已认证主体
database/          PostgreSQL（Bun/pgx）
database/migrate/  goose Provider 默认装配
database/dbtest/   真实数据库测试隔离
httpx/             net/http 生命周期与中间件
httpx/rest/        Huma 适配与 OpenAPI
command/           serve/config/migrate/openapi 等命令工厂
internal/archtest/ 依赖方向与副作用规则
examples/api/      最小完整数据库 API 参考应用
```

依赖规则：

- `fault`、`identity` 不依赖 HTTP、数据库或运行层。
- `config`、`logging`、`i18n` 不依赖业务模块和运行层。
- `cleanup` 不依赖 database、HTTP 或具体驱动。
- `database*` 不依赖 `httpx`；关联信息通过中立日志字段传递。
- 业务核心不依赖 `cleanup`、`command` 或 HTTP 端点；HTTP 子包可依赖核心，核心绝不反向引用。
- `command` 可组合基础设施命令工厂，但不固定安装业务模块。
- 应用 bootstrap 可连接全部层；业务服务不可接收整个应用或资源集合。

## 3. 成熟组件评审标准

组件集成优先级：直接使用原生 API → 提供配置/装配示例 → 少量连接代码 →
确有不可替代的框架语义时才定义新类型。

新增包装必须同时说明：

1. 上游原生 API 是什么；
2. 缺失的具体语义是什么；
3. 为何仅靠装配或示例不足。

不予引入：仅为统一方法名、隐藏依赖、或复制上游 CRUD/路由/缓存门面的包装。
可直接暴露 `*bun.DB`、`chi.Router`、`huma.API`、`*slog.Logger`、gocache 原生类型。
第一期默认组合：net/http + chi、Huma、slog、Bun + pgx、goose、Cobra、go-i18n、
cockroachdb/errors；需要缓存时直接使用 eko/gocache。

## 4. 命名约定

- 目录最后一段与包名一致。
- `cleanup.Stack`：资源清理，不是 App/Manager。
- `logging`：只做 slog 配置与字段辅助，`logging.New` 返回 `*slog.Logger`，不定义 Logger 门面。
- `fault.Error` / `fault.Kind`；`identity.Subject`。
- `New` 只构造；`Open` 获取外部资源；`Register` 登记端点；`httpx.Serve` 运行已构造的 server。
- `config.ReadFile` / `config.Parse` → `Document`；`Decode` 解码指定段。
- 缩写：ID、URL、HTTP、API、DB、DSN、TTL；复合词 OpenAPI、WebSocket、Postgres。
- 业务包单数（`order`），HTTP 子包如 `orderhttp`；路径资源名复数（`/orders`）。
- 配置文件字段 snake_case；JSON 与错误码片段 snake_case；OperationID 如 `order-create`。

## 5. API 参考应用需求

`examples/api`（在对应 PR 交付）必须证明：

- 手写装配即可完成配置、路由、错误、授权、数据库、迁移、可选缓存、测试与运行；
- 同时存在一个不使用数据库/缓存的最小 HTTP 应用路径，证明可选组件不是隐式依赖；
- OpenAPI 可完全离线导出；migrate 不要求认证/缓存环境变量；
- 两个应用实例的语言、错误映射、日志器、缓存与身份策略互不污染。

## 6. 后期形态清单（非第一期验收）

| 形态 | 复用 | 独立增加 |
|---|---|---|
| 控制台应用 | 配置、cleanup、业务、错误 | 原生命令与一次性释放 |
| Web 页面 | 业务服务 | templ/scs、会话、CSRF、页面错误呈现 |
| RPC | 中立业务错误 | 原生服务器与状态映射 |
| WebSocket | HTTP 升级所需能力 | 连接生命周期、心跳、授权、清理 |
| worker/scheduler | cleanup、日志 | Watermill/gocron、幂等与排空约定 |

后期顺序按真实需求确定；不把本表当作本期隐藏工作。

## 7. 测试与 CI

- 单元测试不依赖外部服务；核心契约用外部测试包。
- 集成测试使用 `integration` build tag；缺少已声明依赖必须失败，不得静默跳过。
- CI 至少：格式检查、`go vet`、单元测试；后续 PR 再叠加 race、数据库/缓存集成与独立消费测试。
- 新增上下文与堆栈优先使用 cockroachdb/errors；允许标准 errors 和 fmt.Errorf 的 %w 错误链互操作。

## 8. 文档与版本

- 包文档与导出标识符注释使用中文。
- 按语义化版本打 tag；`v0.x` 允许不兼容变更，CHANGELOG 写明迁移方法。
