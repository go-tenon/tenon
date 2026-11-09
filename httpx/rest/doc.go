// Package rest 提供 Huma 的最小装配：实例级错误响应整形、延迟绑定登记与离线 OpenAPI。
//
// 不引入框架 Router/Endpoint DSL。应用仍可对底层 huma.API 直接 huma.Register。
// 本包不设置 huma.NewError 等进程全局 hook；领域错误在 handler 边界转换，
// 校验错误中的 value 由实例 Transformer 删除。
package rest
