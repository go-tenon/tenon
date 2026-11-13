package article

import (
	"context"

	"github.com/cockroachdb/errors"
	"github.com/danielgtaylor/huma/v2"

	"github.com/go-tenon/tenon/i18n"
	"github.com/go-tenon/tenon/identity"
	"github.com/go-tenon/tenon/sheet"
)

// Import 从表格批量创建文章。
type Import struct {
	create *Create
}

func NewImport(create *Create) *Import {
	return &Import{create: create}
}

// ImportResult 是导入的结果：创建了多少篇文章，以及没有导入的行。
type ImportResult struct {
	Created  int             `json:"created"`
	Failures []ImportFailure `json:"failures"`
}

// ImportFailure 是没有导入的一行，Errors 以字段名为键。
type ImportFailure struct {
	Line   int               `json:"line"`
	Errors map[string]string `json:"errors"`
}

// Handle 逐行创建文章。每一行单独提交，一行失败不影响其他行；只有文件无法读取或数据库出错时返回错误。
func (a *Import) Handle(ctx context.Context, subject identity.Subject, r *sheet.Reader) (ImportResult, error) {
	result := ImportResult{Failures: []ImportFailure{}}
	records, err := sheet.Decode[Changes](ctx, r)
	if err != nil {
		return result, err
	}
	for rec, err := range records {
		if err != nil {
			return result, err
		}
		if rec.Valid() {
			if err := a.row(ctx, subject, &rec); err != nil {
				return result, err
			}
		}
		if !rec.Valid() {
			result.Failures = append(result.Failures, ImportFailure{Line: rec.Line, Errors: rec.Errors})
			continue
		}
		result.Created++
	}
	return result, nil
}

// row 用一行的数据创建文章。slug 已被占用只是这一行的问题，标在 slug 字段上；其他错误原样返回。
func (a *Import) row(ctx context.Context, subject identity.Subject, rec *sheet.Record[Changes]) error {
	_, err := a.create.Handle(ctx, subject, rec.Data)
	if errors.Is(err, ErrSlugTaken) {
		rec.SetError("slug", i18n.T(ctx, ErrSlugTaken.Code(), nil))
		return nil
	}
	return err
}

type ImportInput struct {
	RawBody huma.MultipartFormFiles[struct {
		File huma.FormFile `form:"file" required:"true"`
	}]
}

type ImportOutput struct {
	Body ImportResult
}

// HTTP 处理 POST /articles/import，上传 .csv 或 .xlsx 文件。
func (a *Import) HTTP(ctx context.Context, in *ImportInput) (*ImportOutput, error) {
	subject, _ := identity.FromContext(ctx)
	file := in.RawBody.Data().File
	format, err := sheet.FormatOf(file.Filename)
	if err != nil {
		return nil, err
	}
	r, err := sheet.NewReader(file, format)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	result, err := a.Handle(ctx, subject, r)
	if err != nil {
		return nil, err
	}
	return &ImportOutput{Body: result}, nil
}
