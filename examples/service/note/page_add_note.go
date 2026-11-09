package note

import (
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/ryan053857/go-kit/examples/service/user"
	"github.com/ryan053857/go-kit/httpx/auth"
	"github.com/ryan053857/go-kit/httpx/web"
)

// AddNote 处理列表页上的 HTMX 表单：成功时返回新的 <li> 片段，由 htmx 追加到列表末尾。
//
// 表单较复杂时，用 github.com/go-playground/form 解码、github.com/go-playground/validator 校验。
type AddNote struct {
	Notes *Store
}

func (a *AddNote) Mount(r chi.Router) {
	r.Method(http.MethodPost, "/notes", web.HandlerFunc(a.serve))
}

func (a *AddNote) serve(w http.ResponseWriter, r *http.Request) error {
	u, _ := auth.From[*user.User](r.Context()) // auth.RequireLogin 已保证已登录
	n, err := a.Notes.Create(r.Context(), u.Name, r.FormValue("text"))
	if errors.Is(err, ErrEmpty) {
		// 校验失败：不替换页面内容，通过 HX-Trigger 让页面弹出提示（见 views.Layout 中的 hx-on:notify）。
		if err := web.Trigger(w, "notify", map[string]string{"message": "笔记内容不能为空"}); err != nil {
			return err
		}
		w.WriteHeader(http.StatusNoContent)
		return nil
	}
	if err != nil {
		return err
	}
	return web.Render(w, r, http.StatusOK, noteItem(n))
}
