# mygoravel

约定驱动、显式装配的 Go 应用框架。当前面向常规 HTTP API，提供配置、错误、日志、路由、校验、认证、数据库、迁移、可选缓存、测试与运行管理的完整使用路径。

通过普通 Go 代码连接依赖，直接使用社区组件的原生能力：chi / Huma、slog、Bun / pgx、goose、Cobra、go-i18n、cockroachdb/errors，以及可选的 eko/gocache。

## 开始使用

- [阅读用户文档](docs/README.md)
- [运行第一个应用](docs/installation.md)
- [编写 HTTP 路由](docs/routing.md)

需要 Go 1.27.3，数据库支持 PostgreSQL。无需数据库的最小 HTTP 程序见[路由教程](docs/routing.md#first-route)。

## 学习与查阅

| 要做的事情 | 文档 |
| --- | --- |
| 管理配置与密钥 | [配置](docs/configuration.md) |
| 组织业务和依赖 | [目录结构](docs/directory-structure.md) |
| 处理错误和日志 | [错误处理](docs/errors.md) · [日志](docs/logging.md) |
| 保护业务资源 | [认证与授权](docs/authentication.md) |
| 使用数据库和迁移 | [数据库](docs/database.md) · [迁移](docs/migrations.md) |
| 添加查询缓存 | [缓存](docs/cache.md) |
| 测试和发布应用 | [测试](docs/testing.md) · [部署](docs/deployment.md) |

Module 路径为 `github.com/ryan053857/mygoravel`。依赖版本以 [go.mod](go.mod) 为准，使用与代码版本对应的文档。

## 参与开发

在仓库根目录执行单元测试：

```bash
go test ./...
```

真实 PostgreSQL 集成测试和独立模块消费验收见[测试指南](docs/testing.md#integration-tests)。CI 配置见 [.github/workflows/ci.yml](.github/workflows/ci.yml)。

框架设计与实施规格保留在 [spec.go](spec.go)。`legacy/` 是独立的旧 module，不参与当前主构建；当前使用方式以用户文档为准。
