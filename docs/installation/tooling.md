# 开发工具

开发一个带前端的应用时，你要同时运行好几样东西：每个前端的 Vite 开发服务器、Go 应用，还有每次修改接口后重新生成的客户端代码。`tenon` 是开发 Tenon 应用的命令行工具，它把这些事情合在一起，只负责开发和构建：

| 命令 | 作用 |
| --- | --- |
| `tenon init` | 生成新项目 |
| `tenon add frontend` | 给项目加一个前端 |
| `tenon dev` | 一起运行全部前端的 Vite 和应用。修改 Go 代码后重新编译、平滑重启，并重新生成客户端；修改前端代码后由 Vite 热更新 |
| `tenon build` | 先构建全部前端，再编译应用，前端的构建产物嵌入二进制 |
| `tenon generate` | 生成 TypeScript 和 Go 客户端 |

`serve`、`work`、`migrate`、`routes`、`openapi` 这些运行时的命令在应用自己的二进制中，部署后照样可用，见[命令行](../general/commands.md)。前端和 API 按目录约定发现，见[前端与 API](#conventions)；`tenon.json` 只在需要改变默认行为时才写。

- [安装](#installation)
- [前端与 API](#conventions)
- [tenon init](#init)
- [tenon add frontend](#add-frontend)
- [tenon dev](#dev)
- [tenon build](#build)
- [tenon generate](#generate)
- [tenon.json](#configuration)
- [应用这一侧](#app-side)

<a id="installation"></a>
## 安装

用 [tenon init](#init) 生成的项目已经装好了。已有的项目把 `tenon` 加入 `go.mod` 的工具，版本随框架一起固定：

```bash
go get -tool github.com/go-tenon/tenon/cmd/tenon
```

然后用 `go tool tenon` 运行。前端的依赖和脚本用 [Bun](https://bun.sh) 的 workspace 管理，在项目根目录的 `package.json` 中写好脚本，开发者只需要 `bun run dev`：

```json
{
  "workspaces": ["apps/web", "packages/client"],
  "scripts": {
    "dev": "go tool tenon dev",
    "build": "go tool tenon build",
    "generate": "go tool tenon generate"
  }
}
```

`go.mod` 中加上 `ignore node_modules`，`go build ./...`、`go test ./...` 不会进入各层的 `node_modules`。

<a id="conventions"></a>
## 前端与 API

**前端**是 `apps` 中带 `vite.config.ts`（或 `.mts`、`.js`、`.mjs`）的目录，目录名是前端的名称，例如 `apps/web`、`apps/admin`。名称以小写字母开头，由小写字母、数字和连字符组成。每个前端的 `package.json` 有三个脚本：

```json
{
  "scripts": {
    "dev": "vite",
    "build": "vite build",
    "typecheck": "tsc --noEmit"
  }
}
```

构建产物在前端目录的 `dist` 中，由同目录的 `frontend.go` 嵌入程序，见[单页应用](../responses/spa.md#embedding)。

**API** 是路由中用 `rest.New` 创建的每一个 API。有 `packages/client/package.json` 时，每个 API 的 TypeScript 客户端生成到 `packages/client/src/<名称>`，名称默认是 `BasePath` 的最后一段：

| BasePath | 客户端的目录 | 前端中导入 |
| --- | --- | --- |
| `/api` | `packages/client/src/api` | `@todo/client/api` |
| `/api/admin` | `packages/client/src/admin` | `@todo/client/admin` |
| `/api/v1` | `packages/client/src/v1` | `@todo/client/v1` |

两个 API 的名称相同时，`generate` 报错，在 [tenon.json](#configuration) 中为其中一个改名。`packages/client/package.json` 用子路径导出每个客户端：

```json
{
  "name": "@todo/client",
  "exports": { "./*": "./src/*/api.ts" }
}
```

<a id="framework-packages"></a>
### 框架的前端包

框架的前端包（`@go-tenon/client` 等）随 Go 模块分发，不从 npm 安装：它们的源码在框架的 `js` 目录中，版本就是 `go.mod` 中的框架版本。`tenon` 在用到前端依赖之前（`init`、`add frontend`、`dev`、`build`、`generate`）把它们复制到项目的 `.tenon/js/<包>`，项目把它们列入 Bun workspace，前端用 `workspace:*` 依赖：

```json
{
  "workspaces": [".tenon/js/client", "apps/web", "packages/client"]
}
```

```json
{
  "dependencies": { "@go-tenon/client": "workspace:*" }
}
```

`.tenon` 不提交到版本库。升级框架（`go get github.com/go-tenon/tenon@<版本>`）后，下一次 `tenon dev` 复制新版本的包，包的 `package.json` 变了时重新 `bun install`。框架被 `replace` 到本地目录时复制本地的代码，修改框架的前端包后重新运行 `tenon dev` 即可。刚 clone 的项目先运行一次 `tenon dev`、`build` 或 `generate`，再单独运行 `bun install`。

<a id="init"></a>
## tenon init

```bash
go run github.com/go-tenon/tenon/cmd/tenon@main init todo
```

在 `todo` 目录生成一个能直接运行的项目：Go 应用加一个 React 前端 `apps/web`，带一个待办事项的示例资源，数据保存在内存中，生成后即可运行。目录结构与入口说明见[脚手架](scaffolding.md#creating-a-project)。

生成后它运行 `go mod tidy`、生成 TypeScript 客户端，装了 Bun 时再运行 `bun install`。`--module` 是 Go 模块路径，`--name` 是应用的名称，默认都是目录名；npm 包的作用域由目录名得出，例如 `@todo/web`、`@todo/client`。目录必须不存在或为空。

`go.mod` 要求的框架版本就是运行 `init` 的这个版本：用 `@main` 运行时是 main 分支当前的提交，用 `@v1.2.0` 时是这个版本。开发框架本身时用 `--tenon <框架目录>`，`go.mod` 用 `replace` 指向本地的副本。

<a id="add-frontend"></a>
## tenon add frontend

```bash
go tool tenon add frontend admin
```

在 `apps/admin` 生成一个最小的 React 前端：Vite 的 `base` 是 `/admin/`，包名是 `@<作用域>/admin`，依赖 `@<作用域>/client`。它把前端加入根目录 `package.json` 的 `workspaces`，装了 Bun 时运行 `bun install`，然后打印挂载这个前端的代码：

```go
router.Handle("/admin/*", httpx.NewSPA(admin.Dist, httpx.SPAOptions{Base: "/admin/", DevServer: cfg.DevServers["admin"]}))
```

把它加在挂载 `/*` 的那一行之前，并导入 `<模块>/apps/admin`。`--base` 指定其他的挂载路径，以 `/` 开头和结尾。在项目根目录运行，`apps/admin` 必须尚未创建。

<a id="dev"></a>
## tenon dev

```bash
bun run dev   # 即 go tool tenon dev
```

它同时运行：

- **Vite**：为每个前端选一个空闲端口，在前端目录中运行 `bun run dev --port <端口> --strictPort`。启动前把[框架的前端包](#framework-packages)复制到 `.tenon/js`，根目录没有 `node_modules` 或这些包的 `package.json` 变了时 `bun install`；前端没有 `dist` 时写一个占位的 `index.html`，`go:embed` 才能编译通过，开发时页面来自 Vite。
- **应用**：等全部 Vite 能访问后，把 `main` 编译到临时目录，用 `serve` 中的参数运行，并在最后追加 `--config .tenon/dev.yaml`。这个文件中是各前端开发服务器的地址（`dev_servers`），应用的 `httpx.SPA` 把页面请求转给对应的 Vite，见[应用这一侧](#app-side)。浏览器打开应用的地址，页面和 API 同源，热更新照常工作。
- **重新编译**：模块中的 `.go` 文件（测试除外）、`go.mod`、`go.sum` 变化后重新编译。成功时平滑停止旧进程（`SIGTERM`），启动新的，再运行一次 [`generate`](#generate)，前端的类型随接口一起更新；失败时打印编译错误，旧进程继续运行。跳过隐藏目录、`node_modules`、`vendor`、`testdata` 和 `dist`。
- **服务端渲染的页面**（模块中有 `.templ` 文件时）：`go tool templ generate --watch`，在 `templ.proxyPort`（默认 7331）代理应用并注入自动刷新的脚本，这时打开 http://localhost:7331。只改了文字时直接生效，跳过重新编译。`templ.static` 目录中的文件变化时也刷新浏览器。
- **Tailwind 和其他工具**：见 [tenon.json](#configuration)。

启动前检查用到的命令是否已安装：Go、Bun、templ（`go get -tool github.com/a-h/templ/cmd/templ`）、Tailwind。有前端时 `serve` 的参数中必须有 `--config`。按 `Ctrl-C` 停止后，它平滑停止应用，再运行 `templ generate`、每个前端的 `bun run build`、`tailwindcss --minify` 和各工具的 `build`，把监听模式的输出恢复为正式版本。

<a id="build"></a>
## tenon build

```bash
bun run build   # 即 go tool tenon build
```

有前端时先复制[框架的前端包](#framework-packages)，在根目录运行一次 `bun install --frozen-lockfile`，再在每个前端目录中运行 `bun run build`，最后 `go build -trimpath -o bin/api ./cmd/api`。`-o` 指定输出的二进制，`GOOS`、`GOARCH` 等环境变量照常生效。`--frozen-lockfile` 要求 `bun.lock` 与各个 `package.json` 一致，CI 中安装的依赖与本地完全相同。Docker 镜像的写法见[构建与发布](../responses/spa.md#building)。

<a id="generate"></a>
## tenon generate

编译应用（前端没有 `dist` 时同样先写占位页），用它的 `client` 命令生成客户端：有 `packages/client/package.json` 时为每个 API 生成 TypeScript 客户端，见[客户端代码](../responses/clients.md#typescript)；`tenon.json` 中设置了 `go` 的 API 生成 Go 客户端，见 [Go 客户端](../responses/clients.md#go)。`tenon dev` 每次重新编译成功后会自动运行它。把生成的代码提交到仓库，CI 中重新生成后用 `git diff --exit-code` 检查是否忘了更新。

<a id="configuration"></a>
## tenon.json

```json
{
  "$schema": "https://raw.githubusercontent.com/go-tenon/tenon/main/cmd/tenon/schema.json",
  "apis": {
    "/api/v1": { "name": "public", "go": "../api" }
  }
}
```

`$schema` 让编辑器补全字段并显示说明。没有写的字段使用默认值：

| 字段 | 默认值 | 说明 |
| --- | --- | --- |
| `main` | `./cmd/api` | 应用入口的包 |
| `serve` | `["serve", "--config", "config/app.yaml,config/local.yaml"]`，`local.yaml` 不存在时去掉它 | 开发时运行应用的参数 |
| `url` | `http://localhost:8080` | 应用的地址：`dev` 打印它，templ 的代理访问它 |
| `apis.<BasePath>.name` | `BasePath` 的最后一段 | 这个 API 的 TypeScript 客户端的目录名 |
| `apis.<BasePath>.go` | 不生成 | 这个 API 的 Go 客户端的目录 |
| `templ.proxyPort` | `7331`，有 `.templ` 文件时启用 | templ 代理的端口 |
| `templ.static` | 不监视 | 文件变化时刷新浏览器的目录 |
| `tailwind.input`、`tailwind.output` | 不使用 | Tailwind 的入口和生成的样式表，见[用 Tailwind 编写样式](../responses/assets.md#tailwind) |
| `tools` | 空 | 其他工具，`watch` 是开发时的监听命令，`build` 是停止后的生成命令 |

前端不在 `tenon.json` 中配置，见[前端与 API](#conventions)。

例如用 esbuild 打包公开页面自己的脚本：

```json
{
  "tools": [{
    "watch": ["esbuild", "internal/web/js/app.js", "--bundle", "--outdir=internal/web/static/js", "--watch=forever"],
    "build": ["esbuild", "internal/web/js/app.js", "--bundle", "--outdir=internal/web/static/js", "--minify"]
  }]
}
```

<a id="app-side"></a>
## 应用这一侧

路由是运行时注册的，`tenon generate` 要借助应用自己的 `client` 命令。应用在根命令中加入 `httpcmd.Commands`，它返回 `openapi`、`routes`、`client` 三个命令，参数是构造应用全部路由的函数，见[命令行](../general/commands.md#declaring-the-cli)：

```go
root.AddCommand(httpcmd.Commands(func() chi.Routes { return routes(config.Default[Config](), nil) })...)
```

`routes` 是 `serve` 也在用的路由函数。命令用默认配置和 `nil` 依赖调用它，只读取构造出的路由。

应用的配置中有一个 `dev_servers`，`tenon dev` 通过 `.tenon/dev.yaml` 填写它，挂载前端时按名称取出地址，见[单页应用的开发](../responses/spa.md#development)：

```go
type Config struct {
    HTTP       httpx.Config      `yaml:"http"`
    DevServers map[string]string `yaml:"dev_servers"`
}
```
