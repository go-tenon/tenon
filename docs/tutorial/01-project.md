# 1. 创建项目

欢迎来到 Tenon 的入门教程。在接下来的七章中，我们会从一个空目录开始，一步步做出一个博客：作者登录后写文章，草稿只有自己能看到，发布之后所有人都能读到。

这一章，我们先用 `tenon init` 生成项目，把它跑起来，再看看项目里都有些什么。

- [环境要求](#requirements)
- [生成项目](#creating-the-project)
- [启动开发](#running)
- [项目里有什么](#layout)

<a id="requirements"></a>
## 环境要求

- Go 1.27.4 或更高版本
- [Bun](https://bun.sh)：安装前端依赖、运行 Vite
- Docker：从下一章开始，用它在本机启动 PostgreSQL、Redis、Memcached 和 NSQ

<a id="creating-the-project"></a>
## 生成项目

```bash
go run github.com/go-tenon/tenon/cmd/tenon@main init blog --module example.com/blog
```

`init` 在 `blog` 目录生成一个 Go 应用加 React 单页应用的项目，然后安装 Go 和前端的依赖，生成前端调用接口用的 TypeScript 客户端。`--module` 是 Go 模块路径，教程中的代码都用 `example.com/blog`。

<a id="running"></a>
## 启动开发

```bash
cd blog
bun run dev
```

打开 http://localhost:8080，能看到一个待办列表：添加、勾选、删除都可以用。它是模板自带的示例，数据保存在内存中，重启后就没了。从第 4 章开始，我们把它换成博客的文章。

`bun run dev` 运行的是 `go tool tenon dev`，它同时做几件事：

- 运行 Vite。修改前端代码，页面立即热更新；
- 编译并运行 Go 应用。修改 Go 代码，应用自动重新编译、重启；
- 每次重新编译后，重新生成 `frontend/src/api` 中的 TypeScript 客户端。Go 接口加了一个字段，前端马上就能用到它的类型。

页面和接口在同一个地址下：`/api` 是 JSON 接口，其余的请求交给前端。接口文档在 http://localhost:8080/api/docs，试着在这里调用一下 `todo-create`。

按 `Ctrl-C` 停止。

<a id="layout"></a>
## 项目里有什么

```text
blog/
├── go.mod                 tool github.com/go-tenon/tenon/cmd/tenon：开发工具的版本随框架固定
├── package.json           dev、build、generate 脚本，React、TanStack Query、Vite
├── vite.config.ts         前端源码在 frontend 中
├── config/
│   ├── app.yaml           应用的配置
│   └── local.yaml         只在本机开发时使用：把页面请求转给 Vite
├── cmd/api/main.go        程序入口：serve 命令启动 HTTP 服务
├── internal/todo/todo.go  示例接口
└── frontend/
    ├── frontend.go        把构建好的页面嵌入程序
    ├── index.html
    └── src/
        ├── App.tsx        页面
        ├── client.ts      创建调用接口的客户端
        └── api/           生成的客户端
```

Go 代码在 `cmd` 和 `internal` 中，前端代码在 `frontend` 中。构建时前端被打包进 Go 程序，部署时只有一个二进制文件。

到这里，你已经有了一个可以运行的应用：修改前端代码，页面立即更新；修改 Go 代码，应用自动重启。

下一章，我们会启动数据库等基础设施，并把应用的装配整理成以后各章都能沿用的结构。
