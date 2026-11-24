package rest

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/go-tenon/tenon/httpx"
	"github.com/go-tenon/tenon/identity"
	"github.com/go-tenon/tenon/logging"
)

// Authenticator 从请求中识别调用者，供 RequireAuth 使用。
// 请求没有携带凭据时返回 ID 为空的主体或 errorx.Unauthenticated 错误；后端故障时返回 5xx 类错误。
type Authenticator func(ctx huma.Context) (identity.Subject, error)

const (
	authMetadata         = "tenon.auth"
	schemesMetadata      = "tenon.auth.schemes"
	confirmMetadata      = "tenon.confirmed"
	directOnlyMetadata   = "tenon.direct_only"
	accountScopeMetadata = "tenon.account_scope"
)

// tokenSchemes 是 RequireToken 在 OpenAPI 中登记的两种携带方式，任选其一。会话 cookie 排在前面：
// 文档页默认选第一种，登录后在浏览器中试用接口时，浏览器自动带上会话 cookie，不用填写。
func tokenSchemes(cookie string) []namedScheme {
	return []namedScheme{
		{"session", &huma.SecurityScheme{Type: "apiKey", In: "cookie", Name: cookie,
			Description: "浏览器登录后的会话 cookie，由浏览器自动携带。登录后在文档页中试用接口时选这一项，不用填写。"}},
		{"bearer", &huma.SecurityScheme{Type: "http", Scheme: "bearer",
			Description: "Authorization: Bearer <token>，供 App、脚本和第三方使用。请求同时带有两者时以它为准。"}},
	}
}

// namedScheme 是一个带名称的认证方案。
type namedScheme struct {
	name   string
	scheme *huma.SecurityScheme
}

// RequireToken 使用 Options.Validate 要求请求携带有效的 token，并且主体能执行 permissions 中的全部权限。
// 与 rest.RequireToken(api, validate, permissions...) 相同，免去在每条路由重复传入校验器。
// 未设置 Options.Validate 时 panic。
func (a *API) RequireToken(permissions ...string) func(op *huma.Operation) {
	return RequireToken(a, a.validate, permissions...)
}

// RequireToken 要求请求携带有效的 token，校验器 validate 返回有效主体，并且主体能执行 permissions 中的全部权限
// （identity.Subject.Can）。validate 为 nil 时使用 Options.Validate；两者皆为空时 panic。token 有两种携带方式，是同一种 token：
//
//   - 浏览器：会话 cookie（Options.SessionCookie），由浏览器自动附加，页面上的脚本读不到它；
//   - App、脚本和第三方：Authorization: Bearer。两者都有时以它为准。
//
// 用会话 cookie 认证的写请求（GET、HEAD、OPTIONS 以外）必须来自同源的页面（httpx.SameOrigin），
// 否则返回 403 http.cross_site：cookie 是浏览器自动附加的，这一步防止跨站请求伪造。Bearer 不受影响。
// 路由表中的访问策略为 AccessToken。
func RequireToken(api *API, validate identity.TokenValidator, permissions ...string) func(op *huma.Operation) {
	if validate == nil && api != nil {
		validate = api.validate
	}
	if validate == nil {
		panic("rest: nil token validator")
	}
	session := api.session
	authenticate := func(ctx huma.Context) (identity.Subject, error) {
		token := httpx.BearerToken(ctx.Header("Authorization"))
		if token == "" {
			token = session.Token(ctx.Header("Cookie"))
		}
		if token == "" {
			return identity.Subject{}, nil
		}
		return validate(ctx.Context(), token)
	}
	// crossSite 拒绝用会话 cookie 认证、来自其他站点的写请求。
	crossSite := func(ctx huma.Context) error {
		if httpx.SafeMethod(ctx.Method()) || httpx.BearerToken(ctx.Header("Authorization")) != "" || session.Token(ctx.Header("Cookie")) == "" {
			return nil
		}
		return sameOrigin(ctx)
	}
	return requireAuth(api, AccessToken, tokenSchemes(session.CookieName()), "Bearer", crossSite, authenticate, permissions...)
}

// sameOrigin 检查请求来自同一来源，否则返回 403。
func sameOrigin(ctx huma.Context) error {
	if httpx.SameOrigin(ctx.Header("Sec-Fetch-Site"), ctx.Header("Origin"), ctx.Host()) {
		return nil
	}
	return httpx.ErrCrossSite.New()
}

// SameOrigin 只接受同源页面发出的请求（httpx.SameOrigin），其他请求返回 403 http.cross_site。
// 用于设置会话 cookie 的公开接口，例如浏览器的登录：防止其他网站让用户在不知情时登录攻击者的账号（登录 CSRF）。
// App 等客户端不经过这些接口，它们调用在响应体中返回 token 的接口。
func SameOrigin(api *API) func(op *huma.Operation) {
	return func(op *huma.Operation) {
		op.Middlewares = append(op.Middlewares, func(ctx huma.Context, next func(huma.Context)) {
			if err := sameOrigin(ctx); err != nil {
				writeError(api, ctx, problem(api, ctx, err))
				return
			}
			next(ctx)
		})
	}
}

