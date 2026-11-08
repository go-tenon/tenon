package support

import (
	"fmt"
	"runtime/debug"
)

// PanicError 是由 recover 到的 panic 转换而来的错误。
//
// PanicError 刻意不实现 Unwrap，避免 panic 的值被错误链判断误认为
// 可正常处理的业务错误或 context.Canceled；调用方应按内部故障处理。
// 需要检查 panic 的原始值时，用 errors.As 取出 *PanicError 后读取 Value。
type PanicError struct {
	// Value 是传给 panic 的值。
	Value any
	// Stack 是 recover 时 goroutine 的调用栈，格式同 debug.Stack。
	Stack []byte
}

// Error 返回 panic 的值与调用栈，便于直接用 slog.Any("err", err) 记录。
func (e *PanicError) Error() string {
	return fmt.Sprintf("panic: %v\n\n%s", e.Value, e.Stack)
}

// Catch 调用 fn 并返回其错误；fn panic 时返回 *PanicError，而不是让进程崩溃。
//
// 用于后台 goroutine、队列消费者、定时任务等没有框架兜底 panic 的地方：
//
//	go func() {
//		if err := support.Catch(func() error { return job.Run(ctx) }); err != nil {
//			slog.ErrorContext(ctx, "job failed", "err", err)
//		}
//	}()
func Catch(fn func() error) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = &PanicError{Value: r, Stack: debug.Stack()}
		}
	}()
	return fn()
}
