# 部署

[文档首页](README.md) / 测试与部署

构建、配置检查、迁移、启动和停止都由应用入口明确编排。本章说明如何把这些框架能力接到自己的部署流程。

本章内容：

- [构建应用](#build)
- [配置与启动](#configuration)
- [数据库发布](#release)
- [健康检查](#probes)
- [平滑退出](#shutdown)
- [日志与网络边界](#diagnostics)
- [升级验证](#upgrades)

<a id="build"></a>

## 构建应用

对于[快速开始](installation.md)中根目录包含 main.go 的应用：

```bash
go build -trimpath -o bin/api .
```

如果入口位于 cmd/api，则把构建目标改为 `./cmd/api`。构建路径由应用布局决定。固定 go.mod/go.sum，并记录构建提交。

<a id="configuration"></a>

## 配置与启动

最小程序可以直接执行 `./bin/api`。如果应用使用 YAML，应在入口定义配置路径和加载逻辑；使用环境变量时，通过 WithLookup 显式提供查询函数。

`--config`、serve 或 doctor 等名称只有在应用定义或安装相应命令后才存在。可参考[应用命令](commands.md)组合需要的入口，再让部署平台调用这些实际存在的命令。

为数据库连接和外部探测设置明确的 context deadline。凭据与 TLS 参数按部署环境配置，不写入源码。

<a id="release"></a>

## 数据库发布

在发布步骤中构造 goose.Provider 并执行迁移，确认成功后再启动依赖新结构的应用。具体连接方式见[迁移](migrations.md)。

Provider 的数据库锁可以串行化迁移执行，但不会自动协调应用版本切换。滚动升级时保持新旧代码对数据库结构的兼容。

<a id="probes"></a>

## 健康检查

应用自己登记存活和就绪路径，并让部署平台使用相应路径及成功状态。一个常见约定是 live 只检查进程响应，ready 检查排空状态与必要依赖。

使用短超时探测数据库。可丢弃缓存通常不影响 ready。可直接使用[生命周期章节](lifecycle.md#health)的 handler 写法，不需要额外探针框架。

<a id="shutdown"></a>

## 平滑退出

进程管理器应把 SIGTERM 传给应用二进制。入口将取消信号传给 httpx.Serve，后者执行排空，再由入口关闭依赖。

设置 ShutdownTimeout，并为平台停止宽限期留出排空和后续清理的时间。预算为 0 表示立即关闭。框架不额外等待代理摘流，相关策略由部署平台配置。

业务和驱动必须遵守 context；任意忽略取消的 goroutine 无法被强制终止。

<a id="diagnostics"></a>

## 日志与网络边界

显式把日志器传给 API，通常使用 stderr 作为服务日志输出。用 request_id 关联访问结果与错误诊断。

位于反向代理后时配置实际 TrustedProxies 网段。TLS 终止、CORS、限流和认证来源由应用按需接入，默认 HTTP 中间件不自动配置这些系统。

<a id="upgrades"></a>

## 升级验证

框架当前工具链要求以 [go.mod](../go.mod) 为准，数据库集成测试使用 PostgreSQL 18。应用添加的可选组件应有自己的版本固定和集成验证。

升级后执行单元测试、race 和实际依赖测试。源码迁移基线比较见[发布后的迁移](migrations.md#published-migrations)。

---

[上一篇：测试](testing.md) · [返回文档首页](README.md)
