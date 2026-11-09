# mygoravel

约定驱动、显式装配的 Go 应用框架。第一期交付常规 HTTP API 的完整路径：
配置、错误、日志、路由、校验、鉴权、数据库、迁移、按需缓存、测试与运行管理。

Module：`github.com/ryan053857/mygoravel`。设计规格与实施计划见 [`spec.go`](./spec.go)；
编码约定见 [`docs/conventions.md`](./docs/conventions.md)。

## 第一期目标包

| 包 | 状态 | 职责 |
|---|---|---|
| `config` | 已交付 | 文档解析、按段解码、环境注入、Secret 脱敏 |
| `logging` | 已交付 | slog 构建与上下文字段辅助 |
| `i18n` | 已交付 | go-i18n 原生 Bundle 的目录组合与校验 |
| `fault` | 已交付 | 传输中立的 Code/Kind/公开参数 |
| `cleanup` | 已交付 | 资源清理栈 |
| `httpx` | 已交付 | net/http 排空与默认边界中间件 |
| `httpx/rest` | 已交付 | Huma 装配、延迟绑定与离线 OpenAPI |
| `identity` | 已交付 | 已认证主体 |
| `database` | 已交付 | PostgreSQL（Bun + pgx）连接与事务 |
| `database/migrate` | 已交付 | goose Provider 与已发布迁移检查 |
| `database/dbtest` | 已交付 | 真实数据库测试隔离 |
| `command` | 已交付 | serve / config / migrate / openapi 命令工厂 |
| `examples/api` | 已交付 | 手写参考 API 应用 |

包只在有实际代码时创建。默认组合：chi、Huma、slog、Bun、pgx、goose、Cobra、go-i18n、
cockroachdb/errors；缓存直接使用 eko/gocache。

## 旧代码

`legacy/` 是独立 module 中的旧 go-kit 快照，退出第一期主构建，仅作需求参考。
说明见 [`legacy/README.md`](./legacy/README.md)。

## 开发

```bash
go test ./...
```

```bash
# 完整集成验收需要 PostgreSQL 18 和 Redis 7
export DATABASE_TEST_DSN='postgres://postgres:postgres@127.0.0.1:55432/postgres?sslmode=disable'
export REDIS_TEST_ADDR='127.0.0.1:6379'
go test -race -count=1 -tags=integration ./...
```

文档：[`docs/support-matrix.md`](./docs/support-matrix.md)、[`docs/production.md`](./docs/production.md)、[`docs/api-review.md`](./docs/api-review.md)。
CI 见 `.github/workflows/ci.yml`。

独立消费验收会生成临时 Go 模块代理，从模块 zip 下载当前候选版本，并导出完整
`examples/api` 运行测试；不使用 `replace`。验收覆盖离线 OpenAPI、迁移、数据库写入、
缓存与权限、实际进程 SIGTERM 退出，以及无数据库/缓存的最小 HTTP 应用。
