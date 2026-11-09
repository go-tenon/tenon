# 生产启动与排空

1. Release 步骤显式执行 `migrate up`；`serve` 默认不跑迁移。
2. 进程收到 SIGTERM/SIGINT 后：停止就绪 → `Shutdown` 排空请求 → 再关闭数据库/缓存。
3. `httpx.Serve` 返回后，才应执行 `cleanup.Stack.Close` 关闭依赖。
4. live（`/livez`）只说明进程可响应；ready（`/readyz`）探测必要依赖并设短超时。
5. 普通加速缓存故障默认不影响 ready。

监听意外退出也进入相同排空流程。排空超时取消请求上下文并 Close，服务/驱动必须遵守 context；
无法强杀不响应取消的任意 Go handler。停止预算为零时立即取消并关闭。

默认请求日志不记录 URL 查询、Authorization 或原始请求体；错误诊断只输出稳定 code 和安全堆栈。
缓存读取失败可回源，过期/失效不承诺强一致；安全相关数据不得照搬这个查询缓存策略。

CI 首次发布前比较示例 fixture；发布后设置仓库变量 MIGRATION_BASELINE_REF 为明确的发布 tag，
`scripts/check-published-migrations.sh` 检查该 tag 的已发布迁移仍存在且字节不变。
