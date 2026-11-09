package rest

import (
	"context"
	"net/http"

	"github.com/cockroachdb/errors"

	"github.com/danielgtaylor/huma/v2"

	"github.com/ryan053857/go-kit/authz"
	"github.com/ryan053857/go-kit/httpx/auth"
)

// OpenAPI security scheme 名称，与 SecuritySchemes、RequireAuth 和 Authorize 配合使用。
const (
	SchemeBearer  = "bearer"
	SchemeSession = "session"
)

// SecuritySchemes 返回 Bearer 与 Session Cookie 两种 security scheme 的定义，
// cookieName 为会话 Cookie 名（scs 的 sm.Cookie.Name）。在创建 API 前设置：
//
//	config := huma.DefaultConfig("Order API", "1.0.0")
//	config.Components.SecuritySchemes = rest.SecuritySchemes(sm.Cookie.Name)
func SecuritySchemes(cookieName string) map[string]*huma.SecurityScheme {
	return map[string]*huma.SecurityScheme{
		SchemeBearer:  {Type: "http", Scheme: "bearer"},
		SchemeSession: {Type: "apiKey", In: "cookie", Name: cookieName},
	}
}

// RequireAuth 返回 huma.Operation.Security 的值：Bearer 或 Session 任一认证通过，
// 且当前用户具有全部 abilities（见 authz）即可访问。
//
//	huma.Register(api, huma.Operation{
//		...
//		Security: rest.RequireAuth("order.update"),
//	}, a.handle)
//
// 只允许其中一种方式时，直接按 OpenAPI 的格式书写，例如
// []map[string][]string{{rest.SchemeBearer: {}}}。
func RequireAuth(abilities ...string) []map[string][]string {
	if abilities == nil {
		abilities = []string{}
	}
	return []map[string][]string{
		{SchemeBearer: abilities},
		{SchemeSession: abilities},
	}
}

// Authorize 返回 huma 中间件，按每个 Operation 声明的 Security 进行鉴权：
//
//   - 未声明 Security 的 Operation 直接放行。
//   - Security 中的各项是“或”关系，每项内的多个 scheme 是“且”关系（OpenAPI 语义）；
//     空项 {} 表示允许匿名访问。
//   - SchemeSession：context 中已有类型为 P 的 Principal（由 auth.LoadSession 放入）即通过。
//   - SchemeBearer：调用 verifyBearer 校验 Authorization 头中的 token，通过后用 auth.WithPrincipal 放入 context。
//     verifyBearer 为 nil 时 Bearer 始终不通过。
//   - Security 中声明的 scope 是能力（ability）：由 gate.Allows 判断（gate 同时检查 API Key 等凭证的 scope）；
//     gate 为 nil 时只要求 Principal 实现 auth.ScopeChecker 且具备全部 scope。
//
// 未认证时响应 401；已认证但没有能力时响应 403。
// verifyBearer 返回 auth.ErrUnauthenticated 时按 token 无效处理，返回其他错误时响应 500。
//
//	api.UseMiddleware(rest.Authorize(api, users.VerifyAccessToken, gate))
func Authorize[P auth.Principal](api huma.API, verifyBearer func(ctx context.Context, token string) (P, error), gate *authz.Gate) func(huma.Context, func(huma.Context)) {
	return func(ctx huma.Context, next func(huma.Context)) {
		reqs := ctx.Operation().Security
		if len(reqs) == 0 {
			next(ctx)
			return
		}

		c := ctx.Context()
		sessionP, hasSession := auth.From[P](c)

		// Bearer token 只在某项要求用到时才校验，且最多校验一次。
		var (
			bearerP       P
			hasBearer     bool
			bearerChecked bool
		)
		checkBearer := func() error {
			if bearerChecked {
				return nil
			}
			bearerChecked = true
			token, ok := auth.BearerToken(ctx.Header("Authorization"))
			if !ok || verifyBearer == nil {
				return nil
			}
			p, err := verifyBearer(c, token)
			switch {
			case errors.Is(err, auth.ErrUnauthenticated):
				return nil
			case err != nil:
				return err
			}
			bearerP, hasBearer = p, true
			return nil
		}

		authenticated := false
		for _, req := range reqs {
			if len(req) == 0 {
				next(ctx) // 允许匿名
				return
			}

			var (
				p          P
				fromBearer bool
				ok         = true
				scopes     []string
			)
			for scheme, s := range req {
				scopes = append(scopes, s...)
				switch scheme {
				case SchemeSession:
					if !hasSession {
						ok = false
					} else if !fromBearer {
						p = sessionP
					}
				case SchemeBearer:
					if err := checkBearer(); err != nil {
						_ = huma.WriteErr(api, ctx, http.StatusInternalServerError, "verify bearer token", err)
						return
					}
					if !hasBearer {
						ok = false
					} else {
						p, fromBearer = bearerP, true
					}
				default:
					ok = false
				}
			}
			if !ok {
				continue
			}
			authenticated = true
			pc := c
			if fromBearer {
				pc = auth.WithPrincipal(c, p)
			}
			if !allowed(pc, p, scopes, gate) {
				continue
			}
			if fromBearer {
				ctx = huma.WithContext(ctx, pc)
			}
			next(ctx)
			return
		}

		if authenticated {
			_ = huma.WriteErr(api, ctx, http.StatusForbidden, "", authz.ErrForbidden)
			return
		}
		ctx.SetHeader("WWW-Authenticate", "Bearer")
		_ = huma.WriteErr(api, ctx, http.StatusUnauthorized, "", auth.ErrUnauthenticated)
	}
}

// allowed 检查 Security 中声明的能力：有 gate 时由 gate 判断（同时检查 Principal 的 scope），
// 否则只检查 Principal 的 scope。ctx 中已有 p。
func allowed(ctx context.Context, p auth.Principal, abilities []string, gate *authz.Gate) bool {
	for _, a := range abilities {
		if gate != nil {
			if !gate.Allows(ctx, a) {
				return false
			}
			continue
		}
		sc, ok := p.(auth.ScopeChecker)
		if !ok || !sc.HasScope(a) {
			return false
		}
	}
	return true
}
