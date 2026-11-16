// Package doctor 检查应用的运行环境：依赖的版本和设置、迁移是否执行、权限是否足够。查到的每一个问题都带着修改方法。
//
// 它与就绪检查（health）分工不同：就绪检查每隔几秒运行一次，只确认依赖连得上；自检在部署、升级和排查问题时运行，
// 读取依赖的设置，例如 PostgreSQL 的 wal_level，告诉运维人员哪里不对、怎样修改。
//
// 检查项按依赖分组。各个包提供自己的一组，例如 database.Doctor、queue.Doctor，应用在入口组装：
//
//	groups := []doctor.Group{
//	    database.Doctor(db),
//	    migrate.Doctor(db, migrations.FS),
//	    queue.Doctor(db, nc, cfg.Queue),
//	}
//	report := doctor.Run(ctx, groups...)
//
// doctorcmd 把它们做成 doctor 命令；Require 在启动或迁移之前运行它们，有问题时拒绝继续。
package doctor

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/cockroachdb/errors"

	"github.com/go-tenon/tenon/errorx"
	"github.com/go-tenon/tenon/logging"
)

// CheckTimeout 是每项检查的最长时间，超时按失败处理。
const CheckTimeout = 10 * time.Second

// Check 是一项检查。
type Check struct {
	// Name 出现在结果中，在组内唯一，例如 wal_level。
	Name string
	// Run 执行检查：返回 nil 表示通过；返回 *Problem 表示查到了问题，见 Fail 和 Warn；返回 Skip 表示这一项不适用。
	// 返回其他错误表示检查本身没能完成，例如连接中断，结果为失败，同组后面的检查跳过。
	Run func(ctx context.Context) error
}

// Group 是同一个依赖的一组检查。组内按顺序执行，前面的检查可以是后面的前提；各组之间并行执行。
type Group struct {
	// Name 是依赖的名字，例如 postgres、queue。
	Name   string
	Checks []Check
}

// Problem 是一项检查查到的问题。
type Problem struct {
	// Summary 说明哪里不对，例如 "wal_level is replica; the queue needs logical"。
	Summary string
	// Fix 说明怎样修改，可以有多行。
	Fix string
	// Doc 是相关文档的地址，可以为空。
	Doc string
	// Warning 为 true 时只是提示：程序可以运行，但存在隐患，例如可能写满磁盘的设置。
	Warning bool
}

// Fail 返回使检查失败的问题：程序在这种环境中不能正常工作。
func Fail(summary, fix string) *Problem { return &Problem{Summary: summary, Fix: fix} }

// Warn 返回只作提示的问题：程序可以运行，但存在隐患。
func Warn(summary, fix string) *Problem { return &Problem{Summary: summary, Fix: fix, Warning: true} }

// See 设置相关文档的地址并返回 p。
func (p *Problem) See(doc string) *Problem {
	p.Doc = doc
	return p
}

func (p *Problem) Error() string { return p.Summary }

// skip 表示一项检查不适用。
type skip struct{ reason string }

func (s *skip) Error() string { return s.reason }

// Skip 返回表示检查不适用的错误，reason 说明原因，例如 "queue migrations have not been applied"。
func Skip(reason string) error { return &skip{reason: reason} }

// Status 是一项检查的结果。
type Status string

// 检查的结果。
const (
	Passed  Status = "passed"
	Warning Status = "warning"
	Failed  Status = "failed"
	Skipped Status = "skipped"
)

// Result 是一项检查的结果。
type Result struct {
	Group  string `json:"group"`
	Check  string `json:"check"`
	Status Status `json:"status"`
	// Summary 是问题、跳过的原因，或者检查没能完成的错误；通过时为空。
	Summary string `json:"summary,omitempty"`
	Fix     string `json:"fix,omitempty"`
	Doc     string `json:"doc,omitempty"`
}

// Report 是一次自检的结果。
type Report struct {
	// Results 按传入的顺序列出每一组的每一项检查。
	Results []Result `json:"results"`
}

