package attachment

import (
	"context"

	"github.com/google/uuid"

	"github.com/go-tenon/tenon/vector"
)

type Find struct{ vectors *vector.Store }

func NewFind(vectors *vector.Store) *Find { return &Find{vectors: vectors} }

// Find 返回文章附件中与问题最相关的分段。
func (a *Find) Find(ctx context.Context, articleID uuid.UUID, question string) ([]vector.Hit[ChunkDoc], error) {
	return Chunks.Search(ctx, a.vectors, vector.Query{
		Text:   question,
		Filter: vector.Eq("article_id", articleID),
		Limit:  20,
	})
}
