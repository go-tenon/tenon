# 安装与快速开始

[文档首页](README.md) / 入门

mygoravel 是普通 Go module。本章从一个空目录开始，运行只依赖 HTTP 能力的 API；数据库、缓存和认证在需要时分别接入。

本章内容：

- [环境要求](#requirements)
- [创建应用](#create-project)
- [添加框架依赖](#dependency)
- [编写入口](#entrypoint)
- [发起请求](#first-request)
- [添加应用能力](#next-steps)
- [停止与排查](#troubleshooting)

<a id="requirements"></a>

## 环境要求

当前代码使用 Go 1.27.3，精确工具链要求以仓库 go.mod 为准。本章的最小 HTTP API 只需 Go 工具链。

<a id="create-project"></a>

## 创建应用

```bash
mkdir hello-api
cd hello-api
go mod init example.com/hello-api
```

目录名和 module 路径由你决定。下文命令均在这个应用目录运行。

<a id="dependency"></a>

## 添加框架依赖

选择已推送的框架版本或提交，将其写入环境变量后安装：

```bash
MYGORAVEL_VERSION='<版本号或提交>'
go get "github.com/ryan053857/mygoravel@$MYGORAVEL_VERSION"
```

将占位符替换为实际版本。使用与所安装代码同版本的文档；go get 下载已发布的版本或提交。

<a id="entrypoint"></a>

## 编写入口

把[路由章节中的完整 main.go](routing.md#first-route)保存为当前目录的 `main.go`。这个程序显式完成四件事：构造 API、注册 `/ping`、监听 HTTP、响应退出信号。

然后整理依赖并启动：

```bash
go mod tidy
go run .
```

服务监听 `127.0.0.1:8080`。运行参数直接写在 main.go 中。

<a id="first-request"></a>

## 发起请求

保持服务运行，在另一个终端执行：

```bash
curl -i http://localhost:8080/ping
```

成功时返回 200，JSON 中的 message 为 pong；Huma 还会加入描述响应结构的 `$schema` 链接。

浏览器打开 `http://localhost:8080/docs` 查看接口文档。原始 OpenAPI 位于 `http://localhost:8080/openapi.json`。

<a id="next-steps"></a>

## 添加应用能力

根据实际需求逐步加入：

- 用[配置](configuration.md)加载自己的结构体。
- 用[认证与授权](authentication.md)保护需要身份的接口。
- 用[数据库](database.md)打开 PostgreSQL 连接，用[迁移](migrations.md)管理表结构。
- 用[应用命令](commands.md)安装需要的配置检查、文档导出和运行入口。

应用通过普通参数传递依赖，在入口显式定义命令、配置字段和路由。

<a id="troubleshooting"></a>

## 停止与排查

在服务终端按 Ctrl+C，入口会排空请求后退出。

| 现象 | 检查方法 |
| --- | --- |
| 找不到框架版本 | 确认版本或提交已经推送，且 module 路径正确 |
| 缺少依赖 | 在应用目录运行 go mod tidy |
| 8080 已占用 | 修改 main.go 中 net.Listen 的地址 |
| 路由返回 404 | 检查 Operation.Path 与登记函数是否被调用 |
| 注册时提示缺少安全声明 | 为 Operation 调用 Public 或 RequireBearer |

---

[返回文档首页](README.md) · [下一篇：配置](configuration.md)
