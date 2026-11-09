package note

import (
	"errors"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/go-chi/chi/v5"

	"github.com/ryan053857/go-kit/httpx/auth"
	"github.com/ryan053857/go-kit/httpx/web"
	"github.com/ryan053857/go-kit/modules/account"
	"github.com/ryan053857/go-kit/starter/views"
)

// NotePage 是笔记详情页，演示页面中的错误写法与 API 相同：返回 huma.Error404NotFound。
type NotePage struct {
	Notes *Store
	View  *views.Renderer
}

func (a *NotePage) Mount(r chi.Router) {
	r.Method(http.MethodGet, "/notes/{id}", web.HandlerFunc(a.serve))
}

func (a *NotePage) serve(w http.ResponseWriter, r *http.Request) error {
	p, _ := auth.From[*account.Principal](r.Context())
	n, err := a.Notes.Get(r.Context(), p.ID, chi.URLParam(r, "id"))
	if errors.Is(err, ErrNotFound) {
		return huma.Error404NotFound("笔记不存在")
	}
	if err != nil {
		return err
	}
	return a.View.Page(w, r, http.StatusOK, "笔记", noteDetail(n))
}
