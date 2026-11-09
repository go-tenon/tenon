package logging

import (
	"io"
	"log/slog"
	"os"
	"strings"
)

// 输出格式。
const (
	FormatJSON = "json"
	FormatText = "text"
)

// 标准关联字段名。
const (
	AttrService   = "service"
	AttrVersion   = "version"
	AttrComponent = "component"
	AttrRequestID = "request_id"
	AttrTraceID   = "trace_id"
	AttrMessageID = "message_id"
	AttrAttempt   = "attempt"
)

// Options 是 New 的参数；零值可用：JSON、Info、stdout。
type Options struct {
	Level     slog.Leveler
	Format    string
	Writer    io.Writer
	AddSource bool
	// Attrs 是进程级静态字段，例如 service / version。
	Attrs []slog.Attr
}

// New 按 opts 构造 *slog.Logger；Handler 已包装，会输出 WithAttrs 挂在 context 上的字段。
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
	logger := slog.New(NewContextHandler(h))
	if len(opts.Attrs) > 0 {
		args := make([]any, 0, len(opts.Attrs))
		for _, a := range opts.Attrs {
			args = append(args, a)
		}
		logger = logger.With(args...)
	}
	return logger
}
