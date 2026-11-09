# examples/api

手写参考 HTTP API：类型配置、订单创建/读取/更新/游标分页、Bearer 鉴权、可选查询缓存。
服务直接使用 Bun 事务和 eko/gocache，HTTP DTO 与业务模型分离。

## 运行

先创建 PostgreSQL 数据库，再执行：

```bash
export DATABASE_URL='postgres://postgres:postgres@127.0.0.1:55432/orders?sslmode=disable'
export API_TOKEN='replace-with-your-secret'
go run . config check --config config.yaml
go run . migrate up --config config.yaml
go run . serve --config config.yaml
```

没有内置 `dev` token；示例静态凭据必须显式配置。请求使用
`Authorization: Bearer $API_TOKEN`。生产应用可替换原生 `rest.TokenValidator`。

```bash
go run . openapi                         # 完全离线，不读运行配置
go run . config show --format json       # 凭据脱敏
go run . migrate status                  # JSON 迁移状态
go run . doctor                          # 仅探测数据库和启用的缓存
go run . --version                       # 应用、框架、Go 和可用的构建信息
```

`migrate` 只解码 database 段，未设置 API_TOKEN 或缓存变量也能执行。
`config check` 聚合各配置段的错误；`config show` 支持 yaml/json。
stdout 留给结果，stderr 留给日志和诊断。退出码：0 成功、2 用法错误、3 配置错误、
4 外部依赖不可用、5 内部错误。

## HTTP 契约

- `POST /orders`、`GET /orders`、`GET /orders/{id}`、`PATCH /orders/{id}` 均要求 bearer 和 orders scope。
- 同一主体的订单标题唯一；冲突返回 409。服务层再次检查身份和资源所有权。
- 列表按 created_at、id 倒序，游标携带版本、主体和排序字段；不能跨主体复用。
- 错误响应使用 Problem Details，包含 code、request_id；不回显输入值、SQL 或堆栈。
- `/livez` 表示进程可响应；`/readyz` 以短超时检查数据库，排空时撤销就绪。
- 请求体默认 1 MiB，请求时限 30 秒，停机排空预算 10 秒。shutdown_timeout=0 立即关闭。
- trusted_proxies 默认为空；default_language 支持 en/zh-CN，请求可用 Accept-Language 选择。

## 可选缓存

cache.driver 默认 none，可设 memory 或 redis；Redis 使用 cache.addr。
namespace 必须按部署/应用隔离，键包含 order:v1，ttl 默认 1 分钟。
缓存命中仍检查资源权限。miss 回源；后端故障和反序列化失败分别记录诊断后回源。
更新提交后失效，回滚保留旧缓存。该查询缓存只提供最终一致性，并发回填仍可能保留旧值至 TTL。
内存过期任务和 Redis 客户端均由应用拥有并关闭；缓存故障不影响 ready。

新迁移追加 SQL；已有基线文件不改写。首次发布前用 testdata/baseline 演练升级，
发布后 CI 通过 MIGRATION_BASELINE_REF 指定真实发布 tag。非事务迁移失败须检查实际数据库后人工恢复。
开发命令见 Taskfile.yml；完整测试需 DATABASE_TEST_DSN、REDIS_TEST_ADDR。
