package queue

import (
	"context"
	"encoding/json"
	"time"

	"github.com/cockroachdb/errors"

	"github.com/go-tenon/tenon/errorx"
)

// HandleOption 调整 Topic.Handle 注册的处理函数，没有指定的沿用 Worker 的 Config。
type HandleOption func(*handler)

// MaxAttempts 设置这个 topic 的任务最多处理的次数，覆盖 Config.MaxAttempts。n 小于 1 时 panic。
func MaxAttempts(n int) HandleOption {
	if n < 1 {
		panic("queue: max attempts must be positive")
	}
	return func(h *handler) { h.maxAttempts = n }
}

// Concurrency 设置这个 topic 同时处理的任务数上限，覆盖 Config.Concurrency。n 小于 1 时 panic。
func Concurrency(n int) HandleOption {
	if n < 1 {
		panic("queue: concurrency must be positive")
	}
	return func(h *handler) { h.concurrency = n }
}

// OnDead 注册任务最终放弃时的回调：无论因重试次数耗尽，还是因返回 Permanent 或业务错误而不再重试，
// 在任务转入失败任务表之前都会调用一次 fn，适合将业务记录标记为最终失败（例如消息送达失败）。
// payload 无法解码为 T 时跳过 fn；fn 返回错误时任务稍后重试调用。fn 为 nil 时 panic。
func OnDead[T any](fn func(ctx context.Context, payload T, err error) error) HandleOption {
	if fn == nil {
		panic("queue: nil OnDead callback")
	}
	return func(h *handler) {
		h.onDead = func(ctx context.Context, raw []byte, cause error) error {
			var payload T
			if err := json.Unmarshal(raw, &payload); err != nil {
				return nil
			}
			return fn(ctx, payload, cause)
		}
	}
}

// attemptKey 是执行次数在 context 中的键。
type attemptKey struct{}

// attemptInfo 是本次执行的次数 n 和最多执行的次数 max。
type attemptInfo struct{ n, max int }

// AttemptFromContext 返回处理中的任务本次是第几次执行（从 1 开始），以及这是否是最后一次：
// 本次返回错误时任务转入失败任务表。处理函数据此在放弃之前记下最终结果，例如把投递记录标记为失败。
// 返回 IsPermanent 报告为 true 的错误时无论第几次都直接结束，与 final 无关。ctx 不是任务处理函数的 context 时返回 0 和 false。
func AttemptFromContext(ctx context.Context) (attempt int, final bool) {
	a, ok := ctx.Value(attemptKey{}).(attemptInfo)
	if !ok {
		return 0, false
	}
	return a.n, a.n >= a.max
}

// Permanent 标记最终的错误：任务直接转入失败任务表，不再重试，例如参数非法或数据已被删除。err 为 nil 时返回 nil。
//
// 业务错误（errorx 的 InvalidArgument、NotFound、Conflict 等分类）不需要标记，默认就不重试，见 IsPermanent；
// Permanent 用于其他重试也是徒劳的错误，例如推送服务拒绝了配置不对的请求。
func Permanent(err error) error {
	if err == nil {
		return nil
	}
	return &permanentError{err}
}

// permanentError 标记不再重试的错误。
type permanentError struct{ err error }

// Error 返回包装的错误的信息。
func (e *permanentError) Error() string { return e.err.Error() }

// Unwrap 返回包装的错误。
func (e *permanentError) Unwrap() error { return e.err }

// IsPermanent 报告以 err 失败的任务是否直接结束，不再重试。依次判断：
//
//   - 被 Permanent 标记：结束；
//   - 被 Retry 或 RetryAfter 标记，或者带有 errorx.RetryAfter 给出的等待时间：重试；
//   - 系统故障（errorx 的 Unavailable、Internal 分类，以及不是业务错误的普通错误和 panic）
//     和 errorx.ResourceExhausted：重试，这些失败过一段时间可能消失；
//   - 其他业务错误，例如 errorx.NotFound、errorx.InvalidArgument、errorx.Conflict：结束，重试的结果不会不同。
//
// worker 据此决定重试还是转入失败任务表；处理函数在放弃之前记下最终结果时，也用它判断这次是不是最后一次。
func IsPermanent(err error) bool {
	var p *permanentError
	if errors.As(err, &p) {
		return true
	}
	var r *retryError
	if errors.As(err, &r) || errorx.RetryAfterOf(err) > 0 {
		return false
	}
	kind := errorx.KindOf(err)
	return !kind.Systemic() && kind != errorx.ResourceExhausted
}

// Retry 标记应当按退避时间重试的错误，用于默认不重试的业务错误，
// 例如从只读副本读到了还没有同步的数据而返回的 errorx.NotFound。err 为 nil 时返回 nil。
// 重试仍然计入执行次数，次数用完时照常转入失败任务表；同时被 Permanent 包装时直接结束。
func Retry(err error) error {
	if err == nil {
		return nil
	}
	return &retryError{err, -1}
}

// RetryAfter 标记应当在 d 之后重试的错误，代替按执行次数计算的退避时间，
// 例如第三方接口返回 429 并给出了 Retry-After。d 不大于 0 时立即重试。业务错误被它标记时同样重试。
// 带有 errorx.RetryAfter 的业务错误不需要标记，按其中的时间重试。
// 重试仍然计入执行次数，次数用完时照常转入失败任务表；同时被 Permanent 包装时直接结束。err 为 nil 时返回 nil。
func RetryAfter(err error, d time.Duration) error {
	if err == nil {
		return nil
	}
	return &retryError{err, max(d, 0)}
}

// retryError 是 Retry 和 RetryAfter 标记的错误。delay 小于 0 时按退避时间重试。
type retryError struct {
	err   error
	delay time.Duration
}

// Error 返回包装的错误的信息。
func (e *retryError) Error() string { return e.err.Error() }

// Unwrap 返回包装的错误。
func (e *retryError) Unwrap() error { return e.err }

// retryDelay 返回第 attempts 次处理以 err 失败后到下一次处理的等待时间：
// RetryAfter 给出的时间，其次是 errorx.RetryAfter 给出的时间，都没有时按执行次数退避。
func retryDelay(err error, attempts int) time.Duration {
	var r *retryError
	if errors.As(err, &r) && r.delay >= 0 {
		return r.delay
	}
	if d := errorx.RetryAfterOf(err); d > 0 {
		return d
	}
	return backoff(attempts)
}
