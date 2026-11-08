// Package errs 为业务错误提供与传输方式无关的分类。
//
// 业务层只表达“这是哪一类错误”以及“可以展示给用户的文案”，
// REST、页面、gRPC、异步队列等传输层各自把分类翻译成状态码、页面或重试策略。
// 本包不依赖任何传输层，也不提供堆栈：调用链上用 fmt.Errorf("...: %w", err)
// 补充上下文即可。
//
//	// 业务层
//	if errors.Is(err, sql.ErrNoRows) {
//		return errs.Wrap(err, errs.NotFound, "订单不存在")
//	}
//
//	// 传输层
//	switch errs.CodeOf(err) { ... }
//	msg := errs.MessageOf(err) // 只会是业务层显式给出的文案
//
// 分类沿用 gRPC canonical codes 的子集，便于与 gRPC 一一对应，
// 并按 Google AIP-193 映射到 HTTP 状态码。
package errs

import (
	"context"
	"errors"
)

// Code 是错误分类，取值为本包定义的常量。
type Code string

const (
	// InvalidArgument 表示调用方传入的参数不合法，与系统状态无关。
	InvalidArgument Code = "invalid_argument"
	// FailedPrecondition 表示系统当前状态不允许该操作，例如订单已关闭仍尝试支付。
	FailedPrecondition Code = "failed_precondition"
	// Unauthenticated 表示调用方身份未认证或认证已失效。
	Unauthenticated Code = "unauthenticated"
	// PermissionDenied 表示调用方已认证但无权执行该操作。
	PermissionDenied Code = "permission_denied"
	// NotFound 表示请求的资源不存在。
	NotFound Code = "not_found"
	// AlreadyExists 表示要创建的资源已存在。
	AlreadyExists Code = "already_exists"
	// Aborted 表示因并发冲突（如乐观锁、事务冲突）而中止，通常可以重试。
	Aborted Code = "aborted"
	// ResourceExhausted 表示配额或限流耗尽，稍后可重试。
	ResourceExhausted Code = "resource_exhausted"
	// Unavailable 表示依赖的服务暂时不可用，稍后可重试。
	Unavailable Code = "unavailable"
	// DeadlineExceeded 表示操作超时。
	DeadlineExceeded Code = "deadline_exceeded"
	// Canceled 表示操作被调用方取消。
	Canceled Code = "canceled"
	// Unimplemented 表示该操作未实现或不支持。
	Unimplemented Code = "unimplemented"
	// Internal 表示内部错误，也是所有未分类错误的默认分类。
	Internal Code = "internal"
)

// Error 是带分类的错误。
type Error struct {
	// Code 为错误分类，零值视为 Internal。
	Code Code
	// Message 是可以展示给最终用户的文案，不应包含内部细节；可以为空。
	Message string
	// Err 是内部原因，只用于日志与 errors.Is/As，不会通过 MessageOf 暴露。
	Err error
}

// Error 返回用于日志的完整描述：Message 与内部原因。
func (e *Error) Error() string {
	switch {
	case e.Message != "" && e.Err != nil:
		return e.Message + ": " + e.Err.Error()
	case e.Message != "":
		return e.Message
	case e.Err != nil:
		return e.Err.Error()
	default:
		return string(e.code())
	}
}

// Unwrap 返回内部原因。
func (e *Error) Unwrap() error { return e.Err }

func (e *Error) code() Code {
	if e.Code == "" {
		return Internal
	}
	return e.Code
}

// New 返回指定分类与文案的错误。
func New(code Code, msg string) error {
	return &Error{Code: code, Message: msg}
}

// Wrap 给 err 加上分类与文案，err 保留为内部原因。err 为 nil 时返回 nil。
func Wrap(err error, code Code, msg string) error {
	if err == nil {
		return nil
	}
	return &Error{Code: code, Message: msg, Err: err}
}

// CodeOf 返回 err 的分类：
//
//   - nil 返回空字符串；
//   - 链上最外层的 *Error 决定分类，因此外层可以重新分类；
//   - 链上有 FieldErrors 时为 InvalidArgument；
//   - context.Canceled 与 context.DeadlineExceeded 分别为 Canceled 与 DeadlineExceeded；
//   - 其他错误为 Internal。
func CodeOf(err error) Code {
	if err == nil {
		return ""
	}
	if e, ok := errors.AsType[*Error](err); ok {
		return e.code()
	}
	if _, ok := errors.AsType[FieldErrors](err); ok {
		return InvalidArgument
	}
	switch {
	case errors.Is(err, context.Canceled):
		return Canceled
	case errors.Is(err, context.DeadlineExceeded):
		return DeadlineExceeded
	}
	return Internal
}

// MessageOf 返回链上最外层 *Error 的 Message。
// 未分类的错误返回空字符串，从而不会把内部错误文本暴露给用户；
// 文案为空时由传输层按分类给出默认文案。
func MessageOf(err error) string {
	if e, ok := errors.AsType[*Error](err); ok {
		return e.Message
	}
	return ""
}
