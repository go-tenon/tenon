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

此处需要标准库 `net/netip`。列表为空时保留直接连接的 RemoteAddr。TrustedProxies 填写应用实际使用的反向代理网段。

该策略处理客户端地址。公网域名、协议、TLS 和用户身份由相应的入口配置与认证组件处理。

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

普通 `http.Handler` 可以单独安装 `httpx.Middleware`：

```go
handler := httpx.Middleware(httpx.MiddlewareOptions{
    Logger: logger,
})(mux)
```

`mux` 是原生 `http.Handler`，`logger` 是应用日志器。同一请求链安装一次该中间件；`rest.New` 已完成安装。

<a id="cancellation"></a>

## 请求结束与取消

默认中间件设置请求 context 的 deadline。处理器同步执行，并主动响应取消、返回对应结果。服务器读写时限见[请求与校验](requests.md#limits)。

---

[上一篇：路由与 OpenAPI](routing.md) · [下一篇：请求与校验](requests.md)
