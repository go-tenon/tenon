# TanStack Start 的接入

`@go-tenon/start` 把 [TanStack Start](https://tanstack.com/start) 接到 Tenon：前端由 Go 托管，服务端渲染的页面由 Go 托管的 Bun 渲染（见[托管前端与服务端渲染](hosting.md)），服务端渲染和浏览器调用同一组 Go 接口，前端代码不需要区分自己在哪一边运行。

阅读前需要了解：[托管前端与服务端渲染](hosting.md)；[客户端代码](../http/clients.md)。

- [安装到应用](#setup)
- [调用接口](#calling-the-api)
- [预渲染](#prerendering)
- [打印模板](#print-templates)
- [编译](#compiling)
- [开发](#development)
- [常见问题](#troubleshooting)
- [API 参考](#api-reference)

<a id="setup"></a>
## 安装到应用

前端是一个普通的 TanStack Start 应用，加上五处 Tenon 的约定。`tenon init` 和 `tenon add frontend` 生成的前端已经按本节配置好。

> [!NOTE]
> `src/client.ts(x)` 和 `src/server.ts` 是 TanStack Start 约定的浏览器入口和服务端入口。调用接口的客户端不要放在 `src/client.ts` 中，否则它会被当成浏览器的入口，页面不会水合；模板中客户端在 `src/api.ts`。

`package.json` 的 `tenon` 字段写挂载路径和是否在服务端渲染，见[两种渲染方式](hosting.md#modes)：

```json title="package.json"
"tenon": { "base": "/", "ssr": true }
```

`vite.config.ts` 使用 `tenon`，它包含 TanStack Start 和 React 的插件，并从 `package.json` 读取上面的字段：`base` 作为 Vite 的 `base`，`ssr` 为 `false` 时打开 TanStack Start 的 SPA 模式，构建时生成页面外壳。`vite.config.ts` 中不另写 `base`：

```ts title="vite.config.ts"
import { defineConfig } from "vite"
import { tenon } from "@go-tenon/start/vite"

export default defineConfig({ plugins: [tenon({ prerender: ["/", "/docs"] })] })
```

`src/start.ts` 安装 `requestMiddleware`，它记住页面请求的请求头，服务端渲染调用接口时转交给 Go：

```ts title="src/start.ts"
import { createStart } from "@tanstack/react-start"
import { requestMiddleware } from "@go-tenon/start"

export const startInstance = createStart(() => ({ requestMiddleware: [requestMiddleware] }))
```

`src/router.tsx` 把 Go 生成的 nonce 交给路由，内联脚本才能通过 Go 设置的内容安全策略：

```tsx title="src/router.tsx"
import { createRouter } from "@tanstack/react-router"
import { cspNonce } from "@go-tenon/start"
import { routeTree } from "./routeTree.gen"

export function getRouter() {
  return createRouter({ routeTree, ssr: { nonce: cspNonce() } })
}
```

`src/server.ts` 用 `streamHandler` 渲染页面，它在响应中报告匹配到的路由模板，Go 用它命名链路的 span：

```ts title="src/server.ts"
import { createServerEntry, createStartHandler } from "@tanstack/react-start/server"
import { streamHandler } from "@go-tenon/start/server"

export default createServerEntry({ fetch: createStartHandler(streamHandler) })
```

<a id="calling-the-api"></a>
## 调用接口

把 `apiFetch` 交给生成的客户端：

```ts title="src/client.ts"
import { createSession } from "@go-tenon/client"
import { apiFetch } from "@go-tenon/start"
import { createClient } from "@blog/client/api"

export const session = createSession({ load: async () => ({ account: (await api.user.me()).id }) })
export const api = createClient({ baseURL: "/api", fetch: apiFetch, session })
```

- 在浏览器中，它就是同源的 `fetch`，会话 cookie 由浏览器带上。
- 在服务端渲染中，它经 Go 提供的内部 unix socket 调用同一组接口，原样带上页面请求的 `Cookie`、`X-Request-ID`、`traceparent` 和 `Accept-Language`：Go 照常认出是谁，日志和链路与页面请求连在一起。Bun 不解析 cookie，也不持有任何密钥。

所以 loader 中直接调用接口，服务端和浏览器得到相同的结果：

```tsx
export const Route = createFileRoute("/articles/$id")({
  loader: ({ params }) => api.article.get(params.id),
  head: ({ loaderData }) => ({ meta: [{ title: loaderData?.title }] }),
  component: ArticlePage,
})
```

服务端渲染的前端中，不需要服务端渲染的页面（例如要登录才能看的页面）在路由上设置 `ssr: false`，只在浏览器中渲染。整个前端都不需要服务端渲染时，把 `package.json` 中的 `ssr` 设为 `false`，见[切换渲染方式](hosting.md#switching)。

<a id="prerendering"></a>
## 预渲染

`tenon` 的 `prerender` 列出构建时渲染成 HTML 的页面，适合官网首页、产品文档这类内容在代码仓库中、不随请求变化的页面。从这些页面出发跟随链接，同一前缀下的页面一起预渲染；`"/"` 只表示首页本身。

请求这些页面时，Go 直接返回预渲染的 HTML，不再渲染。HTML 中的 nonce 在构建时是占位符，返回时换成本次请求的 nonce。其余页面在请求时渲染。

只在浏览器中渲染的前端（`"ssr": false`）同样可以预渲染页面，但不能预渲染 `"/"`：页面外壳由首页渲染而来，构建时报错。

<a id="print-templates"></a>
## 打印模板

`@go-tenon/start/print` 声明打印模板：Go 用 `frontend.Host.Render` 把数据交给它，渲染成完整的 HTML 后生成 PDF。打印模板在服务端渲染，只能放在 `"ssr": true` 的前端中。`printHandler(templates)` 加在服务端入口中，写法见[生成 PDF：编写模板](../libraries/pdf.md#writing-templates)。

<a id="compiling"></a>
## 编译

前端的 `bun run build` 运行 `vite build`，结果在前端目录的 `dist` 中：`dist/client` 是浏览器的文件（脚本、样式、预渲染的页面、页面外壳），`dist/server` 是服务端入口。`tenon build` 把每个前端的 `dist/client` 复制到 `apps/dist/<名称>`，由 Go 直接返回，见[开发工具](../installation/tooling.md#build)。

有前端在服务端渲染时，`tenon build` 再用 `tenon-start compile` 把这些前端的服务端入口连同 Bun 编译成一个可执行文件：

```bash
bunx tenon-start compile --out apps/dist/ssr web=apps/web/dist/server/server.js shop=apps/shop/dist/server/server.js
```

- 每个参数是 `<前端的名称>=<服务端入口>`。可执行文件按环境变量 `TENON_FRONTEND` 只加载其中一个前端，Go 为每个服务端渲染的前端启动一个进程。
- `--target` 是运行的系统和架构，`bun-linux-x64` 或 `bun-linux-arm64`。没有写时按环境变量 `GOARCH`（`tenon build` 设为 Go 程序的架构，见 `--arch`），再没有时是当前机器的架构。

<a id="development"></a>
## 开发

开发时 Go 不启动 SSR 进程，把页面请求转给 Vite 的开发服务器（见[托管前端：开发](hosting.md#development)）。这时没有内部 socket，`apiFetch` 经环境变量 `API_ORIGIN` 中的地址调用 Go，例如 `http://localhost:8080`，由 `tenon dev` 设置。

<a id="troubleshooting"></a>
## 常见问题

**服务端渲染时报错 `neither API_SOCKET nor API_ORIGIN is set`**

页面在 Go 之外渲染了，例如直接运行了 `vite dev`。用 `tenon dev` 启动开发环境，或者设置 `API_ORIGIN`。

**服务端渲染的页面是未登录状态，浏览器中是已登录**

`src/start.ts` 没有安装 `requestMiddleware`，`apiFetch` 拿不到页面请求的 cookie。

**浏览器控制台报内联脚本违反内容安全策略**

`createRouter` 没有设置 `ssr: { nonce: cspNonce() }`。

<a id="api-reference"></a>
## API 参考

| 导入 | 名称 | 说明 |
| --- | --- | --- |
| `@go-tenon/start` | `requestMiddleware` | 记住页面请求的请求头，安装在 `createStart` 的 `requestMiddleware` 中 |
| | `apiFetch` | 调用 Go 接口的 `fetch`，交给生成的客户端 |
| | `cspNonce()` | 本次页面请求的 nonce，浏览器中为 `undefined` |
| | `forwardHeaders`、`socketPath` | 与 Go 的约定，见[与 SSR 进程的约定](hosting.md#contract) |
| `@go-tenon/start/server` | `streamHandler` | 流式渲染页面，报告 `X-Tenon-Route` |
| `@go-tenon/start/serve` | `serve(options)` | Bun 中的 SSR 服务，`tenon-start compile` 生成的入口调用它；静态文件由 Go 返回，不经过它 |
| `@go-tenon/start/print` | `definePrint`、`printHandler`、`PrintDocument` | 打印模板，见[打印模板](#print-templates) |
| `@go-tenon/start/vite` | `tenon(options)` | Vite 插件：TanStack Start、React、挂载路径、预渲染和 SPA 模式 |
| | `frontendConfig(dir?)` | 读取前端 `package.json` 的 `tenon` 字段：`{ base, ssr }` |
| 命令 | `tenon-start compile` | 把服务端渲染的前端连同 Bun 编译成一个可执行文件 |
| 命令 | `tenon-start routes` | 在前端的目录中生成路由树 `src/routeTree.gen.ts`：按 `vite.config.ts` 启动一次不监听端口的 Vite，TanStack Start 的插件生成它，与 `vite dev` 生成的相同（包括 Start 的 `Register` 类型声明）。前端的 `typecheck` 先运行它 |
