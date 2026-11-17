package rest

import (
	"log/slog"
	"net/http"
	"reflect"
	"slices"
	"strings"

	"github.com/cockroachdb/errors"
	"github.com/danielgtaylor/huma/v2"
	"github.com/go-chi/chi/v5"

	"github.com/go-tenon/tenon/httpx"
	"github.com/go-tenon/tenon/identity"
	"github.com/go-tenon/tenon/logging"
)

// API 包装 huma.API，携带实例级策略。
type API struct {
	huma.API
	router   chi.Router
	basePath string
	logger   *slog.Logger
	accounts identity.Accounts
	session  httpx.SessionCookie
	watcher  identity.CredentialWatcher
	names    *schemaNames

	endpoints []Endpoint
	resources []Resource
	settings  []Settings
}

// Options 创建 API。
type Options struct {
	Title         string
	Version       string
	Logger        *slog.Logger                  // 记录 5xx 错误；nil 表示 slog.Default()。
	Transformers  []huma.Transformer            // 追加在 Huma 默认 Transformer 之后。
	BasePath      string                        // API 挂载的路径前缀，例如挂在 router.Route("/api", ...) 中时为 /api。它写入 OpenAPI 的 servers，文档页和客户端据此拼出完整地址；路由本身仍按传给 New 的路由注册。一个应用有多个 API 时，openapi、client 命令用 --base-path 按它选择。
	Tenant        func(ctx huma.Context) string // 从请求中取出本次请求所在的租户，例如路径参数 ctx.Param("tenant")、子域名或请求头；结果在每个操作（包括公开的操作）开始时写入 identity.WithTenant。nil 表示始终是平台本身（""）。
	Accounts      identity.Accounts             // 接受的账号体系和各自的 Resolver：认证通过后按主体的 Type 调用 Resolver，得到本次请求的主体，例如加载用户在租户中的权限；Type 不在其中时响应 401，Resolver 为 nil 时直接使用凭据的主体，Resolver 的错误按分类响应（Unauthenticated 类或 ID 为空时 401）。Type、ID、Method、Credential、Scopes、Impersonator 总是取自凭据。有需要认证的操作时必须设置
	SessionCookie httpx.SessionCookie           // RequireToken 读取的浏览器会话 cookie，零值是 httpx.DefaultSessionCookieName。同一个源下的几个 API 属于不同的账号体系时，各用一个名称，见 httpx.SessionCookie。名称不合法时 New panic。
	Credentials   identity.CredentialWatcher    // 让 RequireAuth 保护的长连接（例如 SSE）在收到复查信号时立即重新校验凭据，撤销 token、停用用户后连接随即结束；nil 时只按 30 秒的间隔定期校验。通常是 broadcast.NewCredentials 的返回值。
	Permissions   []identity.Permission         // 应用的权限目录，例如 authz.Catalog。它写入 OpenAPI 文档的 x-tenon-permissions（按名称排序），客户端据此生成权限常量和说明；路由需要的权限另外写在每个操作的 x-required-permissions 中。nil 时不写。
}

