package article

import (
	"context"

	"github.com/danielgtaylor/huma/v2"

	"github.com/go-tenon/tenon/sheet"
)

type TemplateInput struct {
	Format sheet.Format `query:"format" enum:"csv,xlsx" default:"xlsx"`
}

// Template 处理 GET /articles/import/template，下载导入模板。
func (a *Import) Template(ctx context.Context, in *TemplateInput) (*huma.StreamResponse, error) {
	return download("articles-template", in.Format, func(w *sheet.Writer) error {
		return sheet.Template(w, Changes{Title: "第一篇文章", Slug: "hello-world", Content: "正文"})
	}), nil
}
