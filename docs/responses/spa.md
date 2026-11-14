# 单页应用

用 React、Vue 等写的单页应用，可以和 API 打包进同一个二进制文件：Vite 的构建产物通过 `embed` 嵌入程序，由 `httpx.SPA` 提供。页面和 API 同源，部署时只有一个二进制文件。一个应用可以有多个单页应用，例如运营后台挂在 `/admin/`，用户端挂在 `/`。

单页应用调用 [JSON API](../http/routing.md)，请求带 Bearer 令牌（见[单页应用的登录](../security/api-tokens.md#single-page-applications)），客户端由 `client --ts` 从路由生成，见[客户端代码](clients.md#typescript)。后台和用户端都是单页应用；公开页面可以[在服务端渲染](views.md)，两者共用一个 Router。`tenon init` 生成的项目已经按本页的方式组织好。

- [嵌入构建产物](#embedding)
- [挂载](#mounting)
- [缓存与前端路由](#caching-and-routing)
- [开发](#development)
- [构建与发布](#building)
- [API 参考](#api-reference)

<a id="embedding"></a>
## 嵌入构建产物

每个前端是 `apps` 中的一个目录，见[开发工具](../installation/tooling.md#conventions)。`bun run build` 把前端构建到它的 `dist`，例如 `apps/web/dist`。在前端目录中加一个 Go 文件，把它嵌入程序，下面是 `apps/web/frontend.go`：

```go
package web

import (
    "embed"
    "io/fs"
)

//go:embed dist
var dist embed.FS

// Dist 是 Vite 的构建产物，包含 index.html 和 assets 目录。
var Dist, _ = fs.Sub(dist, "dist")
```

`dist` 不提交到版本库。没有 `dist` 时直接 `go build` 会失败（`pattern dist: no matching files found`）；`tenon dev` 和 `tenon generate` 会先写一个占位的 `index.html`，发布时用 [tenon build](../installation/tooling.md#build)，它总是先构建前端，见[构建与发布](#building)。

<a id="mounting"></a>
## 挂载

创建 `SPA`，挂载到根路径。API 和其他路由照常注册，chi 优先匹配更具体的路径：

```go
router.Route("/api/v1", func(r chi.Router) {
    api := rest.New(r, rest.Options{Title: "Blog API", Version: "1.0.0", BasePath: "/api/v1"})
    // 用 api 注册接口，见路由一章
})
router.Handle("/realtime", rt.Handler())
router.Handle("/*", httpx.NewSPA(web.Dist, httpx.SPAOptions{DevServer: cfg.DevServers["web"]}))
```

`NewSPA` 在创建时读取全部文件，没有 `index.html` 时 panic：这是构建的问题，启动时就应当发现。`DevServer` 见[开发](#development)。[routes 命令](../general/commands.md#routes-command)把 `/*` 列为公开路由：页面本身公开，前端打开后调用 API，API 收到 `401` 时前端跳转到登录页。

API 要放在子路由下。`rest` 给子路由设置了 JSON 格式的 `404`，写错的接口地址得到 `route not found`。

前端不放在根路径时，把 Vite 的 `base` 和 `SPAOptions.Base` 设为同一个路径。多个前端各挂一个 `SPA`，挂在 `/*` 的那个放在最后：

```go
router.Handle("/admin/*", httpx.NewSPA(admin.Dist, httpx.SPAOptions{Base: "/admin/", DevServer: cfg.DevServers["admin"]}))
router.Handle("/*", httpx.NewSPA(web.Dist, httpx.SPAOptions{DevServer: cfg.DevServers["web"]}))
```

```ts
// apps/admin/vite.config.ts
export default defineConfig({
  base: "/admin/",
  plugins: [react()],
})
```

[tenon add frontend](../installation/tooling.md#add-frontend) 生成的前端已经设置好 `base`，并打印挂载的代码。

<a id="caching-and-routing"></a>
## 缓存与前端路由

| 请求的地址 | 响应 |
| --- | --- |
| `assets/` 下的文件，例如 `/assets/index-GpEN3EET.js` | Vite 生成的文件名带内容哈希，`Cache-Control: public, max-age=31536000, immutable`，永久缓存 |
| `/`、`/index.html`，以及 `public` 目录中的文件，例如 `/favicon.svg` | `Cache-Control: no-cache` 和 `ETag`，浏览器每次确认，没有变化时返回 `304` |
| 不存在的地址，浏览器打开页面（`Accept` 含 `text/html`） | 前端路由，例如 `/articles/42`：返回 `index.html`，由 TanStack Router 渲染 |
| 不存在的地址，其他请求 | `404`。缺失的脚本、图片和写错地址的 `fetch` 得到的都是 `404` |
| `assets/` 下不存在的文件 | `404` |

HTML 文件带有 `Content-Security-Policy: frame-ancestors 'none'`，只有本站能把应用嵌入框架，防止诱导点击。需要被嵌入时，在 `SPAOptions.FrameAncestors` 中列出允许的来源，例如 `[]string{"https://partner.example.com"}`。

`index.html` 不缓存，发布新版本后，用户下次打开页面就会加载新的脚本。已经打开的页面仍在运行旧版本，它的脚本在 `assets/` 中按需加载的分块，在新版本中已经不存在了，会得到 `404`。TanStack Router 按路由懒加载时，可以在路由的 `errorComponent` 中提示刷新页面。

<a id="development"></a>
## 开发

开发时前端由 Vite 的开发服务器提供，修改代码后浏览器立即更新。把 `SPAOptions.DevServer` 设为它的地址，`SPA` 就把收到的请求全部转发给它，包括热更新的 WebSocket。浏览器仍然打开应用的地址，页面和 API 与生产环境一样同源。

`DevServer` 取自应用配置。在配置结构体中加一个字段，按前端的名称保存开发服务器的地址：

```go
type Config struct {
    // ……
    DevServers map[string]string `yaml:"dev_servers"` // 只在 tenon dev 时有值
}
```

不用自己填写它。[tenon dev](../installation/tooling.md#dev) 为每个前端的 Vite 选一个端口，把地址写入 `.tenon/dev.yaml`，追加在 `serve` 的 `--config` 之后：

```yaml
# .tenon/dev.yaml，由 tenon dev 生成
dev_servers:
  admin: http://localhost:46809
  web: http://localhost:41177
```

用[开发工具](../installation/tooling.md)一起启动全部 Vite 和应用，打开 http://localhost:8080：

```bash
bun run dev   # package.json 中是 "dev": "go tool tenon dev"
```

修改 Go 代码后应用自动重新编译、重启，并重新生成 [TypeScript 客户端](clients.md#typescript)，前端的热更新照常进行。按约定的[目录结构](../concepts/structure.md)组织时零配置，见 [tenon dev](../installation/tooling.md#dev)。生产环境中没有 `dev_servers`，`DevServer` 为空，`SPA` 提供嵌入的构建产物。

设置了 `DevServer` 时，`NewSPA` 跳过构建产物，但 `dist` 仍然要存在，程序才能编译。`tenon dev` 第一次启动时，没有 `node_modules` 就先运行 `bun install`，没有 `dist` 就写一个占位页；停止后构建一次，`dist` 是最新的。Vite 没有启动时，页面返回 `502`，内容是连接错误。

<a id="building"></a>
## 构建与发布

[tenon build](../installation/tooling.md#build) 先构建全部前端，再构建 Go：

```bash
bun run build   # 即 go tool tenon build：复制框架的前端包，bun install --frozen-lockfile，每个前端 bun run build，go build -trimpath -o bin/api ./cmd/api
```

`--frozen-lockfile` 要求 `bun.lock` 与各个 `package.json` 一致，CI 中安装的依赖与本地完全相同。CI 用 [setup-bun](https://github.com/oven-sh/setup-bun) 安装 Bun。

[Docker 镜像](../installation/deployment.md#docker)的构建阶段中装上 Bun，用 `tenon build` 构建。前端依赖的框架前端包由 `tenon` 从 Go 模块中复制（见[框架的前端包](../installation/tooling.md#framework-packages)），所以前端要在 Go 的构建阶段中构建：

```dockerfile
FROM golang:1.27 AS build
COPY --from=oven/bun:1 /usr/local/bin/bun /usr/local/bin/bun
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go tool tenon build -o /out/api && mkdir /out/storage
```

后面的运行阶段不变。只运行 Go 测试和 lint 的 CI 任务放一个空页面让程序能编译即可：

```bash
for app in apps/*/; do mkdir -p "$app/dist" && touch "$app/dist/index.html"; done
```

<a id="api-reference"></a>
## API 参考

包 `github.com/go-tenon/tenon/httpx`。

### NewSPA

```go
func NewSPA(fsys fs.FS, opts SPAOptions) *SPA
```

- **参数**
    - `fsys`：构建产物所在的目录，必须包含 `index.html`，通常是 `embed.FS` 的子目录
    - `opts`：见下面的 `SPAOptions`
- **返回**：单页应用的处理器（`http.Handler`）

创建时读取全部文件。设置了 `DevServer` 时跳过 `fsys`。选项不合法、读取失败或没有 `index.html` 时 panic。

### SPAOptions

| 字段 | 说明 |
| --- | --- |
| `Base string` | 挂载的路径，以 `/` 开头和结尾，与 Vite 的 `base` 相同；默认 `/` |
| `DevServer string` | 非空时把全部请求转发给前端的开发服务器，跳过 `fsys`。只在开发时设置 |
| `FrameAncestors []string` | 允许把页面嵌入框架的来源；默认不允许 |
