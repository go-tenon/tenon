# 目录结构与依赖

[文档首页](README.md) / 核心概念

应用按业务组织代码，入口负责装配，HTTP 处理器负责输入输出，业务服务负责规则。依赖关系可以直接从普通 Go 参数中读出来。

本章内容：

- [应用目录](#layout)
- [显式依赖](#dependencies)
- [业务与入口](#business)
- [框架包速查](#packages)
- [命名与边界](#boundaries)

<a id="layout"></a>

## 应用目录

小型应用可以只有 `main.go`。业务增长后，可以采用下面的布局：

```text
my-app/
├── go.mod
├── cmd/
│   └── api/
│       └── main.go       进程入口和信号处理
├── internal/
│   ├── bootstrap/       应用自己的配置与资源装配
│   └── note/
│       ├── service.go   业务规则和数据库操作
│       └── notehttp/    HTTP 输入输出与路由登记
└── migrations/          应用自有的 SQL 迁移
```

这是可选的应用布局，不是框架自动识别的目录协议。只在出现真实代码时建立目录。

<a id="dependencies"></a>

## 显式依赖

服务可以直接接收所需的原生对象。例如一个依赖数据库的服务：

```go
type Service struct {
    db *bun.DB
}

func New(db *bun.DB) *Service {
    return &Service{db: db}
}
```

这里的 bun 来自 `github.com/uptrace/bun`。连接由入口打开，再传给服务；构造函数本身不执行查询或启动后台任务。

当需要替换实现或隔离测试时，由实际消费方定义小接口。不必为每个服务增加 Repository、容器或服务查找方法。

<a id="business"></a>

## 业务与入口

字段格式、HTTP 路径和响应头属于入口。业务规则、资源所有权及事务边界属于服务。

处理器把解析后的输入转换为业务参数，调用服务，然后构造输出 DTO。业务服务可以接收 context 处理取消，但不应从 context 中读取数据库、缓存或依赖容器。

<a id="packages"></a>

## 框架包速查

| 包 | 用途 |
| --- | --- |
| config | 配置文档、按段解码、校验与敏感值脱敏 |
| cleanup | 逆序关闭入口拥有的资源 |
| fault | 稳定业务错误码和分类 |
| logging | slog 装配与上下文字段 |
| i18n | go-i18n 消息目录组合 |
| identity | 已认证主体和请求内身份传递 |
| httpx | HTTP 中间件及停机协调 |
| httpx/rest | Huma 路由绑定、错误转换与 OpenAPI |
| database | PostgreSQL 连接与约束错误识别 |
| database/migrate | goose.Provider 装配及迁移源码比较 |
| database/dbtest | 隔离的真实测试数据库 |
| command | Cobra 运维命令工厂 |

缓存按需使用 eko/gocache；查询和事务直接使用 Bun。

<a id="boundaries"></a>

## 命名与边界

业务包使用单数，如 note；资源路径用复数，如 `/notes`。JSON 使用 snake_case，OperationID 使用 `note-create`，业务错误码使用 `note.not_found`。Go 缩写遵循 ID、HTTP、DSN、TTL 等惯用写法。

依赖从入口指向业务，从业务指向实际需要的基础能力。业务不反向依赖自己的 HTTP 子包，fault 和 identity 保持传输中立。当前指南面向 HTTP API。

---

[上一篇：配置](configuration.md) · [下一篇：请求与资源生命周期](lifecycle.md)