// Count 返回结果为 s 的检查数。
func (r Report) Count(s Status) int {
	n := 0
	for _, res := range r.Results {
		if res.Status == s {
			n++
		}
	}
	return n
}

// ErrFailed 是 Report.Err 和 Require 返回的错误的模板，分类为 Unavailable，命令以 ExitUnavailable 退出。
var ErrFailed = errorx.Define(errorx.Unavailable, "doctor.failed")

// Err 在有检查失败时返回 ErrFailed 分类的错误，消息列出每一项失败和修改方法；全部通过或只有提示时返回 nil。
func (r Report) Err() error {
	failed := r.Count(Failed)
	if failed == 0 {
		return nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%d check(s) failed", failed)
	for _, res := range r.Results {
		if res.Status != Failed {
			continue
		}
		b.WriteString("\n  " + res.Group + "/" + res.Check + ": " + res.Summary)
		if res.Fix != "" {
			b.WriteString("\n")
			b.WriteString(strings.TrimSuffix(indent("    ", "fix: ", res.Fix), "\n"))
		}
	}
	return ErrFailed.Wrap(errors.New(b.String()))
}

// Run 执行全部检查。各组并行，组内按顺序；每项检查最多 CheckTimeout，panic 按失败处理。
func Run(ctx context.Context, groups ...Group) Report {
	results := make([][]Result, len(groups))
	var wg sync.WaitGroup
	for i, g := range groups {
		wg.Go(func() { results[i] = runGroup(ctx, g) })
	}
	wg.Wait()
	var report Report
	for _, rs := range results {
		report.Results = append(report.Results, rs...)
	}
	return report
}

// runGroup 按顺序执行一组检查。一项检查没能完成时，后面的检查跳过。
func runGroup(ctx context.Context, g Group) []Result {
	results := make([]Result, 0, len(g.Checks))
	broken := ""
	for _, c := range g.Checks {
		res := Result{Group: g.Name, Check: c.Name}
		if broken != "" {
			res.Status, res.Summary = Skipped, "skipped because "+broken+" could not be checked"
			results = append(results, res)
			continue
		}
		err := runCheck(ctx, c.Run)
		var problem *Problem
		var skipped *skip
		switch {
		case err == nil:
			res.Status = Passed
		case errors.As(err, &problem):
			res.Status, res.Summary, res.Fix, res.Doc = Failed, problem.Summary, problem.Fix, problem.Doc
			if problem.Warning {
				res.Status = Warning
			}
		case errors.As(err, &skipped):
			res.Status, res.Summary = Skipped, skipped.reason
		default:
			res.Status, res.Summary = Failed, err.Error()
			broken = c.Name
		}
		results = append(results, res)
	}
	return results
}

// runCheck 调用 fn，最多等待 CheckTimeout，把 panic 转换为错误。不理会 ctx 的检查超时后不再等待。
func runCheck(ctx context.Context, fn func(context.Context) error) error {
	ctx, cancel := context.WithTimeout(ctx, CheckTimeout)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		defer func() {
			if v := recover(); v != nil {
				done <- errors.Newf("panic: %v", v)
			}
		}()
		done <- fn(ctx)
	}()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		return errors.Wrapf(ctx.Err(), "no answer within %s", CheckTimeout)
	}
}

// Require 执行全部检查，把提示写入日志，有检查失败时返回 ErrFailed 分类的错误。在启动服务或执行迁移之前调用，
// 环境不满足要求时拒绝继续，并说明怎样修改。logger 为 nil 时使用带 component=doctor 的 slog.Default()。
func Require(ctx context.Context, logger *slog.Logger, groups ...Group) error {
	if logger == nil {
		logger = logging.Or(nil).With(logging.AttrComponent, "doctor")
	}
	report := Run(ctx, groups...)
	for _, res := range report.Results {
		if res.Status == Warning {
			logger.WarnContext(ctx, "doctor warning", "group", res.Group, "check", res.Check,
				"summary", res.Summary, "fix", res.Fix)
		}
	}
	return report.Err()
}
