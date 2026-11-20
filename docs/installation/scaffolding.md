# 脚手架

在[安装](installing.md)中，我们手写了一个只有 `/ping` 的程序。真实的项目还需要配置文件、命令行、前端页面、开发时的自动重启……一个个手写很费时间。`tenon init` 一次生成这些，得到一个能直接运行的项目：一个 Go 写的 API，一个 React 写的网页，以及一个待办事项的例子把两者连起来。

这一页先生成项目、运行起来，动手改一改，再回头看它由哪些部分组成。

阅读前需要了解：[安装](installing.md)。

- [准备工作](#requirements)
- [生成项目](#creating-a-project)
- [运行开发环境](#developing)
- [动手改一改](#changing)
- [项目里有什么](#layout)
- [入口文件](#entrypoint)
- [前端与 API 同源](#frontend)
- [构建](#building)
- [CI](#ci)
- [常见问题](#troubleshooting)
- [下一步](#next-steps)

<a id="requirements"></a>
## 准备工作

除了 Go 1.27.4 或更高的版本，还需要 [Bun](https://bun.sh)。Bun 是一个 JavaScript 运行环境，这里用它安装前端的依赖、运行前端的开发服务器。按 Bun 官网的说明安装后，查看版本：

```bash
bun --version
```

<a id="creating-a-project"></a>
## 生成项目

在你想放项目的位置运行（`todo` 是新项目的目录名）：

```bash
go run github.com/go-tenon/tenon/cmd/tenon@latest init todo
```

这条命令会：

1. 创建 `todo` 目录，写入项目的全部文件。目录必须不存在或者是空的。
2. 运行 `go mod tidy` 下载 Go 依赖，`bun install` 安装前端依赖。
3. 根据接口生成前端调用接口用的 TypeScript 代码（下文的“客户端”）。

Go module 的路径和应用的名称默认都是目录名，可以用 `--module` 和 `--name` 指定，例如 `--module github.com/acme/todo`。其他选项见[开发工具](tooling.md#init)。

项目的 `go.mod` 中记录了生成时使用的 Tenon 版本：用 `@latest` 生成时是当时最新发布的版本，用 `@v0.0.1` 时是这个版本，用 `@main` 时是主分支当时的提交。

<a id="developing"></a>
## 运行开发环境

进入项目目录，启动开发环境：

```bash
cd todo
bun run dev
```

在浏览器中打开 <http://localhost:8080>，可以添加、勾选、删除待办事项。接口文档在 <http://localhost:8080/api/docs>。

`bun run dev` 运行的是 `go tool tenon dev`。它同时启动两个程序：Go 写的 API，以及前端的开发服务器 Vite。你只需要访问 8080 端口，页面和接口都在这里，原因见下文的[前端与 API 同源](#frontend)。

<a id="changing"></a>
## 动手改一改

保持 `bun run dev` 运行，试着修改代码。

**修改前端。** 打开 `apps/web/src/routes/index.tsx`，改一下页面上的文字，保存。浏览器中的页面立刻更新，不需要刷新，这叫热更新。

**修改 Go 代码。** 打开 `internal/todo/todo.go`，给返回的错误提示或者某个字段的校验规则做一点修改，保存。终端中会看到程序被重新编译、重新启动。

修改接口的输入或输出时，开发环境还会重新生成前端的客户端（`packages/client/src/api`）。例如给 `Todo` 加一个字段，前端的代码马上就能用到这个字段，写错字段名时编辑器会立刻提示。

<a id="layout"></a>
## 项目里有什么

项目可以分成三部分：Go 写的后端、React 写的前端（TanStack Start 和 Mantine），以及根据它们自动生成的代码。

```text
todo/
├── go.mod                 Go module；Tenon 的开发工具也记在这里，版本与框架相同
├── package.json           前端的 workspace，以及 dev、build、generate 等脚本
├── README.md              怎样运行，以及 AI 编程助手怎样接入 Go、Mantine、TanStack 的官方资源
├── .github/workflows/     CI：检查、测试和构建，见下文的 CI
├── AGENTS.md              项目说明：怎样运行、提交前检查什么、代码约定；AI 编程助手读它
├── CLAUDE.md              一行 @AGENTS.md，Claude Code 经它读 AGENTS.md
├── .mcp.json              Claude Code 的 MCP 配置（Codex 在 .codex/，Cursor 在 .cursor/，Antigravity 在 .agents/）
├── .claude/skills/        给 AI 编程助手的技能（Codex 读 .agents/skills/）
├── config/app.yaml        应用的配置，例如监听地址
├── cmd/todo/main.go       程序的入口
├── internal/todo/todo.go  待办事项：类型、数据（保存在内存中）和四个接口
├── apps/apps.go           把前端的构建产物（apps/dist）打包进 Go 程序
├── apps/web/              前端：React 写的网页，挂在 / 下
│   ├── package.json       依赖和脚本；tenon 字段写挂载路径和是否在服务端渲染
│   ├── vite.config.ts     前端构建工具 Vite 的配置
│   └── src/
│       ├── router.tsx     创建前端应用：路由、登录状态等
│       ├── start.ts       TanStack Start 的设置：请求中间件
│       ├── server.ts      服务端渲染的入口
│       ├── theme.ts       主题：样式预设和品牌色
│       ├── api.ts         创建调用接口的客户端
│       └── routes/        页面：一个文件一个页面
├── packages/client/       生成的 TypeScript 客户端，前端用它调用接口
└── .tenon/                开发工具生成的内容（框架的前端包和文档），不提交到代码仓库
```

刚开始只需要关心三个地方：

- **`internal/`**：业务代码。`internal/todo` 是一个例子，你的业务也按模块放在这里，例如 `internal/article`。
- **`cmd/todo/main.go`**：程序的入口，把业务模块注册到路由上。目录名取自项目名，构建出的程序是 `bin/todo`。
- **`apps/web/src/routes/`**：前端页面。

`packages/` 和 `.tenon/` 由工具生成，不需要手动修改。各个目录的详细约定见[目录结构](../concepts/structure.md)。`AGENTS.md`、技能和 MCP 配置让 Claude Code、Codex、Cursor、Antigravity 按项目的约定写代码，见 [AI 编程助手](ai-assistants.md)。

<a id="entrypoint"></a>
## 入口文件

打开 `cmd/todo/main.go`。它和[安装](installing.md#graceful-shutdown)第三步的程序做的是同一件事：创建路由器、安装中间件、注册接口、启动服务、收到信号后平稳地停止。不同的是它多了三样东西：配置文件、命令行和前端的页面。我们分段来看。

### 配置

```go
type Config struct {
    HTTP       httpx.Config      `yaml:"http"`
    DevServers map[string]string `yaml:"dev_servers"`
}
```

`Config` 描述了配置文件 `config/app.yaml` 的结构。`HTTP` 对应文件中的 `http` 段，例如 `addr: ":8080"`。安装那一页中写在代码里的监听地址，现在放进了配置文件，改地址不需要重新编译。`DevServers` 只在开发时由 `tenon dev` 填写，生产环境中为空。配置的写法见[配置](../general/configuration.md)。

### 命令行

```go
func main() {
    root := command.Root("todo", "todo")
    root.AddCommand(
        command.Serve(open, parts),
        command.Config[Config](),
        httpcmd.Health(func(cfg Config) httpx.Config { return cfg.HTTP }),
    )
    root.AddCommand(httpcmd.Commands(func() chi.Routes { return routes(config.Default[Config](), nil, nil) })...)
    command.Execute(root)
}
```

程序现在有了子命令。`go run ./cmd/todo serve` 启动服务，`go run ./cmd/todo config check` 检查配置文件，以后还可以加上 `migrate`（数据库迁移）等命令：

- **`command.Serve(open, parts)`** 创建 `serve` 命令。它先读取并检查配置，调用 `open` 准备好程序要用的资源，再运行 `parts` 中列出的部分；收到停止信号时取消 ctx，和安装那一页的第三步一样。
- **`command.Config[Config]()`** 加上 `config check`、`config show` 两个命令。
- **`httpcmd.Health`** 加上 `health` 命令，在 Docker 中用作健康检查，见[部署](deployment.md#health-checks)。
- **`httpcmd.Commands`** 加上 `openapi`（导出接口文档）、`routes`（列出全部路由）、`client`（生成客户端）。开发工具用它们生成前端的客户端。

命令行的写法见[命令行](../general/commands.md)。

### open 和 parts

```go
type App struct {
    cfg   Config
    store *todo.Store
}

func open(_ context.Context, cfg Config, _ *cleanup.Stack) (*App, error) {
    slog.SetDefault(logging.New(logging.Config{}))
    return &App{cfg: cfg, store: todo.NewStore()}, nil
}

func parts(a *App) []command.Part {
    return []command.Part{{Name: "http", Short: "接口和页面", Start: a.serveHTTP}}
}
```

`App` 保存程序运行时共用的东西，现在是配置和待办事项的存储。`open` 创建它们；以后接上数据库，也在这里打开数据库，并把关闭操作登记到 `stack`，程序退出时自动关闭。

`parts` 列出程序中一直运行的**部分**。现在只有 HTTP 服务一个；以后加上处理后台任务的 worker、执行定时任务的调度器，就在这里各加一行。`serve` 运行全部部分，`serve http` 只运行其中一个，见[serve：运行常驻进程](../general/commands.md#serve-command)。

### serveHTTP 和 routes

```go
func (a *App) serveHTTP(d *command.Daemon) (func(context.Context) error, error) {
    var router chi.Router
    api := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { router.ServeHTTP(w, r) })
    pages, err := frontend.New(apps.Dist, frontend.Options{API: api, DevServers: a.cfg.DevServers})
    if err != nil {
        return nil, err
    }
    router = routes(a.cfg, a.store, pages.Handler())
    ln, err := d.Listen("tcp", a.cfg.HTTP.Addr)
    if err != nil {
        return nil, err
    }
    return func(ctx context.Context) error {
        return pages.Run(ctx, func(ctx context.Context) error {
            return httpx.Serve(ctx, a.cfg.HTTP, ln, router)
        })
    }, nil
}

func routes(cfg Config, store *todo.Store, pages http.Handler) chi.Router {
    router := chi.NewRouter()
    router.Use(httpx.Defaults(cfg.HTTP)...)
    router.Get("/livez", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
    router.Route("/api", func(r chi.Router) {
        r.Use(httpx.Timeout(cfg.HTTP.Timeout))
        api := rest.New(r, rest.Options{Title: "todo", Version: "0.1.0", BasePath: "/api"})
        todo.RegisterRoutes(api, store)
    })
    if pages != nil {
        router.Handle("/*", pages)
    }
    return router
}
```

`serveHTTP` 分两步：先准备好一切，包括取得监听端口 `ln`；再返回运行服务的函数。端口被占用这类错误在服务开始运行之前就会返回。

`routes` 做了四件事：

1. 安装默认的中间件，和安装那一页的第二步相同。
2. 注册存活检查 `/livez`：程序能响应就返回 `204`，`health` 命令请求的就是它。
3. 在 `/api` 下创建 API，注册待办事项的接口。超时中间件只装在 `/api` 上，因为接口应当很快返回，而页面相关的连接可能保持很久。
4. 其余的地址（`/*`）都是页面，交给 `pages`，见下一节。

为什么要把 `routes` 单独拆出来？因为它有两个调用者：

- `serveHTTP` 用真实的配置和数据调用它，启动服务；
- `openapi`、`client` 等命令用默认配置和 `nil` 调用它，只为了读出有哪些接口，生成文档和客户端，不需要真的连接数据库或启动页面。

`todo.RegisterRoutes` 只是把 `store` 保存下来，处理请求时才使用它，所以传 `nil` 也能注册全部接口。以后你新增一个业务模块，只需要在 `routes` 中加一行 `RegisterRoutes`，服务、接口文档和客户端都会看到它。

`serveHTTP` 比安装那一页的 `run` 多了页面：`frontend.New` 读取嵌入的 `apps.Dist`，创建托管全部前端的 `Host`，它的 `Handler` 按挂载路径返回页面。`web` 在服务端渲染，所以 `pages.Run` 先启动它的 SSR 进程、等它就绪，再运行 HTTP 服务；停止时先排空请求，再停 SSR 进程。SSR 进程渲染页面需要数据时，经内部 socket 调用同一个 `router`。

<a id="frontend"></a>
## 页面与 API 同源

浏览器有一条安全规则：一个网页只能自由地调用**同源**的接口。同源指协议、域名和端口都相同，例如 `http://localhost:8080` 上的页面调用 `http://localhost:8080/api/todos`。如果页面在 5173 端口、接口在 8080 端口，就是跨域，需要额外配置，并且登录用的 cookie 也会受限。

所以项目让页面和接口都在同一个端口上，Go 是唯一的入口：

- **开发时**：前端的开发服务器（TanStack Start 的 Vite）在另一个端口上运行，但你不直接访问它。Go 程序在 8080 端口上处理 `/api` 的请求，把其余的请求（页面、热更新）转发给开发服务器。浏览器始终只访问 8080。
- **发布时**：`tenon build` 构建前端，把静态文件放进 `apps/dist`；`web` 在服务端渲染，它的服务端入口连同 Bun 编译成 `apps/dist/ssr`。`apps/apps.go` 把 `apps/dist` 打包进 Go 程序（见 [embed](../concepts/go-primer.md#embed)）。Go 程序直接返回静态文件，从内存中运行 SSR 程序，页面请求经 unix socket 交给它渲染。

`apps/web/package.json` 中的 `"ssr": true` 决定 `web` 在服务端渲染。后台这类不需要搜索引擎收录的前端只在浏览器中渲染，Go 直接返回页面，程序中不带 Bun。细节见[托管前端与服务端渲染](../frontend/hosting.md)和 [TanStack Start 的接入](../frontend/start.md)。

<a id="building"></a>
## 构建

准备上线时，构建出一个可执行文件：

```bash
bun run build
./bin/todo serve --config config/app.yaml
```

`bun run build` 先构建每个前端，把服务端渲染的前端连同 Bun 编译成 SSR 程序（架构与 Go 程序相同），最后编译 Go 程序，把它们打包在其中。部署时只需要带上 `bin/todo` 和配置文件，见[部署](deployment.md)。Go 托管的 SSR 进程只支持 Linux；在 macOS 上开发时用 `bun run dev`。

<a id="ci"></a>
## CI

项目带着 GitHub Actions 的配置 `.github/workflows/ci.yml`，推送到 GitHub 后，每次推送到 `main` 和每个 PR 都会运行。它有两个并行的任务：

| 任务 | 做什么 |
| --- | --- |
| `check` | `go tool tenon generate` 之后检查工作区没有变化：改了接口却没有提交重新生成的客户端时失败；`go tool tenon agents` 之后检查技能没有变化：升级框架后没有运行 `bun run agents` 时失败；然后是 `gofmt`、`go vet` 和 `go test` |
| `build` | 同步框架的前端包，`bun install --frozen-lockfile`；`bun run typecheck` 之后检查路由树 `routeTree.gen.ts` 没有变化；最后 `bun run build` |

`tenon` 的版本由 `go.mod` 固定，Bun 的版本写在 `package.json` 的 `packageManager` 中，CI 与本地用的是同样的版本。

生成的代码（客户端、路由树、技能）都要提交：CI 重新生成一次，有差异就说明有人忘了提交。`tenon init` 已经生成了它们，第一次提交时就是完整的。

项目用到数据库以后，在 `check` 任务中加上 PostgreSQL，见[在 CI 中运行数据库测试](../testing/database.md#database-tests-in-ci)。代码不放在 GitHub 上时，删掉 `.github` 目录，按同样的步骤写你所用平台的 CI。

<a id="troubleshooting"></a>
## 常见问题

**`tenon init` 报错目录不是空的**

`init` 只往空目录或不存在的目录中写入，避免覆盖已有的文件。换一个目录名，或者先清空目录。

**`bun: command not found`**

还没有安装 Bun，或者安装后没有重新打开终端，见[准备工作](#requirements)。

**打开 8080 后页面一片空白**

前端的开发服务器还在启动，等终端中出现 Vite 就绪的提示后刷新页面。一直空白时，看终端中有没有前端的报错。

**前端代码中找不到新加的字段**

客户端没有重新生成。开发环境运行时会自动生成；没有运行开发环境时，手动运行 `bun run generate`。

<a id="next-steps"></a>
## 下一步

- 跟着[入门教程](../tutorial/README.md)把待办事项换成博客：接上数据库、加上登录和草稿；
- 了解 `dev`、`build`、`generate` 这几个命令的细节，读[开发工具](tooling.md)；
- 准备上线时读[部署](deployment.md)。
