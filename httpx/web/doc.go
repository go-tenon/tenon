// Package web 提供用 templ + HTMX 编写服务端渲染页面时的少量胶水代码。
//
// 模板直接使用 github.com/a-h/templ，本包只补充：
//
//	HandlerFunc     返回 error 的 handler，错误统一交给 ErrorHandler 渲染
//	Render、Page    先渲染到缓冲区再输出；Page 按是否 HTMX 局部请求决定是否套用布局
//	Redirect、Trigger、IsHTMX、IsPartial   HTMX 响应头与请求判断
//	Assets          静态资源：按内容哈希生成带版本的 URL，并设置缓存头
//	Vendor          内置的 htmx.min.js（版本见 HTMXVersion）
//
// # 页面 action
//
// 一个页面一个 .go 文件加一个 .templ 文件：
//
//	// order/order_list_page.go
//	type OrderListPage struct{ Orders *Service }
//
//	func (a *OrderListPage) Mount(r chi.Router) {
//		r.Method(http.MethodGet, "/orders", web.HandlerFunc(a.serve))
//	}
//
//	func (a *OrderListPage) serve(w http.ResponseWriter, r *http.Request) error {
//		orders, err := a.Orders.List(r.Context())
//		if err != nil {
//			return err // 500，记录带调用栈的日志后交给 web.ErrorHandler
//		}
//		return web.Page(w, r, http.StatusOK, views.Layout("订单"), OrderList(orders))
//	}
//
// 可预期的错误直接返回 huma.Error404NotFound 等：任何实现了 GetStatus() int 的错误
// 都按其状态码处理，因此页面与 API 使用同一种写法。
//
// # 组装
//
//	// 错误页：显式替换默认实现（默认只输出状态码对应的纯文本）。
//	web.ErrorHandler = func(w http.ResponseWriter, r *http.Request, status int, err error) {
//		_ = web.Render(w, r, status, views.ErrorPage(status))
//	}
//
//	static, err := web.NewAssets("/static/", assets.FS) // 产品自己 go:embed 的目录
//	vendor, err := web.NewAssets("/vendor/", web.Vendor)
//	r.Handle("/static/*", static)
//	r.Handle("/vendor/*", vendor)
//
//	// 布局模板中：<script src={ vendor.URL("htmx.min.js") }></script>
//
// 表单解码与校验直接使用 github.com/go-playground/form 与 github.com/go-playground/validator。
package web
