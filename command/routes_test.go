package command_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/danielgtaylor/huma/v2"

	"github.com/ryan053857/mygoravel/command"
	"github.com/ryan053857/mygoravel/httpx/rest"
	"github.com/ryan053857/mygoravel/identity"
)

func TestRoutesCommand(t *testing.T) {
	type input struct{}
	type output struct{ Body string }
	for _, tc := range []struct {
		name string
		args []string
		want []string
		omit []string
	}{
		{"all", nil, []string{"GET /articles article-list public 获取文章 列表", "POST /articles article-create bearer: articles 创建文章", "GET /livez - unknown -", "GET /docs - unknown -"}, nil},
		{"method", []string{"--method", "post"}, []string{"POST /articles article-create bearer: articles 创建文章"}, []string{"article-list", "/livez", "/docs"}},
		{"path", []string{"--path", "live"}, []string{"GET /livez - unknown -"}, []string{"article-create", "article-list", "/docs"}},
		{"combined", []string{"--path", "articles", "--method", "get"}, []string{"GET /articles article-list public 获取文章 列表"}, []string{"article-create", "/docs"}},
		{"empty", []string{"--path", "missing"}, nil, []string{"article-create", "article-list", "/docs", "/livez"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out, diagnostics bytes.Buffer
			root := command.Root("api", "test", command.IO{Out: &out, Err: &diagnostics})
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			calls := 0
			root.AddCommand(command.NewRoutes(func(got context.Context) (*rest.API, error) {
				calls++
				if got != ctx {
					t.Fatal("factory did not receive the command context")
				}
				api := rest.New(rest.Options{Mode: rest.ModeOpenAPI})
				factory := func() func(context.Context, *input) (*output, error) {
					t.Fatal("routes constructed a runtime handler")
					return nil
				}
				create := huma.Operation{OperationID: "article-create", Method: http.MethodPost, Path: "/articles", Summary: "创建文章"}
				rest.RequireBearer(api, func(huma.Context, string) (identity.Subject, error) {
					t.Fatal("routes invoked authentication")
					return identity.Subject{}, nil
				}, "articles")(&create)
				rest.Register(api, create, factory)
				list := huma.Operation{OperationID: "article-list", Method: http.MethodGet, Path: "/articles", Summary: "获取文章\t\n列表"}
				rest.Public()(&list)
				rest.Register(api, list, factory)
				api.Router().Get("/livez", func(http.ResponseWriter, *http.Request) {
					t.Fatal("routes invoked a native handler")
				})
				return api, nil
			}))
			root.SetArgs(append([]string{"routes"}, tc.args...))
			if err := root.ExecuteContext(ctx); err != nil {
				t.Fatal(err)
			}
			if calls != 1 || diagnostics.Len() != 0 {
				t.Fatalf("factory calls=%d stderr=%s", calls, diagnostics.String())
			}
			lines := strings.Split(strings.TrimSpace(out.String()), "\n")
			for i := range lines {
				lines[i] = strings.Join(strings.Fields(lines[i]), " ")
			}
			got := strings.Join(lines, "\n")
			if lines[0] != "METHOD PATH OPERATION ID ACCESS SUMMARY" {
				t.Fatalf("missing header: %s", got)
			}
			for _, want := range tc.want {
				if !slices.Contains(lines, want) {
					t.Errorf("missing %q in %s", want, got)
				}
			}
			for _, omit := range tc.omit {
				if strings.Contains(got, omit) {
					t.Errorf("unexpected %q in %s", omit, got)
				}
			}
		})
	}
}

func TestRoutesRejectsInvalidArgumentsBeforeBuildingAPI(t *testing.T) {
	for _, args := range [][]string{{"routes", "extra"}, {"routes", "--unknown"}, {"routes", "--path"}} {
		root := command.Root("api", "test", command.IO{Out: io.Discard, Err: io.Discard})
		root.AddCommand(command.NewRoutes(func(context.Context) (*rest.API, error) {
			t.Fatal("invalid arguments built the API")
			return nil, nil
		}))
		root.SetArgs(args)
		if err := root.Execute(); command.ExitCode(err) != command.ExitUsage {
			t.Errorf("args=%v error=%v", args, err)
		}
	}
}

func TestRoutesRejectsMissingAPI(t *testing.T) {
	for _, tc := range []struct {
		name  string
		build func(context.Context) (*rest.API, error)
		want  string
	}{
		{"nil factory", nil, "routes: nil API factory"},
		{"nil API", func(context.Context) (*rest.API, error) { return nil, nil }, "routes: API factory returned nil"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out, diagnostics bytes.Buffer
			root := command.Root("api", "test", command.IO{Out: &out, Err: &diagnostics})
			root.AddCommand(command.NewRoutes(tc.build))
			root.SetArgs([]string{"routes"})
			err := root.Execute()
			if err == nil || err.Error() != tc.want {
				t.Fatalf("got %v, want %q", err, tc.want)
			}
			if command.ExitCode(err) != command.ExitInternal {
				t.Fatalf("exit code = %d, want ExitInternal", command.ExitCode(err))
			}
			if out.Len() != 0 || diagnostics.Len() != 0 {
				t.Fatalf("unexpected output: stdout=%q stderr=%q", out.String(), diagnostics.String())
			}
		})
	}
}

type failingRoutesWriter struct{ err error }

func (w failingRoutesWriter) Write([]byte) (int, error) { return 0, w.err }

func TestRoutesPropagatesErrors(t *testing.T) {
	want := errors.New("unavailable")
	for _, tc := range []struct {
		name  string
		build func(context.Context) (*rest.API, error)
		out   io.Writer
	}{
		{"factory", func(context.Context) (*rest.API, error) { return nil, want }, io.Discard},
		{"output", func(context.Context) (*rest.API, error) { return rest.New(rest.Options{Mode: rest.ModeOpenAPI}), nil }, failingRoutesWriter{want}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := command.Root("api", "test", command.IO{Out: tc.out})
			root.AddCommand(command.NewRoutes(tc.build))
			root.SetArgs([]string{"routes"})
			if err := root.Execute(); !errors.Is(err, want) {
				t.Fatalf("got %v, want %v", err, want)
			}
		})
	}
}
