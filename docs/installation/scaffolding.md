# 脚手架

`tenon init` 生成一个能直接运行的项目：Go API、React 单页应用、开发与构建脚本，以及一个内存里的待办事项示例。最小 API 见[安装](installing.md#api-only)。

- [环境要求](#requirements)
- [生成项目](#creating-a-project)
- [开发](#developing)
- [构建](#building)
- [项目里有什么](#layout)
- [入口文件在做什么](#entrypoint)
- [示例资源](#example-resource)
- [前端与同源](#frontend)
- [下一步](#next-steps)

<a id="requirements"></a>
## 环境要求

- Go 1.27.4 或更高版本（精确的工具链要求以框架的 [go.mod](../../go.mod) 为准）
- [Bun](https://bun.sh)，用来安装前端依赖、运行 Vite

<a id="creating-a-project"></a>
## 生成项目

```bash
go run github.com/go-tenon/tenon/cmd/tenon@main init todo
```

`--module` 指定 Go 模块路径，`--name` 指定应用名称，默认都是目录名。目录必须是空目录或尚未创建。

生成后它会运行 `go mod tidy`、生成 TypeScript 客户端；本机装了 Bun 时再运行 `bun install`。`go.mod` 要求的框架版本就是这次运行 `init` 的版本：用 `@main` 时是 main 分支当前提交，用 `@v1.2.0` 时是该标签。开发框架本身时可用 `--tenon <框架目录>`，`go.mod` 会用 `replace` 指向本地副本。更多选项见[开发工具](tooling.md#init)。

<a id="developing"></a>
## 开发

```bash
cd todo
bun run dev
```

打开 `http://localhost:8080`，就能添加、勾选、删除待办事项。接口文档在 `http://localhost:8080/api/docs`。

在浏览器打开 **8080**。应用把页面请求转发给本机的 Vite（见下方[前端与同源](#frontend)）；远程开发时转发或访问远端的 8080 即可。

- **修改前端代码**：页面会热更新。
- **修改 Go 代码**：应用自动重新编译、重启，`frontend/src/api` 里的客户端也会跟着重新生成。比如给 `Todo` 加一个字段，前端立刻就能用到它的类型。

`bun run dev` 运行的是 [`go tool tenon dev`](tooling.md#dev)，细节见[开发工具](tooling.md)。

<a id="building"></a>
## 构建

```bash
bun run build
./bin/api serve --config config/app.yaml
```

`bun run build` 先构建前端，再把它嵌入 Go 程序。得到的 `bin/api` 是单一二进制，部署见[部署](deployment.md)。

<a id="layout"></a>
## 项目里有什么

```text
todo/
├── go.mod                 tool github.com/go-tenon/tenon/cmd/tenon：开发工具随框架的版本固定
├── package.json           dev、build、generate 脚本；React、TanStack Query、Vite
├── vite.config.ts         root 指向 frontend
├── config/
│   ├── app.yaml           监听地址等应用配置
│   └── local.yaml         开发时把页面请求转给 Vite（只叠加在本地）
├── cmd/api/main.go        程序入口：命令行、serve 和 routes
├── internal/todo/todo.go  示例资源：待办事项的四个接口，数据保存在内存中
└── frontend/
    ├── frontend.go        把构建产物 dist 嵌入程序
    ├── index.html
    └── src/
        ├── App.tsx        待办列表页面
        ├── client.ts      创建调用接口的客户端
        └── api/           生成的 TypeScript 客户端
```

Go 代码在 `cmd` 和 `internal` 中，前端在 `frontend` 中。构建时前端打进二进制，部署时通常带上这一个文件和配置目录即可。目录约定见[目录结构](../concepts/structure.md)。

<a id="entrypoint"></a>
## 入口文件在做什么

`cmd/api/main.go` 由四部分组成：配置、命令行、`serve` 和 `routes`。

### 配置

```go
type Config struct {
    HTTP              httpx.Config `yaml:"http"`
    FrontendDevServer string       `yaml:"frontend_dev_server"`
}
```

`HTTP` 对应 `config/app.yaml` 里的 `http` 段（例如 `addr: ":8080"`）。`FrontendDevServer` 只在开发时由 `config/local.yaml` 设置，生产构建保持为空。配置如何加载与叠加见[配置](../general/configuration.md)。

### 命令行

```go
func main() {
    root := command.Root("api", "todo")
    root.AddCommand(
        command.Run("serve", "启动 HTTP 服务", serve),
        command.Config[Config](),
    )
    root.AddCommand(httpcmd.Commands(func() chi.Routes { return routes(config.Default[Config](), nil) })...)
    command.Execute(root)
}
```

| 片段 | 作用 |
| --- | --- |
| `command.Root` | 根命令，带共用的 `--config` |
| `command.Run("serve", …, serve)` | 创建 `serve`：先读 `.env`、加载并校验配置，再调用 `serve(ctx, cfg)`；收到停止信号时取消 `ctx` |
| `command.Config[Config]()` | 挂上 `config check`、`config show`，按你的 `Config` 类型解析 YAML |
| `httpcmd.Commands(…)` | 挂上 `openapi`、`routes`、`client`，供 `go tool tenon generate` 等使用，部署后也可单独调用 |

命令行约定见[命令行](../general/commands.md)。

### serve 和 routes

```go
func serve(ctx context.Context, cfg Config) error {
    slog.SetDefault(logging.New(logging.Config{}))
    return httpx.ListenAndServe(ctx, cfg.HTTP, routes(cfg, todo.NewStore()))
}

func routes(cfg Config, store *todo.Store) chi.Router {
    router := chi.NewRouter()
    router.Use(httpx.Defaults(cfg.HTTP)...)
    router.Route("/api", func(r chi.Router) {
        r.Use(httpx.Timeout(cfg.HTTP.Timeout))
        api := rest.New(r, rest.Options{Title: "todo", Version: "0.1.0", BasePath: "/api"})
        todo.Register(api, store)
    })
    router.Handle("/*", httpx.NewSPA(frontend.Dist, httpx.SPAOptions{DevServer: cfg.FrontendDevServer}))
    return router
}
```

`routes` 构造应用的全部路由：`/api` 下是接口，其余路径交给单页应用，页面与 API 同源。`httpx.Defaults` 安装请求 ID、访问日志、恢复 panic 等基础中间件；`Timeout` 只装在 `/api` 上，页面路由可保持长连接。中间件说明见[中间件](../http/middleware.md)。

`routes` 有两个调用者：

- `serve` 用加载的配置和真实的 `Store` 调用它，启动 HTTP 服务；
- `openapi`、`routes`、`client` 命令用默认配置和 `nil` 调用它，只读取构造出的路由，生成文档和客户端。

`routes` 只保存 `store`，处理请求时才调用它，所以传 `nil` 也能构造出完整的路由。新增资源时，在 `routes` 里加一行 `Register`，服务和生成的文档、客户端会一起看到它。客户端生成见[客户端](../responses/clients.md)。

<a id="example-resource"></a>
## 示例资源

`internal/todo/todo.go` 是一个自包含的资源包：

- `Todo`、`Store`：领域类型与内存存储（可并发）；
- `Register`：四个公开接口——列表、创建、修改、删除；
- 输入输出用结构体描述，校验规则写在字段标签上（如 `minLength`），错误用 `errorx` 定义。

`Register` 只保存 `s`，处理请求时才调用它，所以生成文档时 `s` 是 `nil` 也能注册全部接口。把内存换成 PostgreSQL 的步骤见[数据库](../database/quick-start.md)和[入门教程](../tutorial/03-database.md)。

<a id="frontend"></a>
## 前端与同源

开发时：

1. Vite 在 `5173` 上提供页面与热更新（默认只监听本机回环地址）；
2. Go 在 `8080` 上提供 `/api`，并通过 `SPAOptions.DevServer`（来自 `local.yaml` 的 `frontend_dev_server`）把其余请求——包括 HMR 的 WebSocket——转发给 Vite；
3. 浏览器始终访问 `http://localhost:8080`（或远端机器的 `8080`），页面与 API 同源。

发布时：`tenon build` 把 Vite 的产物写进 `frontend/dist`，由 `frontend/frontend.go` 的 `embed` 打进二进制；`DevServer` 为空，`SPA` 直接提供静态文件。细节见[单页应用](../responses/spa.md)。

<a id="next-steps"></a>
## 下一步

- 跟着[入门教程](../tutorial/README.md)把待办事项换成博客，并接上数据库、登录与草稿；
- 阅读[开发工具](tooling.md)，了解 `dev` / `build` / `generate` 与 `tenon.json`；
- 部署见[部署](deployment.md)；
- 更小的入口见[只写一个 API](installing.md#api-only)。
