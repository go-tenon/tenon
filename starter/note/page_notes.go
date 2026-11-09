package note

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/ryan053857/go-kit/httpx/auth"
	"github.com/ryan053857/go-kit/httpx/web"
	"github.com/ryan053857/go-kit/modules/account"
	"github.com/ryan053857/go-kit/starter/views"
)

// NotesPage 是笔记列表页（首页）。
type NotesPage struct {
	Notes *Store
	View  *views.Renderer
}

func (a *NotesPage) Mount(r chi.Router) {
	r.Method(http.MethodGet, "/", web.HandlerFunc(a.serve))
}

func (a *NotesPage) serve(w http.ResponseWriter, r *http.Request) error {
	p, _ := auth.From[*account.Principal](r.Context())
	notes, err := a.Notes.List(r.Context(), p.ID)
	if err != nil {
		return err
	}
	return a.View.Page(w, r, http.StatusOK, "笔记", notesContent(notes))
}
