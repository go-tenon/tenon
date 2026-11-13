package article

import (
	"context"
	"database/sql"
	"time"

	"github.com/cockroachdb/errors"
	"github.com/google/uuid"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/database"
	"github.com/go-tenon/tenon/identity"
)

// Publish 发布文章。
type Publish struct {
	db *bun.DB
}

func NewPublish(db *bun.DB) *Publish {
	return &Publish{db: db}
}

// Handle 发布一篇草稿：只有作者能发布，不能重复发布。
func (a *Publish) Handle(ctx context.Context, subject identity.Subject, id uuid.UUID) (*Article, error) {
	item := new(Article)
	err := database.RunInTx(ctx, a.db, nil, func(ctx context.Context, tx database.Tx) error {
		err := tx.NewSelect().Model(item).Where("id = ?", id).For("UPDATE").Scan(ctx)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound.New()
		}
		if err != nil {
			return errors.Wrap(err, "load article")
		}
		if !CanEdit(subject, item) {
			return ErrForbidden.New()
		}
		if item.Status == StatusPublished {
			return ErrAlreadyPublished.New()
		}
		now := time.Now().UTC()
		item.Status, item.PublishedAt = StatusPublished, &now
		_, err = tx.NewUpdate().Model(item).Column("status", "published_at", "updated_at").WherePK().Exec(ctx)
		return errors.Wrap(err, "publish article")
	})
	if err != nil {
		return nil, err
	}
	return item, nil
}

type PublishInput struct {
	ID uuid.UUID `path:"id"`
}

// HTTP 处理 POST /articles/{id}/publish。
func (a *Publish) HTTP(ctx context.Context, in *PublishInput) (*ArticleOutput, error) {
	subject, _ := identity.FromContext(ctx)
	item, err := a.Handle(ctx, subject, in.ID)
	if err != nil {
		return nil, err
	}
	return &ArticleOutput{Body: toDTO(item)}, nil
}
