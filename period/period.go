package period

import (
	"time"

	"github.com/go-tenon/tenon/errorx"
)

var (
	// ErrInvalidRange 是起始或结束日期格式非法，或者起始日期晚于结束日期。
	ErrInvalidRange = errorx.Define(errorx.InvalidArgument, "period.invalid_range")
	// ErrRangeTooLarge 是时间窗口的跨度超过了允许的最大天数。
	ErrRangeTooLarge = errorx.Define(errorx.InvalidArgument, "period.range_too_large")
	// ErrInvalidTimeZone 是不认识的 IANA 时区名。
	ErrInvalidTimeZone = errorx.Define(errorx.InvalidArgument, "period.invalid_time_zone")
	// ErrInvalidGranularity 是不支持的分桶粒度，或者当前跨度不允许使用该粒度（例如超过 2 天请求按小时分桶）。
	ErrInvalidGranularity = errorx.Define(errorx.InvalidArgument, "period.invalid_granularity")
)

// Granularity 是时间序列的分桶粒度。
type Granularity string

// 时间序列支持的分桶粒度。
const (
	Hour  Granularity = "hour"  // 按小时，标签格式 2006-01-02 15:00
	Day   Granularity = "day"   // 按自然日，标签格式 2006-01-02
	Week  Granularity = "week"  // 按自然周（周一起始，与 PostgreSQL date_trunc('week', ...) 一致），标签格式 2006-01-02
	Month Granularity = "month" // 按自然月（每月 1 日），标签格式 2006-01-02
)

const (
	defaultDays      = 30  // 未指定日期范围时的默认天数（含当天）
	defaultMaxDays   = 366 // 未指定上限时的最大跨度天数
	autoWeekAbove    = 62  // 超过该天数且未显式指定粒度时，自动切换为按周分桶
	maxHourRangeDays = 2   // 按小时分桶允许的最大天数跨度
)

// Params 是报表与统计接口的时间窗口查询参数，嵌入到输入类型中使用。
// query 标签用于 HTTP 查询参数，json 标签用于 RPC、MCP 等 JSON 入口。
type Params struct {
	From        string      `query:"from" json:"from,omitempty" format:"date" doc:"Start date (inclusive), YYYY-MM-DD."`
	To          string      `query:"to" json:"to,omitempty" format:"date" doc:"End date (inclusive), YYYY-MM-DD."`
	TimeZone    string      `query:"tz" json:"tz,omitempty" maxLength:"64" doc:"IANA time zone, e.g. Asia/Shanghai."`
	Granularity Granularity `query:"granularity" json:"granularity,omitempty" enum:"hour,day,week,month" doc:"Time bucket granularity."`
}

// Options 控制 Resolve 的默认行为与上限；零值字段使用默认值。
type Options struct {
	Now         time.Time // 当前时刻；为零值时取 time.Now()
	DefaultZone string    // 未传 tz 时的默认 IANA 时区；为空时取 UTC
	DefaultDays int       // 未传 from/to 时的默认天数（含当天）；<=0 时为 30
	MaxDays     int       // 允许的最大天数跨度；<=0 时为 366
	AllowHour   bool      // 是否允许按小时分桶（且跨度不超过 2 天）
}

// Window 是解析完成的时间窗口：包含在目标时区中的日期范围、转换为 UTC 的半开查询边界，以及等长上一对比期。
type Window struct {
	From        string         // 规范化后的起始日期 YYYY-MM-DD（含，按 Location）
	To          string         // 规范化后的结束日期 YYYY-MM-DD（含，按 Location）
	TimeZone    string         // IANA 时区名
	Location    *time.Location // 解析后的时区
	Granularity Granularity    // 实际生效的分桶粒度

	Start     time.Time // 主窗口开始时刻（UTC，含）：From 当天本地 00:00:00
	End       time.Time // 主窗口结束时刻（UTC，不含）：To 次日本地 00:00:00
	PrevStart time.Time // 等长上一对比期开始时刻（UTC，含）
	PrevEnd   time.Time // 等长上一对比期结束时刻（UTC，不含），等于 Start
	PrevFrom  string    // 上一对比期起始日期 YYYY-MM-DD（含）
	PrevTo    string    // 上一对比期结束日期 YYYY-MM-DD（含）
	Days      int       // 主窗口包含的自然日天数（From..To 闭区间）

	fromDate time.Time // UTC 午夜表示的起始日历日期，用于无夏令时偏移的日历算术
	toDate   time.Time // UTC 午夜表示的结束日历日期
}

