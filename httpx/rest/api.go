package rest

import (
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"
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
}

// Options 创建 API。
type Options struct {
	Title   string
	Version string
	Mode    Mode
	// Transformers 追加到 Huma 配置；默认始终包含 StripErrorValues。
	Transformers []huma.Transformer
	// Router 可选；nil 时使用 chi.NewRouter()。
	Router chi.Router
}

// New 创建 chi + Huma API。
func New(opts Options) *API {
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
	cfg := huma.DefaultConfig(opts.Title, opts.Version)
	cfg.Transformers = append([]huma.Transformer{StripErrorValues}, opts.Transformers...)
	// 关闭内置 docs 路径可由调用方设置；默认保留。
	h := humachi.New(r, cfg)
	return &API{API: h, mode: opts.Mode, router: r}
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
