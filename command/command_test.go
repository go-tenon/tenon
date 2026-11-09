package command_test

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/ryan053857/mygoravel/command"
)

func TestOpenAPIDoesNotNeedRuntime(t *testing.T) {
	var openedDB bool
	root := command.Root("orders", "test", command.IO{})
	root.AddCommand(command.NewOpenAPI(func(ctx context.Context, cmd *cobra.Command, args []string) error {
		if openedDB {
			t.Fatal("openapi opened db")
		}
		cmd.Println(`{"openapi":"3.1.0"}`)
		return nil
	}))
	root.AddCommand(command.NewMigrate(
		func(ctx context.Context, cmd *cobra.Command, args []string) error {
			openedDB = true
			return nil
		},
		func(ctx context.Context, cmd *cobra.Command, args []string) error { return nil },
		func(ctx context.Context, cmd *cobra.Command, args []string) error { return nil },
	))

	buf := &bytes.Buffer{}
	root.SetOut(buf)
	root.SetArgs([]string{"openapi"})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "openapi") {
		t.Fatalf("out=%q", buf.String())
	}
}

func TestExitCode(t *testing.T) {
	err := command.AsExitError(command.ExitConfig, context.Canceled)
	if command.ExitCode(err) != command.ExitConfig {
		t.Fatal(command.ExitCode(err))
	}
}
