# 托管前端与服务端渲染

`apps` 中的前端由 `go tool tenon build` 构建、嵌入 Go 程序，Go 按挂载路径返回页面。每个前端只在浏览器中渲染，或者由 [TanStack Start](https://tanstack.com/start) 在 Go 托管的 Bun 进程中服务端渲染；部署时都只有一个可执行文件。

包：`frontend`、`@go-tenon/start`。

<a id="modes"></a>
## 两种渲染方式：`package.json` 的 `tenon` 字段

```json title="package.json"
"tenon": { "base": "/admin/", "ssr": false }
```

- `base` 是挂载路径，以 `/` 开头和结尾，各前端互不相同。它也是 Vite 的 `base`，`vite.config.ts` 中不另写，写了不同的值时构建报错 `base … in vite.config differs from tenon.base`。
- `ssr` 选择渲染方式，默认 `false`。`go tool tenon` 和 `@go-tenon/start/vite` 读取同一个字段。`tenon add frontend --render csr|ssr` 写好它，不写时 `site` 布局是 `true`，`rail`、`frame` 是 `false`。

| 情况 | `ssr` | 页面怎样渲染 |
| --- | --- | --- |
| 后台、控制台：要登录，不需要搜索引擎收录 | `false` | Go 返回页面外壳 `_shell.html`（只含根布局），浏览器中的路由渲染页面。程序中不带 Bun，任何系统都能运行 |
| 官网、文档、商品页：要被收录，首屏要快 | `true` | Bun 渲染好再返回，浏览器接着接管。只支持 Linux，见[限制](#limitations) |

<a id="architecture"></a>
## 结构与构建产物 `apps/dist`

Go 是唯一的 HTTP 入口：接口、静态文件、预渲染的页面和页面外壳由 Go 处理，服务端渲染的页面经 unix socket 交给 Bun。请求 ID、链路、日志、限流、安全响应头都在 Go 中处理。Bun 只渲染，不连接数据库、不持有密钥，数据经内部 socket 来自浏览器用的同一组接口。

`apps/apps.go` 用 `go:embed` 嵌入 `apps/dist`。它不提交，目录中只提交 `.gitignore`：

```
apps/dist/
├── frontends.json   # 清单：名称、挂载路径、是否服务端渲染
├── web/             # 每个前端的静态文件：assets/、预渲染的页面、_shell.html
└── ssr              # 全部服务端渲染的前端和 Bun 编成的一个可执行文件
```

<a id="wiring"></a>
## 装配：`frontend.New`、`Handler`、`Run`

`tenon init` 生成的 `cmd/<名称>/main.go` 已经装配好：

```go
var router chi.Router
api := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { router.ServeHTTP(w, r) })
pages, err := frontend.New(apps.Dist, frontend.Options{API: api, DevServers: a.cfg.DevServers})
if err != nil {
    return nil, err
}
router = routes(a.cfg, a.store, pages.Handler()) // /api 下是接口，其余路径 router.Handle("/*", pages)
ln, err := d.Listen("tcp", a.cfg.HTTP.Addr)
if err != nil {
    return nil, err
}
return func(ctx context.Context) error {
    return pages.Run(ctx, func(ctx context.Context) error {
        return httpx.Serve(ctx, a.cfg.HTTP, ln, router)
    })
}, nil
```

`apps.Dist` 是嵌入的目录，`a.cfg.DevServers` 来自 `tenon dev`（见[开发](#development)），`d` 是 `serve` 命令的 `*command.Daemon`。

- `Options.API` 是 Bun 渲染时调用的接口。传整个路由，路径与浏览器相同（`/api/...`）。
- `Handler` 把请求交给挂载路径最长匹配的前端；不在任何前端之下的路径返回 404。新增前端时这段代码不用改。
- `Run` 包住对外的 HTTP 服务，在它前后启停 Bun 进程，见[启动与停止](#lifecycle)。

一个前端的请求按顺序处理：

1. `GET`、`HEAD` 先找静态文件，再找目录下的 `index.html`（预渲染的页面）。`assets/` 下的文件永久缓存，其余返回 `Cache-Control: no-cache`。
2. `assets/` 下找不到的文件返回 404。部署新版本后，旧页面加载已删除的脚本会失败，用[新版本提示](app.md#new-version)让用户刷新。
3. 服务端渲染的前端：其余请求流式转给 Bun。
4. 只在浏览器中渲染的前端：其余 `GET`、`HEAD` 返回页面外壳，其他方法返回 405。

<a id="setup"></a>
## 安装 `@go-tenon/start`：`vite.config.ts`、`start.ts`、`router.tsx`、`server.ts`

前端是一个 TanStack Start 应用，加上四处约定。`tenon init` 和 `tenon add frontend` 生成的前端已经配好。

`tenon()` 包含 TanStack Start 和 React 的插件，从 `package.json` 读取 `base` 和 `ssr`：

```ts title="vite.config.ts"
import { defineConfig } from "vite"
import { tenon } from "@go-tenon/start/vite"

export default defineConfig({ plugins: [tenon()] })
```

`requestMiddleware` 记住页面请求的请求头，服务端渲染调用接口时转交给 Go。漏装时服务端渲染的页面是未登录状态：

```ts title="src/start.ts"
import { createStart } from "@tanstack/react-start"
import { requestMiddleware } from "@go-tenon/start"

export const startInstance = createStart(() => ({ requestMiddleware: [requestMiddleware] }))
```

`cspNonce()` 把 Go 生成的 nonce 交给路由，内联脚本才能通过[内容安全策略](#security)。漏写时浏览器控制台报内联脚本违反内容安全策略。用 `@go-tenon/ui` 的 `createApp` 时写在它的 `router` 选项中：

```tsx title="src/router.tsx"
import { createRouter } from "@tanstack/react-router"
import { cspNonce } from "@go-tenon/start"
import { routeTree } from "./routeTree.gen"

export function getRouter() {
  return createRouter({ routeTree, ssr: { nonce: cspNonce() } })
}
```

`streamHandler` 流式渲染页面，并报告匹配到的路由模板，Go 用它命名 span：

```ts title="src/server.ts"
import { createServerEntry, createStartHandler } from "@tanstack/react-start/server"
import { streamHandler } from "@go-tenon/start/server"

export default createServerEntry({ fetch: createStartHandler(streamHandler) })
```

`src/client.ts(x)` 是 TanStack Start 的浏览器入口。调用接口的客户端放在 `src/api.ts`，放在 `src/client.ts` 中页面不会水合。

<a id="calling-the-api"></a>
## 服务端渲染时调用接口：`apiFetch`

```ts title="src/api.ts"
import { apiFetch } from "@go-tenon/start"
import { createClient } from "@blog/client/api"

export const api = createClient({ baseURL: "/api", fetch: apiFetch })
```

`apiFetch` 在浏览器中是同源的 `fetch`。服务端渲染时，它经 Go 的内部 socket 调用同一组接口，带上页面请求的 `Cookie`、`X-Request-ID`、`traceparent` 和 `Accept-Language`：Go 照常认证，日志和链路与页面请求连在一起。所以 loader 直接调用接口，两边结果相同：

```tsx
export const Route = createFileRoute("/articles/$id")({
  loader: ({ params }) => api.article.get(params.id),
  component: ArticlePage,
})
```

在 Go 之外渲染（例如直接运行 `vite dev`）时报错 `neither API_SOCKET nor API_ORIGIN is set`，改用 `tenon dev`，见[开发](#development)。

<a id="switching"></a>
## 切换渲染方式

改 `package.json` 中的 `ssr`，运行 `go tool tenon build`，Go 代码不用改。改了没有重新构建时，启动报 `server rendering needs dist/ssr` 或 `missing dist/<名称>/_shell.html`。

- 没有前端在服务端渲染时，程序中不带 Bun，体积小约 100 MB。
- 改为只在浏览器中渲染时，`prerender` 不能含 `"/"`：页面外壳由首页渲染而来，构建报错 `prerender "/" needs "ssr": true`。
- 打印模板只能放在服务端渲染的前端中，见[渲染打印模板](#rendering)。
- 服务端渲染的前端中，要登录的页面在路由上设置 `ssr: false`：读取当前用户、检查权限只在浏览器中进行。

<a id="prerendering"></a>
## 预渲染：`tenon({ prerender })`

`prerender` 列出构建时渲染成 HTML 的页面，用于内容在代码仓库中、不随请求变化的页面，例如官网首页和产品文档：

```ts title="vite.config.ts"
export default defineConfig({ plugins: [tenon({ prerender: ["/", "/docs"] })] })
```

- 从列出的页面跟随链接，同一前缀下的页面一起预渲染；`"/"` 只表示首页本身。
- Go 直接返回预渲染的 HTML，其余页面在请求时渲染。
- 只在浏览器中渲染的前端也能预渲染，`"/"` 除外。

<a id="lifecycle"></a>
## 启动与停止：`Run`、`Ready`

1. `Run` 在内部 socket 上提供 `Options.API`（不为 nil 时），为每个服务端渲染的前端启动一个 Bun 进程。超过 `Options.ReadyTimeout`（默认 30 秒）没有就绪时返回错误。
2. 再调用传入的函数接收请求。Bun 进程意外退出时按退避重启（最多间隔 5 秒），期间这个前端的页面返回 502 和 `Retry-After: 1`，静态文件、其他前端和接口不受影响。
3. 停止时传入的函数先排空请求（见[平滑停止](../installation/deployment.md#graceful-shutdown)），再向 Bun 发送 SIGTERM，超过 `Options.StopTimeout`（默认 10 秒）强制结束。

- 没有服务端渲染的前端或在开发模式下，`Run` 只调用传入的函数。
- Bun 进程继承 Go 程序的环境变量，`Options.Env` 追加更多。
- `Ready` 报告全部 Bun 进程是否就绪，可以放进就绪检查。设置 `Options.MeterProvider` 时，指标 `ssr.restarts` 按 `frontend` 记录重启次数。

<a id="security"></a>
## 内容安全策略：`Options.CSP`

Go 为每个页面请求生成 nonce，换掉页面外壳和预渲染页面中的占位符，服务端渲染时交给 Bun（需要 [`cspNonce`](#setup)）。HTML 响应带 `Content-Security-Policy`，默认 `frontend.DefaultCSP`：脚本只来自本站或带 nonce，`connect-src 'self'`，`frame-ancestors 'none'`。需要按请求调整策略时，从 `frontend.DefaultPolicy(nonce)` 出发调用 `AddImgSrc`、`AddConnectSrc`（配合 `frontend.Origin` 从配置地址提取 origin）或修改 `FrameAncestors` 后调用 `String()`，例如允许浏览器向 [OpenObserve](observability.md) 上报：

```go
CSP: func(r *http.Request, nonce string) string {
    p := frontend.DefaultPolicy(nonce)
    p.AddConnectSrc(frontend.Origin("https://o2.example.com"))
    return p.String()
},
```

按请求的路径可以给不同的页面不同的策略，例如只让 `/widget/` 下的页面嵌入其他网站，其余页面保持 `frame-ancestors 'none'`。`allowed` 是应用按请求查出的允许嵌入的来源，例如 `https://shop.example.com`：

```go
CSP: func(r *http.Request, nonce string) string {
    p := frontend.DefaultPolicy(nonce)
    if strings.HasPrefix(r.URL.Path, "/widget/") {
        p.FrameAncestors = strings.Fields(allowed(r))
    }
    return p.String()
},
```

- `r` 是浏览器发来的页面请求，服务端渲染时也是它；它的 context 带着中间件放进去的值，例如租户。
- 页面外壳、预渲染的页面和服务端渲染的页面的响应都带它返回的策略。

开发模式不设置 CSP：开发服务器注入的内联脚本没有 nonce。

<a id="observability"></a>
## 链路与日志

- 页面请求、服务端渲染和它调用的接口在同一条链路中，日志带同一个请求 ID。
- 服务端渲染的页面的 span 按前端路由命名，例如 `GET /articles/$id`。
- Bun 的标准输出和标准错误逐行写入 `Options.Logger`（默认 `slog.Default()`），带 `component=ssr`、`frontend=<名称>`，标准错误记为 WARN。

<a id="rendering"></a>
## 渲染打印模板：`Render`

`Render` 用服务端渲染的前端中的打印模板渲染数据，返回完整的 HTML，交给 [`pdf`](../libraries/pdf.md) 生成 PDF。`pages` 是入口创建的 `*frontend.Host`：

```go
html, err := pages.Render(ctx, "web", "invoice", invoice) // 前端 web 中名为 invoice 的模板
```

- 模板用 `@go-tenon/start/print` 的 `definePrint` 声明，`printHandler` 加在 `src/server.ts` 中，见[生成 PDF：编写模板](../libraries/pdf.md#writing-templates)。
- 数据编码为 JSON 传给模板。模板不调用接口，所以队列任务中也能用。队列 worker 同样用 `Run` 包住运行函数，`Options.API` 留空。
- 没有这个模板时返回 `frontend.ErrUnknownTemplate`；前端不在服务端渲染、Bun 没有就绪（`Run` 之外、重启期间）时返回错误。
- 只在浏览器中渲染的应用要生成 PDF 时，另加一个服务端渲染的前端放打印模板，例如挂在 `/print/`。

<a id="compiling"></a>
## 编译：`go tool tenon build`、`tenon-start compile`

`go tool tenon build` 在每个前端中运行 `vite build`，把浏览器的文件（`dist/client`）复制到 `apps/dist/<名称>`，见[开发工具：构建](../installation/tooling.md#build)。有前端在服务端渲染时，它再运行 `tenon-start compile`，把这些前端的服务端入口（`dist/server`）连同 Bun 编译成 `apps/dist/ssr`。架构与 Go 程序相同（`--arch`），`tenon-start compile --target` 可选 `bun-linux-x64`、`bun-linux-arm64`。

`apps/dist/frontends.json` 不提交。直接 `go run`、`go build` 刚 clone 的项目时启动报 `read dist/frontends.json`：开发用 `bun run dev`，部署用 `go tool tenon build`。

<a id="development"></a>
## 开发：`tenon dev` 与 `Options.DevServers`

`tenon dev` 只写入 `apps/dist/frontends.json`，不构建前端，并把各前端开发服务器的地址交给应用，入口传给 `Options.DevServers`（见[开发工具：应用一侧](../installation/tooling.md#app-side)）。这时：

- 不启动 Bun 进程，请求（包括热更新的 WebSocket）转给前端的 Vite 开发服务器。
- `apiFetch` 在服务端经环境变量 `API_ORIGIN`（`tenon dev` 设置）调用 Go。
- 不设置 CSP；请求 ID 和链路照常转交。

<a id="limitations"></a>
## 限制

- **服务端渲染只支持 Linux**，其他系统上 `Run` 返回 `frontend.ErrUnsupported`。macOS 上开发用开发服务器，部署可以用 Docker。只在浏览器中渲染的前端没有这个限制。
- **体积**：带 Bun 时程序约 100 MB，每多一个服务端渲染的前端多一两 MB。
- **内存**：每个 Bun 进程常驻，空闲约 40 MB，压力下约 150 MB。
