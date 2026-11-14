package clientgen_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/go-tenon/tenon/command"
	"github.com/go-tenon/tenon/httpx/rest"
	"github.com/go-tenon/tenon/httpx/rest/clientgen"
)

func runClient(t *testing.T, args ...string) (string, error) {
	t.Helper()
	root := command.Root("api", "test")
	root.AddCommand(clientgen.Command(func() chi.Routes { return exampleAPI(nil).Router() }))
	var stderr bytes.Buffer
	root.SetErr(&stderr)
	root.SetOut(&bytes.Buffer{})
	root.SetArgs(args)
	err := root.ExecuteContext(context.Background())
	return stderr.String(), err
}

func TestCommandWritesClientAndRemovesStaleFiles(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "api")
	write := func(name, content string) {
		p := filepath.Join(out, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/blog\n\ngo 1.27\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	write("comment/comment.go", clientgen.Header+"\n\npackage comment\n")
	write("helpers.go", "package api\n")

	stderr, err := runClient(t, "client", "--out", out, "--main", exampleMain)
	if err != nil {
		t.Fatal(err, stderr)
	}
	if stderr != "" {
		t.Fatalf("stderr %q", stderr)
	}
	data, err := os.ReadFile(filepath.Join(out, "article.go"))
	if err != nil || !strings.Contains(string(data), `"example.com/blog/api/article"`) {
		t.Fatalf("%s %v", data, err)
	}
	if _, err := os.Stat(filepath.Join(out, "comment")); !os.IsNotExist(err) {
		t.Fatal("stale generated package must be removed", err)
	}
	if _, err := os.Stat(filepath.Join(out, "helpers.go")); err != nil {
		t.Fatal("hand-written files must be kept", err)
	}
}

func TestCommandRequiresOut(t *testing.T) {
	_, err := runClient(t, "client")
	var exit *command.ExitError
	if !errors.As(err, &exit) || exit.Code != command.ExitUsage {
		t.Fatalf("%#v", err)
	}
}

// --ts 把每个 API 的 TypeScript 客户端写到以 API 名称命名的子目录，删除上次生成、这次已经移除的 .ts 文件，保留手写的文件。
func TestCommandWritesTSClient(t *testing.T) {
	root := t.TempDir()
	out := filepath.Join(root, "api") // BasePath /api 的客户端
	if err := os.MkdirAll(out, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string]string{"old.ts": clientgen.Header + "\n", "query.ts": "export {}\n"} {
		if err := os.WriteFile(filepath.Join(out, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	stderr, err := runClient(t, "client", "--ts", root, "--main", exampleMain)
	if err != nil || stderr != "" {
		t.Fatal(err, stderr)
	}
	for _, name := range []string{"api.ts", "errors.ts", "runtime.ts", "query.ts"} {
		if _, err := os.Stat(filepath.Join(out, name)); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
	if _, err := os.Stat(filepath.Join(out, "old.ts")); !os.IsNotExist(err) {
		t.Error("stale generated file must be removed", err)
	}

	// --name 按 BasePath 改名。
	if _, err := runClient(t, "client", "--ts", root, "--name", "/api=public", "--main", exampleMain); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "public", "api.ts")); err != nil {
		t.Error(err)
	}
}

func TestTSDirs(t *testing.T) {
	api := func(base string) *rest.API {
		return rest.New(chi.NewRouter(), rest.Options{Title: "x", BasePath: base})
	}
	apis := []*rest.API{api("/api"), api("/api/admin"), api("/api/v1/")}
	dirs, err := clientgen.TSDirs(apis, nil)
	if err != nil || strings.Join(dirs, " ") != "api admin v1" {
		t.Fatalf("dirs %q, %v", dirs, err)
	}
	if dirs, err := clientgen.TSDirs(apis, map[string]string{"/api/v1/": "public"}); err != nil || dirs[2] != "public" {
		t.Fatalf("renamed %q, %v", dirs, err)
	}
	for name, c := range map[string]struct {
		apis  []*rest.API
		names map[string]string
		want  string
	}{
		"duplicate":  {[]*rest.API{api("/api/v1"), api("/admin/v1")}, nil, `both named "v1"`},
		"invalid":    {[]*rest.API{api("/API")}, nil, "must match"},
		"empty base": {[]*rest.API{api("")}, nil, "must match"},
		"bad rename": {apis, map[string]string{"/api": "Public"}, "must match"},
		"relative":   {apis, map[string]string{"api": "x"}, "must start with /"},
		"no API":     {nil, nil, "no API"},
	} {
		if _, err := clientgen.TSDirs(c.apis, c.names); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}
