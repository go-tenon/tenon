package article_test

import (
	"bytes"
	"context"
	"testing"

	"example.com/blog/internal/article"
	"example.com/blog/internal/user"
	"example.com/blog/migrations"
	"github.com/go-tenon/tenon/command"
	"github.com/go-tenon/tenon/database/dbtest"
	"github.com/go-tenon/tenon/identity"
)

func TestPublishCommand(t *testing.T) {
	ctx := context.Background()
	db := dbtest.Open(t, migrations.FS)
	alice := identity.Subject{Type: user.Type, ID: "alice"}
	item, err := article.NewCreate(db).Handle(ctx, alice, article.Changes{Title: "Hello World", Slug: "hello-world"})
	if err != nil {
		t.Fatal(err)
	}

	run := func(args ...string) (string, error) {
		var out bytes.Buffer
		cmd := article.Commands(db)
		cmd.SetOut(&out)
		cmd.SetErr(&out)
		cmd.SetArgs(args)
		err := cmd.ExecuteContext(ctx)
		return out.String(), err
	}
	if out, err := run("publish", item.ID.String()); err != nil || out != "hello-world\n" {
		t.Fatalf("out = %q, err = %v", out, err)
	}
	// 业务错误按分类转换为退出码：重复发布是 Conflict，以 1 退出
	if _, err := run("publish", item.ID.String()); command.ExitCode(err) != command.ExitFailure {
		t.Fatalf("exit code = %d, err = %v", command.ExitCode(err), err)
	}
	// 参数不是合法的 ID 时是用法错误
	if _, err := run("publish", "not-a-uuid"); command.ExitCode(err) != command.ExitUsage {
		t.Fatalf("exit code = %d, err = %v", command.ExitCode(err), err)
	}
}
