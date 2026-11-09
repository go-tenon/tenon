# HTTP 中间件

[文档首页](README.md) / HTTP 基础

`rest.New` 为 API 安装默认边界行为。你可以通过 Options 调整策略，并在需要时添加原生 chi 或 Huma 中间件。

本章内容：

- [默认行为](#defaults)
- [配置可信代理](#trusted-proxies)
- [添加 chi 中间件](#chi)
- [用于普通 HTTP Handler](#native-handler)
- [请求结束与取消](#cancellation)

<a id="defaults"></a>

## 默认行为

| 能力 | 默认行为 |
| --- | --- |
| 请求关联 | 生成请求 ID，写入 context 和 X-Request-ID |
| 访问日志 | 提供 Logger 时记录方法、结果、耗时和客户端地址 |
| panic 恢复 | 未写响应时返回通用 500；已开始响应时中止连接 |
| 安全头 | 设置 X-Content-Type-Options: nosniff |
| 正文限制 | 默认 1 MiB |
| 请求 context | 默认 30 秒 deadline |
| 转发地址 | 仅信任显式配置的代理网段 |
| 语言 | 提供 Languages 时协商 Accept-Language |

Bearer 认证按路由启用，健康检查由应用注册。日志器和语言目录都需要显式传入。

<a id="trusted-proxies"></a>

## 配置可信代理

只有来自可信代理的连接，才会按从右向左的顺序解析 `X-Forwarded-For`，跳过可信代理地址，选取首个不可信地址作为客户端地址：

```go
api := rest.New(rest.Options{
    Middleware: httpx.MiddlewareOptions{
        TrustedProxies: []netip.Prefix{
            netip.MustParsePrefix("10.20.0.0/16"),
        },
    },
})
```

此处需要标准库 `net/netip`。列表为空时保留直接连接的 RemoteAddr。填写实际反向代理的网段，避免把全部互联网地址都设为可信。

该策略处理客户端地址，不会自动推断公网域名、协议、TLS 或可信用户身份。

<a id="chi"></a>

## 添加 chi 中间件

如果要在默认 API 边界外添加自定义 HTTP 行为，先构造 chi Router 并安装中间件，再传入 `rest.New`：

```go
router := chi.NewRouter()
router.Use(func(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        w.Header().Set("X-Service", "notes")
        next.ServeHTTP(w, r)
    })
})
api := rest.New(rest.Options{Router: router})
```

需要 `github.com/go-chi/chi/v5` 和标准库 `net/http`。chi 要求在注册路由前安装这些中间件。上例的自定义中间件位于默认边界外，应自行处理其自身可能产生的错误。

路由级策略可放在原生 `huma.Operation.Middlewares` 中。认证策略使用[RequireBearer](authentication.md#protect-routes)，让运行行为与文档保持一致。

<a id="native-handler"></a>

## 用于普通 HTTP Handler

不使用 Huma 时，也可以单独使用 `httpx.Middleware`：

```go
handler := httpx.Middleware(httpx.MiddlewareOptions{
    Logger: logger,
})(mux)
```

`mux` 是原生 `http.Handler`，`logger` 是应用日志器。不要再用它包裹已经由 `rest.New` 安装同一中间件的 Router，以免生成重复日志或请求 ID。

<a id="cancellation"></a>

## 请求结束与取消

默认中间件只设置请求 context 的 deadline；处理器必须主动遵守取消。它不统一把所有超时改写成某个 HTTP 状态，也不会启动 goroutine 来强行中断处理器。服务器读写时限见[请求与校验](requests.md#limits)。

---

[上一篇：路由与 OpenAPI](routing.md) · [下一篇：请求与校验](requests.md)
