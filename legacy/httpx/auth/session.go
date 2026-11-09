package auth

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"uuid"

	"github.com/alexedwards/scs/v2"
	"github.com/cockroachdb/errors"
)

// sessionUserKey 是会话中保存用户 ID 的键。
const sessionUserKey = "auth.user_id"

// Login 把 userID 记录到当前会话。
//
// 写入前会更换会话 token，防止会话固定攻击；因此请在校验完密码等凭证之后调用。
// 需要在 sm.LoadAndSave 之内使用。
func Login(ctx context.Context, sm *scs.SessionManager, userID uuid.UUID) error {
	if err := sm.RenewToken(ctx); err != nil {
		return errors.Wrap(err, "auth: renew session token")
	}
	sm.Put(ctx, sessionUserKey, userID.String())
	return nil
}

// Logout 销毁当前会话（包括会话中的其他数据）。
func Logout(ctx context.Context, sm *scs.SessionManager) error {
	if err := sm.Destroy(ctx); err != nil {
		return errors.Wrap(err, "auth: destroy session")
	}
	return nil
}

// SessionUserID 返回当前会话中登录的用户 ID，未登录时 ok 为 false。
func SessionUserID(ctx context.Context, sm *scs.SessionManager) (uuid.UUID, bool) {
	id, err := uuid.Parse(sm.GetString(ctx, sessionUserKey))
	return id, err == nil
}

// LoadSession 返回全局中间件：会话中有登录用户时，调用 load 加载 Principal 并用 WithPrincipal 放入 context；
// 未登录的请求原样放行（是否必须登录由 RequireLogin 或 rest.Authorize 决定）。
//
// load 返回 ErrUnauthenticated（用户已删除、被禁用等）时，从会话中移除该用户并按未登录处理；
// 返回其他错误时记录 Error 日志并响应 500。
//
// 需要挂在 sm.LoadAndSave 之后。
func LoadSession[P Principal](sm *scs.SessionManager, load func(ctx context.Context, userID uuid.UUID) (P, error)) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := r.Context()
			userID, ok := SessionUserID(ctx, sm)
			if !ok {
				next.ServeHTTP(w, r)
				return
			}

			p, err := load(ctx, userID)
			switch {
			case errors.Is(err, ErrUnauthenticated):
				sm.Remove(ctx, sessionUserKey)
				next.ServeHTTP(w, r)
				return
			case err != nil:
				slog.ErrorContext(ctx, "load session principal", "user_id", userID, "err", fmt.Sprintf("%+v", err))
				http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
				return
			}
			next.ServeHTTP(w, r.WithContext(WithPrincipal(ctx, p)))
		})
	}
}

// RequireLogin 返回页面路由组使用的中间件：context 中没有类型为 P 的 Principal 时拒绝请求。
//
//   - 普通请求：303 跳转到 loginURL；GET/HEAD 请求会附带 next 参数（原请求的路径与查询），
//     登录成功后可用 SafeRedirect(next, "/") 跳回。
//   - HTMX 请求（HX-Request: true）：响应 401 并设置 HX-Redirect，让浏览器整页跳转，
//     而不是把登录页插入局部片段；next 取自 HX-Current-URL，即用户当前所在的页面。
//
// API 路由请使用 rest.Authorize，它按 huma 的 OpenAPI security 声明返回 401/403。
func RequireLogin[P Principal](loginURL string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if _, ok := From[P](r.Context()); ok {
				next.ServeHTTP(w, r)
				return
			}

			if r.Header.Get("HX-Request") == "true" {
				back := ""
				if u, err := url.Parse(r.Header.Get("HX-Current-URL")); err == nil {
					back = u.RequestURI()
				}
				w.Header().Set("HX-Redirect", withNext(loginURL, back))
				w.WriteHeader(http.StatusUnauthorized)
				return
			}

			back := ""
			if r.Method == http.MethodGet || r.Method == http.MethodHead {
				back = r.URL.RequestURI()
			}
			http.Redirect(w, r, withNext(loginURL, back), http.StatusSeeOther)
		})
	}
}

// withNext 在 loginURL 上设置 next 查询参数；next 为空或为 "/" 时不添加。
func withNext(loginURL, next string) string {
	if next == "" || next == "/" {
		return loginURL
	}
	u, err := url.Parse(loginURL)
	if err != nil {
		return loginURL
	}
	q := u.Query()
	q.Set("next", next)
	u.RawQuery = q.Encode()
	return u.String()
}

// SafeRedirect 在 target 是本站相对路径（以单个 / 开头）时返回 target，否则返回 fallback。
// 用于校验登录后跳转的 next 参数，防止被利用跳转到外部网站（开放重定向）。
//
//	http.Redirect(w, r, auth.SafeRedirect(r.FormValue("next"), "/"), http.StatusSeeOther)
func SafeRedirect(target, fallback string) string {
	if !strings.HasPrefix(target, "/") || strings.HasPrefix(target, "//") || strings.HasPrefix(target, "/\\") {
		return fallback
	}
	u, err := url.Parse(target)
	if err != nil || u.Scheme != "" || u.Host != "" {
		return fallback
	}
	// 浏览器会忽略路径中的制表符与换行，例如 "/\t/evil.com" 会被当作 "//evil.com"。
	if strings.ContainsAny(target, "\t\r\n") {
		return fallback
	}
	return target
}
