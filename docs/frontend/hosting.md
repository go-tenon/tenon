# 托管前端与服务端渲染

`apps` 中的每个前端都由 Go 托管：`go tool tenon build` 把它们构建好、嵌入 Go 程序，运行时 Go 按挂载路径返回页面。每个前端选一种渲染方式：后台只在浏览器中渲染；官网、内容站这类需要搜索引擎收录、首屏要快的页面在服务端渲染，由 [TanStack Start](https://tanstack.com/start) 在 Bun 中完成。只有用到服务端渲染时，程序中才带 Bun。两种方式部署时都只有一个文件。

阅读前需要了解：[脚手架](../installation/scaffolding.md)；[部署](../installation/deployment.md)。

- [两种渲染方式](#modes)
- [结构](#architecture)
- [装配](#wiring)
- [切换渲染方式](#switching)
- [启动与停止](#lifecycle)
- [安全](#security)
- [链路与日志](#observability)
- [开发](#development)
- [渲染打印模板](#rendering)
- [常见问题](#faq)
- [与 SSR 进程的约定](#contract)
- [限制](#limitations)
- [API 参考](#api-reference)

<a id="modes"></a>
## 两种渲染方式

前端的 `package.json` 用 `tenon` 字段声明挂载路径和是否在服务端渲染：

```json
{
  "name": "@helpdesk/admin",
  "tenon": { "base": "/admin/", "ssr": false }
}
```

- `base` 是前端挂载的路径，以 `/` 开头和结尾，同时是 Vite 的 `base`，不在 `vite.config.ts` 中另写。
- `ssr` 选择渲染方式，默认 `false`。

| 情况 | `ssr` | 页面怎样渲染 |
| --- | --- | --- |
| 后台、控制台：要登录才能看，不需要搜索引擎收录 | `false` | Go 返回页面外壳，页面在浏览器中渲染。程序中不带 Bun，可以在任何系统上运行 |
| 官网、文档、商品页：要被搜索引擎收录，首屏要快 | `true` | 页面在 Bun 中渲染好再返回，浏览器接着接管页面 |

`tenon add frontend` 按布局写好这个字段：`site` 布局是 `true`，`rail` 和 `frame` 是 `false`。`go tool tenon` 和 `@go-tenon/start` 的 Vite 插件读取同一个字段，所以切换方式只改这一处，见[切换渲染方式](#switching)。

只在浏览器中渲染的前端，构建时由 TanStack Start 生成一个页面外壳 `_shell.html`：只含根布局（`__root.tsx`），不含任何页面的内容。浏览器拿到外壳后加载脚本，再由前端路由渲染当前地址的页面。

<a id="architecture"></a>
## 结构

```
浏览器 ──► Go（唯一的 HTTP 入口）
            ├─ /api/* ── Go 处理
            ├─ 静态文件、预渲染的页面 ── Go 直接返回
            ├─ 只在浏览器中渲染的前端 ── Go 返回页面外壳
            └─ 服务端渲染的前端 ── unix socket ──► Bun（这个前端的 SSR 进程）
                                                  └─ 需要数据时 ── 内部 unix socket ──► Go 的接口
```

- **Go 是唯一的入口**：请求 ID、链路、访问日志、限流、安全响应头只在 Go 中处理一次。
- **静态文件都由 Go 返回**：脚本、样式、图片、预渲染的页面和页面外壳嵌入 Go 程序，两种前端都一样。SSR 进程重启时，这些文件照常可以访问。
- **Bun 只负责渲染**：它不连接数据库，也不持有密钥。渲染时需要的数据，经内部 socket 调用 Go 的接口，和浏览器调用的是同一组接口。页面请求流式转发，不缓冲。
- **最多一个 Bun 可执行文件**：全部服务端渲染的前端编进同一个可执行文件（约 100 MB）。每个前端仍然是独立的进程，进程按环境变量 `TENON_FRONTEND` 只加载自己的前端。可执行文件启动时写入内存文件（memfd）执行，不落盘；Go 被强制结束时，SSR 进程随之结束。

`go tool tenon build` 的产物在 `apps/dist` 中（被 `.gitignore` 忽略），`apps/apps.go` 用 `go:embed` 嵌入它：

```
apps/dist/
├── frontends.json   # 前端的清单：名称、挂载路径、是否服务端渲染
├── web/             # 每个前端的静态文件：assets/、预渲染的页面、页面外壳 _shell.html
├── admin/
└── ssr              # 只在有服务端渲染的前端时存在
```

<a id="wiring"></a>
## 装配

`tenon init` 生成的 `cmd/api/main.go` 已经装配好：

```go
pages, err := frontend.New(apps.Dist, frontend.Options{
    API:        api,             // SSR 进程渲染时调用的接口，通常就是对外的接口路由
    DevServers: cfg.DevServers,  // 只在 tenon dev 时有值
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

`frontend.New` 读取 `apps/dist` 中的清单，为每个前端准备处理器。`Handler` 挂在接口之后，处理其余全部请求：请求交给挂载路径最长匹配的前端，例如 `/admin/users` 交给挂在 `/admin/` 的前端，`/about` 交给挂在 `/` 的前端；不在任何前端之下的路径返回 404。`Run` 包住对外的 HTTP 服务，在它前后管理 SSR 进程，应用不需要自己安排启动和停止的顺序。

用 `tenon add frontend` 新增前端时，这段代码不用改：新的前端写进清单，`Handler` 按它的挂载路径分发。

一个前端的请求按下面的顺序处理：

1. `GET` 和 `HEAD` 请求先找静态文件：先找路径对应的文件，再找目录下的 `index.html`（预渲染的页面）。
2. `assets/` 下找不到的文件返回 404。这些是构建出的脚本和样式，返回页面会让浏览器把 HTML 当作脚本执行。
3. 服务端渲染的前端：其余请求转给它的 SSR 进程。
4. 只在浏览器中渲染的前端：其余 `GET` 和 `HEAD` 请求返回页面外壳，其他方法返回 405。

<a id="switching"></a>
## 切换渲染方式

改 `package.json` 中的 `ssr`，再运行 `go tool tenon build`：

```json
"tenon": { "base": "/", "ssr": false }
```

构建时 `apps/dist` 从头生成：没有前端在服务端渲染时，`apps/dist/ssr` 不再生成，程序中不再带 Bun，体积小约 100 MB。Go 代码不用改。

从服务端渲染改为只在浏览器中渲染时，还要检查两处：

- `vite.config.ts` 中的 `prerender` 不能包含 `"/"`：页面外壳由首页 `/` 渲染而来，构建时报错 `prerender "/" needs "ssr": true`。其他页面（例如 `"/docs"`）照常可以预渲染。
- 打印模板（`Render`）只在服务端渲染的前端中可用，见[渲染打印模板](#rendering)。

反过来改为服务端渲染时，页面默认在服务端渲染；要登录才能看的页面在路由上设置 `ssr: false`，见 [TanStack Start 的接入](start.md)。

<a id="lifecycle"></a>
## 启动与停止

`Run` 的顺序：

1. 在内部 socket 上提供 `Options.API`（设置了时），为每个服务端渲染的前端启动一个 SSR 进程，等它们的 socket 都可以连接。超过 `ReadyTimeout`（默认 30 秒）还没就绪时返回错误，不开始接收请求。
2. 调用传入的函数，开始接收请求。SSR 进程意外退出时按退避重启：第一次等 100 毫秒，之后翻倍，最多 5 秒；连续运行超过 10 秒后从头计算。重启期间这个前端的页面请求返回 502 和 `Retry-After: 1`，静态文件、其他前端和接口不受影响。
3. 收到停止信号后，传入的函数先排空对外的请求，进行中的页面渲染完成后才返回，见[平滑停止](../installation/deployment.md#graceful-shutdown)；然后向 SSR 进程发送 SIGTERM，超过 `StopTimeout`（默认 10 秒）再强制结束；最后关闭内部入口。

没有服务端渲染的前端时，`Run` 只调用传入的函数。

`Ready` 报告全部 SSR 进程是否可以接收请求，可以放进就绪检查。指标 `ssr.restarts` 记录意外退出后重启的次数，属性 `frontend` 是前端的名称。

<a id="security"></a>
## 安全

- **内容安全策略**：Go 为每个页面请求生成一个 nonce。返回构建时生成的页面（页面外壳、预渲染的页面）时，Go 把其中的占位 nonce 换成它；服务端渲染时经请求头交给 Bun，Bun 把它写到内联脚本上。HTML 响应都带上 `Content-Security-Policy`，默认是 `DefaultCSP`：脚本只能来自本站或带这个 nonce，不允许被其他网站嵌入。用 `Options.CSP` 定制。客户端伪造的 nonce 请求头会被覆盖。
- **内部入口**：抽象 socket 没有文件权限，同一网络命名空间中的进程都能连接，所以内部入口只接受与 Go 进程同一用户的连接（按 `SO_PEERCRED` 检查）。它提供的是同一组接口，认证照常进行，不会因为来自内部而放宽。

<a id="observability"></a>
## 链路与日志

- Go 把请求 ID（`X-Request-ID`）和 W3C `traceparent` 转交给 Bun，Bun 调用接口时原样带回：页面请求、渲染和它调用的接口在同一条链路中，日志带同一个请求 ID。
- Bun 在响应头 `X-Tenon-Route` 中报告匹配到的前端路由模板，例如 `/articles/$id`；Go 用它命名 span，例如 `GET /articles/$id`。这个头不会发给浏览器。只在浏览器中渲染的前端由 Go 直接返回页面，span 按 Go 的路由命名。
- SSR 进程的标准输出和标准错误逐行写入 Go 的日志，带 `component=ssr` 和 `frontend=<名称>`；标准错误记为 WARN。

<a id="development"></a>
## 开发

`Options.DevServers` 不为空时（`tenon dev` 设置），不启动 SSR 进程，每个前端的请求转给它的开发服务器，包括热更新的 WebSocket。两种前端在开发时都由 Vite 提供，只在浏览器中渲染的前端由开发服务器返回页面外壳。开发服务器注入的内联脚本没有 nonce，所以开发模式不设置 CSP；请求 ID 和链路照常转交。

`tenon dev` 只写入 `apps/dist/frontends.json`，不构建前端；页面都来自开发服务器。

<a id="rendering"></a>
## 渲染打印模板

`Render(ctx, frontend, name, data)` 用服务端渲染的前端 `frontend` 中名为 `name` 的打印模板渲染 `data`，返回完整的 HTML，通常交给 [`pdf`](../libraries/pdf.md) 生成 PDF。`data` 编码为 JSON 传给模板，请求 ID 和链路照常转交；模板写法见[生成 PDF：编写模板](../libraries/pdf.md#writing-templates)。

```go
html, err := pages.Render(ctx, "web", "invoice", invoice)
```

- 打印模板只用传入的数据渲染，不调用接口，所以队列任务中没有用户的 cookie 也能使用。队列 worker 同样用 `Run` 包住自己的运行函数，不提供接口时 `Options.API` 留空。
- 模板的地址是 `POST /__tenon/print/{name}`，只供 `Render` 调用：对外的页面请求访问 `/__tenon/` 下的地址时返回 `404`。
- 没有这个模板时返回 `ErrUnknownTemplate`；这个前端不在服务端渲染、SSR 进程没有就绪（`Run` 之外、重启期间）时返回错误。开发模式下请求开发服务器。

只在浏览器中渲染的应用需要生成 PDF 时，可以单独加一个服务端渲染的前端放打印模板，例如挂在 `/print/`。

<a id="faq"></a>
## 常见问题

**启动时报 `read dist/frontends.json`。** 原因：`apps/dist` 被 `.gitignore` 忽略，刚 clone 的项目中没有它，直接用 `go run` 或 `go build` 编译了应用。解决：开发时用 `bun run dev`（`tenon dev` 写入清单），部署时用 `go tool tenon build`。

**启动时报 `server rendering needs dist/ssr` 或 `missing dist/<名称>/_shell.html`。** 原因：改了 `package.json` 中的 `tenon` 字段，但没有重新构建，清单和构建产物对不上。解决：重新运行 `go tool tenon build`。

**构建时报 `base … in vite.config differs from tenon.base`。** 原因：`vite.config.ts` 中写了 `base`，与 `package.json` 中的不同。解决：删掉 `vite.config.ts` 中的 `base`，挂载路径只写在 `package.json` 中。

**部署新版本后，打开着的页面切换路由时报脚本加载失败。** 原因：脚本的文件名带内容哈希，新版本中旧的文件已经不存在，Go 对 `assets/` 下找不到的文件返回 404。解决：用 `@go-tenon/ui` 的新版本提示，让用户刷新页面，见[应用骨架](app.md)。

<a id="contract"></a>
## 与 SSR 进程的约定

[`@go-tenon/start`](start.md) 已经按这些约定实现，应用不需要自己处理。自己编写 SSR 进程时：

| 约定 | 说明 |
| --- | --- |
| 环境变量 `TENON_FRONTEND` | 这个进程渲染的前端的名称；可执行文件中有全部服务端渲染的前端，按它运行其中一个 |
| 环境变量 `SSR_SOCKET` | 监听的 socket，形如 `@tenon-ssr-…`。Bun 中要把开头的 `@` 换成 `\0`：`Bun.serve({ unix: "\0tenon-ssr-…" })` |
| 环境变量 `API_SOCKET` | 调用接口的 socket，同样把 `@` 换成 `\0`：`fetch("http://go/api/…", { unix: "\0tenon-api-…" })` |
| 请求头 `X-CSP-Nonce` | 写到全部内联脚本上 |
| 请求头 `X-Request-ID`、`traceparent`、`Cookie` | 调用接口时原样带上 |
| 响应头 `X-Tenon-Route` | 可选，匹配到的路由模板 |
| `POST /__tenon/print/{name}` | 可选，用打印模板渲染请求体中的 JSON，返回完整的 HTML；没有这个模板时返回 404 |
| SIGTERM | 停止接收请求并退出；Go 发送它之前已经排空了对外的请求 |

页面请求的路径带着前端的挂载路径，例如 `/shop/products/1`；静态文件和预渲染的页面由 Go 返回，不会转给 SSR 进程。

<a id="limitations"></a>
## 限制

- **服务端渲染只支持 Linux**：从内存执行依赖 memfd，抽象 socket 也是 Linux 特有的。其他系统上有服务端渲染的前端时，`Run` 返回 `frontend.ErrUnsupported`；在 macOS 上开发时用开发服务器。Docker 容器中可以正常使用。只在浏览器中渲染的前端没有这个限制。
- **程序体积**：带 Bun 时程序大约 100 MB；几个服务端渲染的前端共用它，每多一个前端只多一两 MB。
- **内存**：每个 SSR 进程常驻，空闲时约 40 MB，压力下约 150 MB。

<a id="api-reference"></a>
## API 参考

包 `github.com/go-tenon/tenon/frontend`。

| 名称 | 说明 |
| --- | --- |
| `New(apps fs.FS, Options) (*Host, error)` | 读取 `apps` 中 `dist/frontends.json`，检查构建产物，为每个前端准备处理器；不启动进程 |
| `(*Host).Handler() http.Handler` | 全部前端的页面处理器，按挂载路径分发 |
| `(*Host).Run(ctx, serve) error` | 启动 SSR 进程，运行 `serve`，停止时按顺序关闭；一个 Host 只能运行一次 |
| `(*Host).Ready() bool` | 全部 SSR 进程是否可以接收请求；开发模式和没有服务端渲染的前端时总是 true |
| `(*Host).Render(ctx, frontend, name, data) ([]byte, error)` | 用打印模板渲染数据，返回完整的 HTML，见[渲染打印模板](#rendering) |
| `Entry` | 清单中的一个前端：`Name`、`Base`、`SSR` |
| `DistDir`、`ManifestFile`、`ExecutableFile`、`ShellFile` | 构建产物中的路径：`dist`、`frontends.json`、`ssr`、`_shell.html` |
| `PrintPath`、`ErrUnknownTemplate` | 打印模板的地址前缀；没有这个模板 |
| `DefaultCSP(nonce) string` | 默认的内容安全策略 |
| `ErrUnsupported` | 当前系统不能运行 SSR 进程 |
| `EnvFrontend`、`EnvSocket`、`EnvAPISocket` | 传给 SSR 进程的环境变量名 |
| `HeaderNonce`、`HeaderRoute`、`PrerenderNonce` | 与 SSR 进程约定的请求头和响应头；构建时写在页面中的占位 nonce |

| `Options` 字段 | 说明 |
| --- | --- |
| `API` | 内部入口提供的接口；nil 时不提供，例如只用 `Render` 生成 PDF 的队列 worker |
| `DevServers` | 各前端开发服务器的地址，按前端的名称；不为空时是开发模式 |
| `CSP` | 由 nonce 生成内容安全策略，默认 `DefaultCSP` |
| `Env` | SSR 进程额外的环境变量 |
| `Logger` | 默认 `slog.Default()` |
| `MeterProvider` | 记录 `ssr.restarts`，nil 时不记录 |
| `ReadyTimeout` | 等待就绪的时间，默认 30 秒 |
| `StopTimeout` | 等待退出的时间，默认 10 秒 |
