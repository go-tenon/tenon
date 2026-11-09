# 第一期支持矩阵

| 组件 | 版本 / 约束 |
|---|---|
| Go | 与 `go.mod` 中 `go` 行一致（当前 1.27.x） |
| PostgreSQL | 18+ |
| Redis（可选） | 7.x（仅缓存示例/集成） |
| chi | v5 |
| Huma | v2.40.x |
| Bun + pgx | bun v1.3.x / pgx v5.12.x |
| goose | v3.29.x |
| Cobra | v1.10.x |
| go-i18n | v2.7.x |
| cockroachdb/errors | v1.14.x |
| eko/gocache | lib/store v4.x |

不声称任意上游版本均可组合；升级依赖须跑对应契约与 integration 测试。
