package cleanup

import (
	"context"
	"time"

	"github.com/cockroachdb/errors"
)

// defaultTimeout 用于调用方未设置 deadline 的清理。
const defaultTimeout = 10 * time.Second

type entry struct {
	name string
	fn   func(context.Context) error
}

// Stack 按后进先出顺序执行具名清理函数。零值可直接使用。
//
// 所有者串行调用：构建阶段 Add，退出阶段 Close/CloseWith。
type Stack struct {
	items  []entry
	closed bool
}

// Add 登记清理函数。name 用于错误信息。Stack 已关闭时返回错误。
func (s *Stack) Add(name string, fn func(context.Context) error) error {
	if fn == nil {
		return errors.New("cleanup: nil cleanup function")
	}
	if name == "" {
		name = "resource"
	}
	if s.closed {
		return errors.Newf("cleanup: cannot add %q after close", name)
	}
	s.items = append(s.items, entry{name: name, fn: fn})
	return nil
}

// MustAdd 登记清理函数，失败时 panic。
func (s *Stack) MustAdd(name string, fn func(context.Context) error) {
	if err := s.Add(name, fn); err != nil {
		panic(err)
	}
}

// Close 使用独立有限上下文逆序执行清理，每个函数执行一次。
func (s *Stack) Close(ctx context.Context) error {
	return s.CloseWith(ctx, nil)
}

// CloseWith 执行清理并合并 runErr 与清理错误，保留 errors.Is 匹配关系。
func (s *Stack) CloseWith(ctx context.Context, runErr error) error {
	cleanErr := s.close(ctx)
	switch {
	case runErr == nil:
		return cleanErr
	case cleanErr == nil:
		return runErr
	default:
		return errors.Join(runErr, cleanErr)
	}
}

func (s *Stack) close(ctx context.Context) error {
	if s.closed {
		return nil
	}
	s.closed = true
	items := s.items
	s.items = nil

	cleanCtx, cancel := detachedContext(ctx)
	defer cancel()

	var errs []error
	for i := len(items) - 1; i >= 0; i-- {
		e := items[i]
		if err := e.fn(cleanCtx); err != nil {
			errs = append(errs, errors.Wrapf(err, "cleanup %s", e.name))
		}
	}
	return errors.Join(errs...)
}

func detachedContext(ctx context.Context) (context.Context, context.CancelFunc) {
	base := context.WithoutCancel(ctx)
	if deadline, ok := ctx.Deadline(); ok {
		return context.WithDeadline(base, deadline)
	}
	return context.WithTimeout(base, defaultTimeout)
}