// RequireAuth 为路由声明名为 name 的安全方案：在 OpenAPI 组件中登记 scheme 并标记路由的 Security，
// 同时安装运行时中间件。
//
// 运行时用 identity.Establish 得到本次请求的主体并写入 identity.WithSubject：
//
//   - authenticate 识别调用者：没有凭据或凭据无效返回 401，认证后端故障返回对应的 5xx；
//   - 主体的 Tenant 由凭据设置且与本次请求的租户（identity.TenantFromContext）不同时返回 403；
//   - 主体的 Type 不在 Options.Accounts 中时返回 401，否则调用这套账号的 Resolver 得到本次请求的主体，见 Options.Accounts；
//   - 主体缺少 permissions 中的某项权限时返回 403。
//
// 认证通过后安装 identity.CredentialCheck：SSE 等长连接期间定期重新执行以上检查（identity.Reestablish），
// 凭据失效或失去权限时结束连接。
// 每条路由只能声明一种认证方案。RequireToken 是同类的内置方案。Options.Accounts 为空时 panic。
func RequireAuth(api *API, name string, scheme *huma.SecurityScheme, authenticate Authenticator, permissions ...string) func(op *huma.Operation) {
	if name == "" || scheme == nil || authenticate == nil {
		panic("rest: RequireAuth requires a scheme name, a security scheme and an authenticator")
	}
	challenge := ""
	if scheme.Type == "http" && scheme.Scheme != "" {
		challenge = strings.ToUpper(scheme.Scheme[:1]) + scheme.Scheme[1:]
	}
	return requireAuth(api, name, []namedScheme{{name, scheme}}, challenge, nil, authenticate, permissions...)
}

// requireAuth 是 RequireAuth 和 RequireToken 的共同实现：schemes 是在 OpenAPI 中任选其一的携带方式，
// guard 不为 nil 时在认证之前检查请求，返回的错误原样呈现。
func requireAuth(api *API, name string, schemes []namedScheme, challenge string, guard func(huma.Context) error, authenticate Authenticator, permissions ...string) func(op *huma.Operation) {
	if len(api.accounts) == 0 {
		panic("rest: Options.Accounts is required for authenticated operations")
	}
	names := make([]string, len(schemes))
	security := make([]map[string][]string, len(schemes))
	for i, s := range schemes {
		registerScheme(api, s.name, s.scheme)
		names[i] = s.name
		security[i] = map[string][]string{s.name: {}}
	}
	required := append([]string(nil), permissions...)
	return func(op *huma.Operation) {
		if op.Metadata == nil {
			op.Metadata = map[string]any{}
		}
		if _, ok := op.Metadata[authMetadata]; ok {
			panic("rest: duplicate authentication declaration")
		}
		op.Metadata[authMetadata] = name
		op.Metadata[schemesMetadata] = names
		if len(required) > 0 {
			if op.Extensions == nil {
				op.Extensions = map[string]any{}
			}
			op.Extensions["x-required-permissions"] = required
		}
		op.Security = security
		op.Middlewares = append(op.Middlewares, func(ctx huma.Context, next func(huma.Context)) {
			ctx.SetHeader("Cache-Control", "no-store")
			if guard != nil {
				if err := guard(ctx); err != nil {
					writeError(api, ctx, problem(api, ctx, err))
					return
				}
			}
			// authenticate 读取请求头等与 context 无关的部分，复查时只替换 context。
			auth := func(c context.Context) (identity.Subject, error) {
				return authenticate(huma.WithContext(ctx, c))
			}
			meta := ctx.Operation().Metadata
			directOnly, _ := meta[directOnlyMetadata].(bool)
			accountScope := api.accountScope
			if v, ok := meta[accountScopeMetadata].(bool); ok {
				accountScope = v
			}
			policy := identity.Policy{
				Accounts:      api.accounts,
				Impersonation: api.impersonation,
				CheckTenant:   api.checkTenant,
				DirectOnly:    directOnly,
				AccountScope:  accountScope,
			}
			sub, err := identity.Establish(ctx.Context(), auth, policy, required...)
			switch {
			case err == nil:
			case identity.Failed(err):
				api.logger.ErrorContext(ctx.Context(), "authentication backend failed", "scheme", name, "error", err)
				logging.RecordError(trace.SpanFromContext(ctx.Context()), err)
				writeError(api, ctx, huma.NewErrorWithContext(ctx, httpx.StatusOf(err), "authentication unavailable"))
				return
			case errors.Is(err, identity.ErrUnauthenticated):
				if challenge != "" {
					ctx.SetHeader("WWW-Authenticate", challenge)
				}
				writeError(api, ctx, problem(api, ctx, err))
				return
			default:
				writeError(api, ctx, problem(api, ctx, err))
				return
			}
			if maxAge, ok := meta[confirmMetadata].(time.Duration); ok && !sub.Auth.ConfirmedWithin(maxAge) {
				writeError(api, ctx, problem(api, ctx, identity.ErrConfirmRequired.New()))
				return
			}
			// 长连接期间（例如 SSE）重新执行全部检查：凭据被撤销、主体或代为登录者变化、操作者失效、退出租户或失去权限时结束连接。
			recheck := func(c context.Context) error {
				_, err := identity.Reestablish(c, sub, auth, policy, required...)
				return err
			}
			reqCtx := identity.WithCredentialCheck(identity.WithSubject(ctx.Context(), sub), recheck)
			if sub.Impersonated() {
				reqCtx = logging.WithAttrs(reqCtx, slog.String(logging.AttrImpersonator, sub.Impersonator.String()))
				trace.SpanFromContext(reqCtx).SetAttributes(attribute.String("enduser.impersonator", sub.Impersonator.String()))
			}
			if api.watcher != nil {
				reqCtx = identity.WithCredentialWatcher(reqCtx, api.watcher)
			}
			next(huma.WithContext(ctx, reqCtx))
		})
	}
}

