package database_test

import (
	"context"
	"database/sql"
	"errors"
	"uuid"

	"github.com/uptrace/bun"

	"github.com/ryan053857/go-kit/database"
)

type Post struct {
	bun.BaseModel `bun:"table:posts"`
	database.Model

	Title string `bun:"title,notnull"`
}

// 业务函数接收 bun.IDB，既可以传 *bun.DB，也可以传事务中的 bun.Tx。
func createPost(ctx context.Context, db bun.IDB, title string) (*Post, error) {
	p := &Post{Title: title}
	if _, err := db.NewInsert().Model(p).Exec(ctx); err != nil { // 自动填充 ID 与时间戳
		return nil, err
	}
	return p, nil
}

var errPostNotFound = errors.New("post not found")

func getPost(ctx context.Context, db bun.IDB, id uuid.UUID) (*Post, error) {
	p := new(Post)
	err := db.NewSelect().Model(p).Where("id = ?", id).Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, errPostNotFound
	}
	return p, err
}

func renamePost(ctx context.Context, db bun.IDB, p *Post, title string) error {
	p.Title = title
	_, err := db.NewUpdate().Model(p).WherePK().Exec(ctx) // 自动更新 UpdatedAt
	return err
}

func ExampleModel() {
	ctx := context.Background()
	db, err := database.Open(ctx, database.DefaultConfig())
	if err != nil {
		return
	}
	defer db.Close()

	_ = db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		p, err := createPost(ctx, tx, "hello")
		if err != nil {
			return err
		}
		return renamePost(ctx, tx, p, "hello, world")
	})
	_, _ = getPost(ctx, db, uuid.NewV7())
}
