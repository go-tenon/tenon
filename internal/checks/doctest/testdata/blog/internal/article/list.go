package article

import (
	"context"

	"github.com/cockroachdb/errors"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/identity"
	"github.com/go-tenon/tenon/page"
)

// List 和 toDTOs 在文档中没有给出。
type List struct{ db *bun.DB }

func toDTOs(items []Article) []ArticleDTO {
	out := make([]ArticleDTO, len(items))
	for i := range items {
		out[i] = toDTO(&items[i])
	}
	return out
}

// orders 是 ListQuery.Sort 每个取值对应的排序。
var orders = map[string]string{
	"created":  "created_at, id",
	"-created": "created_at DESC, id DESC",
	"title":    "title, id",
	"-title":   "title DESC, id DESC",
}

// Handle 返回符合条件的一页文章和总数。
func (a *List) Handle(ctx context.Context, subject identity.Subject, q ListQuery) ([]Article, int64, error) {
	var items []Article
	sel := a.db.NewSelect().Model(&items).
		OrderExpr(orders[q.Sort]).
		Limit(q.Limit()).
		Offset(q.Offset())
	if q.Status != "" {
		sel.Where("status = ?", q.Status)
	}
	if q.Q != "" {
		sel.Where("title LIKE ?", "%"+q.Q+"%")
	}
	total, err := sel.ScanAndCount(ctx)
	if err != nil {
		return nil, 0, errors.Wrap(err, "list articles")
	}
	return items, total, nil
}

type ListOutput struct {
	Body page.Offset[ArticleDTO]
}

// HTTP 处理 GET /articles。
func (a *List) HTTP(ctx context.Context, in *ListQuery) (*ListOutput, error) {
	subject, _ := identity.FromContext(ctx)
	items, total, err := a.Handle(ctx, subject, *in)
	if err != nil {
		return nil, err
	}
	return &ListOutput{Body: page.NewOffset(toDTOs(items), in.OffsetParams, total)}, nil
}
