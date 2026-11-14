// Package rest 连接 Huma 的类型化路由、安全声明、错误响应和 OpenAPI。
//
// New 在 chi 路由上创建 API，Register 登记处理器并生成接口定义。注册时只保存依赖，处理请求时才使用，
// 生成文档时就能用零值的依赖构造同一套路由，再用 APIs 或 Lookup 从中取回 API。
// 错误响应沿用 Huma 的 Problem Details 结构，业务错误在处理器边界附加 code 并翻译 detail。
package rest
