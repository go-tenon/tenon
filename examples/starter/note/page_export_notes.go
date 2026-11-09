package note

import (
	"context"
	"io"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/go-chi/chi/v5"

	"github.com/ryan053857/go-kit/httpx/auth"
	"github.com/ryan053857/go-kit/httpx/web"
	"github.com/ryan053857/go-kit/modules/account"
	"github.com/ryan053857/go-kit/modules/export"
	"github.com/ryan053857/go-kit/examples/starter/views"
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

// ExportNotes 导出当前用户的全部笔记：
//
//	GET  /notes/export?format=csv|xlsx   同步导出，直接下载
//	POST /notes/export-jobs              异步导出（数据量大时），完成后站内通知用户下载
type ExportNotes struct {
	Notes *Store
	Jobs  *export.Jobs
}

func (a *ExportNotes) Mount(r chi.Router) {
	r.Method(http.MethodGet, "/notes/export", web.HandlerFunc(a.serve))
	r.Method(http.MethodPost, "/notes/export-jobs", web.HandlerFunc(a.start))
}

// RegisterExport 注册笔记的异步导出。
func RegisterExport(jobs *export.Jobs, notes *Store) {
	jobs.Register("notes", "笔记", func(ctx context.Context, job *export.Job, w io.Writer) error {
		return export.Write(w, job.Format, notesTable, export.QueryRows[Note](ctx, notes.query(job.UserID)))
	})
}

func (a *ExportNotes) start(w http.ResponseWriter, r *http.Request) error {
	format, err := export.ParseFormat(r.FormValue("format"))
	if err != nil {
		return huma.Error400BadRequest("不支持的格式")
	}
	p, _ := auth.From[*account.Principal](r.Context())
	if _, err := a.Jobs.Start(r.Context(), p.ID, "notes", format, nil); err != nil {
		return err
	}
	return views.Toast(w, r, "已开始导出，完成后会在通知中提供下载链接", false)
}

func (a *ExportNotes) serve(w http.ResponseWriter, r *http.Request) error {
	format, err := export.ParseFormat(r.URL.Query().Get("format"))
	if err != nil {
		return huma.Error400BadRequest("不支持的格式")
	}
	p, _ := auth.From[*account.Principal](r.Context())
	return export.Serve(w, format, notesTable, export.QueryRows[Note](r.Context(), a.Notes.query(p.ID)))
}
