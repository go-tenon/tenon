// Package errorx 定义业务错误的稳定错误码、分类和公开参数。
//
// Define 创建模板，New 和 Wrap 创建实例；只有 Unavailable 和 Internal 这类系统故障
// 在调用处附加 cockroachdb/errors 堆栈，可预期的业务结果由错误码定位。
// errors.Is 按错误码匹配，errors.As 提取错误实例，传输边界负责状态映射。
// 跨进程传递错误由各入口的协议负责，例如 HTTP 接口和 RPC 的错误响应，调用方的客户端把它还原为 Error。
//
//	var ErrNotFound = errorx.Define(errorx.NotFound, "article.not_found")
//
//	return ErrNotFound.New(errorx.Public("article_id", id))
package errorx
