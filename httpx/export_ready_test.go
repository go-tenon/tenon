package httpx

import "context"

// WithDrainStarted 供测试构造 Serve 已经开始排空时的请求 context。
func WithDrainStarted(ctx context.Context) context.Context {
	ch := make(chan struct{})
	close(ch)
	return context.WithValue(ctx, drainKey{}, ch)
}
