// Package period 定义报表与统计接口的时间窗口参数、解析规则和时间桶补零。
//
// 把 Params 嵌入接口的输入类型，在业务动作中调用 Resolve 按时区计算出 UTC 的半开区间 [Start, End)
// 和等长的上一对比期 [PrevStart, PrevEnd)，作为参数传给 SQL（不使用数据库的 NOW()）：
//
//	type SummaryInput struct {
//		period.Params
//		TeamID string `query:"team_id"`
//	}
//
//	w, err := period.Resolve(in.Params, period.Options{DefaultZone: "Asia/Shanghai"})
//	if err != nil {
//		return nil, err
//	}
//	// SQL 使用 WHERE created_at >= w.Start AND created_at < w.End
//	// 趋势结果用 period.FillSeries(w, rows, key, zero) 按连续时间桶补零。
package period
