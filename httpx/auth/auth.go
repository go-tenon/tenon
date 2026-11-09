// Package auth 提供 Session 与 Bearer 两种认证方式共用的胶水代码。
//
// 认证得到的调用方（Principal）由产品定义具体类型，本包用泛型存取，
// 业务代码拿到的就是自己的类型，不需要类型断言：
//
//	type User struct{ ID, Name string; Roles []string }
//	func (u *User) UserID() string { return u.ID }
//
//	u, ok := auth.From[*User](ctx)
//
// # Session
//
// 会话直接使用 github.com/alexedwards/scs/v2，由产品创建 SessionManager 并选择存储
// （postgresstore、redisstore 等；使用 app 包时已配置为 postgresstore），本包只约定会话中保存用户 ID 的方式：
//
//	Login / Logout       登录时更换会话 token（防会话固定攻击），退出时销毁会话
//	LoadSession          全局中间件：会话中有用户时加载 Principal 放入 context
//	RequireLogin         页面路由组中间件：未登录时跳转登录页（HTMX 请求用 HX-Redirect）
//
// # Bearer
//
// token 如何校验（JWT、数据库查询……）完全由产品决定。本包只提供 BearerToken 解析请求头；
// 与 huma 对接的鉴权中间件见 httpx/rest 的 Authorize。
//
// # 组装
//
//	sm := scs.New()
//	sm.Store = postgresstore.New(db.DB)
//
//	r := chi.NewRouter()
//	r.Use(
//		httpx.RealIP(...), httpx.RequestID, httpx.AccessLog(nil), httpx.Recover(nil),
//		http.NewCrossOriginProtection().Handler, // Cookie 会话需要 CSRF 防护
//		sm.LoadAndSave,
//		auth.LoadSession(sm, users.LoadPrincipal),
//	)
//	r.Group(func(r chi.Router) {
//		r.Use(auth.RequireLogin[*User]("/login"))
//		r.Get("/settings", settingsPage)
//	})
package auth

import (
	"context"
	"errors"
	"log/slog"
	"strings"

	"github.com/ryan053857/go-kit/httpx"
)

// ErrUnauthenticated 表示凭证无效：会话中的用户已不存在或被禁用、token 无效或过期等。
//
// 产品提供的加载与校验函数在这些情况下应返回它（或包装它的错误），
// 调用方据此按“未登录”处理；返回其他错误则视为服务端故障，响应 500。
var ErrUnauthenticated = errors.New("auth: unauthenticated")

// Principal 是已认证的调用方，由产品定义具体类型。
type Principal interface {
	// UserID 返回调用方 ID，用作日志的 user_id 字段。
	UserID() string
}

// ScopeChecker 可由 Principal 实现，用于 httpx/rest 的 Authorize 检查 OpenAPI security 中声明的 scope。
type ScopeChecker interface {
	HasScope(scope string) bool
}

type principalKey[P Principal] struct{}

// WithPrincipal 返回携带 p 的 context，同时用 httpx.WithLogAttrs 挂上 user_id 日志字段。
func WithPrincipal[P Principal](ctx context.Context, p P) context.Context {
	ctx = context.WithValue(ctx, principalKey[P]{}, p)
	return httpx.WithLogAttrs(ctx, slog.String("user_id", p.UserID()))
}

// From 返回 ctx 中类型为 P 的 Principal；未登录时 ok 为 false。
func From[P Principal](ctx context.Context) (p P, ok bool) {
	p, ok = ctx.Value(principalKey[P]{}).(P)
	return p, ok
}

// BearerToken 从 Authorization 请求头的值中解析 Bearer token（scheme 不区分大小写）。
//
//	token, ok := auth.BearerToken(r.Header.Get("Authorization"))
func BearerToken(authorization string) (string, bool) {
	scheme, token, ok := strings.Cut(strings.TrimSpace(authorization), " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") {
		return "", false
	}
	token = strings.TrimSpace(token)
	return token, token != ""
}
