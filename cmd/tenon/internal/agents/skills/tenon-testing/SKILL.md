---
name: tenon-testing
description: 为 Tenon 应用写 Go 测试：业务动作的规则直接测 Handle，数据库用 dbtest 的临时库，HTTP 入口用 httptest 测认证、验证和错误响应，命令和 OpenAPI 文档的测试，以及用 synctest 控制时间。新增或修改业务动作、接口、命令之后使用。
---

# 测试

先读 `.tenon/docs/testing/testing.md`。测试分两层：业务规则直接调用业务动作的 `Handle`；认证、验证、错误响应这些入口的行为通过 HTTP 测试覆盖。

## 步骤

1. **测试文件按业务动作命名**，例如 `publish_test.go`。顶层测试的第一行调用 `t.Parallel()`；修改全局状态的测试（`t.Setenv`、`slog.SetDefault`）串行运行。
2. **业务规则**：`db := dbtest.Open(t, migrations.FS)` 建一个已执行迁移的临时库，构造业务动作，用不同的 `identity.Subject` 调用 `Handle`，用 `errors.Is(err, article.ErrForbidden)` 检查业务错误。见 `.tenon/docs/testing/testing.md#testing-actions`。
3. **HTTP 入口**：用 `rest.New(chi.NewRouter(), ...)` 建一个测试用的 API，传入固定的令牌校验函数，调用模块的 `RegisterRoutes`；用 `httptest` 发请求，检查状态码和错误码（`auth.unauthenticated`、`auth.forbidden`、字段的验证错误）。在 `Handle` 之前就会被拒绝的请求，`db` 可以传 `nil`。见 `.tenon/docs/testing/testing.md#http-tests`。
4. **命令**：见 `.tenon/docs/testing/testing.md#command-tests` 和 `.tenon/docs/testing/testing.md#testing-action-commands`。
5. **运行**：`go test ./...`。数据库测试需要 `POSTGRES_TEST_URL`，没有设置时直接失败，本机用 `compose.test.yml` 启动，见 `.tenon/docs/testing/testing.md#running-database-tests-locally`。

## 要点

- 与时间有关的逻辑用 `testing/synctest` 控制时间，见 `.tenon/docs/testing/testing.md#controlling-time`。SQL 中的时间要由 Go 传入，假时钟才对查询有效。
- 能在进程内模拟的外部服务就在进程内模拟：S3 用 gofakes3，HTTP API 用 `httptest`。框架各组件的测试工具（日志、出站请求、队列、邮件等）见 `.tenon/docs/testing/testing.md#test-helpers`。
- 每次判断都要查询数据库的测试只用少量样本；统计性质、边界值留给纯函数的测试。
- 接口文档的变化可以用测试锁住，见 `.tenon/docs/testing/testing.md#testing-openapi`。
- 前端的纯逻辑用 Vitest，关键流程用 Playwright，见 `.tenon/docs/frontend/conventions.md#checks`。
