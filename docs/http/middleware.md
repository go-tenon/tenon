# 中间件

HTTP 中间件是 `func(http.Handler) http.Handler`，安装在 chi Router 上，再把 Router 交给 `rest.New`。这一页讲默认中间件、限流、跨域、Basic 认证、App 最低版本、响应压缩、自定义中间件和 `http` 段的配置。

包：`httpx`。

- 中间件是否生效只取决于代码中有没有安装它，配置文件只提供参数；参数不合法时（例如超时为 `0`）构造中间件就 panic。
- 先安装中间件再注册路由：chi 不允许在注册路由之后调用 `router.Use`。

<a id="default-middleware"></a>
## 示例：安装 `httpx.Defaults`

`cfg.HTTP` 是应用根配置中的 `httpx.Config`，见[配置](#http-configuration)：

```go
router := chi.NewRouter()
router.Use(httpx.Defaults(cfg.HTTP)...) // 有自己的翻译时加 httpx.Languages(languages)
router.Use(httpx.Timeout(cfg.HTTP.Timeout))

api := rest.New(router, rest.Options{})
```

`httpx.Defaults` 按执行顺序返回下面的中间件，日志写到 `slog.Default()`。常用选项包括 `httpx.Languages`（见[本地化](../general/localization.md#enabling-localization)）、`httpx.BodyLimitFor`（按路径覆盖请求体上限）、`httpx.BeforeAccessLog`（在访问日志前插入脱敏中间件）、`httpx.WithCompress`（启用响应压缩）与 `httpx.WithContext`（为请求包装上下文，例如 `database.TrackPrimary`）。

| 中间件 | 行为 |
| --- | --- |
| `AssignRequestID` | 确定请求 ID，见[请求 ID](#request-id) |
| `NoSniff` | 设置 `X-Content-Type-Options: nosniff` |
| `ClientIP` | 从可信代理的 `X-Forwarded-For` 取客户端地址，见[可信代理](#trusted-proxies) |
| `Locale` | 按 `Accept-Language` 选择语言，见[本地化](../general/localization.md#determining-the-locale) |
| `AccessLog` | 每个请求一条访问日志，见[访问日志](#access-log) |
| `Recover` | panic 时记录堆栈；还没写出响应时返回 500，否则中止连接 |
| `BodyLimit` | 请求体超过 `body_limit.max_bytes` 时返回 413 |
| `AcquireTimeout` | 等待空闲数据库连接超过 `acquire_timeout` 时返回 503，见[连接用满时](../database/database.md#pool-exhaustion) |

每一个都可以单独安装或省略：没有 `AccessLog` 时 `httpx.AddAccessLogAttrs` 不做任何事，没有 `Locale` 时 `i18n.T` 原样返回消息 ID。认证按路由启用，见[认证](../security/authentication.md#protecting-routes)；请求的指标和链路用 `httpx.Telemetry`，见[可观测性](../general/telemetry.md#http)。

<a id="custom-middleware"></a>
## 自定义中间件

<a id="chi-middleware"></a>
### 全局中间件：`router.Use`

自己写的中间件装在 `httpx.Defaults` 之后，被它拒绝的请求也有请求 ID 和访问日志。例如维护期间拒绝写请求，`readOnly` 是由运维开关切换的 `atomic.Bool`：

```go
router.Use(httpx.Defaults(cfg.HTTP)...)
router.Use(func(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        if readOnly.Load() && r.Method != http.MethodGet && r.Method != http.MethodHead {
            httpx.WriteProblem(w, http.StatusServiceUnavailable, "read-only mode")
            return
        }
        next.ServeHTTP(w, r)
    })
})
```

`httpx.WriteProblem` 在 Huma 之外写出与 API 相同格式的错误响应。装在子路由（`router.Route`、`router.With`）上的中间件只对这个子路由生效。

<a id="route-middleware"></a>
### 路由中间件：`Operation.Middlewares`

只对一条路由生效的逻辑写在 Huma 的 `Operation.Middlewares` 中，它在 Router 的中间件之后、处理器之前执行：

```go
op := huma.Operation{OperationID: "post-get", Method: http.MethodGet, Path: "/posts/{id}", Deprecated: true}
op.Middlewares = append(op.Middlewares, func(ctx huma.Context, next func(huma.Context)) {
    ctx.SetHeader("Deprecation", "true")
    next(ctx)
})
```

认证用 [`rest.RequireToken`](../security/authentication.md#protecting-routes)，它同时在 OpenAPI 文档中标记路由需要认证。

<a id="request-timeout"></a>
## 请求超时：`httpx.Timeout`

`httpx.Timeout(cfg.HTTP.Timeout)` 为请求的 context 设置超时（`http.timeout.duration`，默认 30 秒）。超时后 context 被取消，传入了 `ctx` 的查询和外部调用随之停止，处理器返回的错误决定响应。

它不在 `httpx.Defaults` 中，可以只装在部分路由组上；需要更长时间的接口放在超时更长的路由组中。[服务端推送](sse.md)开始推送后脱离这个超时。

<a id="rate-limiting"></a>
## 限流：`httpx.RateLimit`

配额用完时返回 `429` 和 `Retry-After`，限流器出错时返回 `503`。装在 `httpx.Defaults` 之后，`KeyByIP` 才能读到 `ClientIP` 解析出的地址：

```go
router.Use(httpx.RateLimit(ratelimit.NewMemory(cfg.HTTP.RateLimit), httpx.KeyByIP))
```

自定义 key、按用户限流、多实例共享计数见[限流](../libraries/rate-limiting.md)。

<a id="cors"></a>
## 跨域请求：`httpx.CORS`

单页应用、桌面或移动端的 WebView 与 API 不同源时安装。只装在 API 的路由组上，并且在认证、限流之前：预检请求（`OPTIONS`）直接以 `204` 应答，不进入它们。

```go
router.Route("/api", func(r chi.Router) {
    r.Use(httpx.CORS(cfg.HTTP.CORS))
    // 认证、限流和路由
})
```

```yaml
http:
  cors:
    allowed_origins: ["https://app.example.com", "https://*.example.com", "capacitor://localhost"] # scheme://host[:port]，最多一个 *；默认 ["*"]
    expose_headers: [X-Total-Count] # X-Request-ID、Content-Language、Retry-After、Location、Content-Disposition 之外的响应头
    max_age: 2h                     # 浏览器缓存预检结果的时间，Chrome 最多 2 小时
```

- 跨域请求不携带凭据：[会话 cookie](protocol.md#sessions) 只随同源请求发送，跨域的调用方用 `Authorization: Bearer`，所以允许任何来源也是安全的。
- 请求头按预检声明的原样允许。`401`、`429` 等错误响应也带 CORS 头。

<a id="basic-auth"></a>
## HTTP Basic 认证：`httpx.BasicAuth`

内部文档、运维页面这类不接入账号体系的页面用它保护。`cfg.Ops` 是“用户名 → [密码哈希](../security/encryption.md)”的配置项，`hasher` 是 `hashing.Hasher`，`opsLimiter` 是 `ratelimit.Limiter`：

```go
verify := func(_ context.Context, user, password string) (bool, error) {
    hash, ok := cfg.Ops[user]
    return ok && hasher.Check(password, hash), nil
}
ops := router.With(
    httpx.RateLimit(opsLimiter, httpx.KeyByIP), // 限制暴力尝试
    httpx.BasicAuth("ops", verify),
)
```

- `verify` 返回 `false` 或没有凭据时返回 `401` 和 `WWW-Authenticate: Basic`；返回错误时返回 `503` 并记录 Error 日志。
- 认证通过后 `httpx.BasicUser(ctx)` 返回用户名。只在 HTTPS 下使用：每个请求都带明文密码。

> [!WARNING]
> 浏览器给跨站请求也带上 Basic 凭据。页面中有修改数据的请求时，用 `http.CrossOriginProtection` 拒绝跨站请求。

<a id="app-version"></a>
## App 最低版本：`httpx.AppVersion`

App 在每个请求中带 `X-App-Version: ios/1.4.2`，版本低于该平台的最低版本时返回 `426`，错误码 `app.upgrade_required`。线路格式见[客户端协议](protocol.md#app-version)，App 端见[客户端代码](clients.md#app-version)。

```go
router.Route("/api", func(r chi.Router) {
    r.Use(httpx.CORS(cfg.HTTP.CORS))
    r.Use(httpx.AppVersion(cfg.HTTP.AppVersion)) // 在 CORS 之后，WebView 才能读到 426
})
```

```yaml
http:
  app_version:
    minimum: # 平台名小写，由 App 决定；没有列出的平台不限制
      ios: 1.4.0
      android: 1.3.0
```

- 没有这个请求头的请求放行（浏览器、第三方调用方），格式错误时返回 `400`。
- 按语义化版本比较：`1.4` 等于 `1.4.0`，`1.4.0-beta.1` 低于 `1.4.0`。
- 访问日志中有 `app` 字段，提高最低版本前用它统计旧版本的用户。

> [!IMPORTANT]
> App 从第一个版本起就发送这个请求头：没有带它的旧版本无法被拦截。

<a id="compression"></a>
## 响应压缩：`httpx.Compress`

应用前面没有负责压缩的反向代理或 CDN 时，装在 `httpx.Defaults` 之后：

```go
router.Use(httpx.Compress())
```

只压缩 1 KiB 以上的文本类响应（`text/*`、JSON、JavaScript、XML、SVG、WebAssembly），客户端接受 zstd 时用 zstd，否则用 gzip。[服务端推送](sse.md)、WebSocket 握手和条件请求的 `304` 照常工作。

<a id="custom-stack"></a>
## 自行组装中间件

默认组合不合适时直接挑选单个中间件。例如 `/uploads/` 允许 20 MiB 的请求体：`BodyLimit` 装上之后内层无法放宽，所以在安装时按路径选择。

```go
router.Use(
    httpx.AssignRequestID(cfg.HTTP.RequestID),
    httpx.NoSniff(),
    httpx.ClientIP(cfg.HTTP.ClientIP),
    httpx.Locale(nil), // nil 时只用框架内置的消息
    httpx.AccessLog(cfg.HTTP.AccessLog, nil),
    httpx.Recover(nil),
    func(next http.Handler) http.Handler {
        small := httpx.BodyLimit(cfg.HTTP.BodyLimit)(next)
        large := httpx.BodyLimit(httpx.BodyLimitConfig{MaxBytes: 20 << 20})(next)
        return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
            if strings.HasPrefix(r.URL.Path, "/uploads/") {
                large.ServeHTTP(w, r)
                return
            }
            small.ServeHTTP(w, r)
        })
    },
    httpx.AcquireTimeout(cfg.HTTP.AcquireTimeout),
)
```

- 上传路由还要设置相同的 `Operation.MaxBodyBytes`，见[请求体大小](requests.md#body-size)。
- `AccessLog` 装在 `Recover` 之外，panic 产生的 500 才会出现在访问日志中。
- 用在 `rest` 之外的 `http.Handler` 上时用 `chi.Chain(httpx.Defaults(cfg.HTTP)...).Handler(mux)`。

<a id="http-configuration"></a>
## 配置

`httpx.Config` 对应 `http` 段：顶层是服务器参数，每个有参数的中间件对应一个同名子段。下面是默认值。

```yaml
http:
  addr: ${HTTP_ADDR:-:8080} # host:port
  read_header_timeout: 5s
  read_timeout: 30s
  write_timeout: 30s        # SSE 推送时不受它限制
  idle_timeout: 1m
  drain_delay: 0s           # 停机时先让 /readyz 返回 503、等负载均衡摘流再关监听的时间（热重载交接时跳过）
  shutdown_timeout: 10s     # 停机时排空请求的上限
  acquire_timeout: 1s       # 等待空闲数据库连接的上限；0 表示等到请求超时
  request_id:
    header: X-Request-ID    # 见请求 ID
  client_ip:
    trusted_proxies: []     # 见可信代理
  access_log:
    ignore_paths: []        # 见访问日志
  timeout:
    duration: 30s           # httpx.Timeout
  body_limit:
    max_bytes: 1048576      # 1 MiB
  rate_limit: {rate: 10, interval: 1s, burst: 20} # httpx.RateLimit
  # cors 见跨域请求，app_version 见 App 最低版本
```

- 除 `drain_delay` 和 `acquire_timeout`（允许为 `0`）外，超时和数值都必须为正数，否则加载配置时报错。
- `httpx.Config` 放在应用的根配置结构体中，由 `config.Load` 填充默认值并校验。测试中从 `httpx.DefaultConfig()` 开始修改：`httpx.Config{}` 交给 `httpx.Defaults` 会 panic。

<a id="request-id"></a>
### 请求 ID

`AssignRequestID` 沿用入站请求头中合法的 ID（1–128 个可打印 ASCII 字符，不含空格），否则生成一个。ID 写入 `X-Request-ID` 响应头，并作为这个请求所有日志的 `request_id` 字段，用 `logging.RequestID(ctx)` 读取，见[调用关联](../general/logging.md#request-correlation)；chi 的 `middleware.GetReqID(ctx)` 读到同一个值。

沿用反向代理生成的 ID，代理日志和应用日志就能关联。代理用其他请求头时设置 `http.request_id.header`：

| 代理 | `header` |
| --- | --- |
| Nginx（`proxy_set_header X-Request-ID $request_id`）、Envoy、Istio、Traefik | 默认的 `X-Request-ID` |
| AWS ALB | `X-Amzn-Trace-Id` |
| Google Cloud 负载均衡 | `X-Cloud-Trace-Context` |
| Cloudflare | `CF-Ray` |

<a id="trusted-proxies"></a>
### 可信代理

`ClientIP` 只在直连地址属于 `http.client_ip.trusted_proxies` 时读取 `X-Forwarded-For`：从右向左跳过可信代理，第一个不可信的地址写入 `r.RemoteAddr`。没有配置时日志中的客户端地址都是代理的地址。

```yaml
http:
  client_ip:
    trusted_proxies: [10.20.0.0/16, fd00::/64] # 主机位全为零；单台机器写 10.20.0.5/32
```

`10.20.0.5/16` 这样主机位不为零的写法在加载配置时报错。

> [!WARNING]
> 只填写实际使用的代理网段。把 `0.0.0.0/0` 设为可信，任何客户端都能伪造自己的地址。

<a id="access-log"></a>
### 访问日志

`AccessLog` 基于 [slog-chi](https://github.com/samber/slog-chi)，每个请求结束后记录一条消息为 `http request` 的日志：

```json
{"level":"WARN","msg":"http request","component":"http","request":{"method":"PUT","route":"/articles/{id}","ip":"203.0.113.7"},"response":{"status":409,"latency":32209},"code":"article.slug_taken","request_id":"9F2K…"}
```

- `5xx` 记为 `ERROR`，`4xx` 记为 `WARN`，其余为 `INFO`；用 `logs.levels.http` 单独调整级别，见[按组件设置级别](../general/logging.md#component-levels)。
- `request.route` 是路由模板，用于按接口聚合。业务错误码是顶层的 `code`，缺少翻译时另有 `"untranslated":true`。
- 处理器用 `httpx.AddAccessLogAttrs(ctx, attrs...)` 追加字段，同名字段后写的覆盖先写的。

```yaml
http:
  access_log:
    ignore_paths: [/livez, /readyz] # 精确匹配，以 / 开头
    user_agent: false
    request_header: false   # Authorization、Cookie 等认证头始终隐藏
    response_header: false  # Set-Cookie 始终隐藏
    request_body: false     # 最多 64 KiB
    response_body: false    # 最多 64 KiB
```

其他字段的脱敏见[敏感信息脱敏](../general/logging.md#redacting-sensitive-data)。
