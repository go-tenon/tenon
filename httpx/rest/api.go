package rest

import (
	"log/slog"
	"net/http"
	"reflect"

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
	watcher  httpx.CredentialWatcher
	names    *schemaNames

	endpoints []Endpoint
}

// Options 创建 API。
type Options struct {
	Title   string
	Version string
	// Logger 记录 5xx 错误；nil 表示 slog.Default()。
	Logger *slog.Logger
	// Transformers 追加在 Huma 默认 Transformer 之后。
	Transformers []huma.Transformer
	// BasePath 是 API 挂载的路径前缀，例如挂在 router.Route("/api", ...) 中时为 /api。
	// 它写入 OpenAPI 的 servers，文档页和客户端据此拼出完整地址；路由本身仍按传给 New 的路由注册。
	// 一个应用有多个 API 时，openapi、client 命令用 --base-path 按它选择。
	BasePath string
	// Tenant 从请求中取出本次请求所在的租户，例如路径参数 ctx.Param("tenant")、子域名或请求头；
	// 结果在每个操作（包括公开的操作）开始时写入 identity.WithTenant。nil 表示始终是平台本身（""）。
	Tenant func(ctx huma.Context) string
	// Accounts 是这个 API 接受的账号体系，以及每一种的 Resolver：认证通过后，按凭据给出的主体的 Type
	// 调用对应的 Resolver，得到本次请求的主体，例如按 identity.TenantFromContext 加载用户在租户中的权限。
	// Type 不在其中的凭据响应 401，与凭据无效相同；Resolver 为 nil 时直接使用凭据给出的主体。
	// Resolver 返回 errorx.Unauthenticated 类错误或 ID 为空的主体时响应 401，其他 4xx 类错误按其分类响应，
	// 例如不是租户成员时返回 errorx.NotFound 得到 404；5xx 类错误响应对应的状态码。
	// 返回的主体中 Type、ID、Method、Credential、Scopes、Impersonator 总是取自凭据。
	//
	// 注册需要认证的操作（RequireAuth、RequireBearer）时必须设置；名称不合法时 New panic。
	Accounts identity.Accounts
	// Credentials 让 RequireAuth 保护的长连接（例如 SSE）在收到复查信号时立即重新校验凭据，
	// 撤销 token、停用用户后连接随即结束；nil 时只按 30 秒的间隔定期校验。通常是 broadcast.NewCredentials 的返回值。
	Credentials httpx.CredentialWatcher
}

// New 在 r 上创建 chi + Huma API，并让 r 上未匹配的路由与方法返回 Problem Details。
// New 不安装任何中间件：在调用前用 r.Use 安装，例如 httpx.Defaults。
//
// 注册路由时只保存依赖、不调用依赖，openapi、routes、client 命令就能用零值的依赖构造同一套路由，
// 不启动服务、不连接数据库，见 command/httpcmd。用 APIs 或 Lookup 可以从构造好的路由中取回 API。
func New(r chi.Router, opts Options) *API {
	if r == nil {
		panic("rest: nil router")
	}
	if err := opts.Accounts.Validate(); err != nil {
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
	// schema 名带包名，不同包的同名类型（例如 article.Changes 和 comment.Changes）不会冲突，见 schemaNames。
	names := &schemaNames{anon: map[reflect.Type]string{}}
	cfg.OpenAPI.Components.Schemas = huma.NewMapRegistry("#/components/schemas/", names.name)
	// Huma 产生的错误由 localizeErrors 转为 ErrorModel，因此文档统一描述为 ErrorModel；
	// 不覆盖全局 huma.NewError，避免影响同一进程中的其他 Huma API。
	cfg.OpenAPI.Components.Schemas.RegisterTypeAlias(reflect.TypeOf(huma.ErrorModel{}), reflect.TypeOf(ErrorModel{}))
	cfg.OpenAPI.Components.Schemas.RegisterTypeAlias(reflect.TypeOf(huma.ErrorDetail{}), reflect.TypeOf(ErrorDetail{}))
	// localizeErrors 排在最前，应用的 Transformer 看到的已经是翻译后的 ErrorModel。
	cfg.Transformers = append([]huma.Transformer{localizeErrors}, opts.Transformers...)
	if opts.BasePath != "" {
		cfg.OpenAPI.Servers = []*huma.Server{{URL: opts.BasePath}}
	}
	adapter := newAdapter(r, cfg)
	api := &API{router: r, basePath: opts.BasePath, logger: opts.Logger, accounts: opts.Accounts, watcher: opts.Credentials, names: names}
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