// New 在 r 上创建 chi + Huma API，并让 r 上未匹配的路由与方法返回 Problem Details。
// New 不安装任何中间件：在调用前用 r.Use 安装，例如 httpx.Defaults。
//
// 注册路由时只保存依赖，处理请求时才使用，openapi、routes、client 命令就能用零值的依赖离线构造同一套路由，
// 见 command/httpcmd。用 APIs 或 Lookup 可以从构造好的路由中取回 API。
func New(r chi.Router, opts Options) *API {
	if r == nil {
		panic("rest: nil router")
	}
	if err := opts.Accounts.Validate(); err != nil {
		panic(errors.Wrap(err, "rest"))
	}
	if err := opts.SessionCookie.Validate(); err != nil {
		panic(errors.Wrap(err, "rest"))
	}
	opts.Logger = logging.Or(opts.Logger).With(logging.AttrComponent, "http")
	if opts.Title == "" {
		opts.Title = "API"
	}
	if opts.Version == "" {
		opts.Version = "0.0.0"
	}
	r.NotFound(func(w http.ResponseWriter, _ *http.Request) {
		httpx.WriteProblem(w, http.StatusNotFound, "route not found")
	})
	r.MethodNotAllowed(func(w http.ResponseWriter, _ *http.Request) {
		httpx.WriteProblem(w, http.StatusMethodNotAllowed, "method not allowed")
	})
	cfg := huma.DefaultConfig(opts.Title, opts.Version)
	// 默认的唯一创建钩子会在响应体加入 $schema 字段并设置 Link 响应头，对 API 使用者价值不大，不启用。
	cfg.CreateHooks = nil
	// schema 名带包名，不同包的同名类型（例如 article.Changes 和 comment.Changes）各有自己的名字，见 schemaNames。
	names := &schemaNames{anon: map[reflect.Type]string{}}
	cfg.OpenAPI.Components.Schemas = huma.NewMapRegistry("#/components/schemas/", names.name)
	// Huma 产生的错误由 localizeErrors 转为 ErrorModel，因此文档统一描述为 ErrorModel；
	// 全局的 huma.NewError 保持原样，同一进程中的其他 Huma API 照常工作。
	cfg.OpenAPI.Components.Schemas.RegisterTypeAlias(reflect.TypeOf(huma.ErrorModel{}), reflect.TypeOf(ErrorModel{}))
	cfg.OpenAPI.Components.Schemas.RegisterTypeAlias(reflect.TypeOf(huma.ErrorDetail{}), reflect.TypeOf(ErrorDetail{}))
	// localizeErrors 排在最前，应用的 Transformer 看到的已经是翻译后的 ErrorModel。
	cfg.Transformers = append([]huma.Transformer{localizeErrors}, opts.Transformers...)
	if len(opts.Permissions) > 0 {
		perms := slices.SortedFunc(slices.Values(opts.Permissions), func(a, b identity.Permission) int { return strings.Compare(a.Name, b.Name) })
		if cfg.OpenAPI.Extensions == nil {
			cfg.OpenAPI.Extensions = map[string]any{}
		}
		cfg.OpenAPI.Extensions["x-tenon-permissions"] = perms
	}
	if opts.BasePath != "" {
		cfg.OpenAPI.Servers = []*huma.Server{{URL: opts.BasePath}}
	}
	adapter := newAdapter(r, cfg)
	api := &API{router: r, basePath: opts.BasePath, logger: opts.Logger, accounts: opts.Accounts, session: opts.SessionCookie, watcher: opts.Credentials, names: names}
	// Huma 在 NewAPI 中就注册文档端点，因此先让 adapter 指向 api，文档端点也能取回它。
	adapter.api = api
	api.API = huma.NewAPI(cfg, adapter)
	if tenant := opts.Tenant; tenant != nil {
		api.UseMiddleware(func(ctx huma.Context, next func(huma.Context)) {
			next(huma.WithContext(ctx, identity.WithTenant(ctx.Context(), tenant(ctx))))
		})
	}
	return api
}

// BasePath 返回 Options.BasePath。
func (a *API) BasePath() string { return a.basePath }

// Router 返回底层 chi 路由，可作为 http.Handler。
func (a *API) Router() chi.Router { return a.router }

// Handler 返回 HTTP 处理器。
func (a *API) Handler() http.Handler { return a.router }

// OpenAPIJSON 返回当前 OpenAPI 文档的 JSON。
func (a *API) OpenAPIJSON() ([]byte, error) {
	return a.OpenAPI().MarshalJSON()
}

// OpenAPI30JSON 返回转换为 OpenAPI 3.0.3 的文档，供只支持 3.0 的客户端生成器使用。
// 3.0 没有 webhooks，转换时去掉；文档端点 /openapi-3.0.json 和 /openapi-3.0.yaml 也是这样。
func (a *API) OpenAPI30JSON() ([]byte, error) {
	return without30(a.OpenAPI()).Downgrade()
}

// without30 返回去掉 3.0 不支持的 webhooks 的浅拷贝，原文档不变。
func without30(doc *huma.OpenAPI) *huma.OpenAPI {
	d := *doc
	d.Webhooks = nil
	return &d
}
