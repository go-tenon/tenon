# 请求与资源生命周期

[文档首页](README.md) / 核心概念

应用入口拥有运行资源。它读取配置、打开连接、构造服务，然后开始监听；退出时先排空请求，再关闭请求所依赖的资源。

本章内容：

- [应用启动](#startup)
- [请求经过什么](#request-flow)
- [管理资源](#resources)
- [平滑停止 HTTP](#shutdown)
- [就绪检查](#health)

<a id="startup"></a>

## 应用启动

对于需要数据库的 API，可以按以下顺序编排：

1. 解码并校验当前入口所需配置。
2. 打开数据库等资源，每成功打开一项就登记关闭操作。
3. 构造业务服务，登记路由。
4. 创建 listener，调用 httpx.Serve。

不需要某项资源时就省略对应步骤。迁移应由独立发布步骤执行，不放进普通 HTTP 启动流程。

<a id="request-flow"></a>

## 请求经过什么

rest.New 提供的默认边界顺序是：

```text
可信代理地址 → 请求 ID → 访问日志 → panic 恢复
→ 安全头与大小限制 → 语言选择 → 按需 Bearer 校验
→ Huma 输入校验 → 业务服务 → 响应转换
```

业务错误在 HTTP 边界转换。日志器和语言目录由应用显式传入，不同实例互不共享这些策略。

<a id="resources"></a>

## 管理资源

资源很少时可以直接使用 defer。需要保留多个关闭错误时，可以使用 cleanup.Stack：

```go
package bootstrap

import (
    "context"

    "github.com/ryan053857/mygoravel/cleanup"
    "github.com/ryan053857/mygoravel/database"
    "github.com/uptrace/bun"
)

func WithDatabase(ctx context.Context, cfg database.Config,
    run func(*bun.DB) error,
) (err error) {
    var stack cleanup.Stack
    defer func() {
        err = stack.CloseWith(context.Background(), err)
    }()

    db, err := database.Open(ctx, cfg)
    if err != nil {
        return err
    }
    stack.MustAdd("database", func(context.Context) error {
        return db.Close()
    })
    return run(db)
}
```

后登记的资源先关闭；后续构建失败时，之前的资源仍会释放。CloseWith 保留运行错误和关闭错误，重复关闭不会再次释放。

Stack 由入口串行使用。清理使用独立有限的 context，未提供 deadline 时预算为 10 秒；清理函数必须遵守该 context。

<a id="shutdown"></a>

## 平滑停止 HTTP

httpx.Serve 接收原生 server 和已经创建的 listener：

```go
return httpx.Serve(ctx, server, listener, httpx.ServeOptions{
    ShutdownTimeout: 10 * time.Second,
    Logger:          logger,
    OnDraining:      draining.Set,
})
```

这里 draining 是 httpx.Draining。收到取消信号或监听异常时，Serve 先调用 OnDraining，再执行 Shutdown；请求根 context 在排空结束或超时后才取消。

超时后关闭连接。预算为 0 时立即取消并关闭。Serve 返回后，入口才关闭数据库等资源。业务查询和外部调用必须使用请求 context，框架不能强制结束忽略取消的 goroutine。

<a id="health"></a>

## 就绪检查

健康路径由应用注册。存活检查可以只说明 HTTP 进程能响应；就绪检查可以读取 draining.Load，并以短超时检查实际必需的依赖。

例如，把下面的 handler 注册为 `/readyz`；其中 db 是已打开的数据库，draining 与 ServeOptions 使用同一个对象：

```go
api.Router().Get("/readyz", func(w http.ResponseWriter, r *http.Request) {
    ctx, cancel := context.WithTimeout(r.Context(), time.Second)
    defer cancel()
    if draining.Load() || db.PingContext(ctx) != nil {
        httpx.WriteProblem(w, http.StatusServiceUnavailable,
            "error.unavailable", httpx.RequestID(r.Context()))
        return
    }
    w.WriteHeader(http.StatusNoContent)
})
```

可丢弃的查询缓存通常不属于 ready 的必需依赖。具体路径、依赖集合和探测预算由应用决定。

---

[上一篇：目录结构与依赖](directory-structure.md) · [下一篇：路由与 OpenAPI](routing.md)
