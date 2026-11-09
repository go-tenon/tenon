package logger

import (
	"context"
	"log/slog"
	"slices"
)

type ctxKey struct{}

// WithAttrs 返回携带 attrs 的新 context。多次调用会累加字段，
// 父 context 不受影响。
//
// 字段只会被 NewContextHandler 包装过的 Handler 输出（New 返回的 Logger 已包装），
// 且只在使用 slog.InfoContext 等带 ctx 的方法时生效。
func WithAttrs(ctx context.Context, attrs ...slog.Attr) context.Context {
	if len(attrs) == 0 {
		return ctx
	}
	prev, _ := ctx.Value(ctxKey{}).([]slog.Attr)
	// slices.Concat 总是分配新切片，避免兄弟 context 之间共享底层数组。
	return context.WithValue(ctx, ctxKey{}, slices.Concat(prev, attrs))
}

// NewContextHandler 包装 h，使其在处理每条记录时追加 WithAttrs 挂在 context 上的字段。
// 若 h 已被包装过则原样返回。
//
// 注意：如果 Logger 通过 WithGroup 开启了分组，context 字段与调用处传入的字段一样，
// 会落在该分组内。
func NewContextHandler(h slog.Handler) slog.Handler {
	if _, ok := h.(contextHandler); ok {
		return h
	}
	return contextHandler{h}
}

type contextHandler struct {
	slog.Handler
}

func (h contextHandler) Handle(ctx context.Context, r slog.Record) error {
	if ctx != nil {
		if attrs, ok := ctx.Value(ctxKey{}).([]slog.Attr); ok {
			r.AddAttrs(attrs...)
		}
	}
	return h.Handler.Handle(ctx, r)
}

func (h contextHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return contextHandler{h.Handler.WithAttrs(attrs)}
}

func (h contextHandler) WithGroup(name string) slog.Handler {
	return contextHandler{h.Handler.WithGroup(name)}
}
