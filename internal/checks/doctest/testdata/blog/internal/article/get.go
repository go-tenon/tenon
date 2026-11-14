package article

import (
	"context"

	"github.com/google/uuid"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/identity"
)

// Get 获取文章。
type Get struct {
	db *bun.DB
}

func NewGet(db *bun.DB) *Get {
	return &Get{db: db}
}

// Handle 返回一篇文章。草稿只有作者本人能看到。
func (a *Get) Handle(ctx context.Context, subject identity.Subject, id uuid.UUID) (*Article, error) {
	if subject.ID == "" {
		return nil, ErrForbidden.New()
	}
	item, err := find(ctx, a.db, id)
	if err != nil {
		return nil, err
	}
	if item.Status == StatusDraft && item.AuthorID != subject.ID {
		return nil, ErrNotFound.New() // 别人的草稿，当作不存在
	}
	return item, nil
}

type GetInput struct {
	ID uuid.UUID `path:"id"`
}

// HTTP 处理 GET /articles/{id}。
func (a *Get) HTTP(ctx context.Context, in *GetInput) (*ArticleOutput, error) {
	subject, _ := identity.FromContext(ctx)
	item, err := a.Handle(ctx, subject, in.ID)
	if err != nil {
		return nil, err
	}
	return &ArticleOutput{Body: toDTO(item)}, nil
}
