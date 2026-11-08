package note

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/shellphy-kit/tools/examples/service/views"
	"github.com/shellphy-kit/tools/httpx/web"
)

// NotesPage 是笔记列表页。直接打开返回整页；页面内“刷新”按钮用 hx-get 局部加载，只返回片段。
type NotesPage struct {
	Notes *Store
}

func (a *NotesPage) Mount(r chi.Router) {
	r.Method(http.MethodGet, "/", web.HandlerFunc(a.serve))
}

func (a *NotesPage) serve(w http.ResponseWriter, r *http.Request) error {
	return web.Page(w, r, http.StatusOK, views.Layout("笔记"), notesContent(a.Notes.List(r.Context())))
}
