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
- [tenon add client：桌面端等客户端](#add-client)
- [tenon agents：AI 编程助手](#agents)
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
| `tenon add client` | 需要桌面端等网页以外的客户端时 |
| `tenon agents` | 升级框架、加了前端或客户端以后，更新给 AI 编程助手的内容 |
| `tenon mcp` | 不用手动运行：AI 编程助手按 MCP 配置启动它，检索框架文档、读开发环境的输出 |

`serve`、`migrate`、`routes` 这类命令不属于 `tenon`，它们在你的应用自己的程序中，部署到服务器上以后照样可以运行，见[命令行](../general/commands.md)。

<a id="init"></a>
## tenon init：生成项目

```bash
go run github.com/go-tenon/tenon/cmd/tenon@latest init todo
```

在 `todo` 目录中生成一个能直接运行的项目，生成的内容见[脚手架](scaffolding.md#creating-a-project)。这里还没有项目，所以用 `go run` 直接运行 `tenon`，不需要先安装。

选项：

- `--module`：Go module 的路径，默认是目录名。
- `--name`：应用的名称，默认是目录名。
- `--agents`：为哪些 AI 编程助手安装技能、写 MCP 配置，默认 `claude,codex,cursor,antigravity`，还可以选 `copilot`，`none` 表示都不写，见 [AI 编程助手](ai-assistants.md#choosing-assistants)。
- `--tenon <框架目录>`：只在开发 Tenon 框架本身时使用，让项目使用本地的框架代码（`go.mod` 中写入 `replace`）。

<a id="dev"></a>
## tenon dev：开发

```bash
bun run dev   # 即 go tool tenon dev
```

开发时你需要同时运行好几样东西，`tenon dev` 把它们合在一起：

1. **前端的开发服务器**（TanStack Start 的 Vite）：每个前端一个，各用一个空闲的端口。修改前端代码后，页面立刻更新（热更新）。服务端渲染时它经环境变量 `API_ORIGIN` 中的地址调用你的接口。
2. **你的 Go 程序**：用 `serve` 命令运行。它把页面请求转发给对应的开发服务器，所以你只需要访问 Go 程序的地址（默认 <http://localhost:8080>），页面和接口都在这里，见[前端与 API 同源](scaffolding.md#frontend)。
3. **监视 Go 代码**：`.go` 文件（测试文件除外）、`go.mod`、`go.sum` 变化后重新编译。编译成功时，先平滑地停止旧的程序（等它处理完正在处理的请求），再启动新的；然后运行一次 [`generate`](#generate)，前端的类型随之更新。编译失败时打印错误，旧的程序继续运行，你改好代码保存后再试。

此外，`tenon.json` 的 `tools` 中的工具也一起运行，见[配置](#configuration)。

启动前，`tenon dev` 会检查需要的命令（Go、Bun 等）是否已经安装，缺少时直接告诉你。

按 Ctrl+C 停止。它先平滑地停止程序，再运行 `tools` 的 `build` 命令。前端不在这时构建：发布时由 `tenon build` 编译。

<a id="generate"></a>
## tenon generate：生成客户端

前端调用接口时，需要知道接口的地址、参数和返回的字段。这些信息已经写在 Go 代码中了，没必要在前端再写一遍：`tenon generate` 编译你的程序，读出全部接口，生成 TypeScript 代码，前端直接调用。接口加了字段，重新生成后前端就能用到；字段写错了，编辑器立刻提示。

```bash
bun run generate   # 即 go tool tenon generate
```

生成的内容：

- 每个 API 的 TypeScript 客户端，在 `packages/client/src/<名称>` 中，见[客户端代码](../http/clients.md#typescript)；
- 在 [tenon.json](#configuration) 中要求了的 Go 客户端，见 [Go 客户端](../http/go-client.md)。

`tenon dev` 每次重新编译成功后会自动运行它，所以平时不需要手动运行。

生成的代码要提交到代码仓库，这样只看代码的人也能读懂前端调用的是什么。在 CI 中重新生成一次，再用 `git diff --exit-code` 检查有没有变化，就能发现“改了接口但忘了重新生成”的情况。

<a id="build"></a>
## tenon build：构建

```bash
bun run build   # 即 go tool tenon build
```

构建出一个可以部署的可执行文件 `bin/blog`，前端打包在其中。它依次做这些事：

1. 安装前端的依赖：`bun install --frozen-lockfile`。`--frozen-lockfile` 要求依赖的版本与 `bun.lock` 完全一致，所以 CI 中构建出的结果和你本地的相同。
2. 清空 `apps/dist`（保留其中提交的 `.gitignore`），在每个前端目录中运行 `bun run build`（`vite build`），把浏览器的构建（`dist/client`，不含 source map）复制到 `apps/dist/<名称>`。
3. 有前端在服务端渲染（`package.json` 中 `"ssr": true`）时，用 `tenon-start compile` 把这些前端的服务端入口连同 Bun 编译成一个可执行文件 `apps/dist/ssr`，架构与 Go 程序相同（`go env GOARCH`，或 `--arch`），见 [TanStack Start 的接入](../frontend/start.md#compiling)。没有时跳过这一步，程序中不带 Bun。
4. 写入前端的清单 `apps/dist/frontends.json`。
5. 编译 Go 程序：`go build -trimpath -o bin/blog ./cmd/blog`。`-trimpath` 去掉二进制中你电脑上的文件路径。`apps/apps.go` 用 `go:embed` 嵌入整个 `apps/dist`。

`-o` 可以指定输出的文件。`--arch amd64` 或 `--arch arm64` 为另一种架构构建（Linux），例如在 arm64 的机器或 Mac 上构建部署到 amd64 服务器的程序；不写时与本机相同。Go 托管 SSR 进程只支持 Linux；前端都只在浏览器中渲染时，程序可以在任何系统上运行。部署见[部署](deployment.md)。

不要用 `GOARCH=amd64 bun run build` 交叉构建：`go tool tenon` 会按 `GOARCH` 把 tenon 本身也编译成目标架构，在本机无法运行。

<a id="add-frontend"></a>
## tenon add frontend：再加一个前端

一个项目可以有多个前端，例如面向用户的网站，加上给运营人员用的后台。在项目根目录运行：

```bash
go tool tenon add frontend admin --layout frame
```

它在 `apps/admin` 中生成一个前端，挂在 `/admin/` 下，加入根目录 `package.json` 的 workspace，安装依赖。`cmd/blog/main.go` 不用改：`frontend.Host` 按 `apps/dist` 中的清单托管全部前端，`/admin/` 下的请求交给新的前端，见[托管前端与服务端渲染](../frontend/hosting.md#wiring)。

生成的前端由四个互相独立的选项组合而成：布局、登录页、样式预设和渲染方式。只写 `--layout` 时，其余三个取这种布局的默认值；任何一个都可以单独换掉，例如 `--layout rail --login split --preset compact`。

`--layout` 选择布局，见[布局与导航](../frontend/layout.md)：

| 布局 | 内容 |
| --- | --- |
| `rail`（默认） | 双栏导航的布局（`RailLayout`），页面在布局路由 `_app` 之下 |
| `frame` | 顶栏加侧栏菜单、带多标签的布局（`FrameLayout`），页面在布局路由 `_app` 之下 |
| `site` | 官网和内容站（`SiteLayout`）：页头、页脚，首页是落地页的占位 |

`--login` 选择登录页的样式，见[登录页](../frontend/app.md#auth)：`centered`（居中的卡片，`CenteredAuthLayout`）、`split`（左右分栏，`SplitAuthLayout`）、`none`（不生成登录页和登录接口，页面都是公开的）。不写时按布局：`rail`、`frame` 是 `centered`，`site` 是 `none`。有登录页时，`rail`、`frame` 的 `_app` 要求先登录；`site` 的页头多一个“登录”按钮。

`--preset` 选择样式预设：`compact`（紧凑）、`comfortable`（舒展）、`spacious`（宽松），见[样式预设与主题](../frontend/app.md#presets)。不写时按布局：`frame` 是 `compact`，`rail` 是 `comfortable`，`site` 是 `spacious`。

`--render` 选择渲染方式：`csr` 只在浏览器中渲染，程序中不需要 Bun；`ssr` 在服务端渲染，搜索引擎可以收录。不写时按布局：`rail`、`frame` 是 `csr`，`site` 是 `ssr`，`site` 的首页还在构建时预渲染。服务端渲染的前端中，需要登录的 `_app` 设置了 `ssr: false`，只在浏览器中渲染。它写在 `package.json` 的 `tenon.ssr` 中，之后可以改，见[切换渲染方式](../frontend/hosting.md#switching)。

`--base` 可以指定其他的挂载路径，以 `/` 开头和结尾，不能与已有的前端相同。`apps/admin` 必须还不存在。

`--api` 是这个前端调用的 API 的路径，默认 `/api`。这个前端使用另一套账号、有自己的 API 时写这个 API 的路径，例如运营后台用 `--api /ops/api`，见[运营后台与多租户](../security/saas-example.md)。生成的文件按这个 API 的客户端（`packages/client/src/<名称>`，名称见[前端与 API 怎样被发现](#conventions)）是否已经生成而不同：

| 情况 | `src/api.ts` | `src/auth.ts` |
| --- | --- | --- |
| 客户端已经生成，其中有 `POST /auth/login`、`POST /auth/logout` 和 `GET /me` | 会话 `session`、客户端 `api`、元数据 `kit` | 登录、退出、读取当前用户都调用 `api` 中生成的方法 |
| 客户端已经生成，其中没有这三个接口 | 客户端 `api`、元数据 `kit`，会话来自 `auth.ts` | 会话，按约定的路径自己发请求 |
| 客户端还没有生成（API 还没有注册） | 不生成 | 同上 |

没有登录页（`--login none`）时 `api.ts` 中没有会话，也不生成 `auth.ts`。先注册 API 和登录接口、运行一次 `tenon generate`，再加前端，生成的代码就直接调用客户端；之后再注册的，照第一行的写法修改这两个文件。

<a id="add-client"></a>
## tenon add client：桌面端等客户端

把一个前端做成桌面端：

```bash
go tool tenon add client admin-desktop --kind electron --frontend admin
```

它在 `clients/admin-desktop` 中生成一个 Electron 桌面端，窗口中显示 `apps/admin`，并在 `apps/admin` 中加上授权页。`--title` 是窗口标题和安装包的名称，默认是客户端的名字。之后用 `go tool tenon dev --client admin-desktop` 同时运行它，用 `go tool tenon build --client admin-desktop` 打包。详细的用法见 [Electron 桌面端](../clients/electron.md)，有哪些种类见[客户端](../clients/README.md)。

<a id="agents"></a>
## tenon agents：AI 编程助手

```bash
bun run agents   # 即 go tool tenon agents
```

更新 AI 编程助手用到的内容：把项目所用版本的框架文档复制到 `.tenon/docs`，为 Claude Code、Codex、Cursor、Antigravity 等助手安装技能（`tenon-action`、`tenon-http-api` 等）、在 MCP 配置中写上 `tenon`。升级框架以后运行它；`tenon add frontend`、`tenon add client` 结束时会自动运行一次，`tenon dev` 每次启动时更新 `.tenon/docs`。AI 编程助手还会按 MCP 配置启动 `tenon mcp`，用它检索框架的文档、读 `tenon dev` 的输出（`.tenon/dev.log`）。Go、Mantine、TanStack 等依赖给 AI 的资源按它们的官方方案接入，见[依赖的官方方案](ai-assistants.md#dependencies)。详细的说明见 [AI 编程助手](ai-assistants.md)。

<a id="troubleshooting"></a>
## 常见问题

**`tenon dev` 提示缺少某个命令**

按提示安装，例如 Bun 见[准备工作](scaffolding.md#requirements)。

**修改了 Go 代码，程序没有更新**

看终端中有没有编译错误：编译失败时旧的程序会继续运行。改正后保存，`tenon dev` 会再编译一次。

**CI 中 `tenon build` 报错 lockfile 与 package.json 不一致**

修改了前端的依赖，但没有提交更新后的 `bun.lock`。在本地运行 `bun install`，提交 `bun.lock`。

**启动时报 `address already in use`**

应用的端口被另一个进程占用了，常见的是另一个项目或者没有停掉的上一次运行。停掉那个进程，或者让应用监听另一个端口：在 `config/local.yaml` 中改 `http.addr`，例如 `":8082"`。`tenon.json` 中没有写 `url` 时，前端的开发服务器调用的地址跟着变。

**`tenon generate` 报错两个 API 的名称相同**

客户端的目录名默认是 API 地址的最后一段（跳过 `api` 和路径参数），`/api/v1` 和 `/admin/v1` 都叫 `v1`。在 [tenon.json](#configuration) 中给其中一个改名，报错信息中给出了写法，例如 `"apis": {"/admin/v1": {"name": "admin"}}`。

<a id="advanced"></a>
## 进阶

<a id="conventions"></a>
### 前端与 API 怎样被发现

`tenon` 不需要你登记有哪些前端和 API，它按目录约定找到它们。

**前端**是 `apps` 中带 `vite.config.ts`（或 `.mts`、`.js`、`.mjs`）的目录，目录名就是前端的名称，例如 `apps/web`、`apps/admin`。名称以小写字母开头，由小写字母、数字和连字符组成。每个前端的 `package.json` 用 `tenon` 字段声明挂载路径和是否在服务端渲染，并有三个脚本：

```json
{
  "tenon": { "base": "/admin/", "ssr": false },
  "scripts": {
    "dev": "vite dev",
    "build": "bunx --bun vite build",
    "typecheck": "tenon-start routes && tsc --noEmit"
  }
}
```

`base` 以 `/` 开头和结尾，两个前端不能相同。`tenon` 和 `@go-tenon/start` 的 Vite 插件读取同一个字段，见[托管前端与服务端渲染](../frontend/hosting.md#modes)。

构建的结果在 `apps/dist` 中，由 `apps/apps.go` 打包进程序。构建的结果不提交，目录中只提交一个 `.gitignore`：它忽略目录中的其他文件，也让 `go:embed all:dist` 在刚 clone、还没有生成任何文件时有文件可嵌入，`go build`、`go vet`、`go test` 照常通过。项目根目录的 `.gitignore` 用 `!apps/dist/` 放行这个目录。`tenon dev`、`tenon generate` 写入其中的清单 `frontends.json`，`tenon build` 写入全部构建结果。

`tenon init` 较早生成的项目没有这个文件，刚 clone 时编译报 `pattern all:dist: no matching files found`。在根目录 `.gitignore` 的 `dist/` 之后加一行 `!apps/dist/`，运行一次 `go tool tenon generate`，提交生成的 `apps/dist/.gitignore`。

**API** 是你在路由中用 `rest.New` 创建的每一个 API。每个 API 的 TypeScript 客户端生成到 `packages/client/src/<名称>`。名称默认取 `BasePath` 的最后一段，跳过 `api` 这一段和路径参数，所以后台的 API 挂在 `/api/admin` 或 `/admin/api` 都叫 `admin`：

| BasePath | 客户端的目录 | 前端中导入 |
| --- | --- | --- |
| `/api` | `packages/client/src/api` | `@todo/client/api` |
| `/api/admin` | `packages/client/src/admin` | `@todo/client/admin` |
| `/admin/api` | `packages/client/src/admin` | `@todo/client/admin` |
| `/t/{tenant}/api` | `packages/client/src/t` | `@todo/client/t` |
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
    "./*/settings": "./src/*/settings.ts",
    "./*/schemas": "./src/*/schemas.ts",
    "./*/kit": "./src/*/kit.ts",
    "./*": "./src/*/api.ts"
  }
}
```

生成的每个文件的内容见[客户端代码](../http/clients.md#generating-types)。

<a id="framework-packages"></a>
### 框架的前端包

Tenon 也提供了一些前端包，例如 `@go-tenon/client`（调用接口、管理登录）和 `@go-tenon/ui`（应用骨架和界面组件）。它们不从 npm 安装，而是随 Go module 一起分发：源码在框架的 `js` 目录中，版本就是 `go.mod` 中框架的版本，前后端的版本永远一致。

`tenon` 在需要前端依赖之前（`init`、`add frontend`、`dev`、`build`、`generate`），把这些包复制到项目的 `.tenon/js/<包名>`。项目把它们列入 Bun 的 workspace，前端用 `workspace:*` 依赖它们：

```json
{
  "workspaces": [".tenon/js/client", ".tenon/js/ui", "apps/web", "packages/client"]
}
```

```json
{
  "dependencies": { "@go-tenon/client": "workspace:*", "@go-tenon/ui": "workspace:*" }
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
| `main` | `cmd` 下唯一一个有 `main.go` 的目录，例如 `./cmd/blog` | 应用入口的包；`cmd` 下有多个程序时写明 |
| `serve` | `["serve", "--config", "config/app.yaml,config/local.yaml"]`，`local.yaml` 不存在时去掉它 | 开发时运行应用的参数 |
| `url` | 按应用配置中的 `http.addr` 推出：端口相同，主机是 `localhost`；没有 `http.addr` 时是 `http://localhost:8080` | 应用的地址：`tenon dev` 打印它，前端的开发服务器经它调用接口。读取的是 `serve` 中 `--config` 的文件，所以改了 `config/app.yaml` 或 `config/local.yaml` 中的端口，这里跟着变；只在应用不在本机这个端口上时才需要写 |
| `apis.<BasePath>.name` | `BasePath` 的最后一段，跳过 `api` 和路径参数 | 这个 API 的 TypeScript 客户端的目录名 |
| `apis.<BasePath>.go` | 不生成 | 这个 API 的 Go 客户端的目录 |
| `tools` | 空 | 其他工具：`watch` 是开发时运行的命令，`build` 是停止后运行的命令 |
| `agents.targets` | `["claude", "codex", "cursor", "antigravity"]` | `tenon agents` 为哪些 AI 编程助手安装技能、写 MCP 配置，见 [AI 编程助手](ai-assistants.md#choosing-assistants) |
| `agents.skip` | 空 | 不安装的框架技能，见 [AI 编程助手](ai-assistants.md#skills) |

例如开发时用 esbuild 打包一个嵌入到其他网站的小组件脚本：

```json
{
  "tools": [{
    "watch": ["esbuild", "widget/src/widget.js", "--bundle", "--outdir=widget/dist", "--watch=forever"],
    "build": ["esbuild", "widget/src/widget.js", "--bundle", "--outdir=widget/dist", "--minify"]
  }]
}
```

前端不在 `tenon.json` 中配置，见[前端与 API 怎样被发现](#conventions)。

<a id="app-side"></a>
### 应用这一侧

`tenon` 需要你的应用配合两件事。用 `tenon init` 生成的项目已经写好了，这里解释它们为什么存在。

**导出接口。** 接口是程序运行时注册的，`tenon generate` 要运行你的程序才能读到它们。应用在根命令中加入 `httpcmd.Commands`，它提供 `openapi`、`routes`、`client` 三个命令，参数是构造全部路由的函数，见[命令行](../general/commands.md#declaring-the-cli)：

```go
root.AddCommand(httpcmd.Commands(func() chi.Routes { return routes(config.Default[Config](), nil, nil) })...)
```

`routes` 就是 `serve` 也在用的那个函数。这些命令用默认配置和 `nil` 调用它，只读取构造出的路由，不连接数据库，也不启动 SSR 进程。

**接收前端开发服务器的地址。** 开发时，`tenon dev` 把各前端的开发服务器地址写进 `.tenon/dev.yaml` 的 `dev_servers`，再用 `--config` 把它交给应用。应用的配置中有这个字段，创建 `frontend.Host` 时把它交给 `DevServers`，每个前端的请求转给自己的开发服务器，见[托管前端：开发](../frontend/hosting.md#development)：

```go
type Config struct {
    HTTP       httpx.Config      `yaml:"http"`
    DevServers map[string]string `yaml:"dev_servers"`
}
```
