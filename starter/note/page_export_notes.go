package note

import (
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/go-chi/chi/v5"

	"github.com/ryan053857/go-kit/httpx/auth"
	"github.com/ryan053857/go-kit/httpx/web"
	"github.com/ryan053857/go-kit/modules/account"
	"github.com/ryan053857/go-kit/modules/export"
)

// notesTable 定义导出的列。
var notesTable = export.Table[Note]{
	Name: "笔记",
	Columns: []export.Column[Note]{
		{Header: "内容", Value: func(n Note) any { return n.Text }},
		{Header: "创建时间", Value: func(n Note) any { return n.CreatedAt }},
		{Header: "修改时间", Value: func(n Note) any { return n.UpdatedAt }},
	},
}

// ExportNotes 导出当前用户的全部笔记：/notes/export?format=csv|xlsx。
type ExportNotes struct {
	Notes *Store
}

func (a *ExportNotes) Mount(r chi.Router) {
	r.Method(http.MethodGet, "/notes/export", web.HandlerFunc(a.serve))
}

func (a *ExportNotes) serve(w http.ResponseWriter, r *http.Request) error {
	format, err := export.ParseFormat(r.URL.Query().Get("format"))
	if err != nil {
		return huma.Error400BadRequest("不支持的格式")
	}
	p, _ := auth.From[*account.Principal](r.Context())
	return export.Serve(w, format, notesTable, export.QueryRows[Note](r.Context(), a.Notes.query(p.ID)))
}
