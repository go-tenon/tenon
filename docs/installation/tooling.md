# 开发工具

在[脚手架](scaffolding.md)中，你用 `bun run dev` 启动了开发环境：它同时运行 Go 程序和前端的开发服务器，修改代码后自动重新编译。这背后是 Tenon 的开发工具 `tenon`。这一页按开发中用到的先后顺序介绍它的命令，最后讲怎样调整它的默认行为。

阅读前需要了解：[脚手架](scaffolding.md)。

- [安装](#installation)
- [命令一览](#overview)
- [tenon init：生成项目](#init)
- [tenon dev：开发](#dev)
- [tenon generate：生成客户端](#generate)
- [tenon build：构建](#build)
- [tenon add frontend：再加一个前端](#add-frontend)
- [常见问题](#troubleshooting)
- [进阶](#advanced)
    - [前端与 API 怎样被发现](#conventions)
    - [框架的前端包](#framework-packages)
    - [tenon.json](#configuration)
    - [应用这一侧](#app-side)

<a id="installation"></a>
## 安装

用 `tenon init` 生成的项目已经装好了，可以跳过这一节。

`tenon` 记录在项目的 `go.mod` 中，作为项目的一个**工具**。这样它的版本和框架的版本一起固定，团队中每个人用的都是同一个版本。给已有的项目加上它：

```bash
go get -tool github.com/go-tenon/tenon/cmd/tenon
```

之后用 `go tool tenon <命令>` 运行它。为了少打几个字，项目根目录的 `package.json` 中写好了脚本，平时用 `bun run dev` 即可：

```json
{
  "scripts": {
    "dev": "go tool tenon dev",
    "build": "go tool tenon build",
    "generate": "go tool tenon generate"
  }
}
```

`go.mod` 中还应当有一行 `ignore node_modules`。前端的依赖目录 `node_modules` 中可能有成千上万个文件，这一行让 `go build ./...`、`go test ./...` 跳过它们。

<a id="overview"></a>
## 命令一览

| 命令 | 什么时候用 |
| --- | --- |
| `tenon init` | 开始一个新项目时，用一次 |
| `tenon dev` | 每天开发时：运行程序，修改后自动重新编译 |
| `tenon generate` | 接口变了以后，重新生成前端的客户端。`dev` 会自动运行它 |
| `tenon build` | 上线前：构建出一个可执行文件 |
| `tenon add frontend` | 需要再加一个前端时，例如运营后台 |

`serve`、`migrate`、`routes` 这类命令不属于 `tenon`，它们在你的应用自己的程序中，部署到服务器上以后照样可以运行，见[命令行](../general/commands.md)。

<a id="init"></a>
## tenon init：生成项目

```bash
go run github.com/go-tenon/tenon/cmd/tenon@main init todo
```

在 `todo` 目录中生成一个能直接运行的项目，生成的内容见[脚手架](scaffolding.md#creating-a-project)。这里还没有项目，所以用 `go run` 直接运行 `tenon`，不需要先安装。

选项：

- `--module`：Go module 的路径，默认是目录名。
- `--name`：应用的名称，默认是目录名。
- `--tenon <框架目录>`：只在开发 Tenon 框架本身时使用，让项目使用本地的框架代码（`go.mod` 中写入 `replace`）。

<a id="dev"></a>
## tenon dev：开发

```bash
bun run dev   # 即 go tool tenon dev
```

开发时你需要同时运行好几样东西，`tenon dev` 把它们合在一起：

1. **前端的开发服务器 Vite**：每个前端一个，各用一个空闲的端口。修改前端代码后，页面立刻更新（热更新）。
2. **你的 Go 程序**：用 `serve` 命令运行。它把页面请求转发给对应的 Vite，所以你只需要访问 Go 程序的地址（默认 <http://localhost:8080>），页面和接口都在这里，见[前端与 API 同源](scaffolding.md#frontend)。
3. **监视 Go 代码**：`.go` 文件（测试文件除外）、`go.mod`、`go.sum` 变化后重新编译。编译成功时，先平滑地停止旧的程序（等它处理完正在处理的请求），再启动新的；然后运行一次 [`generate`](#generate)，前端的类型随之更新。编译失败时打印错误，旧的程序继续运行，你改好代码保存后再试。

此外：

- 修改了设计令牌 `theme/tokens.json` 时，重新生成前端的主题，见[设计令牌](../responses/theme.md)。
- 项目中有服务端渲染的页面（`.templ` 文件）时，同时运行 templ 的生成和自动刷新，这时打开 <http://localhost:7331>，见[视图](../responses/views.md)。

启动前，`tenon dev` 会检查需要的命令（Go、Bun 等）是否已经安装，缺少时直接告诉你。

按 Ctrl+C 停止。它先平滑地停止程序，再把前端等构建为正式的版本，让工作目录中的文件与提交时一致。

<a id="generate"></a>
## tenon generate：生成客户端

前端调用接口时，需要知道接口的地址、参数和返回的字段。这些信息已经写在 Go 代码中了，没必要在前端再写一遍：`tenon generate` 编译你的程序，读出全部接口，生成 TypeScript 代码，前端直接调用。接口加了字段，重新生成后前端就能用到；字段写错了，编辑器立刻提示。

```bash
bun run generate   # 即 go tool tenon generate
```

生成的内容：

- 每个 API 的 TypeScript 客户端，在 `packages/client/src/<名称>` 中，见[客户端代码](../responses/clients.md#typescript)；
- 在 [tenon.json](#configuration) 中要求了的 Go 客户端，见 [Go 客户端](../responses/clients.md#go)；
- 有设计令牌时，前端的主题包 `packages/theme`。

`tenon dev` 每次重新编译成功后会自动运行它，所以平时不需要手动运行。

生成的代码要提交到代码仓库，这样只看代码的人也能读懂前端调用的是什么。在 CI 中重新生成一次，再用 `git diff --exit-code` 检查有没有变化，就能发现“改了接口但忘了重新生成”的情况。

<a id="build"></a>
## tenon build：构建

```bash
bun run build   # 即 go tool tenon build
```

构建出一个可以部署的可执行文件 `bin/api`，前端打包在其中。它依次做这些事：

1. 安装前端的依赖：`bun install --frozen-lockfile`。`--frozen-lockfile` 要求依赖的版本与 `bun.lock` 完全一致，所以 CI 中构建出的结果和你本地的相同。
2. 在每个前端目录中运行 `bun run build`，结果写进各自的 `dist`。
3. 编译 Go 程序：`go build -trimpath -o bin/api ./cmd/api`。`-trimpath` 去掉二进制中你电脑上的文件路径。

`-o` 可以指定输出的文件；`GOOS`、`GOARCH` 等环境变量照常生效，例如在 Mac 上构建 Linux 的版本。部署见[部署](deployment.md)，Docker 镜像的写法见[构建与发布](../responses/spa.md#building)。

<a id="add-frontend"></a>
## tenon add frontend：再加一个前端

一个项目可以有多个前端，例如面向用户的网站，加上给运营人员用的后台。在项目根目录运行：

```bash
go tool tenon add frontend admin --template admin
```

它在 `apps/admin` 中生成一个前端，挂在 `/admin/` 下，加入根目录 `package.json` 的 workspace，安装依赖。最后打印一行代码，你要把它加到路由中，Go 程序才会提供这个前端的页面：

```go
router.Handle("/admin/*", httpx.NewSPA(admin.Dist, httpx.SPAOptions{Base: "/admin/", DevServer: cfg.DevServers["admin"]}))
```

把它放在挂载 `/*` 的那一行之前：路由按顺序匹配，放在后面的话，`/admin/` 下的请求已经被 `/*` 接走了。同时导入 `<你的 module>/apps/admin`。

`--template` 选择模板：

| 模板 | 内容 |
| --- | --- |
| `default`（默认） | 一个最小的 React 前端，见[应用骨架](../responses/app-shell.md) |
| `admin` | 用 antd 写的运营后台：登录页、侧栏、多标签、用户菜单，见[运营后台](../responses/admin.md) |
| `console` | 用 shadcn 和 Tailwind 写的控制台，见 [shadcn registry 与控制台](../responses/registry.md) |

`--base` 可以指定其他的挂载路径，以 `/` 开头和结尾。`apps/admin` 必须还不存在。

<a id="troubleshooting"></a>
## 常见问题

**`tenon dev` 提示缺少某个命令**

按提示安装，例如 Bun 见[准备工作](scaffolding.md#requirements)；用到 templ 时运行 `go get -tool github.com/a-h/templ/cmd/templ`。

**修改了 Go 代码，程序没有更新**

看终端中有没有编译错误：编译失败时旧的程序会继续运行。改正后保存，`tenon dev` 会再编译一次。

**CI 中 `tenon build` 报错 lockfile 与 package.json 不一致**

修改了前端的依赖，但没有提交更新后的 `bun.lock`。在本地运行 `bun install`，提交 `bun.lock`。

**`tenon generate` 报错两个 API 的名称相同**

客户端的目录名默认是 API 地址的最后一段，`/api/v1` 和 `/admin/v1` 都叫 `v1`。在 [tenon.json](#configuration) 中给其中一个改名。

<a id="advanced"></a>
## 进阶

<a id="conventions"></a>
### 前端与 API 怎样被发现

`tenon` 不需要你登记有哪些前端和 API，它按目录约定找到它们。

**前端**是 `apps` 中带 `vite.config.ts`（或 `.mts`、`.js`、`.mjs`）的目录，目录名就是前端的名称，例如 `apps/web`、`apps/admin`。名称以小写字母开头，由小写字母、数字和连字符组成。每个前端的 `package.json` 中有三个脚本：

```json
{
  "scripts": {
    "dev": "vite",
    "build": "vite build",
    "typecheck": "tsc --noEmit"
  }
}
```

构建的结果在前端目录的 `dist` 中，由同一目录中的 `frontend.go` 打包进程序，见[单页应用](../responses/spa.md#embedding)。

**API** 是你在路由中用 `rest.New` 创建的每一个 API。每个 API 的 TypeScript 客户端生成到 `packages/client/src/<名称>`，名称默认是它的 `BasePath` 的最后一段：

| BasePath | 客户端的目录 | 前端中导入 |
| --- | --- | --- |
| `/api` | `packages/client/src/api` | `@todo/client/api` |
| `/api/admin` | `packages/client/src/admin` | `@todo/client/admin` |
| `/api/v1` | `packages/client/src/v1` | `@todo/client/v1` |

`packages/client/package.json` 用子路径导出每个客户端，`@todo/client/api` 是 `api.ts`，`@todo/client/api/queries` 是同一目录中的 `queries.ts`：

```json
{
  "name": "@todo/client",
  "exports": {
    "./*/enums": "./src/*/enums.ts",
    "./*/messages": "./src/*/messages.ts",
    "./*/permissions": "./src/*/permissions.ts",
    "./*/queries": "./src/*/queries.ts",
    "./*/resources": "./src/*/resources.ts",
    "./*/schemas": "./src/*/schemas.ts",
    "./*": "./src/*/api.ts"
  }
}
```

生成的每个文件的内容见[客户端代码](../responses/clients.md#generating-types)。

<a id="framework-packages"></a>
### 框架的前端包

Tenon 也提供了一些前端包，例如 `@go-tenon/client`（调用接口、管理登录）和 `@go-tenon/shell`（应用骨架）。它们不从 npm 安装，而是随 Go module 一起分发：源码在框架的 `js` 目录中，版本就是 `go.mod` 中框架的版本，前后端的版本永远一致。

`tenon` 在需要前端依赖之前（`init`、`add frontend`、`dev`、`build`、`generate`），把这些包复制到项目的 `.tenon/js/<包名>`。项目把它们列入 Bun 的 workspace，前端用 `workspace:*` 依赖它们：

```json
{
  "workspaces": [".tenon/js/client", ".tenon/js/shell", "apps/web", "packages/client"]
}
```

```json
{
  "dependencies": { "@go-tenon/client": "workspace:*", "@go-tenon/shell": "workspace:*" }
}
```

`.tenon` 不提交到代码仓库。升级框架（`go get github.com/go-tenon/tenon@<版本>`）后，下一次运行 `tenon dev` 时复制新版本的包；包的 `package.json` 变了，或者依赖还没有安装（例如刚克隆下来的项目）时，自动运行 `bun install`。

<a id="configuration"></a>
### tenon.json

大多数项目不需要这个文件。需要改变 `tenon` 的默认行为时，在项目根目录创建 `tenon.json`：

```json
{
  "$schema": "https://raw.githubusercontent.com/go-tenon/tenon/main/cmd/tenon/schema.json",
  "apis": {
    "/api/v1": { "name": "public", "go": "../api" }
  }
}
```

`$schema` 让编辑器补全字段并显示说明。这个例子把 `/api/v1` 的 TypeScript 客户端的目录名改为 `public`，并为它生成 Go 客户端，放在 `../api`。

可以设置的字段（没有写的使用默认值）：

| 字段 | 默认值 | 说明 |
| --- | --- | --- |
| `main` | `./cmd/api` | 应用入口的包 |
| `serve` | `["serve", "--config", "config/app.yaml,config/local.yaml"]`，`local.yaml` 不存在时去掉它 | 开发时运行应用的参数 |
| `url` | `http://localhost:8080` | 应用的地址 |
| `apis.<BasePath>.name` | `BasePath` 的最后一段 | 这个 API 的 TypeScript 客户端的目录名 |
| `apis.<BasePath>.go` | 不生成 | 这个 API 的 Go 客户端的目录 |
| `templ.proxyPort` | `7331` | templ 自动刷新代理的端口 |
| `templ.static` | 不监视 | 文件变化时刷新浏览器的目录 |
| `tailwind.input`、`tailwind.output` | 不使用 | Tailwind 的入口和生成的样式表，见[用 Tailwind 编写样式](../responses/assets.md#tailwind) |
| `tools` | 空 | 其他工具：`watch` 是开发时运行的命令，`build` 是停止后运行的命令 |

例如开发时用 esbuild 打包公开页面的脚本：

```json
{
  "tools": [{
    "watch": ["esbuild", "internal/web/js/app.js", "--bundle", "--outdir=internal/web/static/js", "--watch=forever"],
    "build": ["esbuild", "internal/web/js/app.js", "--bundle", "--outdir=internal/web/static/js", "--minify"]
  }]
}
```

前端不在 `tenon.json` 中配置，见[前端与 API 怎样被发现](#conventions)。

<a id="app-side"></a>
### 应用这一侧

`tenon` 需要你的应用配合两件事。用 `tenon init` 生成的项目已经写好了，这里解释它们为什么存在。

**导出接口。** 接口是程序运行时注册的，`tenon generate` 要运行你的程序才能读到它们。应用在根命令中加入 `httpcmd.Commands`，它提供 `openapi`、`routes`、`client` 三个命令，参数是构造全部路由的函数，见[命令行](../general/commands.md#declaring-the-cli)：

```go
root.AddCommand(httpcmd.Commands(func() chi.Routes { return routes(config.Default[Config](), nil) })...)
```

`routes` 就是 `serve` 也在用的那个函数。这些命令用默认配置和 `nil` 调用它，只读取构造出的路由，不连接数据库。

**接收前端开发服务器的地址。** 开发时，`tenon dev` 把各前端的 Vite 地址写进 `.tenon/dev.yaml` 的 `dev_servers`，再用 `--config` 把它交给应用。应用的配置中有这个字段，挂载前端时按名称取出地址，见[单页应用的开发](../responses/spa.md#development)：

```go
type Config struct {
    HTTP       httpx.Config      `yaml:"http"`
    DevServers map[string]string `yaml:"dev_servers"`
}
```
