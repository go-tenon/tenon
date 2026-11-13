package bootstrap

import (
	"errors"

	"github.com/spf13/cobra"
	"github.com/uptrace/bun"

	"example.com/blog/internal/article"
	"example.com/blog/internal/user"
	"github.com/go-tenon/tenon/command"
	"github.com/go-tenon/tenon/identity"
)

func seed(db *bun.DB) *cobra.Command {
	return &cobra.Command{
		Use:   "seed",
		Short: "写入开发用的示例数据",
		Args:  command.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			create, publish := article.NewCreate(db), article.NewPublish(db)
			alice := identity.Subject{Type: user.Type, ID: "alice"}
			for _, c := range []article.Changes{
				{Title: "Hello World", Slug: "hello-world", Content: "……"},
				{Title: "Second Post", Slug: "second-post", Content: "……"},
			} {
				item, err := create.Handle(cmd.Context(), alice, c)
				if errors.Is(err, article.ErrSlugTaken) {
					continue // 之前已经写入过
				}
				if err != nil {
					return err
				}
				if _, err := publish.Handle(cmd.Context(), alice, item.ID); err != nil {
					return err
				}
			}
			return nil
		},
	}
}