// Meta 是写入响应体的时间窗口元数据，向客户端回显实际生效的日期范围、时区、粒度与对比期。
type Meta struct {
	From        string      `json:"from"`
	To          string      `json:"to"`
	TimeZone    string      `json:"time_zone"`
	Granularity Granularity `json:"granularity"`
	PrevFrom    string      `json:"prev_from"`
	PrevTo      string      `json:"prev_to"`
}

// Resolve 校验并解析时间窗口参数 p，按 opts 补全默认时区、默认日期范围与分桶粒度，返回可供 SQL 查询和序列补零使用的 Window。
func Resolve(p Params, opts Options) (Window, error) {
	// 1. 解析时区：拒绝 "Local" 防止受运行宿主时区影响。
	tz := p.TimeZone
	if tz == "" {
		tz = opts.DefaultZone
	}
	if tz == "" {
		tz = "UTC"
	}
	if tz == "Local" {
		return Window{}, ErrInvalidTimeZone.New(errorx.Field("tz"), errorx.Public("time_zone", tz))
	}
	loc, err := time.LoadLocation(tz)
	if err != nil {
		return Window{}, ErrInvalidTimeZone.New(errorx.Field("tz"), errorx.Public("time_zone", tz))
	}

	// 2. 计算默认起止日期并校验格式与顺序。
	now := opts.Now
	if now.IsZero() {
		now = time.Now()
	}
	defDays := opts.DefaultDays
	if defDays <= 0 {
		defDays = defaultDays
	}
	maxDays := opts.MaxDays
	if maxDays <= 0 {
		maxDays = defaultMaxDays
	}
	localNow := now.In(loc)
	today := time.Date(localNow.Year(), localNow.Month(), localNow.Day(), 0, 0, 0, 0, time.UTC)

	var fromDate, toDate time.Time
	switch {
	case p.From == "" && p.To == "":
		toDate = today
		fromDate = today.AddDate(0, 0, -(defDays - 1))
	case p.From != "" && p.To == "":
		fromDate, err = time.Parse(time.DateOnly, p.From)
		if err != nil {
			return Window{}, ErrInvalidRange.New(errorx.Field("from"))
		}
		toDate = today
	case p.From == "" && p.To != "":
		toDate, err = time.Parse(time.DateOnly, p.To)
		if err != nil {
			return Window{}, ErrInvalidRange.New(errorx.Field("to"))
		}
		fromDate = toDate.AddDate(0, 0, -(defDays - 1))
	default:
		fromDate, err = time.Parse(time.DateOnly, p.From)
		if err != nil {
			return Window{}, ErrInvalidRange.New(errorx.Field("from"))
		}
		toDate, err = time.Parse(time.DateOnly, p.To)
		if err != nil {
			return Window{}, ErrInvalidRange.New(errorx.Field("to"))
		}
	}
	if fromDate.After(toDate) {
		return Window{}, ErrInvalidRange.New(errorx.Field("from"))
	}

	// 3. 按纯日历天数计算跨度，不受夏令时 23h/25h 日影响。
	days := int(toDate.Sub(fromDate)/(24*time.Hour)) + 1
	if days > maxDays {
		return Window{}, ErrRangeTooLarge.New(errorx.Field("to"), errorx.Public("max_days", maxDays))
	}

	// 4. 解析并校验分桶粒度。
	gran := p.Granularity
	if gran == "" {
		if days <= autoWeekAbove {
			gran = Day
		} else {
			gran = Week
		}
	} else {
		switch gran {
		case Day, Week, Month:
		case Hour:
			if !opts.AllowHour || days > maxHourRangeDays {
				return Window{}, ErrInvalidGranularity.New(errorx.Field("granularity"))
			}
		default:
			return Window{}, ErrInvalidGranularity.New(errorx.Field("granularity"))
		}
	}

	// 5. 在目标时区构造本地午夜边界并转为 UTC；等长上一对比期按自然日往前平移。
	prevFromDate := fromDate.AddDate(0, 0, -days)
	prevToDate := fromDate.AddDate(0, 0, -1)
	nextDay := toDate.AddDate(0, 0, 1)

	start := localMidnightUTC(fromDate, loc)
	end := localMidnightUTC(nextDay, loc)
	prevStart := localMidnightUTC(prevFromDate, loc)

	return Window{
		From:        fromDate.Format(time.DateOnly),
		To:          toDate.Format(time.DateOnly),
		TimeZone:    tz,
		Location:    loc,
		Granularity: gran,
		Start:       start,
		End:         end,
		PrevStart:   prevStart,
		PrevEnd:     start,
		PrevFrom:    prevFromDate.Format(time.DateOnly),
		PrevTo:      prevToDate.Format(time.DateOnly),
		Days:        days,
		fromDate:    fromDate,
		toDate:      toDate,
	}, nil
}

