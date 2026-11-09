// Package logging 构造 slog.Logger 并连接 context 中的关联字段。
//
// New 返回独立日志器。业务使用 slog 的原生方法，
// WithAttrs 和 NewContextHandler 负责传递 request_id 等字段。
package logging
