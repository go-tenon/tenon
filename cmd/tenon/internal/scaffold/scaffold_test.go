package scaffold_test

import (
	"fmt"
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
		"apps/apps.go", "apps/web/src/start.ts", "apps/web/src/server.ts", "apps/web/src/router.tsx", "apps/web/src/theme.ts",
		"apps/web/src/api.ts", "apps/web/src/routes/__root.tsx", "apps/web/src/routes/index.tsx"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
			t.Errorf("%s: %v", f, err)
		}
	}
	for f, want := range map[string]string{
		"package.json":                   `"name": "todo"`,
		"packages/client/package.json":   `"name": "@todo/client"`,
		"./package.json":                 `".tenon/js/ui"`,
		"apps/web/package.json":          `"tenon": { "base": "/", "ssr": true },`,
		"apps/web/vite.config.ts":        `plugins: [tenon({ prerender: ["/"] })]`,
		"apps/web/src/theme.ts":          `preset: "spacious"`,
		"apps/web/src/start.ts":          "requestMiddleware: [requestMiddleware] }",
		"apps/web/src/router.tsx":        `id: "web"`,
		"apps/web/src/routes/__root.tsx": `{ title: "Todo" }`,
		"apps/web/src/routes/index.tsx":  `from "@todo/client/api"`, // 示例页面覆盖前端模板的首页
		"apps/web/src/api.ts":            "fetch: apiFetch",
		"go.mod":                         "\nignore node_modules\n",
		".gitignore":                     "\ndist/\n",
		"apps/apps.go":                   "//go:embed all:dist\nvar Dist embed.FS\n",
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
	for _, want := range []string{`"example.com/todo/internal/todo"`, `"example.com/todo/apps"`,
		"frontend.New(apps.Dist, frontend.Options{API: api, DevServers: a.cfg.DevServers})", "pages.Run(", "command.Serve(open, parts)"} {
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
		name    string
		f       scaffold.Frontend // 布局、登录页、渲染方式，其余维度按布局取默认值
		ssr     bool
		files   map[string]string
		missing []string
	}{
		{"rail", scaffold.Frontend{Layout: "rail"}, false, map[string]string{
			"src/theme.ts":              `preset: "comfortable", primary: "blue-gray" })`,
			"vite.config.ts":            "plugins: [tenon()],",
			"src/router.tsx":            "permissions: async () => (await me()).permissions ?? []",
			"src/routes/_app.tsx":       `<RailLayout title="后台" user=`,
			"src/routes/_app/index.tsx": `createFileRoute("/_app/")`,
			"src/routes/login.tsx":      "<CenteredAuthLayout brand=\"后台\" title=\"登录\">",
			"src/auth.ts":               "createSession",
		}, []string{"src/routes/index.tsx"}},
		{"frame", scaffold.Frontend{Layout: "frame"}, false, map[string]string{
			"src/theme.ts":        `preset: "compact", primary: "blue-gray", datetimeFormat: "MM/DD HH:mm"`,
			"src/routes/_app.tsx": `<FrameLayout title="后台" tabs user=`,
		}, nil},
		{"site", scaffold.Frontend{Layout: "site"}, true, map[string]string{
			"src/theme.ts":          `createUiTheme({ preset: "spacious", primary: "blue-gray"`,
			"src/routes/index.tsx":  `actions={<Button size="sm">开始使用</Button>}`,
			"src/routes/__root.tsx": `import "@fontsource-variable/inter/index.css"`,
			"package.json":          `"@fontsource-variable/inter"`,
			"vite.config.ts":        `tenon({ prerender: ["/"] })`,
		}, []string{"src/auth.ts", "src/routes/login.tsx"}},
		// 各个维度互相独立：后台的布局可以换登录页、去掉登录、改为服务端渲染，官网可以加上登录。
		{"rail split", scaffold.Frontend{Layout: "rail", Login: "split"}, false, map[string]string{
			"src/routes/login.tsx": "<SplitAuthLayout brand=\"后台\" title=\"登录\">",
		}, nil},
		{"frame no login", scaffold.Frontend{Layout: "frame", Login: "none"}, false, map[string]string{
			"src/routes/_app.tsx": `createFileRoute("/_app")({ component: Layout })`,
		}, []string{"src/auth.ts", "src/routes/login.tsx"}},
		{"frame ssr", scaffold.Frontend{Layout: "frame", Render: "ssr"}, true, map[string]string{
			"src/routes/_app.tsx": `({ ssr: false, beforeLoad: requireAuth, component: Layout })`,
			"vite.config.ts":      "plugins: [tenon()],",
		}, nil},
		{"site login", scaffold.Frontend{Layout: "site", Login: "centered"}, true, map[string]string{
			"src/routes/index.tsx": `<ButtonLink to="/login" size="sm">登录</ButtonLink>`,
			"src/router.tsx":       "session,",
			"src/auth.ts":          "createSession",
		}, nil},
	} {
		root := t.TempDir()
		f := c.f
		f.Scope, f.Name, f.Base, f.Title = "helpdesk", "admin-console", "/admin/", "后台"
		if err := scaffold.WriteFrontend(root, f); err != nil {
			t.Fatal(err)
		}
		dir := filepath.Join(root, "apps", "admin-console")
		files := map[string]string{
			"package.json":   `"build": "bunx --bun vite build"`,
			"src/start.ts":   "createStart(() => ({ requestMiddleware: [requestMiddleware] }))",
			"src/router.tsx": `id: "admin-console"`,
		}
		for k, v := range c.files {
			files[k] = v
		}
		for name, want := range files {
			if b, _ := os.ReadFile(filepath.Join(dir, name)); !strings.Contains(string(b), want) {
				t.Errorf("%s %s missing %q:\n%s", c.name, name, want, b)
			}
		}
		// 挂载路径和是否服务端渲染写在 package.json 的 tenon 字段中。
		tenon := fmt.Sprintf(`"tenon": { "base": "/admin/", "ssr": %t },`, c.ssr)
		if b, _ := os.ReadFile(filepath.Join(dir, "package.json")); !strings.Contains(string(b), tenon) {
			t.Errorf("%s package.json missing %q:\n%s", c.name, tenon, b)
		}
		for _, name := range c.missing {
			if _, err := os.Stat(filepath.Join(dir, name)); err == nil {
				t.Errorf("%s: unexpected %s", c.name, name)
			}
		}
		if err := scaffold.WriteFrontend(root, f); err == nil || !strings.Contains(err.Error(), "already exists") {
			t.Errorf("second write: %v", err)
		}
	}
	if err := scaffold.WriteFrontend(t.TempDir(), scaffold.Frontend{Name: "x", Layout: "antd"}); err == nil {
		t.Error("unknown layout accepted")
	}
	for _, f := range []scaffold.Frontend{{Login: "modal"}, {Preset: "dense"}, {Render: "spa"}} {
		f.Name, f.Layout = "x", "rail"
		if err := scaffold.WriteFrontend(t.TempDir(), f); err == nil {
			t.Errorf("%+v accepted", f)
		}
	}
	// 预设可以和布局分开选。
	root := t.TempDir()
	if err := scaffold.WriteFrontend(root, scaffold.Frontend{Scope: "s", Name: "x", Base: "/x/", Title: "X", Layout: "rail", Preset: "compact"}); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(root, "apps", "x", "src", "theme.ts")); !strings.Contains(string(b), `preset: "compact"`) {
		t.Errorf("rail with compact theme.ts:\n%s", b)
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

// 客户端生成在 clients/<名称> 中；网页宿主在前端中加上授权页，前端没有登录时报错，已有的授权页不覆盖。
func TestWriteClient(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	for _, f := range []scaffold.Frontend{
		{Scope: "todo", Name: "admin", Base: "/admin/", Title: "Admin", Layout: "frame"},
		{Scope: "todo", Name: "promo", Base: "/promo/", Title: "Promo", Layout: "site"},
	} {
		if err := scaffold.WriteFrontend(root, f); err != nil {
			t.Fatal(err)
		}
	}
	c := scaffold.Client{Scope: "todo", Name: "admin-desktop", Kind: "electron", Frontend: "admin", Base: "/admin/", Title: "管理后台"}
	consent, err := scaffold.WriteClient(root, c)
	if err != nil || !consent {
		t.Fatalf("WriteClient: %v, consent %v", err, consent)
	}
	for f, want := range map[string]string{
		"clients/admin-desktop/tenon.json":           `"kind": "electron"`,
		"clients/admin-desktop/package.json":         `"name": "@todo/admin-desktop"`,
		"clients/admin-desktop/electron-builder.yml": "productName: 管理后台",
		"clients/admin-desktop/src/main.ts":          `base: "/admin/"`,
		"clients/admin-desktop/src/preload.ts":       `import "@go-tenon/electron/preload"`,
		"clients/admin-desktop/.gitignore":           "release/",
		"apps/admin/src/routes/oauth/consent.tsx":    `createFileRoute("/oauth/consent")`,
		"apps/admin/src/routes/login.tsx":            "<BrowserSignIn signIn={desktop.auth.signIn}",
		"apps/admin/src/auth.ts":                     "await host()?.auth.signOut()",
	} {
		b, err := os.ReadFile(filepath.Join(root, f))
		if err != nil || !strings.Contains(string(b), want) {
			t.Errorf("%s: want %q, err %v\n%s", f, want, err, b)
		}
	}

	if _, err := scaffold.WriteClient(root, c); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Errorf("existing client: %v", err)
	}
	c.Name = "admin-mac"
	if consent, err := scaffold.WriteClient(root, c); err != nil || consent {
		t.Errorf("second client of the same frontend: %v, consent %v", err, consent)
	}
	c.Name, c.Frontend, c.Base = "promo-desktop", "promo", "/promo/"
	if _, err := scaffold.WriteClient(root, c); err == nil || !strings.Contains(err.Error(), "has no login") {
		t.Errorf("frontend without login: %v", err)
	}
}
