package note

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/ryan053857/go-kit/httpx/web"
	"github.com/ryan053857/go-kit/starter/views"
)

// NotesPage 是笔记列表页。直接打开返回整页；页面内“刷新”按钮用 hx-get 局部加载，只返回片段。
type NotesPage struct {
	Notes *Store
}

func (a *NotesPage) Mount(r chi.Router) {
	r.Method(http.MethodGet, "/", web.HandlerFunc(a.serve))
}

func (a *NotesPage) serve(w http.ResponseWriter, r *http.Request) error {
	notes, err := a.Notes.List(r.Context())
	if err != nil {
		return err
	}
	return web.Page(w, r, http.StatusOK, views.Layout("笔记"), notesContent(notes))
}
