// Package logging 提供 slog 的配置、构建与上下文字段辅助。
//
// New 返回 *slog.Logger，不定义 logging.Logger，也不提供另一套 Info/Error 门面。
// 不修改 slog.Default。业务代码只依赖标准库 slog；只有装配与中间件需要本包。
//
// 标准关联字段名：service、version、component、request_id、trace_id、message_id、attempt。
package logging
