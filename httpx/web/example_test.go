package web_test

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"

	"github.com/a-h/templ"
	"github.com/danielgtaylor/huma/v2"
	"github.com/go-chi/chi/v5"

	"github.com/shellphy-kit/tools/httpx/web"
)

// 实际项目中 Layout 与 OrderView 由 .templ 文件生成，这里用 ComponentFunc 代替。
func Layout(title string) templ.Component {
	return templ.ComponentFunc(func(ctx context.Context, w io.Writer) error {
		_, _ = fmt.Fprintf(w, "<title>%s</title><main>", title)
		_ = templ.GetChildren(ctx).Render(ctx, w)
		_, err := io.WriteString(w, "</main>")
		return err
	})
}

func OrderView(id string) templ.Component {
	return templ.ComponentFunc(func(_ context.Context, w io.Writer) error {
		_, err := fmt.Fprintf(w, "<h1>订单 %s</h1>", id)
		return err
	})
}

// 页面 action：通常放在 order/order_page.go，与 order_page.templ 配对。
type OrderPage struct{}

func (a *OrderPage) Mount(r chi.Router) {
	r.Method(http.MethodGet, "/orders/{id}", web.HandlerFunc(a.serve))
}

func (a *OrderPage) serve(w http.ResponseWriter, r *http.Request) error {
	id := chi.URLParam(r, "id")
	if id == "0" {
		return huma.Error404NotFound("订单不存在")
	}
	return web.Page(w, r, http.StatusOK, Layout("订单"), OrderView(id))
}

func Example() {
	r := chi.NewRouter()
	(&OrderPage{}).Mount(r)

	for _, htmx := range []bool{false, true} {
		req := httptest.NewRequest("GET", "/orders/42", nil)
		if htmx {
			req.Header.Set("HX-Request", "true")
		}
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		fmt.Println(rec.Body.String())
	}

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("GET", "/orders/0", nil))
	fmt.Println(rec.Code)

	// Output:
	// <title>订单</title><main><h1>订单 42</h1></main>
	// <h1>订单 42</h1>
	// 404
}
