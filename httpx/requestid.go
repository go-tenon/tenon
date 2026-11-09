package httpx

import (
	"context"
	"log/slog"
	"net/http"
	"uuid"

	"github.com/ryan053857/go-kit/logger"
)

// RequestIDHeader 是请求 ID 使用的请求头与响应头。
const RequestIDHeader = "X-Request-ID"

const maxRequestIDLen = 128

type requestIDKey struct{}

// RequestID 为每个请求确定一个 ID：请求头 X-Request-ID 合法时沿用（便于串联上游网关或调用方的日志），
// 否则生成 UUIDv7。ID 会：
//
//   - 写回响应头 X-Request-ID；
//   - 通过 logger.WithAttrs 以 request_id 字段挂到 context，之后的 slog.*Context 日志自动带上；
//   - 可用 RequestIDFrom 从 context 读取（例如放进错误响应）。
//
// 合法的 ID 长度为 1–128，只含字母、数字和 - _ . : 字符。
func RequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get(RequestIDHeader)
		if !validRequestID(id) {
			id = uuid.NewV7().String()
		}
		w.Header().Set(RequestIDHeader, id)

		ctx := context.WithValue(r.Context(), requestIDKey{}, id)
		ctx = logger.WithAttrs(ctx, slog.String("request_id", id))
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// RequestIDFrom 返回 RequestID 中间件放入 ctx 的请求 ID，没有则返回空字符串。
func RequestIDFrom(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey{}).(string)
	return id
}

func validRequestID(id string) bool {
	if id == "" || len(id) > maxRequestIDLen {
		return false
	}
	for i := 0; i < len(id); i++ {
		switch c := id[i]; {
		case 'a' <= c && c <= 'z', 'A' <= c && c <= 'Z', '0' <= c && c <= '9':
		case c == '-', c == '_', c == '.', c == ':':
		default:
			return false
		}
	}
	return true
}
