package article

import (
	"context"
	"log/slog"
	"mime"
	"time"

	"github.com/cockroachdb/errors"
	"github.com/danielgtaylor/huma/v2"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/identity"
	"github.com/go-tenon/tenon/sheet"
)

// exportColumns 是可以导出的列。
var exportColumns = sheet.Columns[Article]{
	{Key: "title", Header: "标题", Value: func(a Article) any { return a.Title }},
	{Key: "slug", Header: "Slug", Value: func(a Article) any { return a.Slug }},
	{Key: "status", Header: "状态", Value: func(a Article) any { return a.Status }},
	{Key: "published_at", Header: "发布时间", Value: func(a Article) any { return a.PublishedAt }},
}

// Export 把文章列表导出为表格。
type Export struct {
	db *bun.DB
}

func NewExport(db *bun.DB) *Export {
	return &Export{db: db}
}

// query 返回 subject 能看到的文章，后台导出也用它统计行数。
func (a *Export) query(subject identity.Subject) *bun.SelectQuery {
	q := a.db.NewSelect().Model((*Article)(nil)).OrderExpr("created_at DESC")
	if !subject.Can("articles:read-drafts") {
		q = q.WhereGroup(" AND ", func(q *bun.SelectQuery) *bun.SelectQuery {
			return q.Where("status = ?", StatusPublished).WhereOr("author_id = ?", subject.ID)
		})
	}
	return q
}

// Handle 把 subject 能看到的文章按 cols 写入 w。
func (a *Export) Handle(ctx context.Context, subject identity.Subject, w sheet.RowWriter, cols sheet.Columns[Article]) error {
	rows, err := a.query(subject).Rows(ctx)
	if err != nil {
		return errors.Wrap(err, "export articles")
	}
	defer rows.Close()
	if err := w.Write(cols.Header()...); err != nil {
		return err
	}
	for rows.Next() {
		var item Article
		if err := a.db.ScanRow(ctx, rows, &item); err != nil {
			return errors.Wrap(err, "scan article")
		}
		if err := w.Write(cols.Row(item)...); err != nil {
			return err
		}
	}
	return errors.Wrap(rows.Err(), "export articles")
}

type ExportInput struct {
	Format  sheet.Format `query:"format" enum:"csv,xlsx" default:"xlsx"`
	Columns []string     `query:"columns" doc:"要导出的列，默认全部"`
}

// HTTP 处理 GET /articles/export。
func (a *Export) HTTP(ctx context.Context, in *ExportInput) (*huma.StreamResponse, error) {
	subject, _ := identity.FromContext(ctx)
	cols, err := exportColumns.Select(in.Columns)
	if err != nil {
		return nil, err
	}
	return download("articles", in.Format, func(w *sheet.Writer) error {
		return a.Handle(ctx, subject, w, cols)
	}), nil
}

// download 以附件形式返回表格，文件名是 name 加上日期和扩展名。
func download(name string, format sheet.Format, write func(w *sheet.Writer) error) *huma.StreamResponse {
	return &huma.StreamResponse{Body: func(hctx huma.Context) {
		filename := name + "-" + time.Now().Format("20060102") + "." + string(format)
		hctx.SetHeader("Content-Type", format.ContentType())
		hctx.SetHeader("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": filename}))
		w := sheet.NewWriter(hctx.BodyWriter(), format)
		err := errors.CombineErrors(write(w), w.Close())
		if err != nil {
			slog.ErrorContext(hctx.Context(), "export failed", "error", err)
		}
	}}
}
