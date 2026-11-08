// Package logger 提供基于标准库 log/slog 的日志初始化与 context 字段透传。
//
// 本包只做两件事：
//
//   - New：按统一约定构造 *slog.Logger（JSON/Text、级别、输出位置）。
//   - WithAttrs：把字段挂到 context 上，之后所有 slog.*Context 调用自动带上。
//
// 业务代码只依赖标准库 slog，不需要 import 本包；只有 main 与中间件需要。
//
// # 字段命名约定
//
// 为了让各产品的日志在采集平台上能用同一套查询与告警规则，统一使用以下字段名：
//
//	service, env, version      进程级静态字段，在 main 中通过 Logger.With 设置
//	trace_id, span_id          链路追踪
//	request_id, user_id        请求级字段，在中间件中通过 WithAttrs 设置
//	err                        错误，slog.Any("err", err)
package logger

import (
	"io"
	"log/slog"
	"os"
	"strings"
)

// 日志输出格式。
const (
	FormatJSON = "json"
	FormatText = "text"
)

// Options 是 New 的参数，零值即可用：JSON 格式、Info 级别、输出到 stdout。
type Options struct {
	// Level 为最低输出级别，nil 表示 slog.LevelInfo。
	// 传入 *slog.LevelVar 即可在运行时调整级别。
	Level slog.Leveler

	// Format 为 FormatJSON（默认）或 FormatText，不区分大小写。
	// 其他值按 FormatJSON 处理。
	Format string

	// Writer 为输出目标，nil 表示 os.Stdout。
	Writer io.Writer

	// AddSource 为 true 时记录调用位置（source 字段）。
	AddSource bool
}

// New 按 opts 构造 *slog.Logger，其 Handler 已由 NewContextHandler 包装，
// 会自动输出通过 WithAttrs 挂在 context 上的字段。
func New(opts Options) *slog.Logger {
	w := opts.Writer
	if w == nil {
		w = os.Stdout
	}

	hopts := &slog.HandlerOptions{
		AddSource: opts.AddSource,
		Level:     opts.Level,
	}

	var h slog.Handler
	if strings.EqualFold(opts.Format, FormatText) {
		h = slog.NewTextHandler(w, hopts)
	} else {
		h = slog.NewJSONHandler(w, hopts)
	}

	return slog.New(NewContextHandler(h))
}
