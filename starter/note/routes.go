package note

import (
	"github.com/go-chi/chi/v5"

	"github.com/ryan053857/go-kit/httpx/rest"
)

// APIActions 列出本模块的 REST action。
func APIActions(notes *Store) []rest.Action {
	return []rest.Action{
		&ListNotes{Notes: notes},
		&CreateNote{Notes: notes},
	}
}

// MountPages 注册本模块的页面，调用方负责挂载 auth.RequireLogin。
func MountPages(r chi.Router, notes *Store) {
	(&NotesPage{Notes: notes}).Mount(r)
	(&AddNote{Notes: notes}).Mount(r)
	(&NotePage{Notes: notes}).Mount(r)
}
