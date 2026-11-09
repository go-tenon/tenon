# 生产启动与排空

1. Release 步骤显式执行 `migrate up`；`serve` 默认不跑迁移。
2. 进程收到 SIGTERM/SIGINT 后：停止就绪 → `Shutdown` 排空请求 → 再关闭数据库/缓存。
3. `httpx.Serve` 返回后，才应执行 `cleanup.Stack.Close` 关闭依赖。
4. live（`/healthz` 类）只说明进程可响应；ready 探测必要依赖并设短超时。
5. 普通加速缓存故障默认不影响 ready。
