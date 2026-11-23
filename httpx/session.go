package httpx

import (
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/cockroachdb/errors"

	"github.com/go-tenon/tenon/errorx"
)

// DefaultSessionCookieName 是会话 cookie 默认的名称。
const DefaultSessionCookieName = "__Host-session"

// ErrCrossSite 表示浏览器从其他站点的页面发出了带会话 cookie 的写请求（跨站请求伪造），或者跨站调用了会设置
// 会话 cookie 的登录接口。
var ErrCrossSite = errorx.Define(errorx.PermissionDenied, "http.cross_site")

// SessionCookie 是浏览器保存登录 token 的 cookie，零值的名称是 DefaultSessionCookieName。
// 登录接口用 Set 把 token 写进 Set-Cookie，rest.RequireToken 和 realtime 从请求中读取它。
//
//   - HttpOnly：页面上的脚本读不到它，XSS 拿不走登录凭据；
//   - Secure、__Host- 前缀：只通过 HTTPS 发送（浏览器把 http://localhost 视为安全），只属于当前主机，
//     子域名既不能设置也不能覆盖它；
//   - SameSite=Lax：其他站点的页面发出的请求不带它，只有顶层的页面跳转例外。
//
// 同一个源下有多套账号体系时（例如运营后台和租户控制台），各用一个名称，例如 SessionCookie{Name: "__Host-ops"}，
// 两个 cookie 同时存在、互不覆盖，各自的 API 只读自己的那一个（rest.Options.SessionCookie）。
// 需要将代为登录会话与普通登录会话隔离时，设置 Impersonation（例如 "__Host-impersonation"）：
// Token 优先读取 Impersonation cookie，没有时再读取 Name；退出代为登录只需清除 ImpersonationCookie()，保留原登录态。
type SessionCookie struct {
	Name          string // 以 __Host- 开头；空字符串表示 DefaultSessionCookieName。
	Impersonation string // 可选的独立代为登录 cookie 名称，以 __Host- 开头且与 Name 不同；为空表示不单独拆分。
}

// Validate 检查名称以 __Host- 开头，并且是合法的 cookie 名称；设置了 Impersonation 时一并校验且不能与 Name 相同。
func (c SessionCookie) Validate() error {
	if err := validateCookieName(c.CookieName()); err != nil {
		return err
	}
	if c.Impersonation != "" {
		if err := validateCookieName(c.Impersonation); err != nil {
			return err
		}
		if c.Impersonation == c.CookieName() {
			return errors.Newf("httpx: impersonation cookie name %q must differ from session cookie name", c.Impersonation)
		}
	}
	return nil
}

// validateCookieName 检查 cookie 名称以 __Host- 开头且只含合法 token 字符。
func validateCookieName(name string) error {
	if !strings.HasPrefix(name, "__Host-") || len(name) == len("__Host-") ||
		strings.IndexFunc(name, func(r rune) bool { return r > 127 || !isTokenChar(byte(r)) }) >= 0 {
		return errors.Newf("httpx: session cookie name %q must start with __Host- and be a valid cookie name", name)
	}
	return nil
}

// CookieName 返回实际的 cookie 名称：Name，为空时是 DefaultSessionCookieName。
func (c SessionCookie) CookieName() string {
	if c.Name == "" {
		return DefaultSessionCookieName
	}
	return c.Name
}

// ImpersonationCookie 返回代为登录使用的 SessionCookie：配置了 Impersonation 时使用它，否则回退到当前 CookieName。
func (c SessionCookie) ImpersonationCookie() SessionCookie {
	if c.Impersonation != "" {
		return SessionCookie{Name: c.Impersonation}
	}
	return SessionCookie{Name: c.CookieName()}
}

// Set 返回保存 token 的 Set-Cookie。maxAge 是浏览器保存它的时长，通常是 apitoken.Config 的 Lifetime；
// token 是否有效仍由服务端判断，例如空闲超时后 cookie 还在，请求照样返回 401。
// maxAge 不为正数或名称不合法时 panic。
func (c SessionCookie) Set(token string, maxAge time.Duration) *http.Cookie {
	if maxAge <= 0 {
		panic("httpx: SessionCookie.Set needs a positive maxAge")
	}
	ck := c.cookie(token)
	ck.MaxAge = int(maxAge / time.Second)
	return ck
}

// Clear 返回删除这个 cookie 的 Set-Cookie，用于退出登录。名称不合法时 panic。
func (c SessionCookie) Clear() *http.Cookie {
	ck := c.cookie("")
	ck.MaxAge = -1
	return ck
}

// cookie 返回保存会话 value 的 cookie。
func (c SessionCookie) cookie(value string) *http.Cookie {
	mustValid("SessionCookie", c.Validate())
	return &http.Cookie{Name: c.CookieName(), Value: value, Path: "/", HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode}
}

// Token 返回 Cookie 请求头中这个会话 cookie 的值，没有时返回空字符串。
// 配置了 Impersonation 时优先读取代为登录 cookie，非空直接返回，否则读取普通会话 cookie。
func (c SessionCookie) Token(cookieHeader string) string {
	if cookieHeader == "" {
		return ""
	}
	cookies, err := http.ParseCookie(cookieHeader)
	if err != nil {
		return ""
	}
	if c.Impersonation != "" {
		for _, ck := range cookies {
			if ck.Name == c.Impersonation && ck.Value != "" {
				return ck.Value
			}
		}
	}
	name := c.CookieName()
	for _, ck := range cookies {
		if ck.Name == name {
			return ck.Value
		}
	}
	return ""
}

// SameOrigin 报告浏览器的请求是否来自同源的页面，用于拒绝跨站请求伪造：浏览器为每个请求发送 Sec-Fetch-Site，
// 同源时为 same-origin；没有这个请求头时比较 Origin 与请求的 Host。两者都没有时返回 false：
// 带着会话 cookie 却不说明来源的请求不是现代浏览器发出的，按跨站处理。
func SameOrigin(secFetchSite, origin, host string) bool {
	if secFetchSite != "" {
		return secFetchSite == "same-origin"
	}
	if origin == "" || origin == "null" {
		return false
	}
	u, err := url.Parse(origin)
	return err == nil && u.Host != "" && strings.EqualFold(u.Host, host)
}

// SafeMethod 报告方法是否只读取数据（GET、HEAD、OPTIONS）。跨站请求伪造只针对其他方法。
func SafeMethod(method string) bool {
	return method == http.MethodGet || method == http.MethodHead || method == http.MethodOptions
}

// BearerToken 取出 Authorization 请求头的值 h 中 Bearer 后的 token，Bearer 大小写不敏感；不是 Bearer 时返回空字符串。
// rest.RequireToken、mcpx 和 realtime 都用它读取客户端的 token。
func BearerToken(h string) string {
	scheme, token, ok := strings.Cut(strings.TrimSpace(h), " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") {
		return ""
	}
	return strings.TrimSpace(token)
}
