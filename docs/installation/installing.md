# 安装与生成项目

用 `tenon init` 生成能直接运行的项目：Go 写的 API 加 TanStack Start 写的前端，用一个待办事项的例子连起来。这一页说明怎样运行它、项目的组成、入口的装配、构建和 CI。

包：`cmd/tenon`、`command`、`frontend`。

<a id="requirements"></a>
## 准备工作

- Go 1.27.4 或更高的版本；
- [Bun](https://bun.sh)：安装前端的依赖、运行前端的开发服务器和构建；
- Docker：仅用于开发和测试期，在主工作区启动 PostgreSQL、NATS、Redis 等依赖服务。

<a id="creating-a-project"></a>
## 用 `tenon init` 生成项目

```bash
go run github.com/go-tenon/tenon/cmd/tenon@latest init todo
cd todo
bun run dev   # 打开 http://localhost:8080，接口文档在 http://localhost:8080/api/docs
```

`init` 写入项目的全部文件，运行 `go mod tidy`，生成 TypeScript 客户端，运行 `bun install` 并生成路由树，最后安装给 AI 编程助手的技能和配置（见 [AI 编程助手](ai-assistants.md)）。目录必须不存在或者是空的。

- `--module`、`--name` 指定 Go module 的路径和应用的名称，默认都是目录名，例如 `--module github.com/acme/todo`。其他选项见 [tenon init](tooling.md#init)。
- `go.mod` 记录生成时的 Tenon 版本：`@latest` 是最新发布的版本，`@main` 是主分支当时的提交。0.x 版本之间可能不兼容，升级前先看发布说明。

<a id="developing"></a>
## 运行开发环境：`bun run dev`

`bun run dev` 运行 `go tool tenon dev`：同时运行 Go 程序和前端的开发服务器（Vite）。只访问 Go 程序的端口 8080，页面和接口都在这里，见[页面与 API 同源](#frontend)。

- 改前端（例如 `apps/web/src/routes/index.tsx`）：页面热更新。
- 改 Go 代码（例如 `internal/todo/todo.go`）：重新编译、平滑地重启；编译失败时终端中打印错误，旧的程序继续运行。
- 改接口的输入或输出：重新生成前端的客户端 `packages/client/src/api`，前端代码中立刻能用新的字段。

**主工作区依赖 + `git worktree` 并行开发**：在主工作区运行 `docker compose up -d --wait` 常驻 PostgreSQL、NATS、Redis 等依赖，业务开发和调试在独立的 `git worktree` 中执行 `bun run dev` 和 `go test ./...`，复用主工作区的依赖容器。多个 worktree 同时运行业务服务时，在各自的 `config/local.yaml`（不提交）中改写 `http.addr`（例如 `:8081`）错开端口，需要时为每个 worktree 指定独立的开发库名；`tenon dev` 自动为各前端开发服务器分配空闲端口。

细节见 [tenon dev](tooling.md#dev)。

<a id="layout"></a>
## 项目里有什么

```text
todo/
├── go.mod                 Go module；tool 一行记录开发工具 tenon，版本与框架相同
├── package.json           Bun workspace，dev、build、generate、agents、typecheck 脚本
├── README.md              怎样运行；Go、Mantine、TanStack 给 AI 的资源怎样接入
├── AGENTS.md              项目说明和代码约定，AI 编程助手读它
├── .mcp.json              Claude Code 的 MCP 配置，其他助手的见 AI 编程助手
├── .claude/skills/        给 AI 编程助手的技能（Codex、Antigravity 读 .agents/skills/）
├── .github/workflows/     CI
├── config/app.yaml        应用的配置，例如监听地址 http.addr
├── cmd/todo/main.go       程序的入口
├── internal/todo/todo.go  待办事项：类型、内存中的存储和四个接口
├── locales/web.zh-CN.yaml 前端 web 的界面文字
├── apps/apps.go           用 go:embed 把前端的构建产物 apps/dist 打包进 Go 程序
├── apps/web/              前端，挂在 / 下，在服务端渲染
│   ├── package.json       tenon 字段：挂载路径和是否在服务端渲染
│   └── src/               router.tsx、start.ts、server.ts、theme.ts、i18n.ts、api.ts；routes/ 中一个文件一个页面
├── packages/client/       生成的 TypeScript 客户端，前端用它调用接口
└── tenon/                 框架同步的文档（tenon/docs）和前端包（tenon/js），不手改
```

- 业务代码按模块放在 `internal/` 中，例如 `internal/article`。入口在 `cmd/<名称>/main.go`，构建出的程序是 `bin/<名称>`。页面在 `apps/web/src/routes/` 中。
- `packages/` 和 `tenon/` 由工具生成或同步，不手动修改。

各目录的约定见[目录结构](../concepts/structure.md)。

<a id="entrypoint"></a>
## 入口文件：配置和命令

`cmd/todo/main.go` 定义配置、登记命令：

```go
type Config struct {
    HTTP       httpx.Config      `yaml:"http"`        // config/app.yaml 的 http 段
    DevServers map[string]string `yaml:"dev_servers"` // 前端开发服务器的地址，只在 tenon dev 时有值
}

func main() {
    root := command.Root("todo", "todo")
    root.AddCommand(
        command.Serve(open, parts),  // serve：读取并检查配置，调用 open，运行 parts
        command.Config[Config](),    // config check、config show
        httpcmd.Health(func(cfg Config) httpx.Config { return cfg.HTTP }), // health：Docker 的健康检查
    )
    // openapi、routes、client：开发工具用它们生成文档和客户端
    root.AddCommand(httpcmd.Commands(func() chi.Routes { return routes(config.Default[Config](), nil, nil) })...)
    command.Execute(root)
}
```

`go run ./cmd/todo serve` 启动服务，收到 `SIGTERM` 或 Ctrl+C 时平滑地停止，见[应用生命周期](../concepts/lifecycle.md)。配置见[配置](../general/configuration.md)，命令见[命令行](../general/commands.md)。

<a id="entry-parts"></a>
### `open` 和 `parts`

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

- `App` 保存进程中各部分共用的资源。`open` 创建它们；接上数据库后也在这里打开，把关闭登记到 `cleanup.Stack`，进程退出时自动关闭。
- `parts` 列出常驻运行的部分。加上队列的 worker、定时任务的调度器时各加一项；`serve` 运行全部部分，`serve http` 只运行其中一个，见 [serve](../general/commands.md#serve-command)。

<a id="entry-routes"></a>
### `serveHTTP` 和 `routes`

```go
func (a *App) serveHTTP(d *command.Daemon) (func(context.Context) error, error) {
    var router chi.Router
    api := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { router.ServeHTTP(w, r) })
    pages, err := frontend.New(apps.Dist, frontend.Options{API: api, DevServers: a.cfg.DevServers})
    if err != nil {
        return nil, err
    }
    router = routes(a.cfg, a.store, pages.Handler())
    ln, err := d.Listen("tcp", a.cfg.HTTP.Addr) // 端口被占用时在这里返回错误
    if err != nil {
        return nil, err
    }
    return func(ctx context.Context) error {
        // 先启动 SSR 进程，再运行 HTTP 服务；停止时先排空请求，再停 SSR 进程
        return pages.Run(ctx, func(ctx context.Context) error {
            return httpx.Serve(ctx, a.cfg.HTTP, ln, router)
        })
    }, nil
}

func routes(cfg Config, store *todo.Store, pages http.Handler) chi.Router {
    router := chi.NewRouter()
    router.Use(httpx.Defaults(cfg.HTTP)...) // 请求 ID、访问日志、捕获 panic 等默认中间件
    router.Get("/livez", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
    router.Route("/api", func(r chi.Router) {
        r.Use(httpx.Timeout(cfg.HTTP.Timeout)) // 超时只装在接口上，页面的连接可能保持很久
        api := rest.New(r, rest.Options{Title: "todo", Version: "0.1.0", BasePath: "/api"})
        todo.RegisterRoutes(api, store)
    })
    if pages != nil {
        router.Handle("/*", pages) // 其余的路径都是页面
    }
    return router
}
```

- `routes` 有两个调用者：`serveHTTP` 用真实的配置和依赖启动服务；`openapi`、`client` 等命令用默认配置和 `nil` 构造路由，只读出接口。所以 `RegisterRoutes` 只保存依赖，处理请求时才使用它们。
- 新的业务模块在 `routes` 中加一行 `RegisterRoutes`，服务、接口文档和客户端都会包含它。
- 每个接口都要声明访问策略（`rest.Public()`、`RequireToken` 等），没有声明时 `rest.Register` 在启动时 panic，需要保护的接口不会被悄悄公开，见[访问策略](../http/routing.md#access-policies)。
- `frontend.New` 读取嵌入的 `apps.Dist`，按挂载路径托管全部前端；SSR 进程渲染页面时经内部 socket 调用同一个 `router`，见[托管前端与服务端渲染](../frontend/hosting.md)。

<a id="frontend"></a>
## 页面与 API 同源

Go 程序是唯一的入口，页面和接口在同一个源上：页面调用 `/api` 不跨域，登录的 cookie 照常发送。

- **开发时**：前端的开发服务器在另一个空闲端口上运行。Go 程序处理 `/api`，把其余的请求（页面、热更新）转发给开发服务器。
- **发布时**：`tenon build` 把前端的静态文件放进 `apps/dist`，服务端渲染的前端连同 Bun 编译成 `apps/dist/ssr`，`apps/apps.go` 把整个目录嵌入 Go 程序。Go 程序直接返回静态文件，从内存中运行 SSR 程序，页面请求经 unix socket 交给它渲染。

`apps/web/package.json` 中 `"tenon": {"ssr": true}` 让 `web` 在服务端渲染。后台这类不需要搜索引擎收录的前端只在浏览器中渲染，程序中不带 Bun，见[切换渲染方式](../frontend/hosting.md#switching)。

<a id="building"></a>
## 构建

```bash
bun run build                         # 即 go tool tenon build，输出 bin/todo
./bin/todo serve --config config/app.yaml
```

部署时只需要 `bin/todo` 和配置文件，见[部署](deployment.md)。Go 托管的 SSR 进程只支持 Linux：在 macOS 上用 `bun run dev` 开发，构建好的程序在 Linux 上运行。构建的步骤和选项见 [tenon build](tooling.md#build)。

<a id="ci"></a>
## CI

`.github/workflows/ci.yml` 在推送到 `main` 和每个 PR 时运行两个并行的任务：

| 任务 | 做什么 |
| --- | --- |
| `check` | `go tool tenon generate` 后检查工作区没有变化（改了接口没有提交重新生成的客户端时失败）；`go tool tenon agents` 后检查技能没有变化（升级框架后没有运行 `bun run agents` 时失败）；`gofmt`、`go vet`、`go test ./...` |
| `build` | `go tool tenon generate` 复制框架的前端包，`bun install --frozen-lockfile`；`bun run typecheck` 后检查路由树 `routeTree.gen.ts` 没有变化；`bun run build` |

- 生成的代码（客户端、路由树、技能）都要提交：CI 重新生成一次，有差异就失败。`tenon init` 已经生成了它们。
- `tenon` 的版本由 `go.mod` 固定，Bun 的版本由 `package.json` 的 `packageManager` 固定，CI 与本地相同。
- 用到数据库后，在 `check` 中启动 PostgreSQL，见[在 CI 中运行数据库测试](../testing/testing.md#database-tests-in-ci)。代码不在 GitHub 上时，删掉 `.github`，按同样的步骤写所用平台的 CI。

<a id="existing-module"></a>
## 在已有的 module 中使用 Tenon

```bash
go get github.com/go-tenon/tenon@latest
go get -tool github.com/go-tenon/tenon/cmd/tenon
```

第二行加入开发工具 `tenon`，见[安装开发工具](tooling.md#installation)。

最小的服务只需要[入口文件](#entry-routes)中 `routes` 的写法：`chi` 路由器装上 `httpx.Defaults`，用 `rest.New` 创建 API、`rest.Register` 注册接口，再用 `httpx.ListenAndServe(ctx, cfg, router)` 运行，`ctx` 取消后平滑地停止。路由和接口见[路由](../http/routing.md)，从零做一个完整项目见[入门教程](../tutorial/01-project.md)。
