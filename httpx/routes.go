package httpx

import (
	"cmp"
	"net/http"
	"slices"

	"github.com/go-chi/chi/v5"
)

// Route 描述一条已注册的路由。
type Route struct {
	// Method 是 HTTP 方法；同一路径上所有方法都由同样描述的处理器响应时（例如 router.Handle、Mount）合并为一条，Method 为 AnyMethod。
	Method string
	Path   string
	// Access 是路由声明的访问策略：AccessPublic、AccessDocs、AccessUnknown，
	// 或认证方式的名字，例如 JSON API 的 bearer、页面的 session。
	// 没有可识别声明的路由一律为 AccessUnknown，它不表示允许匿名访问。
	Access string
	// Permissions 是需要认证的路由要求调用者能执行的全部权限。
	Permissions []string
	// OperationID、Summary 来自 JSON API 的 huma.Operation，页面路由没有。
	OperationID string
	Summary     string
}

// Route.Access 的通用取值。
const (
	AccessPublic  = "public"  // 显式声明为公开
	AccessDocs    = "docs"    // Huma 自动注册的文档端点，例如 /docs、/openapi.json
	AccessUnknown = "unknown" // 无法从声明确定策略，不表示允许匿名访问
)

// RouteDescriber 由路由的处理器实现，向 Routes 报告访问策略等信息。
// httpx/rest 注册的 API 路由、web 注册的页面路由和 web.Assets 都实现了它。
type RouteDescriber interface {
	// DescribeRoute 返回除 Method、Path 以外的字段。
	DescribeRoute() Route
}

// AnyMethod 是 Route.Method 的取值，表示这条路由响应任意 HTTP 方法。
const AnyMethod = "*"

// anyMethods 是 chi 的 Handle 和 Mount 注册的全部方法。
var anyMethods = []string{
	http.MethodConnect, http.MethodDelete, http.MethodGet, http.MethodHead, http.MethodOptions,
	http.MethodPatch, http.MethodPost, http.MethodPut, "QUERY", http.MethodTrace,
}

// Routes 遍历 router（包括挂载的子路由），返回按路径、方法排序的路由表。
// 处理器实现 RouteDescriber 时使用它的描述，否则 Access 为 AccessUnknown。
// 不调用任何处理器；挂载的不透明 http.Handler 显示为一条 /prefix/* 路由，方法为 AnyMethod。
func Routes(router chi.Routes) []Route {
	var routes []Route
	// Walk 只会返回回调产生的错误；这里的回调始终成功。
	_ = chi.Walk(router, func(method, path string, handler http.Handler, _ ...func(http.Handler) http.Handler) error {
		route := Route{Access: AccessUnknown}
		if d, ok := handler.(RouteDescriber); ok {
			route = d.DescribeRoute()
			route.Permissions = slices.Clone(route.Permissions)
		}
		route.Method, route.Path = method, path
		routes = append(routes, route)
		return nil
	})
	routes = collapseAnyMethod(routes)
	slices.SortFunc(routes, func(a, b Route) int {
		return cmp.Or(cmp.Compare(a.Path, b.Path), cmp.Compare(a.Method, b.Method))
	})
	return routes
}

// collapseAnyMethod 把同一路径上覆盖 anyMethods、描述相同的路由合并为一条 AnyMethod 路由。
func collapseAnyMethod(routes []Route) []Route {
	byPath := map[string][]Route{}
	for _, r := range routes {
		byPath[r.Path] = append(byPath[r.Path], r)
	}
	collapsed := map[string]bool{}
	for path, rs := range byPath {
		if len(rs) != len(anyMethods) {
			continue
		}
		same := true
		for _, r := range rs {
			if !slices.Contains(anyMethods, r.Method) || !sameDescription(r, rs[0]) {
				same = false
				break
			}
		}
		collapsed[path] = same
	}
	out := routes[:0:0]
	for _, r := range routes {
		switch {
		case !collapsed[r.Path]:
			out = append(out, r)
		case r.Method == anyMethods[0]:
			r.Method = AnyMethod
			out = append(out, r)
		}
	}
	return out
}

func sameDescription(a, b Route) bool {
	return a.Access == b.Access && a.OperationID == b.OperationID && a.Summary == b.Summary && slices.Equal(a.Permissions, b.Permissions)
}
