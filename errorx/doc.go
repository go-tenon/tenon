// Package errorx 定义业务错误的稳定错误码、分类和公开参数。
//
// Define 创建模板，New 和 Wrap 在调用处附加 cockroachdb/errors 堆栈。
// errors.Is 按错误码匹配，errors.As 提取错误实例，传输边界负责状态映射。
// 已注册 cockroachdb/errors 编解码器，错误跨进程传输后仍保留错误码、分类和公开参数。
//
//	var ErrNotFound = errorx.Define(errorx.NotFound, "note.not_found")
//
//	return ErrNotFound.New(errorx.Public("note_id", id))
package errorx
