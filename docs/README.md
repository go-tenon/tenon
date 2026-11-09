# mygoravel 文档

mygoravel 为 Go HTTP API 提供统一的配置、错误、路由、认证、数据访问和运行约定。应用通过普通构造函数连接依赖，直接使用 Huma、Bun、goose、gocache 和 slog 等组件的原生能力。

第一次使用，建议从[安装与快速开始](installation.md)开始，先完成一次真实请求。只想了解 HTTP 写法，可以直接运行[第一个路由](routing.md#first-route)中的完整程序。

## 入门

- [安装与快速开始](installation.md)：创建自己的 Go module，运行第一个 HTTP API。
- [配置](configuration.md)：默认值、环境变量、按段加载与敏感值脱敏。

## 核心概念

- [目录结构与依赖](directory-structure.md)：组织业务代码，显式连接服务与基础能力。
- [请求与资源生命周期](lifecycle.md)：启动、请求处理、资源关闭和平滑退出。

## HTTP 基础

- [路由与 OpenAPI](routing.md)：声明端点、绑定处理器、离线生成接口文档。
- [HTTP 中间件](middleware.md)：默认边界行为、可信代理和自定义中间件。
- [请求与校验](requests.md)：路径参数、JSON、校验错误、大小限制和超时。
- [响应](responses.md)：JSON、响应状态、响应头与分页结构。
- [错误处理](errors.md)：业务错误、错误链、HTTP 映射与安全响应。
- [日志](logging.md)：结构化日志、请求关联和错误记录责任。

## 安全

- [认证与授权](authentication.md)：Bearer 校验器、主体、scope 和资源所有权。

## 数据库

- [数据库](database.md)：PostgreSQL 连接、Bun 查询、事务和约束错误。
- [数据库迁移](migrations.md)：编写 SQL、嵌入迁移、发布升级与失败恢复。

## 进阶使用

- [缓存](cache.md)：可选内存与 Redis 缓存、回源、失效和资源所有权。
- [本地化](localization.md)：语言协商、消息目录和错误文案。
- [应用命令](commands.md)：配置检查、迁移、依赖探测和离线 OpenAPI。

## 测试与部署

- [测试](testing.md)：HTTP 测试、隔离数据库、集成测试和独立模块消费。
- [部署](deployment.md)：构建、发布、健康检查、排空和升级。

## 如何阅读示例

标明完整文件或完整函数的代码可以放进自己的 Go module；其余代码片段说明要添加到现有处理器或装配函数中的部分。每章会交代片段中的依赖从哪里来。

各章代码独立说明对应的 API。业务类型、配置字段和命令由自己的应用定义；需要复用其他片段时，正文会说明依赖关系。

当前文档对应同一仓库版本的 HTTP API 能力。框架依赖版本见 [go.mod](../go.mod)，可选组件由应用自行添加。
