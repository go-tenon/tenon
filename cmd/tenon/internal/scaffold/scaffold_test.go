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
		"apps/web/frontend.go", "apps/web/src/start.ts", "apps/web/src/server.ts", "apps/web/src/router.tsx", "apps/web/src/theme.ts",
		"apps/web/src/api.ts", "apps/web/src/routes/__root.tsx", "apps/web/src/routes/index.tsx"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
			t.Errorf("%s: %v", f, err)
		}
	}
	for f, want := range map[string]string{
		"package.json":                   `"name": "todo"`,
		"packages/client/package.json":   `"name": "@todo/client"`,
		"./package.json":                 `".tenon/js/ui"`,
		"apps/web/package.json":          `"build": "tenon-start compile --out ssr.js --base /"`,
		"apps/web/vite.config.ts":        `plugins: [tenon({ prerender: ["/"] }), tenonVersion()]`,
		"apps/web/frontend.go":           "//go:embed ssr.js\nvar SSR []byte\n",
		"apps/web/src/theme.ts":          `preset: "site"`,
		"apps/web/src/start.ts":          "requestMiddleware: [requestMiddleware] }",
		"apps/web/src/router.tsx":        `id: "web"`,
		"apps/web/src/routes/__root.tsx": `{ title: "Todo" }`,
		"apps/web/src/routes/index.tsx":  `from "@todo/client/api"`, // 示例页面覆盖前端模板的首页
		"apps/web/src/api.ts":            "fetch: apiFetch",
		"go.mod":                         "\nignore node_modules\n",
		".gitignore":                     "/apps/ssr-runtime.bin\n/apps/*/ssr.js",
		"apps/runtime.go":                "//go:embed ssr-runtime.bin\nvar Runtime []byte\n",
	} {
		if b, _ := os.ReadFile(filepath.Join(dir, f)); !strings.Contains(string(b), want) {
			t.Errorf("%s missing %q:\n%s", f, want, b)
		}
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "apps/web/src/router.tsx")); strings.Contains(string(b), "session") {
		t.Errorf("site frontend without login has a session:\n%s", b)
	}
	goMod, _ := os.ReadFile(filepath.Join(dir, "go.mod"))
	for _, want := range []string{"module example.com/todo\n", "go " + scaffold.GoVersion + "\n",
		"require github.com/go-tenon/tenon v0.0.0\n", "replace github.com/go-tenon/tenon => /src/tenon\n", "tool github.com/go-tenon/tenon/cmd/tenon\n"} {
		if !strings.Contains(string(goMod), want) {
			t.Errorf("go.mod missing %q:\n%s", want, goMod)
		}
	}
	main, _ := os.ReadFile(filepath.Join(dir, "cmd/api/main.go"))
	for _, want := range []string{`"example.com/todo/internal/todo"`, `"example.com/todo/apps/web"`, `cfg.DevServers["web"]`, "web.SSR", "ssr.Run("} {
		if !strings.Contains(string(main), want) {
			t.Errorf("main.go missing %q:\n%s", want, main)
		}
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
	for _, c := range []struct {
		style   string
		files   map[string]string
		missing []string
	}{
		{"console", map[string]string{
			"src/theme.ts":              `preset: "console"`,
			"src/start.ts":              "defaultSsr: false",
			"src/router.tsx":            "permissions: async () => (await me()).permissions ?? []",
			"src/routes/_app.tsx":       `<AppLayout title="后台" />`,
			"src/routes/_app/index.tsx": `createFileRoute("/_app/")`,
			"src/routes/login.tsx":      "<LoginForm login={login} redirect={redirect} />",
			"src/auth.ts":               "createSession",
		}, []string{"src/routes/index.tsx"}},
		{"admin", map[string]string{
			"src/theme.ts":        `preset: "admin"`,
			"src/routes/_app.tsx": `<AppLayout title="后台" tabs />`,
		}, nil},
		{"site", map[string]string{
			"src/theme.ts":         `preset: "site"`,
			"src/routes/index.tsx": `<SiteLayout brand="后台">`,
			"vite.config.ts":       `tenon({ prerender: ["/"] })`,
		}, []string{"src/auth.ts", "src/routes/login.tsx"}},
	} {
		root := t.TempDir()
		f := scaffold.Frontend{Scope: "helpdesk", Name: "admin-console", Base: "/admin/", Title: "后台", Style: c.style}
		if err := scaffold.WriteFrontend(root, f); err != nil {
			t.Fatal(err)
		}
		dir := filepath.Join(root, "apps", "admin-console")
		files := map[string]string{
			"package.json":   `"build": "tenon-start compile --out ssr.js --base /admin/"`,
			"vite.config.ts": `base: "/admin/"`,
			"frontend.go":    "package adminconsole\n",
			"src/router.tsx": `id: "admin-console"`,
		}
		for k, v := range c.files {
			files[k] = v
		}
		for name, want := range files {
			if b, _ := os.ReadFile(filepath.Join(dir, name)); !strings.Contains(string(b), want) {
				t.Errorf("%s %s missing %q:\n%s", c.style, name, want, b)
			}
		}
		for _, name := range c.missing {
			if _, err := os.Stat(filepath.Join(dir, name)); err == nil {
				t.Errorf("%s: unexpected %s", c.style, name)
			}
		}
		if err := scaffold.WriteFrontend(root, f); err == nil || !strings.Contains(err.Error(), "already exists") {
			t.Errorf("second write: %v", err)
		}
	}
	if err := scaffold.WriteFrontend(t.TempDir(), scaffold.Frontend{Name: "x", Style: "antd"}); err == nil {
		t.Error("unknown style accepted")
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
