# 可选的中间件

[中间件](middleware.md)一页介绍了每个应用都应该安装的默认中间件。这一页介绍另外几个按需安装的中间件：限流、跨域请求、HTTP Basic 认证、App 最低版本和响应压缩。它们彼此独立，用到哪个读哪一节即可。

阅读前需要了解：[中间件](middleware.md)。

- [限流](#rate-limiting)
- [跨域请求](#cors)
- [HTTP Basic 认证](#basic-auth)
- [App 最低版本](#app-version)
- [响应压缩](#compression)
- [常见问题](#troubleshooting)

<a id="rate-limiting"></a>
## 限流

`httpx.RateLimit` 按客户端限制请求频率，超出时返回 `429`。它不在默认中间件中，需要时在 `httpx.Defaults` 之后安装：

```go
router.Use(httpx.Defaults(cfg.HTTP)...)
router.Use(httpx.RateLimit(ratelimit.NewMemory(cfg.HTTP.RateLimit), httpx.KeyByIP))
```

参数来自 `http.rate_limit` 段。限流的 key、配额参数、在处理器中按用户限流以及多实例共享计数，见[限流](../libraries/rate-limiting.md)。

<a id="cors"></a>
## 跨域请求

独立部署的单页应用、桌面或移动端的 WebView 与 API 不在同一个源时，浏览器要求 API 返回 CORS 响应头。`httpx.CORS` 只安装在 API 的路由组上，页面不开放跨域：

```go
router.Route("/api", func(r chi.Router) {
    r.Use(httpx.CORS(cfg.HTTP.CORS))
    // ...
})
```

参数来自 `http.cors` 段：

```yaml
http:
  cors:
    allowed_origins: ["https://app.example.com", "https://*.example.com", "capacitor://localhost"]
    expose_headers: [X-Total-Count]
    max_age: 2h
```

| 字段 | 说明 | 默认值 |
| --- | --- | --- |
| `allowed_origins` | 允许的来源，`scheme://host[:port]`，可以有一个 `*` 通配；单独的 `*` 允许任何来源 | `["*"]` |
| `expose_headers` | 还要让前端读取的响应头，`X-Request-ID`、`Content-Language`、`Retry-After`、`Location`、`Content-Disposition` 总是暴露 | 空 |
| `max_age` | 浏览器缓存预检结果的时间，Chrome 最多缓存 2 小时 | `2h` |

跨域请求一律不携带凭据：浏览器的[会话 cookie](../security/api-tokens.md#browser-sessions) 只随同源请求发送，跨域的调用方用 `Authorization: Bearer` 认证。其他网站的网页拿不到用户的 token，所以允许任何来源也是安全的。请求头按预检请求声明的原样允许，`Authorization` 自动包含在内。

预检请求（`OPTIONS`）直接以 `204` 应答，不进入路由、认证和限流，所以要把 `CORS` 安装在它们之前。`401`、`429` 等错误响应同样带有 CORS 头，前端能读到错误内容。

<a id="basic-auth"></a>
## HTTP Basic 认证

内部文档这类给运维和团队成员看的页面，不值得接入应用的账号体系，用 `httpx.BasicAuth` 保护：浏览器弹出登录框，之后的请求自动带上用户名和密码。

账号写在配置中，密码保存为[哈希](../security/hashing.md)，用 `hashing.Hasher` 生成和校验：

```go
type Config struct {
    // ……
    Ops map[string]string `yaml:"ops"` // 运维账号：用户名 → 密码哈希
}

verifyOps := func(_ context.Context, user, password string) (bool, error) {
    hash, ok := cfg.Ops[user]
    return ok && hasher.Check(password, hash), nil
}
```

```go
ops := router.With(
    httpx.RateLimit(opsLimiter, httpx.KeyByIP), // 限制暴力尝试，见限流
    httpx.BasicAuth("ops", verifyOps),
)
```

- 没有凭据或校验不通过时返回 `401` 和 `WWW-Authenticate: Basic realm="ops"`；`verify` 返回错误时返回 `503`，记录一条 Error 日志。
- 认证通过后，`httpx.BasicUser(ctx)` 返回用户名，用来在日志中记下操作者。
- Basic 认证在每个请求中发送明文密码，只能用于 HTTPS。
- 浏览器会给跨站请求自动带上 Basic 凭据：页面中有 `POST` 等修改数据的请求时，用 `http.CrossOriginProtection` 拒绝跨站请求。

<a id="app-version"></a>
## App 最低版本

装在手机和电脑上的 App 升级有快有慢。接口做了旧版本处理不了的变更，或者旧版本有安全问题时，要让它们停止调用接口，提示用户升级。`httpx.AppVersion` 读取 App 在每个请求中带的版本：

```http
X-App-Version: ios/1.4.2
```

版本低于该平台的最低版本时返回 `426`：

```http
HTTP/1.1 426 Upgrade Required
Content-Type: application/problem+json

{"title": "Upgrade Required", "status": 426, "detail": "当前版本过低，请升级到 1.4.0 或更高版本", "code": "app.upgrade_required"}
```

App 收到 `app.upgrade_required` 时显示升级页面，打开应用商店或开始自动更新，见[客户端协议](protocol.md#app-version)。请求头格式错误时返回 `400`。没有这个请求头的请求放行，例如浏览器、单页应用和用 [API token](../security/api-tokens.md) 调用的第三方；没有设置最低版本的平台也放行。

`AppVersion` 只安装在 API 的路由组上，放在 `CORS` 之后，WebView 中的页面才能读到 `426` 的内容：

```go
router.Route("/api", func(r chi.Router) {
    r.Use(httpx.CORS(cfg.HTTP.CORS))
    r.Use(httpx.AppVersion(cfg.HTTP.AppVersion))
    // ...
})
```

最低版本按平台写在 `http.app_version` 段，平台名由 App 自己决定，不区分大小写：

```yaml
http:
  app_version:
    minimum:
      ios: 1.4.0
      android: 1.3.0
```

版本按[语义化版本](https://semver.org/lang/zh-CN/)比较：`1.4` 等于 `1.4.0`，`1.10.0` 高于 `1.9.0`，`1.4.0-beta.1` 低于 `1.4.0`，`+` 之后的构建号不参与比较。修改最低版本后[不停机升级](../installation/operations.md#zero-downtime-upgrades)即可生效。

带这个请求头的请求，访问日志中有 `app` 字段，例如 `"app":"ios/1.4.2"`。提高最低版本之前，先按它统计还有多少用户在用旧版本。

**这个请求头要从 App 的第一个版本就开始发送。** 没有带它的旧版本无法被拦截，只能等用户自己升级，见[客户端代码](clients.md#app-version)。

<a id="compression"></a>
## 响应压缩

应用直接对外提供服务、前面没有负责压缩的反向代理或 CDN 时，在 `httpx.Defaults` 之后安装 `httpx.Compress`：

```go
router.Use(httpx.Defaults(cfg.HTTP)...)
router.Use(httpx.Compress())
```

浏览器同时接受两种编码时使用 zstd，否则使用 gzip。JSON、HTML 片段和样式表通常压缩到原来的几分之一。

只有文本类响应会被压缩：`text/*`、JSON、JavaScript、XML、SVG 和 WebAssembly。图片、字体、PDF、压缩包等原样发送。以下响应也不压缩：

- 小于 1 KiB 的响应；
- 已经带 `Content-Encoding` 的响应；
- `Range` 请求的部分响应。

[服务端推送](sse.md)和 [WebSocket](../libraries/realtime.md) 照常工作：`text/event-stream` 直接写出，每条事件立即送达；带 `Upgrade` 请求头的握手原样交给处理器。静态资源的 `ETag` 保持不变，浏览器协商时照常得到 `304`。

<a id="troubleshooting"></a>
## 常见问题

**浏览器报跨域错误，但服务端已经安装了 `CORS`**

`CORS` 要安装在路由、认证和限流之前，预检请求（`OPTIONS`）才能直接应答，见[跨域请求](#cors)。

**跨域请求带不上 cookie**

这是预期的：会话 cookie 只随同源请求发送，跨域的调用方用 `Authorization: Bearer`，见[跨域请求](#cors)。

**内部页面返回 503，而不是要求输入密码**

Basic 认证的 `verify` 函数返回了错误，日志中有一条 Error 记录，见 [HTTP Basic 认证](#basic-auth)。

**App 收到 426**

App 的版本低于这个平台的最低版本，显示升级页面即可。请求头格式错误时返回 400，见 [App 最低版本](#app-version)。