// Meta 返回可写入响应体的时间窗口元数据。
func (w Window) Meta() Meta {
	return Meta{
		From:        w.From,
		To:          w.To,
		TimeZone:    w.TimeZone,
		Granularity: w.Granularity,
		PrevFrom:    w.PrevFrom,
		PrevTo:      w.PrevTo,
	}
}

// Buckets 返回当前窗口内按 Granularity 排列的全部连续时间桶标签：
// Day/Week/Month 为 YYYY-MM-DD（Week 对齐周一，Month 对齐每月 1 日，与 PostgreSQL date_trunc 一致）；
// Hour 为 YYYY-MM-DD HH:00（在 Location 中）。
func (w Window) Buckets() []string {
	if w.Days <= 0 || w.Location == nil {
		return []string{}
	}
	switch w.Granularity {
	case Hour:
		var out []string
		for cur := w.Start; cur.Before(w.End); cur = cur.Add(time.Hour) {
			out = append(out, cur.In(w.Location).Format("2006-01-02 15:00"))
		}
		return out
	case Week:
		// ISO 周一为每周起始，与 PostgreSQL date_trunc('week', ...) 对齐。
		offset := (int(w.fromDate.Weekday()) + 6) % 7
		cur := w.fromDate.AddDate(0, 0, -offset)
		var out []string
		for !cur.After(w.toDate) {
			out = append(out, cur.Format(time.DateOnly))
			cur = cur.AddDate(0, 0, 7)
		}
		return out
	case Month:
		cur := time.Date(w.fromDate.Year(), w.fromDate.Month(), 1, 0, 0, 0, 0, time.UTC)
		var out []string
		for !cur.After(w.toDate) {
			out = append(out, cur.Format(time.DateOnly))
			cur = cur.AddDate(0, 1, 0)
		}
		return out
	default:
		out := make([]string, 0, w.Days)
		for cur := w.fromDate; !cur.After(w.toDate); cur = cur.AddDate(0, 0, 1) {
			out = append(out, cur.Format(time.DateOnly))
		}
		return out
	}
}

// FillSeries 把稀疏的聚合结果 rows 按 w.Buckets() 补齐为连续序列，缺失的时间桶用 zero(bucket) 填充；
// 返回值永远是非 nil 切片。
func FillSeries[T any](w Window, rows []T, key func(T) string, zero func(bucket string) T) []T {
	buckets := w.Buckets()
	byKey := make(map[string]T, len(rows))
	for _, r := range rows {
		byKey[key(r)] = r
	}
	out := make([]T, len(buckets))
	for i, b := range buckets {
		if v, ok := byKey[b]; ok {
			out[i] = v
		} else {
			out[i] = zero(b)
		}
	}
	return out
}

// localMidnightUTC 返回日历日期 d 在时区 loc 中的 00:00:00 对应的 UTC 时刻。
func localMidnightUTC(d time.Time, loc *time.Location) time.Time {
	return time.Date(d.Year(), d.Month(), d.Day(), 0, 0, 0, 0, loc).UTC()
}
