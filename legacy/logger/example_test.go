package logger_test

import (
	"context"
	"log/slog"
	"os"

	"github.com/ryan053857/go-kit/logger"
)

func Example() {
	// 级别通常来自配置文件，例如 "debug"；LevelVar 也支持运行时修改。
	var level slog.LevelVar
	_ = level.UnmarshalText([]byte("info"))

	log := logger.New(logger.Options{
		Level:  &level,
		Format: logger.FormatJSON,
		Writer: os.Stdout,
	}).With("service", "order")
	slog.SetDefault(log)

	// 在中间件里挂上请求级字段。
	ctx := logger.WithAttrs(context.Background(), slog.String("request_id", "r-123"))

	// 业务代码只使用标准库 slog。
	slog.InfoContext(ctx, "order created", "order_id", 42)
}
