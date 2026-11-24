package mcpx

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"slices"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/go-tenon/tenon/httpx"
	"github.com/go-tenon/tenon/identity"
	"github.com/go-tenon/tenon/logging"
)

// HandlerOptions 是 Handler 的选项。
type HandlerOptions struct {
	Validate      identity.TokenValidator                        // 校验请求头 Authorization: Bearer 中的 token，必填。与 REST 用同一个校验器，例如 apitoken.Store 的 Validate。
	Accounts      identity.Accounts                              // 接受的账号体系和各自的 Resolver，与 rest.Options.Accounts 相同，必填。
	Impersonation *identity.Impersonation                        // 允许代为登录时对操作者的校验规则；nil 时默认拒绝所有代为登录凭据。
	DirectOnly    bool                                           // true 时只接受本人直接登录凭据，拒绝代为登录。
	Tenant        func(r *http.Request) string                   // 从请求中取出本次请求所在的租户，例如子域名或请求头；nil 表示平台本身（""）。凭据绑定了租户时，两者必须相同。
	CheckTenant   func(ctx context.Context, tenant string) error // 可选：校验本次请求所在租户仍然可用（例如未被停用）。
	Resource      *Resource                                      // 不为 nil 时端点是 OAuth 受保护资源：401 响应的 WWW-Authenticate 带上 resource_metadata，Resource.MetadataPath() 上提供元数据，只接受发给 Resource.URL 的令牌
	Permissions   []string                                       // 使用这个端点需要的权限，例如 mcp:use；为空时不额外要求。个人访问令牌的 Scopes 中也要有它，令牌才能用于 MCP。
}

// mcpHandler 包装 MCP HTTP 处理器并实现 httpx.RouteDescriber，让路由表能够审计访问策略与所需权限。
type mcpHandler struct {
	handler     http.Handler
	permissions []string
}

// ServeHTTP 实现 http.Handler。
func (h mcpHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) { h.handler.ServeHTTP(w, r) }

// DescribeRoute 实现 httpx.RouteDescriber。
func (h mcpHandler) DescribeRoute() httpx.Route {
	return httpx.Route{Access: "token", Permissions: slices.Clone(h.permissions), Summary: "MCP"}
}

// Handler 返回以 Streamable HTTP 提供 s 的处理器，挂在一个路由上，例如 router.Handle("/mcp", h)。
//
// 每个请求先按 identity.Establish 确立主体，规则与 rest.RequireToken 相同：没有或无效的 token 返回 401
// 并带 WWW-Authenticate: Bearer，凭据绑定了其他租户或缺少 Permissions 返回 403，认证后端故障返回 5xx。
// 主体写入 ctx，工具中用 identity.FromContext 取出。
//
// 服务是无状态的：每个请求独立处理，响应是 JSON，可以落在任意实例上。
// 因此通信总是由客户端发起，sampling、elicitation 和进度通知这类由服务端发起的消息随之关闭。
// 请求体大小、超时、限流、访问日志和链路沿用挂载它的路由上的中间件。
// Validate 为 nil、Accounts 为空或名称不合法时 panic。
func Handler(s *Server, opts HandlerOptions) http.Handler {
	switch {
	case s == nil:
		panic("mcpx: nil server")
	case opts.Validate == nil:
		panic("mcpx: HandlerOptions.Validate is required")
	case len(opts.Accounts) == 0:
		panic("mcpx: HandlerOptions.Accounts is required")
	}
	if err := opts.Accounts.Validate(); err != nil {
		panic(err)
	}
	if err := opts.Impersonation.Validate(); err != nil {
		panic(err)
	}
	challenge := "Bearer"
	if opts.Resource != nil {
		if err := opts.Resource.Validate(); err != nil {
			panic(err)
		}
		challenge = `Bearer resource_metadata="` + opts.Resource.MetadataURL() + `"`
	}
	sdk := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return s.sdk }, &mcp.StreamableHTTPOptions{
		Stateless:    true,
		JSONResponse: true,
		Logger:       s.sdkLogger(),
		// 防 DNS 重绑定的检查拒绝经本机地址到达、Host 却不是本机的请求，部署在反向代理之后时每个请求都会被拒绝。
		// 它保护的是没有认证、只监听本机的服务；这里每个请求都要带 Bearer token，已经足够。
		DisableLocalhostProtection: true,
	})
	permissions := append([]string(nil), opts.Permissions...)
	return mcpHandler{
		permissions: permissions,
		handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Cache-Control", "no-store")
			ctx := r.Context()
			if opts.Tenant != nil {
				ctx = identity.WithTenant(ctx, opts.Tenant(r))
			}
			token := httpx.BearerToken(r.Header.Get("Authorization"))
			authenticate := func(ctx context.Context) (identity.Subject, error) {
				if token == "" {
					return identity.Subject{}, nil
				}
				return opts.Validate(ctx, token)
			}
			policy := identity.Policy{Accounts: opts.Accounts, Impersonation: opts.Impersonation, CheckTenant: opts.CheckTenant, DirectOnly: opts.DirectOnly}
			sub, err := identity.Establish(ctx, authenticate, policy, permissions...)
			switch {
			case err == nil:
			case identity.Failed(err):
				s.logger.ErrorContext(ctx, "authentication backend failed", "error", err)
				httpx.WriteProblem(w, httpx.StatusOf(err), "authentication unavailable")
				return
			case errors.Is(err, identity.ErrUnauthenticated):
				w.Header().Set("WWW-Authenticate", challenge)
				httpx.WriteProblem(w, http.StatusUnauthorized, httpx.ErrorDetail(ctx, err))
				return
			default:
				httpx.WriteProblem(w, httpx.StatusOf(err), httpx.ErrorDetail(ctx, err))
				return
			}
			reqCtx := identity.WithSubject(ctx, sub)
			if sub.Impersonated() {
				reqCtx = logging.WithAttrs(reqCtx, slog.String(logging.AttrImpersonator, sub.Impersonator.String()))
				trace.SpanFromContext(reqCtx).SetAttributes(attribute.String("enduser.impersonator", sub.Impersonator.String()))
			}
			sdk.ServeHTTP(w, r.WithContext(reqCtx))
		}),
	}
}
