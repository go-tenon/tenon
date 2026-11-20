package rest

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/cockroachdb/errors"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"

	"github.com/go-tenon/tenon/httpx"
)

// AccessToken 是 RequireToken 路由在路由表中的访问策略；RequireAuth 声明的其他方案取其方案名。
const AccessToken = "token"

// adapter 是 Huma 的 chi 适配器：每个操作注册为一个实现 httpx.RouteDescriber 的处理器，
// 让 httpx.Routes 能从任意上层 Router 读出访问策略。其余行为委托给 humachi。
type adapter struct {
	api    *API // 使用这个 adapter 的 API，New 在 huma.NewAPI 之前设置，APIs 据此从路由中取回它。
	router chi.Router
	chi    huma.Adapter
	docs   map[string]bool
	spec30 map[string]func(huma.Context) // 3.0 文档端点的路径和它们的处理器，代替 Huma 自带的处理器。

	template string         // 带参数的 BasePath，例如 /t/{slug}/api；BasePath 不带参数时为空
	prefix   *regexp.Regexp // 匹配 template 的实际路径前缀
}

// newAdapter 创建在 chi 路由 r 上注册操作的适配器。
func newAdapter(r chi.Router, cfg huma.Config, basePath string) *adapter {
	a := &adapter{router: r, chi: humachi.NewAdapter(r), docs: docRoutes(cfg)}
	if strings.Contains(basePath, "{") {
		a.template = basePath
		a.prefix = regexp.MustCompile("^" + pathParam.ReplaceAllString(regexp.QuoteMeta(basePath), "[^/]+") + "$")
	}
	if cfg.OpenAPIPath != "" {
		doc := cfg.OpenAPI
		a.spec30 = map[string]func(huma.Context){
			cfg.OpenAPIPath + "-3.0.json": spec30("application/openapi+json", func() ([]byte, error) { return without30(doc).Downgrade() }),
			cfg.OpenAPIPath + "-3.0.yaml": spec30("application/openapi+yaml", func() ([]byte, error) { return without30(doc).DowngradeYAML() }),
		}
	}
	return a
}

// Handle 把操作 op 注册为实现 httpx.RouteDescriber 的路由，3.0 文档端点换用转换后的处理器。
func (a *adapter) Handle(op *huma.Operation, handler func(huma.Context)) {
	if h, ok := a.spec30[op.Path]; ok && op.Method == http.MethodGet {
		handler = h
	}
	if a.template != "" && a.docs[op.Path] {
		handler = a.concrete(op.Path, handler)
	}
	a.router.Method(op.Method, op.Path, &route{op: op, handler: handler, docs: a.docs[op.Path], adapter: a})
}

// spec30 返回 3.0 文档端点的处理器。Huma 自带的处理器转换时保留 webhooks，而 3.0 没有这一项。
// 每次请求时转换，文档总是最新的。
func spec30(contentType string, render func() ([]byte, error)) func(huma.Context) {
	return func(ctx huma.Context) {
		data, err := render()
		if err != nil {
			ctx.SetStatus(http.StatusInternalServerError)
			return
		}
		ctx.SetHeader("Content-Type", contentType)
		_, _ = ctx.BodyWriter().Write(data)
	}
}

// pathParam 匹配路径模板中的一个参数，例如 {slug}。
var pathParam = regexp.MustCompile(`\\\{[^/]*?\\\}`)

// concrete 包装带参数的 BasePath（例如 /t/{slug}/api）下的文档端点：响应中的路径模板换成这次请求实际的前缀
// （例如 /t/demo/api），文档页读取的文档地址和“发送请求”的地址因此都指向当前的租户。
func (a *adapter) concrete(path string, handler func(huma.Context)) func(huma.Context) {
	return func(ctx huma.Context) {
		u := ctx.URL()
		prefix := strings.TrimSuffix(u.EscapedPath(), path)
		if !a.prefix.MatchString(prefix) {
			handler(ctx)
			return
		}
		buf := &bufferedContext{humaContext: ctx}
		handler(buf)
		_, _ = ctx.BodyWriter().Write(bytes.ReplaceAll(buf.body.Bytes(), []byte(a.template), []byte(prefix)))
	}
}

// humaContext 是 huma.Context 的别名：嵌入时字段名与 huma.Context 的 Context 方法不冲突。
type humaContext = huma.Context

// bufferedContext 把处理器写出的响应体先留在内存中，其余操作交给原来的 huma.Context。
type bufferedContext struct {
	humaContext
	body bytes.Buffer
}

