// Package fault 定义与传输无关的业务失败语义：稳定 Code、Kind 与允许公开的参数。
//
// 错误基础使用 github.com/cockroachdb/errors 提供堆栈与包装；本包不重新导出
// New/Wrap/Is，也不把 HTTP 状态写入业务错误值。HTTP 边界通过 [Kind.HTTPStatus]
// / [HTTPStatus] 读取中立映射。
//
//	var ErrNotFound = fault.Define(fault.NotFound, "order.not_found")
//
//	return ErrNotFound.New(fault.Public("order_id", id))
//	return ErrConflict.Wrap(err, fault.Public("sku", sku))
//
// 模板在包级定义且不可变；每次失败产生带堆栈的独立实例。errors.Is/As 按模板身份匹配。
package fault
