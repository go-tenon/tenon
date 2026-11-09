package httpx

import "net/http"

// SecureHeaders 为每个响应设置以下安全响应头，handler 可以用 w.Header().Set 覆盖：
//
//	X-Content-Type-Options: nosniff
//	X-Frame-Options: SAMEORIGIN
//	Referrer-Policy: strict-origin-when-cross-origin
//
// HSTS 与 CSP 和部署方式、页面内容相关，不在此统一设置：
// HSTS 通常由 TLS 终止的网关添加，CSP 由产品按页面需要设置。
func SecureHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "SAMEORIGIN")
		h.Set("Referrer-Policy", "strict-origin-when-cross-origin")
		next.ServeHTTP(w, r)
	})
}
