# 公开 API 与包装必要性审查（第一期）

| 包 | 是否新增包装 | 理由 |
|---|---|---|
| config | 是 | 按段解码、注入 Lookup、Secret 脱敏、拒绝 null |
| logging | 是（薄） | 仅 slog 装配与 context 字段，返回 `*slog.Logger` |
| i18n | 是（薄） | 分层合并/冻结/校验；仍暴露 go-i18n Bundle |
| fault | 是 | Code/Kind/公开参数；堆栈仍用 cockroachdb/errors |
| cleanup | 是 | 单所有者 LIFO 清理语义 |
| database | 薄装配 | 直接暴露 `*bun.DB` |
| database/migrate | 薄装配 | 直接暴露 `*goose.Provider` |
| httpx | 是（Serve） | 监听/排空协调与错误归并 |
| httpx/rest | 薄适配 | 延迟绑定 + 实例 Transformer；仍可用原生 `huma.Register` |
| command | 薄工厂 | 返回 `*cobra.Command` |
| identity | 是 | 中立 Subject |

未引入：CacheManager、通用 Repository、authz Gate、Router DSL、错误门面 `mygoravel/errors`。
旧实现保留在 `legacy/`，后期路线见 `legacy/README.md`。
