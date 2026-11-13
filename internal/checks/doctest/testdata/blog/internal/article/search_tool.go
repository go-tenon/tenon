package article

import (
	"context"

	"github.com/go-tenon/tenon/page"
	"github.com/go-tenon/tenon/search"
)

type SearchArgs struct {
	Query  string `json:"query,omitempty" maxLength:"100" doc:"关键词；不传时按发布时间从新到旧列出"`
	Author string `json:"author,omitempty" doc:"只看这位作者的文章，值是作者 ID"`
	page.OffsetParams
}

// Tool 处理 MCP 工具 article_search。
func (a *Search) Tool(ctx context.Context, in SearchArgs) (*search.Result[SearchDoc], error) {
	q := search.Query{Text: in.Query, Page: in.OffsetParams}
	if in.Author != "" {
		q.Filter = search.Eq("author_id", in.Author)
	}
	if in.Query == "" {
		q.Sort = []search.Sort{search.Desc("published_at")}
	}
	return SearchIndex.Search(ctx, a.db, q)
}
