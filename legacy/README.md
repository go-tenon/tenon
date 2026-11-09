# legacy：第一期构建外的旧实现快照

本目录是独立 Go module（`github.com/ryan053857/go-kit`），**不属于** `github.com/ryan053857/mygoravel` 的第一期主构建。

## 为何归档

`spec.go` 第一期只交付常规 HTTP API 路径。下列能力与旧装配方式退出本期目标构建，仅作需求与场景参考，后续按真实需要再整合：

| 归档内容 | 退出原因 |
|---|---|
| `app/` | 旧装配层 / Scope 生命周期，由显式 bootstrap + `cleanup`/`command` 替代 |
| `modules/*` | 七个业务模块整体重写不是第一期完成条件；示例业务放在 `examples/api` |
| `examples/starter` | 页面、会话、templ/HTMX、完整账号产品属于后期 Web 形态 |
| `httpx/web`、`httpx/auth` 中的会话/页面路径 | Cookie 会话、CSRF、页面渲染属后期 |
| `messaging/` | Watermill 队列与 worker 属后期 |
| `mail/`、`storage/` | 外部 I/O 集成按需后期加入 |
| `support/` | 泛用工具收纳包，新框架不保留 |
| `authz/` | 通用 Gate/策略注册表不进入第一期；授权用业务函数 |
| `logger/` | 将由 `logging` 按 slog 装配约定重写，不保留旧门面 |
| `config/`、`database/`、`i18n/`、`httpx` 其余部分 | API 不兼容，由对应实施 PR 按规格重写 |
| `docs/design/` | 旧产品化设计笔记，仅作参考 |

## 使用方式

```bash
cd legacy
go test ./...
```

主仓库 CI **不**编译或测试本目录。不要从 `mygoravel` 包 import 本目录。
