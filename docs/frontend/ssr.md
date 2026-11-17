# 服务端渲染

页面由 [TanStack Start](https://tanstack.com/start) 在 Bun 中渲染：公开页面在服务端渲染，搜索引擎可以收录，首屏更快；后台页面可以只在浏览器中渲染。Bun 编译出的 SSR 可执行文件嵌入 Go 程序，由 `ssr` 包从内存中启动和托管，部署时仍然只有一个文件、一个进程树。

阅读前需要了解：[单页应用](spa.md)；[部署](../installation/deployment.md)。

- [结构](#architecture)
- [装配](#wiring)
- [启动与停止](#lifecycle)
- [安全](#security)
- [链路与日志](#observability)
- [开发](#development)
- [渲染打印模板](#rendering)
- [与 SSR 进程的约定](#contract)
- [限制](#limitations)
- [API 参考](#api-reference)

<a id="architecture"></a>
## 结构

```
浏览器 ──► Go（唯一的 HTTP 入口）
            ├─ /api/* ── Go 处理
            └─ 页面 ── unix socket ──► Bun（SSR 进程）
                                        └─ 渲染需要数据时 ── 内部 unix socket ──► Go 的接口
```

- **Go 是唯一的入口**：请求 ID、链路、访问日志、限流、安全响应头只在 Go 中处理一次。页面请求转给 Bun，流式转发，不缓冲。
- **Bun 只负责渲染**：不连接数据库，不持有密钥。渲染时需要的数据，经内部 socket 调用 Go 的接口，和浏览器调用的是同一组接口。
- **一个部署单元**：SSR 可执行文件用 `go:embed` 嵌入，启动时写入内存文件（memfd）执行，不落盘；Go 被强制结束时它随之结束。

两个 socket 都是 Linux 的抽象 unix socket，不在文件系统中留下文件。

<a id="wiring"></a>
## 装配

```go
pages, err := ssr.New(ssr.Options{
    Executable: web.SSR,             // go:embed 嵌入的 SSR 可执行文件
    API:        api,                 // Bun 渲染时调用的接口，通常就是对外的接口路由
    DevServer:  cfg.DevServers["web"], // 只在 tenon dev 时有值
})
if err != nil {
    return err
}
router.Mount("/api", api)
router.Handle("/*", pages.Handler())
return pages.Run(ctx, func(ctx context.Context) error {
    return httpx.ListenAndServe(ctx, cfg.HTTP, router)
})
```

`Handler` 挂在接口之后，处理其余全部请求。`Run` 包住对外的 HTTP 服务，在它前后管理 SSR 进程，应用不需要自己安排启动和停止的顺序。

<a id="lifecycle"></a>
## 启动与停止

`Run` 的顺序：

1. 在内部 socket 上提供 `Options.API`（设置了时），启动 SSR 进程，等它的 socket 可以连接。超过 `ReadyTimeout`（默认 30 秒）还没就绪时返回错误，不开始接收请求。
2. 调用传入的函数，开始接收请求。SSR 进程意外退出时按退避重启：第一次等 100 毫秒，之后翻倍，最多 5 秒；连续运行超过 10 秒后从头计算。重启期间页面请求返回 502 和 `Retry-After: 1`，接口不受影响。
3. 收到停止信号后，传入的函数先排空对外的请求，进行中的页面渲染完成后才返回，见[平滑停止](../installation/deployment.md#graceful-shutdown)；然后向 SSR 进程发送 SIGTERM，超过 `StopTimeout`（默认 10 秒）再强制结束；最后关闭内部入口。

`Ready` 报告 SSR 进程是否可以接收请求，可以放进就绪检查。指标 `ssr.restarts` 记录意外退出后重启的次数。

<a id="security"></a>
## 安全

- **内容安全策略**：Go 为每个页面请求生成一个 nonce，经请求头交给 Bun，Bun 把它写到内联脚本上；HTML 响应带上 `Content-Security-Policy`，默认是 `DefaultCSP`：脚本只能来自本站或带这个 nonce，不允许被其他网站嵌入。用 `Options.CSP` 定制。客户端伪造的 nonce 请求头会被覆盖。
- **内部入口**：抽象 socket 没有文件权限，同一网络命名空间中的进程都能连接，所以内部入口只接受与 Go 进程同一用户的连接（按 `SO_PEERCRED` 检查）。它提供的是同一组接口，认证照常进行，不会因为来自内部而放宽。

<a id="observability"></a>
## 链路与日志

- Go 把请求 ID（`X-Request-ID`）和 W3C `traceparent` 转交给 Bun，Bun 调用接口时原样带回：页面请求、渲染和它调用的接口在同一条链路中，日志带同一个请求 ID。
- Bun 在响应头 `X-Tenon-Route` 中报告匹配到的前端路由模板，例如 `/articles/$id`；Go 用它命名 span，例如 `GET /articles/$id`，而不是挂载页面处理器的 `/*`。这个头不会发给浏览器。
- SSR 进程的标准输出和标准错误逐行写入 Go 的日志，带 `component=ssr`；标准错误记为 WARN。

<a id="development"></a>
## 开发

`Options.DevServer` 不为空时（`tenon dev` 设置），不启动 SSR 进程，页面请求转给前端的开发服务器，包括热更新的 WebSocket。开发服务器注入的内联脚本没有 nonce，所以开发模式不设置 CSP；请求 ID 和链路照常转交。

<a id="rendering"></a>
## 渲染打印模板

`Render(ctx, name, data)` 用 SSR 进程中名为 `name` 的打印模板渲染 `data`，返回完整的 HTML，通常交给 [`pdf`](../libraries/pdf.md) 生成 PDF。`data` 编码为 JSON 传给模板，请求 ID 和链路照常转交；模板写法见[生成 PDF：编写模板](../libraries/pdf.md#writing-templates)。

```go
html, err := pages.Render(ctx, "invoice", invoice)
```

- 打印模板只用传入的数据渲染，不调用接口，所以队列任务中没有用户的 cookie 也能使用。队列 worker 同样用 `Run` 包住自己的运行函数，不提供接口时 `Options.API` 留空。
- 模板的地址是 `POST /__tenon/print/{name}`，只供 `Render` 调用：对外的页面请求访问 `/__tenon/` 下的地址时返回 `404`。
- 没有这个模板时返回 `ErrUnknownTemplate`；SSR 进程没有就绪（`Run` 之外、重启期间）时返回错误。开发模式下请求开发服务器。

<a id="contract"></a>
## 与 SSR 进程的约定

[`@go-tenon/start`](start.md) 已经按这些约定实现，应用不需要自己处理。自己编写 SSR 进程时：

| 约定 | 说明 |
| --- | --- |
| 环境变量 `SSR_SOCKET` | 监听的 socket，形如 `@tenon-ssr-…`。Bun 中要把开头的 `@` 换成 `\0`：`Bun.serve({ unix: "\0tenon-ssr-…" })` |
| 环境变量 `API_SOCKET` | 调用接口的 socket，同样把 `@` 换成 `\0`：`fetch("http://go/api/…", { unix: "\0tenon-api-…" })` |
| 请求头 `X-CSP-Nonce` | 写到全部内联脚本上 |
| 请求头 `X-Request-ID`、`traceparent`、`Cookie` | 调用接口时原样带上 |
| 响应头 `X-Tenon-Route` | 可选，匹配到的路由模板 |
| `POST /__tenon/print/{name}` | 可选，用打印模板渲染请求体中的 JSON，返回完整的 HTML；没有这个模板时返回 404 |
| SIGTERM | 停止接收请求并退出；Go 发送它之前已经排空了对外的请求 |

<a id="limitations"></a>
## 限制

- **只支持 Linux**：从内存执行依赖 memfd，抽象 socket 也是 Linux 特有的。其他系统上 `Run` 返回 `ssr.ErrUnsupported`；在 macOS 上开发时用开发服务器。Docker 容器中可以正常使用。
- **程序体积**：Bun 的运行时编译进 SSR 可执行文件，约 100 MB，嵌入后 Go 程序相应变大。
- **内存**：SSR 进程常驻，空闲时约 40 MB，压力下约 150 MB。

<a id="api-reference"></a>
## API 参考

包 `github.com/go-tenon/tenon/ssr`。

| 名称 | 说明 |
| --- | --- |
| `New(Options) (*Host, error)` | 检查选项并创建 Host；不启动进程 |
| `(*Host).Handler() http.Handler` | 页面的处理器 |
| `(*Host).Run(ctx, serve) error` | 启动 SSR 进程，运行 `serve`，停止时按顺序关闭；一个 Host 只能运行一次 |
| `(*Host).Ready() bool` | SSR 进程是否可以接收请求；开发模式总是 true |
| `(*Host).Render(ctx, name, data) ([]byte, error)` | 用打印模板渲染数据，返回完整的 HTML，见[渲染打印模板](#rendering) |
| `PrintPath`、`ErrUnknownTemplate` | 打印模板的地址前缀；没有这个模板 |
| `DefaultCSP(nonce) string` | 默认的内容安全策略 |
| `ErrUnsupported` | 当前系统不能运行 SSR 进程 |
| `EnvSocket`、`EnvAPISocket` | 传给 SSR 进程的环境变量名 |
| `HeaderNonce`、`HeaderRoute` | 与 SSR 进程约定的请求头和响应头 |

| `Options` 字段 | 说明 |
| --- | --- |
| `Executable` | SSR 可执行文件，`DevServer` 为空时必填 |
| `API` | 内部入口提供的接口；nil 时不提供，例如只用 `Render` 生成 PDF 的队列 worker |
| `DevServer` | 开发服务器地址 |
| `CSP` | 由 nonce 生成内容安全策略，默认 `DefaultCSP` |
| `Env` | SSR 进程额外的环境变量 |
| `Logger` | 默认 `slog.Default()` |
| `MeterProvider` | 记录 `ssr.restarts`，nil 时不记录 |
| `ReadyTimeout` | 等待就绪的时间，默认 30 秒 |
| `StopTimeout` | 等待退出的时间，默认 10 秒 |
