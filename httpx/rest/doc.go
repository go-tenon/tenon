// Package rest 连接 Huma 的类型化路由、安全声明、错误响应和 OpenAPI。
//
// Register 在运行模式构造处理器，在文档模式生成接口定义。
// 错误响应沿用 Huma 的 Problem Details 结构，业务错误在处理器边界附加 code 并翻译 detail。
package rest