// BodyWriter 返回内存中的响应体。
func (c *bufferedContext) BodyWriter() io.Writer { return &c.body }

// ServeHTTP 交给底层的 chi 路由处理请求。
func (a *adapter) ServeHTTP(w http.ResponseWriter, r *http.Request) { a.router.ServeHTTP(w, r) }

// SanitizeInternalContext 委托给 humachi，供 Huma 的 autopatch 重新分发内部请求。
func (a *adapter) SanitizeInternalContext(ctx context.Context) context.Context {
	if s, ok := a.chi.(interface {
		SanitizeInternalContext(context.Context) context.Context
	}); ok {
		return s.SanitizeInternalContext(ctx)
	}
	return ctx
}

// route 是一个 Huma 操作的处理器。
type route struct {
	op      *huma.Operation
	handler func(huma.Context)
	docs    bool
	adapter *adapter
}

// ServeHTTP 用 Huma 的 context 调用操作的处理器。
func (rt *route) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	rt.handler(humachi.NewContext(rt.op, r, w))
}

// DescribeRoute 实现 httpx.RouteDescriber。文档端点为 docs；隐藏或未声明安全要求的操作为 unknown。
func (rt *route) DescribeRoute() httpx.Route {
	if rt.docs && rt.op.OperationID == "" {
		return httpx.Route{Access: httpx.AccessDocs}
	}
	access, permissions := routeAccess(rt.op)
	return httpx.Route{Access: access, Permissions: permissions, OperationID: rt.op.OperationID, Summary: rt.op.Summary}
}

// routeAccess 返回操作 op 在路由表中的访问策略和需要的权限。
func routeAccess(op *huma.Operation) (string, []string) {
	if op.Security == nil {
		return httpx.AccessUnknown, nil
	}
	if len(op.Security) == 0 {
		return httpx.AccessPublic, nil
	}
	if name := authScheme(op); name != "" {
		permissions, _ := op.Extensions["x-required-permissions"].([]string)
		return name, slices.Clone(permissions)
	}
	return httpx.AccessUnknown, nil
}

// docRoutes 按 Huma 的注册规则列出文档端点路径。
func docRoutes(cfg huma.Config) map[string]bool {
	paths := map[string]bool{}
	if cfg.OpenAPIPath != "" {
		for _, suffix := range []string{".json", ".yaml", "-3.0.json", "-3.0.yaml"} {
			paths[cfg.OpenAPIPath+suffix] = true
		}
	}
	if cfg.DocsPath != "" {
		paths[cfg.DocsPath] = true
	}
	if cfg.SchemasPath != "" {
		paths[cfg.SchemasPath+"/{schema}"] = true
	}
	return paths
}

// APIs 遍历 router（包括挂载的子路由），按首次出现的顺序返回其中用 New 创建的全部 API。
// 只读取路由表。找得到的是注册了路由或开启了文档端点的 API。
func APIs(router chi.Routes) []*API {
	var apis []*API
	// Walk 只会返回回调产生的错误；这里的回调始终成功。
	_ = chi.Walk(router, func(_, _ string, handler http.Handler, _ ...func(http.Handler) http.Handler) error {
		if rt, ok := handler.(*route); ok && !slices.Contains(apis, rt.adapter.api) {
			apis = append(apis, rt.adapter.api)
		}
		return nil
	})
	return apis
}

// Lookup 返回 router 中 BasePath 为 basePath 的 API。basePath 为空时 router 中必须恰好有一个 API。
func Lookup(router chi.Routes, basePath string) (*API, error) {
	apis := APIs(router)
	if basePath == "" {
		switch len(apis) {
		case 1:
			return apis[0], nil
		case 0:
			return nil, errors.New("rest: no API registered in the router")
		}
		return nil, errors.Newf("rest: %d APIs registered in the router; choose one by base path: %s", len(apis), basePaths(apis))
	}
	for _, api := range apis {
		if api.basePath == basePath {
			return api, nil
		}
	}
	return nil, errors.Newf("rest: no API with base path %q; registered: %s", basePath, basePaths(apis))
}

// basePaths 返回 apis 的路径前缀，用引号括起来以逗号分隔，用于错误消息。
func basePaths(apis []*API) string {
	paths := make([]string, len(apis))
	for i, api := range apis {
		paths[i] = strconv.Quote(api.basePath)
	}
	return strings.Join(paths, ", ")
}
