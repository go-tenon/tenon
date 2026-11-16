package scaffold_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/go-tenon/tenon/cmd/tenon/internal/scaffold"
)

func TestWrite(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "todo")
	err := scaffold.Write(dir, scaffold.Options{Module: "example.com/todo", Name: "Todo", TenonVersion: "v0.0.0", TenonDir: "/src/tenon"})
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{".gitignore", "go.mod", "package.json", "config/app.yaml", "cmd/api/main.go", "internal/todo/todo.go",
		"packages/client/package.json", "apps/web/package.json", "apps/web/vite.config.ts", "apps/web/tsconfig.json",
		"apps/web/frontend.go", "apps/web/index.html", "apps/web/src/main.tsx", "apps/web/src/app.ts", "apps/web/src/client.ts",
		"apps/web/src/routes/__root.tsx", "apps/web/src/routes/index.tsx", "apps/web/src/routeTree.gen.ts",
		"theme/tokens.json", "theme/theme.go", "packages/theme/package.json"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
			t.Errorf("%s: %v", f, err)
		}
	}
	for f, want := range map[string]string{
		"package.json":                  `"name": "todo"`,
		"packages/client/package.json":  `"name": "@todo/client"`,
		"./package.json":                `".tenon/js/shell"`,
		"packages/theme/package.json":   `"name": "@todo/theme"`,
		"apps/web/src/app.css":          `@import "@todo/theme/tokens.css";`,
		"apps/web/package.json":         `"@go-tenon/shell": "workspace:*"`,
		"apps/web/vite.config.ts":       `base: "/"`,
		"apps/web/frontend.go":          "//go:embed all:dist\n",
		"apps/web/index.html":           "<title>Todo</title>",
		"apps/web/src/routes/index.tsx": `from "@todo/client/api"`, // 示例页面覆盖前端模板的首页
		"apps/web/src/app.ts":           `createApp({ id: "web", routeTree })`,
		"go.mod":                        "\nignore node_modules\n",
		".gitignore":                    ".tenon/",
	} {
		if b, _ := os.ReadFile(filepath.Join(dir, f)); !strings.Contains(string(b), want) {
			t.Errorf("%s missing %q:\n%s", f, want, b)
		}
	}
	goMod, _ := os.ReadFile(filepath.Join(dir, "go.mod"))
	for _, want := range []string{"module example.com/todo\n", "go " + scaffold.GoVersion + "\n",
		"require github.com/go-tenon/tenon v0.0.0\n", "replace github.com/go-tenon/tenon => /src/tenon\n", "tool github.com/go-tenon/tenon/cmd/tenon\n"} {
		if !strings.Contains(string(goMod), want) {
			t.Errorf("go.mod missing %q:\n%s", want, goMod)
		}
	}
	main, _ := os.ReadFile(filepath.Join(dir, "cmd/api/main.go"))
	if !strings.Contains(string(main), `"example.com/todo/internal/todo"`) || !strings.Contains(string(main), `"example.com/todo/apps/web"`) ||
		!strings.Contains(string(main), `cfg.DevServers["web"]`) {
		t.Errorf("main.go:\n%s", main)
	}
	// 没有遗漏的模板标记；JSX 的 {{ }} 保持原样。
	_ = filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			b, _ := os.ReadFile(p)
			if strings.Contains(string(b), "[[") || strings.HasSuffix(p, ".tmpl") {
				t.Errorf("%s is not rendered", p)
			}
		}
		return err
	})
	if err := scaffold.Write(dir, scaffold.Options{Module: "x", Name: "x", TenonVersion: "v1"}); err == nil {
		t.Error("wrote into a non-empty directory")
	}
}

// 生成的 go.mod 要求的 Go 版本与框架相同。
func TestGoVersionMatchesFramework(t *testing.T) {
	b, err := os.ReadFile("../../../../go.mod")
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`(?m)^go (\S+)$`).FindSubmatch(b)
	if m == nil || string(m[1]) != scaffold.GoVersion {
		t.Fatalf("framework go %s, scaffold.GoVersion %s", m[1], scaffold.GoVersion)
	}
}

func TestWriteFrontend(t *testing.T) {
	root := t.TempDir()
	f := scaffold.Frontend{Scope: "helpdesk", Name: "admin-console", Base: "/admin/", Title: "admin-console"}
	if err := scaffold.WriteFrontend(root, f); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, "apps", "admin-console")
	for name, want := range map[string]string{
		"package.json":         `"name": "@helpdesk/admin-console"`,
		"vite.config.ts":       `base: "/admin/"`,
		"frontend.go":          "package adminconsole\n",
		"src/routes/index.tsx": "<h1>admin-console</h1>",
		"src/app.ts":           `id: "admin-console"`,
	} {
		if b, _ := os.ReadFile(filepath.Join(dir, name)); !strings.Contains(string(b), want) {
			t.Errorf("%s missing %q:\n%s", name, want, b)
		}
	}
	if err := scaffold.WriteFrontend(root, f); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Errorf("second write: %v", err)
	}
}

