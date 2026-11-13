package article_test

import (
	"context"
	"errors"
	"testing"

	"example.com/blog/internal/article"
	"example.com/blog/internal/user"
	"example.com/blog/migrations"
	"github.com/go-tenon/tenon/database/dbtest"
	"github.com/go-tenon/tenon/identity"
)

func TestDraftVisibilityAndPublishing(t *testing.T) {
	ctx := context.Background()
	db := dbtest.Open(t, migrations.FS)
	create, get, publish := article.NewCreate(db), article.NewGet(db), article.NewPublish(db)
	alice := identity.Subject{Type: user.Type, ID: "alice"}
	bob := identity.Subject{Type: user.Type, ID: "bob"}

	item, err := create.Handle(ctx, alice, article.Changes{Title: "Hello World", Slug: "hello-world"})
	if err != nil {
		t.Fatal(err)
	}

	// 草稿只有作者能看到
	if _, err := get.Handle(ctx, bob, item.ID); !errors.Is(err, article.ErrNotFound) {
		t.Fatalf("bob read a draft: %v", err)
	}
	// 只有作者能发布，而且不能重复发布
	if _, err := publish.Handle(ctx, bob, item.ID); !errors.Is(err, article.ErrForbidden) {
		t.Fatalf("bob published: %v", err)
	}
	if _, err := publish.Handle(ctx, alice, item.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := publish.Handle(ctx, alice, item.ID); !errors.Is(err, article.ErrAlreadyPublished) {
		t.Fatalf("published twice: %v", err)
	}
	// 发布之后所有人都能看到
	if _, err := get.Handle(ctx, bob, item.ID); err != nil {
		t.Fatal(err)
	}
	// slug 不能重复
	_, err = create.Handle(ctx, bob, article.Changes{Title: "Another", Slug: "hello-world"})
	if !errors.Is(err, article.ErrSlugTaken) {
		t.Fatalf("duplicate slug: %v", err)
	}
}
