package article

import (
	"context"
	"time"

	"github.com/cockroachdb/errors"
	"github.com/google/uuid"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/page"
	"github.com/go-tenon/tenon/queue"
	"github.com/go-tenon/tenon/search"
)

// SearchDoc 是文章在搜索索引中的文档，只放展示搜索结果需要的字段。
type SearchDoc struct {
	ID          uuid.UUID `json:"id"`
	Slug        string    `json:"slug"`
	Title       string    `json:"title" search:"text,boost"`
	Content     string    `json:"content" search:"text"`
	AuthorID    string    `json:"author_id" search:"filter"`
	PublishedAt time.Time `json:"published_at" search:"filter"`
}

// SearchIndex 是文章的搜索索引。
var SearchIndex = search.Define[SearchDoc]("articles")

// LoadSearchDocs 读取文章当前的状态：已发布的文章返回它的文档，草稿和已删除的文章返回空，从索引中删除。
func LoadSearchDocs(ctx context.Context, tx bun.IDB, key string) ([]SearchDoc, error) {
	id, err := uuid.Parse(key)
	if err != nil {
		return nil, queue.Permanent(err)
	}
	var items []Article
	err = tx.NewSelect().Model(&items).Where("id = ? AND status = ?", id, StatusPublished).Scan(ctx)
	if err != nil {
		return nil, errors.Wrap(err, "load article")
	}
	docs := make([]SearchDoc, len(items))
	for i, item := range items {
		docs[i] = SearchDoc{
			ID: item.ID, Slug: item.Slug, Title: item.Title, Content: item.Content,
			AuthorID: item.AuthorID, PublishedAt: *item.PublishedAt,
		}
	}
	return docs, nil
}

// ScanSearchKeys 按 ID 的顺序列出 after 之后的已发布文章，重建索引时使用。
func ScanSearchKeys(ctx context.Context, db bun.IDB, after string, limit int) ([]string, error) {
	q := db.NewSelect().Model((*Article)(nil)).Column("id").
		Where("status = ?", StatusPublished).OrderExpr("id").Limit(int64(limit))
	if after != "" {
		id, err := uuid.Parse(after)
		if err != nil {
			return nil, queue.Permanent(err)
		}
		q = q.Where("id > ?", id)
	}
	var ids []uuid.UUID
	if err := q.Scan(ctx, &ids); err != nil {
		return nil, errors.Wrap(err, "scan articles")
	}
	keys := make([]string, len(ids))
	for i, id := range ids {
		keys[i] = id.String()
	}
	return keys, nil
}

// Search 搜索已发布的文章。
type Search struct{ db *bun.DB }

func NewSearch(db *bun.DB) *Search { return &Search{db: db} }

type SearchInput struct {
	Q      string `query:"q" maxLength:"100" doc:"Keywords."`
	Author string `query:"author" doc:"Only articles by this author."`
	page.OffsetParams
}

type SearchOutput struct {
	Body *search.Result[SearchDoc]
}

func (a *Search) HTTP(ctx context.Context, in *SearchInput) (*SearchOutput, error) {
	q := search.Query{Text: in.Q, Page: in.OffsetParams}
	if in.Author != "" {
		q.Filter = search.Eq("author_id", in.Author)
	}
	if in.Q == "" {
		q.Sort = []search.Sort{search.Desc("published_at")} // 没有关键词时最新的在前
	}
	res, err := SearchIndex.Search(ctx, a.db, q)
	if err != nil {
		return nil, err
	}
	return &SearchOutput{Body: res}, nil
}
