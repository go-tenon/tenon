# tools

为多个 Go 产品提供可复用的基础组件和功能模块，目前包含配置加载（`config`）、日志（`logger`）和通用工具（`support`）。

社区已有成熟组件时，产品直接集成，不在本仓库额外封装：

- HTTP Client：[Resty](https://github.com/go-resty/resty)
- 错误处理：[cockroachdb/errors](https://github.com/cockroachdb/errors)

使用示例见 [examples/service](./examples/service)，运行测试：`go test ./...`。
