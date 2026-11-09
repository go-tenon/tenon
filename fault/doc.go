// Package fault 定义业务错误的稳定错误码、分类和公开参数。
//
// Define 创建模板，New 和 Wrap 在调用处附加 cockroachdb/errors 堆栈。
// errors.Is 按模板身份匹配，errors.As 提取错误实例。传输边界负责状态映射。
//
//	var ErrNotFound = fault.Define(fault.NotFound, "note.not_found")
//
//	return ErrNotFound.New(fault.Public("note_id", id))
package fault
