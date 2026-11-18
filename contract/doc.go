// Package contract 是接口契约中与入口无关的部分：按框架的规则从 Go 类型得到 JSON Schema 并据此校验参数，
// 带显示文字的枚举在 schema 中的命名类型，以及验证错误的规则和译文。
//
// 规则由 Huma 的 schema 引擎实现，字段标签（doc、minLength、enum、format 等）在 HTTP 接口（httpx/rest）、
// MCP 工具（mcpx）和 RPC 端点（natsrpc）中含义相同，同一个类型可以同时用作三者的输入；
// 生成的客户端（clientgen）从 OpenAPI 文档取得同样的规则。各入口的登记、认证和响应格式在各自的包中。
package contract