// DirectOnly 要求调用者为本人直接登录，拒绝所有代为登录的凭据（返回 403 auth.impersonation_forbidden）。
// 用于修改密码、修改个人资料、管理设备与个人令牌、付款、删除账号等不宜代为执行的操作。
// 与 RequireToken 等认证方案一起使用，先后不限；只有它而没有认证方案时 Register panic。
// OpenAPI 中记录为扩展字段 x-direct-only。
func DirectOnly() func(op *huma.Operation) {
	return func(op *huma.Operation) {
		if op.Metadata == nil {
			op.Metadata = map[string]any{}
		}
		op.Metadata[directOnlyMetadata] = true
		if op.Extensions == nil {
			op.Extensions = map[string]any{}
		}
		op.Extensions["x-direct-only"] = true
	}
}

// AccountScoped 将单条路由标记为账号自省接口（例如 /me、/auth/logout、/tenants）：放行绑定了租户的凭据
// 并保留 Subject.BoundTenant。整组 API 都是账号自省接口时，可以直接设置 rest.Options.AccountScope。
// 与 RequireToken 等认证方案一起使用，先后不限；只有它而没有认证方案时 Register panic。
func AccountScoped() func(op *huma.Operation) {
	return func(op *huma.Operation) {
		if op.Metadata == nil {
			op.Metadata = map[string]any{}
		}
		op.Metadata[accountScopeMetadata] = true
	}
}

// Confirmed 要求主体在 maxAge 之内验证过身份（identity.Auth.Time）：登录，或之后确认身份（apitoken 的 Confirm）。
// 用于修改邮箱、邀请成员、删除账号等敏感操作；超过时返回 403 auth.confirm_required，客户端让用户再次输入密码或验证码，
// 调用应用的确认接口后重试。个人访问令牌、OAuth 令牌和代为登录的 token 没有验证情况，总是返回 403。
//
// 与 RequireToken 等认证方案一起使用，先后不限；只有它而没有认证方案时 Register panic。
// OpenAPI 中记录为扩展字段 x-confirm-max-age（秒）。maxAge 不为正数时 panic。
func Confirmed(maxAge time.Duration) func(op *huma.Operation) {
	if maxAge <= 0 {
		panic("rest: Confirmed needs a positive maxAge")
	}
	return func(op *huma.Operation) {
		if op.Metadata == nil {
			op.Metadata = map[string]any{}
		}
		op.Metadata[confirmMetadata] = maxAge
		if op.Extensions == nil {
			op.Extensions = map[string]any{}
		}
		op.Extensions["x-confirm-max-age"] = int(maxAge / time.Second)
	}
}

// registerScheme 在 OpenAPI 组件中登记安全方案。
func registerScheme(api huma.API, name string, scheme *huma.SecurityScheme) {
	oapi := api.OpenAPI()
	if oapi.Components == nil {
		oapi.Components = &huma.Components{}
	}
	if oapi.Components.SecuritySchemes == nil {
		oapi.Components.SecuritySchemes = map[string]*huma.SecurityScheme{}
	}
	oapi.Components.SecuritySchemes[name] = scheme
}

// Public 显式标记端点为公开（无 Security）。
func Public() func(op *huma.Operation) {
	return func(op *huma.Operation) {
		op.Security = []map[string][]string{}
		if _, ok := op.Metadata[authMetadata]; ok {
			panic("rest: cannot mark an authenticated endpoint public")
		}
	}
}

// authScheme 返回 RequireAuth 或 RequireToken 为路由声明的方案名；声明缺失或 Security 被改动时返回空字符串。
func authScheme(op *huma.Operation) string {
	name, _ := op.Metadata[authMetadata].(string)
	names, _ := op.Metadata[schemesMetadata].([]string)
	if name == "" || len(op.Security) != len(names) {
		return ""
	}
	for i, s := range names {
		if _, ok := op.Security[i][s]; !ok || len(op.Security[i]) != 1 {
			return ""
		}
	}
	return name
}

// problem 转换中间件中的业务错误；api 为 *API 时按请求语言翻译 detail。
func problem(api huma.API, ctx huma.Context, err error) huma.StatusError {
	if a, ok := api.(*API); ok {
		return a.httpError(ctx.Context(), err)
	}
	return FromError(err)
}
