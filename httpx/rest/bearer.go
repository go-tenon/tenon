package rest

import (
	"strings"

	"github.com/danielgtaylor/huma/v2"

	"github.com/ryan053857/mygoravel/fault"
	"github.com/ryan053857/mygoravel/identity"
)

// TokenValidator 校验 bearer token 并返回主体。
type TokenValidator func(ctx huma.Context, token string) (identity.Subject, error)

var (
	errUnauthenticated = fault.Define(fault.Unauthenticated, "auth.unauthenticated")
	errForbidden       = fault.Define(fault.PermissionDenied, "auth.forbidden")
)

// EnsureBearerScheme 在 OpenAPI 组件中登记 bearer 安全方案。
func EnsureBearerScheme(api huma.API) {
	oapi := api.OpenAPI()
	if oapi.Components == nil {
		oapi.Components = &huma.Components{}
	}
	if oapi.Components.SecuritySchemes == nil {
		oapi.Components.SecuritySchemes = map[string]*huma.SecurityScheme{}
	}
	oapi.Components.SecuritySchemes["bearer"] = &huma.SecurityScheme{
		Type:         "http",
		Scheme:       "bearer",
		BearerFormat: "opaque",
	}
}

// RequireBearer 同时设置 OpenAPI Security 与运行时中间件。
// 未声明安全要求的端点不要调用本函数。scopes 全部需要满足。
func RequireBearer(api huma.API, validate TokenValidator, scopes ...string) func(op *huma.Operation) {
	if validate == nil {
		panic("rest: nil token validator")
	}
	EnsureBearerScheme(api)
	scopeCopy := append([]string(nil), scopes...)
	return func(op *huma.Operation) {
		if op.Metadata == nil {
			op.Metadata = map[string]any{}
		}
		if op.Metadata["mygoravel.bearer"] == true {
			panic("rest: duplicate bearer declaration")
		}
		op.Metadata["mygoravel.bearer"] = true
		if len(scopeCopy) > 0 {
			if op.Extensions == nil {
				op.Extensions = map[string]any{}
			}
			op.Extensions["x-required-scopes"] = scopeCopy
		}
		op.Security = []map[string][]string{{"bearer": {}}}
		op.Middlewares = append(op.Middlewares, func(ctx huma.Context, next func(huma.Context)) {
			ctx.SetHeader("Cache-Control", "no-store")
			token := bearerToken(ctx.Header("Authorization"))
			if token == "" {
				ctx.SetHeader("WWW-Authenticate", `Bearer`)
				_ = huma.WriteErr(api, ctx, 401, "unauthenticated", FromFault(errUnauthenticated.New()))
				return
			}
			sub, err := validate(ctx, token)
			if err != nil && HTTPStatus(err) >= 500 {
				if a, ok := api.(*API); ok && a.logger != nil {
					a.logger.ErrorContext(ctx.Context(), "authentication backend failed", "code", fault.CodeOf(err))
				}
				_ = huma.WriteErr(api, ctx, HTTPStatus(err), "authentication unavailable")
				return
			}
			if err != nil || sub.ID == "" {
				ctx.SetHeader("WWW-Authenticate", `Bearer`)
				_ = huma.WriteErr(api, ctx, 401, "unauthenticated", FromFault(err))
				return
			}
			for _, sc := range scopeCopy {
				if !sub.HasScope(sc) {
					_ = huma.WriteErr(api, ctx, 403, "forbidden", FromFault(errForbidden.New(fault.Public("scope", sc))))
					return
				}
			}
			next(huma.WithContext(ctx, identity.WithSubject(ctx.Context(), sub)))
		})
	}
}

// Public 显式标记端点为公开（无 Security）。
func Public() func(op *huma.Operation) {
	return func(op *huma.Operation) {
		op.Security = []map[string][]string{}
		if op.Metadata != nil && op.Metadata["mygoravel.bearer"] == true {
			panic("rest: cannot mark an authenticated endpoint public")
		}
	}
}

func bearerToken(h string) string {
	const p = "Bearer "
	if len(h) < len(p) || !strings.EqualFold(h[:len(p)], p) {
		return ""
	}
	return strings.TrimSpace(h[len(p):])
}
