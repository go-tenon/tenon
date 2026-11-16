// Package health 检查应用依赖的可用性，例如数据库、NATS 是否连得上。它与传输方式无关：
// httpx.Ready 把结果作为 HTTP 就绪检查返回，其他入口（例如以后的 gRPC 健康检查服务）可以使用同一个 Checker。
package health

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/cockroachdb/errors"

	"github.com/go-tenon/tenon/logging"
)

// DefaultTimeout 是 Options.Timeout 不大于 0 时每项检查的超时。
const DefaultTimeout = time.Second

// Check 是一项检查：一个依赖，以及确认它可用的方法。
type Check struct {
	// Name 出现在结果和日志中，例如 database、nats。
	Name string
	// Ping 确认依赖可用，返回错误表示不可用，例如 db.PingContext。超过 Options.Timeout 视为失败。
	Ping func(ctx context.Context) error
	// Optional 表示这个依赖坏了服务仍然可以降级运行，例如缓存：失败时在结果中报告，不让整体检查失败。
	// 否则一个缓存节点的短暂故障就会让全部实例同时被摘除。
	Optional bool
}

// Options 是 New 的选项。
type Options struct {
	// Checks 是要检查的依赖。只放必需的依赖，以及标为 Optional 的降级依赖。
	Checks []Check
	// Timeout 是每项检查的超时，不大于 0 时为 DefaultTimeout。
	Timeout time.Duration
	// Logger 记录检查结果的变化，nil 表示带 component=health 的 slog.Default()。
	Logger *slog.Logger
}

// Result 是一项检查的结果。
type Result struct {
	Name     string
	Optional bool
	// Err 是失败的原因，通过时为 nil。原因可能包含内部信息，只适合写日志，不要返回给调用方。
	Err error
}

// Report 是一次检查的结果。
type Report struct {
	// Ready 报告必需的检查是否都通过。
	Ready bool
	// Results 是每项检查的结果，顺序与 Options.Checks 相同。
	Results []Result
}

// Checker 执行一组检查。它可以被并发使用。
type Checker struct {
	checks  []Check
	timeout time.Duration
	logger  *slog.Logger

	mu      sync.Mutex
	failing map[string]bool // 上一次失败的检查，用于只记录变化
}

// New 返回执行 opts.Checks 的 Checker。名称为空、重复或 Ping 为 nil 时 panic。
func New(opts Options) *Checker {
	seen := map[string]bool{}
	for _, c := range opts.Checks {
		switch {
		case c.Name == "":
			panic("health: check name is required")
		case c.Ping == nil:
			panic("health: check " + c.Name + " has no Ping")
		case seen[c.Name]:
			panic("health: duplicate check " + c.Name)
		}
		seen[c.Name] = true
	}
	c := &Checker{
		checks:  append([]Check(nil), opts.Checks...),
		timeout: opts.Timeout,
		logger:  opts.Logger,
		failing: map[string]bool{},
	}
	if c.timeout <= 0 {
		c.timeout = DefaultTimeout
	}
	if c.logger == nil {
		c.logger = logging.Or(nil).With(logging.AttrComponent, "health")
	} else {
		c.logger = logging.Or(c.logger)
	}
	return c
}

// Run 并行执行全部检查，每项不超过 Timeout，按时返回：不理会 ctx 的 Ping 超时后按失败处理，panic 也按失败处理。
//
// 日志只记录变化：一项检查从通过变为失败时记录 Warn 和原因，恢复时记录 Info，
// 部署平台每隔几秒的探测因此只在状态变化时留下日志。
func (c *Checker) Run(ctx context.Context) Report {
	errs := c.run(ctx)
	report := Report{Ready: true, Results: make([]Result, len(c.checks))}
	for i, check := range c.checks {
		report.Results[i] = Result{Name: check.Name, Optional: check.Optional, Err: errs[i]}
		if errs[i] != nil && !check.Optional {
			report.Ready = false
		}
	}
	c.logChanges(ctx, errs)
	return report
}

// run 并行执行全部检查，返回每项的错误。超时的检查不等它返回，按失败处理。
func (c *Checker) run(ctx context.Context) []error {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	errs := make([]error, len(c.checks))
	var wg sync.WaitGroup
	for i, check := range c.checks {
		wg.Go(func() {
			done := make(chan error, 1)
			go func() { done <- ping(ctx, check.Ping) }()
			select {
			case err := <-done:
				errs[i] = err
			case <-ctx.Done():
				errs[i] = errors.Wrapf(ctx.Err(), "no answer within %s", c.timeout)
			}
		})
	}
	wg.Wait()
	return errs
}

// ping 调用 fn，把 panic 转换为错误。
func ping(ctx context.Context, fn func(context.Context) error) (err error) {
	defer func() {
		if v := recover(); v != nil {
			err = errors.Newf("panic: %v", v)
		}
	}()
	return fn(ctx)
}

// logChanges 记录与上一次相比结果变化了的检查。
func (c *Checker) logChanges(ctx context.Context, errs []error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for i, check := range c.checks {
		failed := errs[i] != nil
		if failed == c.failing[check.Name] {
			continue
		}
		c.failing[check.Name] = failed
		if failed {
			c.logger.WarnContext(ctx, "readiness check failed", "check", check.Name, "optional", check.Optional, "error", errs[i])
		} else {
			c.logger.InfoContext(ctx, "readiness check recovered", "check", check.Name)
		}
	}
}
