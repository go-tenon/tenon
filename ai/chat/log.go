package chat

import (
	"context"
	"fmt"
	"log/slog"

	"trpc.group/trpc-go/trpc-agent-go/log"

	"github.com/go-tenon/tenon/logging"
)

// SetLogger 让 trpc-agent-go 的日志写到 l，带 component=ai 字段，可以按组件设置级别。
// trpc-agent-go 的日志是进程级的全局设置，在装配时调用一次。
func SetLogger(l *slog.Logger) {
	a := slogAdapter{logging.Or(l).With(logging.AttrComponent, "ai")}
	log.Default = a
	log.ContextDefault = a
}

// slogAdapter 把 trpc-agent-go 的日志接口转到 slog。
type slogAdapter struct{ l *slog.Logger }

// log 以 level 记录 msg。
func (a slogAdapter) log(level slog.Level, msg string) {
	a.l.Log(context.Background(), level, msg)
}

// Debug 以 Debug 级别记录参数拼接的消息。
func (a slogAdapter) Debug(args ...any) { a.log(slog.LevelDebug, fmt.Sprint(args...)) }

// Debugf 以 Debug 级别记录格式化的消息。
func (a slogAdapter) Debugf(format string, args ...any) {
	a.log(slog.LevelDebug, fmt.Sprintf(format, args...))
}

// Info 以 Info 级别记录参数拼接的消息。
func (a slogAdapter) Info(args ...any) { a.log(slog.LevelInfo, fmt.Sprint(args...)) }

// Infof 以 Info 级别记录格式化的消息。
func (a slogAdapter) Infof(format string, args ...any) {
	a.log(slog.LevelInfo, fmt.Sprintf(format, args...))
}

// Warn 以 Warn 级别记录参数拼接的消息。
func (a slogAdapter) Warn(args ...any) { a.log(slog.LevelWarn, fmt.Sprint(args...)) }

// Warnf 以 Warn 级别记录格式化的消息。
func (a slogAdapter) Warnf(format string, args ...any) {
	a.log(slog.LevelWarn, fmt.Sprintf(format, args...))
}

// Error 以 Error 级别记录参数拼接的消息。
func (a slogAdapter) Error(args ...any) { a.log(slog.LevelError, fmt.Sprint(args...)) }

// Errorf 以 Error 级别记录格式化的消息。
func (a slogAdapter) Errorf(format string, args ...any) {
	a.log(slog.LevelError, fmt.Sprintf(format, args...))
}

// Fatal 和 Fatalf 只记录错误，进程继续运行：应用的生命周期由应用自己决定。
func (a slogAdapter) Fatal(args ...any) { a.log(slog.LevelError, fmt.Sprint(args...)) }

// Fatalf 以 Error 级别记录格式化的消息，进程继续运行。
func (a slogAdapter) Fatalf(format string, args ...any) {
	a.log(slog.LevelError, fmt.Sprintf(format, args...))
}
