package rest

import (
	"log/slog"
	"net/http"
	"reflect"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"
	goi18n "github.com/nicksnyder/go-i18n/v2/i18n"

	"github.com/ryan053857/mygoravel/httpx"
	"github.com/ryan053857/mygoravel/i18n"
	"github.com/ryan053857/mygoravel/logging"
)

// Mode 控制登记行为。
type Mode int

const (
	// ModeServe 为运行模式：调用 newHandler 并挂载处理器。
	ModeServe Mode = iota
	// ModeOpenAPI 为文档模式：只生成 schema，绝不调用 newHandler。
	ModeOpenAPI
)

// API 包装 huma.API，携带模式与实例级策略。
type API struct {
	huma.API
	mode   Mode
	router chi.Router
	logger *slog.Logger
}

// Options 创建 API。
type Options struct {
	Middleware httpx.MiddlewareOptions
	Title      string
	Version    string
	Mode       Mode
	// Transformers 追加到 Huma 配置；默认始终包含 StripErrorValues。
	Transformers []huma.Transformer
	// Router 可选；nil 时使用 chi.NewRouter()。
	Router chi.Router
}

// New 创建 chi + Huma API。
func New(opts Options) *API {
	if opts.Middleware.Logger != nil {
		opts.Middleware.Logger = slog.New(logging.NewContextHandler(opts.Middleware.Logger.Handler()))
	}
	if opts.Title == "" {
		opts.Title = "API"
	}
	if opts.Version == "" {
		opts.Version = "0.0.0"
	}
	r := opts.Router
	if r == nil {
		r = chi.NewRouter()
	}
	r.Use(httpx.Middleware(opts.Middleware))
	r.NotFound(func(w http.ResponseWriter, r *http.Request) {
		httpx.WriteProblem(w, 404, "error.not_found", httpx.RequestID(r.Context()))
	})
	r.MethodNotAllowed(func(w http.ResponseWriter, r *http.Request) {
		httpx.WriteProblem(w, 405, "error.method_not_allowed", httpx.RequestID(r.Context()))
	})
	cfg := huma.DefaultConfig(opts.Title, opts.Version)
	cfg.OpenAPI.Components.Schemas.RegisterTypeAlias(reflect.TypeOf(huma.ErrorModel{}), reflect.TypeOf(ErrorModel{}))
	cfg.Transformers = append([]huma.Transformer{StripErrorValues, func(ctx huma.Context, status string, v any) (any, error) {
		if e, ok := v.(*ErrorModel); ok && opts.Middleware.Languages != nil {
			if msg, err := goi18n.NewLocalizer(opts.Middleware.Languages, i18n.Locale(ctx.Context())).Localize(&goi18n.LocalizeConfig{MessageID: e.Code}); err == nil {
				e.Detail = msg
			} else {
				e.Detail = http.StatusText(e.Status)
				if e.Status >= 500 {
					e.Detail = "An unexpected error occurred"
				}
				if opts.Middleware.Logger != nil {
					opts.Middleware.Logger.WarnContext(ctx.Context(), "missing error translation", "code", e.Code)
				}
			}
		}
		return v, nil
	}}, opts.Transformers...)
	// 关闭内置 docs 路径可由调用方设置；默认保留。
	h := humachi.New(r, cfg)
	return &API{API: h, mode: opts.Mode, router: r, logger: opts.Middleware.Logger}
}

// Mode 返回当前模式。
func (a *API) Mode() Mode { return a.mode }

// Router 返回底层 chi 路由，可作为 http.Handler。
func (a *API) Router() chi.Router { return a.router }

// Handler 返回 HTTP 处理器。
func (a *API) Handler() http.Handler { return a.router }

// OpenAPIJSON 返回当前 OpenAPI 文档的 JSON。
func (a *API) OpenAPIJSON() ([]byte, error) {
	return a.OpenAPI().MarshalJSON()
}
