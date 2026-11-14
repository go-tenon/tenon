package apitoken

import (
	"net/http"
	"time"
)

// Session 是 JWT 模式中登录、刷新和确认身份接口的响应体，所有客户端按同一个格式读取：
//
//	{"access_token": "eyJ…", "refresh_token": "…", "expires_at": "2026-11-11T08:10:00Z"}
//
// 客户端把 access_token 放在 Authorization: Bearer 中；refresh_token 不为空时替换已保存的 refresh token，
// 为空时继续使用已保存的那一个（宽限期内的重复刷新、确认身份，以及 refresh token 放在 cookie 中时）。
// 用 NewSession 或 RefreshCookie.Session 创建。
type Session struct {
	AccessToken  string    `json:"access_token" doc:"放在 Authorization: Bearer 中的 access token"`
	RefreshToken string    `json:"refresh_token,omitempty" doc:"不为空时替换已保存的 refresh token；为空时继续使用已保存的。refresh token 放在 cookie 中时总是为空"`
	ExpiresAt    time.Time `json:"expires_at" doc:"access token 的过期时间"`
}

// NewSession 返回 p 的响应体，refresh token 放在响应体中。App、桌面端、小程序和脚本使用它。
func NewSession(p Pair) Session {
	return Session{AccessToken: p.Access, RefreshToken: p.Refresh, ExpiresAt: p.ExpiresAt}
}

// Session 返回 p 的响应体和保存 refresh token 的 cookie，响应体中没有 refresh token。浏览器中的单页应用使用它。
// p.Refresh 为空时 cookie 为 nil，不改动浏览器中的 cookie，见 Set。
func (c RefreshCookie) Session(p Pair) (Session, *http.Cookie) {
	return Session{AccessToken: p.Access, ExpiresAt: p.ExpiresAt}, c.Set(p)
}

// RefreshRequest 是刷新接口的请求体：App 等客户端传入 refresh_token；浏览器留空，refresh token 从 RefreshCookie 读取。
type RefreshRequest struct {
	RefreshToken string `json:"refresh_token,omitempty" doc:"App 等客户端传入；浏览器留空，使用 cookie"`
}