func TestAddWorkspace(t *testing.T) {
	root := t.TempDir()
	p := filepath.Join(root, "package.json")
	if err := os.WriteFile(p, []byte("{\n  \"name\": \"todo\",\n  \"workspaces\": [\"apps/web\", \"packages/client\"],\n  \"scripts\": {}\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for range 2 { // 已经有的成员不重复加入
		if err := scaffold.AddWorkspace(root, "apps/admin"); err != nil {
			t.Fatal(err)
		}
	}
	b, _ := os.ReadFile(p)
	want := "{\n  \"name\": \"todo\",\n  \"workspaces\": [\n    \"apps/admin\",\n    \"apps/web\",\n    \"packages/client\"\n  ],\n  \"scripts\": {}\n}\n"
	if string(b) != want {
		t.Errorf("package.json:\n%s", b)
	}
	if err := os.WriteFile(p, []byte(`{"name": "todo"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := scaffold.AddWorkspace(root, "apps/admin"); err == nil {
		t.Error("no error without workspaces")
	}
}

func TestSlug(t *testing.T) {
	for name, want := range map[string]string{"todo": "todo", "My Blog": "my-blog", "Hello__World!": "hello-world", "博客": "app", "v2-api": "v2-api"} {
		if got := scaffold.Slug(name); got != want {
			t.Errorf("Slug(%q) = %q", name, got)
		}
	}
}

func TestWriteAdminFrontend(t *testing.T) {
	root := t.TempDir()
	f := scaffold.Frontend{Scope: "helpdesk", Name: "ops", Base: "/ops/", Title: "运营后台", Template: "admin"}
	if err := scaffold.WriteFrontend(root, f); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, "apps", "ops")
	for name, want := range map[string]string{
		"package.json":              `"@go-tenon/admin": "workspace:*"`,
		"vite.config.ts":            `base: "/ops/"`,
		"frontend.go":               "package ops\n",
		"AGENTS.md":                 "挂在 `/ops/`",
		"biome.json":                "noRestrictedImports",
		"src/app.ts":                `id: "ops"`,
		"src/routes/__root.tsx":     `import { antd } from "@helpdesk/theme"`,
		"src/routes/_app.tsx":       "beforeLoad: requireAuth",
		"src/routes/login.tsx":      `<AuthLayout title="运营后台">`,
		"src/routes/_app/index.tsx": "欢迎使用运营后台",
		"src/routeTree.gen.ts":      "LoginRouteImport",
	} {
		if b, _ := os.ReadFile(filepath.Join(dir, name)); !strings.Contains(string(b), want) {
			t.Errorf("%s missing %q:\n%s", name, want, b)
		}
	}
	if err := scaffold.WriteFrontend(t.TempDir(), scaffold.Frontend{Name: "x", Template: "vue"}); err == nil {
		t.Error("unknown template accepted")
	}
}

func TestAddMCPServer(t *testing.T) {
	root := t.TempDir()
	read := func() string {
		b, _ := os.ReadFile(filepath.Join(root, ".mcp.json"))
		return string(b)
	}
	if err := scaffold.AddMCPServer(root, "antd", scaffold.AntdMCP); err != nil {
		t.Fatal(err)
	}
	if got := read(); !strings.Contains(got, `"@ant-design/cli@6.6.8"`) {
		t.Fatalf(".mcp.json:\n%s", got)
	}
	// 已有的服务和其他字段保留；同名的服务不覆盖。
	if err := os.WriteFile(filepath.Join(root, ".mcp.json"), []byte(`{"mcpServers": {"antd": {"command": "mine"}, "db": {"command": "db"}}, "other": 1}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := scaffold.AddMCPServer(root, "antd", scaffold.AntdMCP); err != nil {
		t.Fatal(err)
	}
	if err := scaffold.AddMCPServer(root, "shadcn", map[string]any{"command": "x"}); err != nil {
		t.Fatal(err)
	}
	got := read()
	for _, want := range []string{`"command": "mine"`, `"db"`, `"other": 1`, `"shadcn"`} {
		if !strings.Contains(got, want) {
			t.Errorf(".mcp.json missing %s:\n%s", want, got)
		}
	}
}
