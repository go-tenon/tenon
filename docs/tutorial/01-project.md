# 1. 创建项目

入门教程分八章做一个博客：作者登录后写文章，草稿只有自己能看到，发布后所有人都能读到；最后加上管理作者和权限的后台。每章结束时项目都能运行，代码都是完整的文件。

这一章用 `tenon init` 生成项目并启动开发。

<a id="requirements"></a>
## 准备工作

安装 Go 1.27.4 或更高的版本、[Bun](https://bun.sh)（安装前端依赖、运行前端开发服务器）和 Docker（从第 2 章开始启动数据库等服务）：

```text
go version
bun --version
docker --version
```

<a id="creating-the-project"></a>
## 生成项目

```bash
go run github.com/go-tenon/tenon/cmd/tenon@latest init blog --module example.com/blog
```

命令在 `blog` 目录中生成 Go 后端和 React 前端，下载依赖，生成前端调用接口的代码。教程中的代码都用 `example.com/blog` 作为 module 路径，照样使用它，代码才能直接复制。

<a id="running"></a>
## 启动开发

```bash
cd blog
bun run dev
```

打开 <http://localhost:8080>，是模板自带的待办列表，第 4 章换成文章。

`bun run dev` 运行 `go tool tenon dev`：

- 运行前端的开发服务器，修改前端代码后页面立即更新；
- 编译并运行 Go 程序，修改 Go 代码后自动重新编译、重启；
- 每次编译后按 Go 中的接口重新生成 `packages/client/src/api` 中的 TypeScript 客户端。

`/api` 开头的请求是 JSON 接口，其余请求交给前端渲染页面。接口文档在 <http://localhost:8080/api/docs>，可以在这里直接调用接口，例如 `todo-create`。按 `Ctrl-C` 停止。

<a id="layout"></a>
## 项目里有什么

```text
blog/
├── go.mod                       Go module；Tenon 的开发工具也记在这里，版本与框架相同
├── package.json                 前端的 workspace；dev、build、generate 脚本
├── AGENTS.md                    项目说明和代码约定，AI 编程助手读它
├── config/app.yaml              应用的配置
├── cmd/blog/main.go             程序的入口：serve 命令启动 HTTP 服务
├── internal/todo/todo.go        示例的接口
├── apps/apps.go                 把前端的构建产物打包进 Go 程序
├── apps/web/                    前端
│   ├── package.json             React、TanStack Start、TanStack Query、Mantine；挂载路径和是否在服务端渲染
│   ├── vite.config.ts
│   └── src/
│       ├── router.tsx           装配路由和数据请求
│       ├── theme.ts             主题：样式预设和品牌色
│       ├── api.ts               创建调用接口的客户端
│       └── routes/              页面，一个文件一个页面
└── packages/client/src/api/     生成的客户端
```

- Go 代码在 `cmd` 和 `internal` 中，`internal` 下一个业务一个包，例如 `internal/todo`、以后的 `internal/article`。
- 前端在 `apps/web` 中，页面在 `src/routes` 中，界面用 [Mantine](https://mantine.dev) 的组件和 `@go-tenon/ui` 的布局。默认生成单语言项目，文字直接写在页面代码中；需要多语言时见[界面文字](../frontend/i18n.md)。
- 生成的客户端在 `packages/client` 中，前端用 `@blog/client/api` 导入，不要手动修改。

`apps/web` 在服务端渲染（`package.json` 中 `"ssr": true`），构建时连同 Bun 打包进 Go 程序，见[托管前端与服务端渲染](../frontend/hosting.md)。各目录的说明见[安装](../installation/installing.md#layout)。
