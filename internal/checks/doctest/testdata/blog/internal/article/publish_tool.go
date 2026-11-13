package article

import (
	"context"

	"github.com/google/uuid"

	"github.com/go-tenon/tenon/identity"
)

type PublishArgs struct {
	ID uuid.UUID `json:"id" format:"uuid" doc:"文章 ID。不知道 ID 时先用 article_search 查找"`
}

// Tool 处理 MCP 工具 article_publish。
func (a *Publish) Tool(ctx context.Context, in PublishArgs) (*ArticleDTO, error) {
	subject, _ := identity.FromContext(ctx)
	item, err := a.Handle(ctx, subject, in.ID)
	if err != nil {
		return nil, err
	}
	dto := toDTO(item)
	return &dto, nil
}
