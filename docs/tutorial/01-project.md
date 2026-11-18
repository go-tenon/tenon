# 1. 创建项目

欢迎来到 Tenon 的入门教程。在接下来的八章中，我们会从一个空目录开始，一步步做出一个博客：作者登录后写文章，草稿只有自己能看到，发布之后所有人都能读到；最后再加上一个后台，管理员在后台中管理作者和他们的权限。

每一章都在上一章的基础上增加一点功能，结束时项目都能运行。代码都是完整的文件，可以直接复制。

**这一章要做的：** 用 `tenon init` 生成项目，把它运行起来，再看看项目中都有些什么。

阅读前需要了解：Go 的基本语法。如果还没有读过[安装](../installation/installing.md)，建议先读一遍，它解释了一个 Tenon 接口由哪几部分组成。

- [准备工作](#requirements)
- [生成项目](#creating-the-project)
- [启动开发](#running)
- [项目里有什么](#layout)
- [小结](#summary)

<a id="requirements"></a>
## 准备工作

你需要安装三样东西：

- **Go 1.27.4 或更高的版本。**
- **[Bun](https://bun.sh)**：一个 JavaScript 运行环境，用来安装前端的依赖、运行前端的开发服务器。
- **Docker**：从下一章开始，我们用它在你的电脑上启动数据库等服务，不需要一个个安装它们。

安装后在终端中检查：

```text
go version
bun --version
docker --version
```

<a id="creating-the-project"></a>
## 生成项目

在你放代码的目录中运行：

```bash
go run github.com/go-tenon/tenon/cmd/tenon@main init blog --module example.com/blog
```

这条命令在 `blog` 目录中生成一个项目，包括一个 Go 写的后端和一个 React 写的前端；然后下载 Go 和前端的依赖，并生成前端调用接口用的代码。

`--module` 是这个项目的 Go module 路径，也就是在代码中导入它的包时写的前缀。教程中的代码都写作 `example.com/blog`，例如 `example.com/blog/internal/article`，所以请照样使用这个路径，代码才能直接复制。

<a id="running"></a>
## 启动开发

```bash
cd blog
bun run dev
```

打开 <http://localhost:8080>，能看到一个待办列表：添加、勾选、删除都可以用。它是模板自带的例子，数据保存在内存中，程序重启后就没了。从第 4 章开始，我们把它换成博客的文章。

`bun run dev` 运行的是 Tenon 的开发工具 `go tool tenon dev`，它同时做了几件事：

- **运行前端的开发服务器。** 页面由 [TanStack Start](https://tanstack.com/start) 渲染，修改前端代码，页面立即更新，不需要刷新；
- **编译并运行 Go 程序。** 修改 Go 代码，程序自动重新编译、重新启动；
- **重新生成前端的客户端。** 每次重新编译后，根据 Go 中的接口重新生成 `packages/client/src/api` 中的 TypeScript 代码。Go 的接口加了一个字段，前端的代码马上就能用到它。

页面和接口在同一个地址下：`/api` 开头的是 JSON 接口，其余的请求交给前端，由它渲染页面。接口文档在 <http://localhost:8080/api/docs>，可以在这里直接调用接口，试着调用一下 `todo-create`，再回到页面刷新，看看新加的待办事项。

在终端中按 `Ctrl-C` 停止。

<a id="layout"></a>
## 项目里有什么

```text
blog/
├── go.mod                       Go module；Tenon 的开发工具也记在这里，版本与框架相同
├── package.json                 前端的 workspace；dev、build、generate 脚本
├── config/app.yaml              应用的配置
├── cmd/api/main.go              程序的入口：serve 命令启动 HTTP 服务
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

可以把项目分成三部分来看：

- **Go 代码**在 `cmd` 和 `internal` 中。`cmd/api/main.go` 是程序的入口，`internal` 中按业务分成一个个包，例如现在的 `internal/todo`，以后的 `internal/article`。
- **前端代码**在 `apps/web` 中，页面在 `src/routes` 中。界面用 [Mantine](https://mantine.dev) 的组件，框架的 `@go-tenon/ui` 提供布局和样式预设。
- **生成的代码**在 `packages/client` 中，前端用 `@blog/client/api` 导入它。不需要手动修改，它随接口自动更新。

博客的页面要被搜索引擎收录，所以 `apps/web` 在服务端渲染（`package.json` 中 `"ssr": true`）。构建时，前端的文件连同渲染页面用的 Bun 打包进 Go 程序：Go 程序直接返回静态文件，启动时把 Bun 运行起来，页面请求交给它渲染。部署时仍然只有一个可执行文件，见[部署](../installation/deployment.md)和[托管前端与服务端渲染](../frontend/hosting.md)。每个目录的详细说明见[脚手架](../installation/scaffolding.md#layout)。

<a id="summary"></a>
## 小结

这一章，你：

- 用 `tenon init` 生成了一个项目；
- 用 `bun run dev` 启动了开发环境，修改代码后会自动重新编译、页面自动更新；
- 知道了项目分成 Go 代码、前端代码和生成的代码三部分。

下一章，我们用 Docker 启动数据库等服务，并把程序的入口整理成以后各章都能沿用的结构。
