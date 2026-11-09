package note

import (
	"net/http"

	"github.com/cockroachdb/errors"

	"github.com/go-chi/chi/v5"

	"github.com/ryan053857/go-kit/httpx/auth"
	"github.com/ryan053857/go-kit/httpx/web"
	"github.com/ryan053857/go-kit/modules/account"
	"github.com/ryan053857/go-kit/examples/starter/views"
)

// AddNote 处理列表页上的 HTMX 表单：成功时返回新的列表项片段，由 htmx 插入到列表开头。
//
// 表单较复杂时，用 github.com/go-playground/form 解码、github.com/go-playground/validator 校验。
type AddNote struct {
	Notes *Store
}

func (a *AddNote) Mount(r chi.Router) {
	r.Method(http.MethodPost, "/notes", web.HandlerFunc(a.serve))
}

func (a *AddNote) serve(w http.ResponseWriter, r *http.Request) error {
	p, _ := auth.From[*account.Principal](r.Context()) // auth.RequireLogin 已保证已登录
	n, err := a.Notes.Create(r.Context(), p.ID, r.FormValue("text"))
	if errors.Is(err, ErrEmpty) {
		// 校验失败：不插入列表项，只弹出提示。
		return views.Toast(w, r, "笔记内容不能为空", true)
	}
	if err != nil {
		return err
	}
	return web.Render(w, r, http.StatusOK, noteItem(n))
}
