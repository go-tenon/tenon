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
| `i18n` | 已交付 | go-i18n 目录分层组合与冻结 Bundle |
| `fault` | 已交付 | Code/Kind/公开参数与 HTTP 中立映射 |
| `cleanup` | 已交付 | 资源清理栈 |
| `httpx` | 已交付 | net/http 运行与停机排空 |
| `httpx/rest` | 已交付 | Huma 装配、延迟绑定与离线 OpenAPI |
| `identity` | 已交付 | 已认证主体 |
| `database` | 已交付 | PostgreSQL（Bun + pgx）连接与事务 |
| `database/migrate` | 已交付 | goose Provider 与已发布迁移检查 |
| `database/dbtest` | 已交付 | 真实数据库测试隔离 |
| `command` | 后续 PR | serve / config / migrate / openapi 命令工厂 |
| `examples/api` | 后续 PR | 手写参考 API 应用 |

包只在有实际代码时创建。默认组合：chi、Huma、slog、Bun、pgx、goose、Cobra、go-i18n、
cockroachdb/errors；缓存直接使用 eko/gocache。

## 旧代码

`legacy/` 是独立 module 中的旧 go-kit 快照，退出第一期主构建，仅作需求参考。
说明见 [`legacy/README.md`](./legacy/README.md)。

## 开发

```bash
go test ./...
```

集成测试与参考应用随后续 PR 加入。CI 见 `.github/workflows/ci.yml`。
