package cleanup

import (
	"context"
	"sync"
	"time"

	"github.com/cockroachdb/errors"
)

// 清理默认截止时间；若调用方 context 带有更早的 deadline 则沿用剩余时间。
const defaultTimeout = 10 * time.Second

type entry struct {
	name string
	fn   func(context.Context) error
}

// Stack 按后进先出顺序执行具名清理函数。零值可直接使用。
//
// 约定为单所有者串行调用：构建阶段 Add，退出阶段 Close/CloseWith。
// 不支持关闭时继续登记，也不承诺任意并发使用。
type Stack struct {
	mu     sync.Mutex
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
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return errors.Newf("cleanup: cannot add %q after close", name)
	}
	s.items = append(s.items, entry{name: name, fn: fn})
	return nil
}

// MustAdd 与 Add 相同，失败时 panic；便于 defer 装配路径在测试中快速失败。
func (s *Stack) MustAdd(name string, fn func(context.Context) error) {
	if err := s.Add(name, fn); err != nil {
		panic(err)
	}
}

// Close 使用独立有限上下文逆序执行清理，重复调用为 no-op。
func (s *Stack) Close(ctx context.Context) error {
	return s.CloseWith(ctx, nil)
}

// CloseWith 先执行清理，再把清理错误与 runErr 一并返回。
// runErr 不会被丢弃：两者都非 nil 时 errors.Join(runErr, cleanErr)，Is 仍能匹配原始错误。
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
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	items := s.items
	s.items = nil
	s.mu.Unlock()

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
	if ctx == nil {
		ctx = context.Background()
	}
	base := context.WithoutCancel(ctx)
	if deadline, ok := ctx.Deadline(); ok {
		return context.WithDeadline(base, deadline)
	}
	return context.WithTimeout(base, defaultTimeout)
}
