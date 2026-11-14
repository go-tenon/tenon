package schedule

import (
	"strings"
	"time"

	"github.com/adhocore/gronx"
	"github.com/cockroachdb/errors"
)

// when 判断任务在某一秒是否到期。
type when struct {
	// cron 是带秒的 cron 表达式，every 为 0 时使用。5 段的表达式和 @hourly 等预定义写法在前面补上秒 0。
	cron  string
	every time.Duration // @every 的间隔，按 Unix 时间对齐
}

// parse 解析 Add 的表达式：5 段（分钟精度，在第 0 秒触发）、6 段（第一段是秒）、
// @hourly 这类预定义写法，以及 "@every 15s"。
func parse(expr string) (when, error) {
	if d, ok := strings.CutPrefix(expr, "@every "); ok {
		every, err := time.ParseDuration(strings.TrimSpace(d))
		if err != nil {
			return when{}, errors.Wrapf(err, "schedule: invalid interval in %q", expr)
		}
		if every < time.Second || every%time.Second != 0 {
			return when{}, errors.Newf("schedule: interval in %q must be a whole number of seconds", expr)
		}
		return when{every: every}, nil
	}
	// 补上秒：gronx 从 v1.20.7 起把 5 段表达式的秒当作 *，这一分钟的每一秒都算到期，Next、Prev 也随之改变。
	// 写出秒 0 之后，5 段表达式只在第 0 秒到期。
	segs, err := gronx.Segments(expr)
	if err != nil {
		return when{}, errors.Newf("schedule: invalid cron expression %q", expr)
	}
	cron := strings.Join(segs, " ")
	if !gronx.IsValid(cron) {
		return when{}, errors.Newf("schedule: invalid cron expression %q", expr)
	}
	return when{cron: cron}, nil
}

// due 报告任务在 t 这一秒是否到期，cron 表达式按 loc 计算。
func (w when) due(g *gronx.Gronx, t time.Time, loc *time.Location) bool {
	if w.every > 0 {
		return t.Unix()%int64(w.every/time.Second) == 0
	}
	ok, err := g.IsDue(w.cron, t.In(loc))
	return ok && err == nil
}
