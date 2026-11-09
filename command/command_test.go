package command_test

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/ryan053857/mygoravel/command"
	"github.com/ryan053857/mygoravel/errorx"
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
	unavailable := errorx.Define(errorx.Unavailable, "demo.db")
	for _, tc := range []struct {
		err  error
		want int
	}{
		{nil, command.ExitOK},
		{command.AsExitError(command.ExitConfig, unavailable.New()), command.ExitConfig},
		{fmt.Errorf("sync: %w", unavailable.Wrap(context.DeadlineExceeded)), command.ExitUnavailable},
		{errorx.Define(errorx.NotFound, "demo.missing").New(), command.ExitFailure},
		{errorx.Define(errorx.Internal, "demo.bug").New(), command.ExitInternal},
		{context.Canceled, command.ExitInternal},
	} {
		if got := command.ExitCode(tc.err); got != tc.want {
			t.Fatalf("ExitCode(%v) = %d, want %d", tc.err, got, tc.want)
		}
	}
}
