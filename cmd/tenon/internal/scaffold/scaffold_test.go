package scaffold_test

import (
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"go.yaml.in/yaml/v4"

	"github.com/go-tenon/tenon/cmd/tenon/internal/dev"
	"github.com/go-tenon/tenon/cmd/tenon/internal/scaffold"
)

func TestWrite(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "todo")
	err := scaffold.Write(dir, scaffold.Options{Module: "example.com/todo", Name: "Todo", TenonVersion: "v0.0.0", TenonDir: "/src/tenon"})
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{".gitignore", ".gitattributes", "AGENTS.md", "README.md", "go.mod", "package.json", "config/app.yaml", "config/prod.yaml.example", "cmd/todo/main.go", "internal/todo/todo.go",
		"packages/client/package.json", "apps/web/package.json", "apps/web/vite.config.ts", "apps/web/tsconfig.json",
		"apps/apps.go", "apps/web/public/favicon.svg", "apps/web/src/start.ts", "apps/web/src/server.ts", "apps/web/src/client.tsx", "apps/web/src/observe.ts", "apps/web/src/router.tsx", "apps/web/src/theme.ts",
		"apps/web/src/api.ts", "apps/web/src/routes/__root.tsx", "apps/web/src/routes/index.tsx"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
			t.Errorf("%s: %v", f, err)
		}
	}
	for f, want := range map[string]string{
		"package.json":                   `"name": "todo"`,
		"packages/client/package.json":   `"name": "@todo/client"`,
		"./package.json":                 `"tenon/js/ui"`,
		".gitattributes":                 `tenon/** linguist-generated=true`,
		"apps/web/package.json":          `"tenon": { "base": "/", "ssr": true },`,
		"apps/web/vite.config.ts":        `plugins: [tenon({ prerender: ["/"] })]`,
		"apps/web/src/theme.ts":          `preset: "spacious"`,
		"apps/web/src/start.ts":          "requestMiddleware: [requestMiddleware] }",
		"apps/web/src/router.tsx":        `onError: reportRouteError`,
		"apps/web/src/routes/__root.tsx": `telemetryMeta()`,
		"apps/web/src/client.tsx":        `openobserveRum.init`,
		"apps/web/src/observe.ts":        `export function enableObserve`,
		"./apps/web/package.json":        `"@openobserve/browser-rum"`,
		"config/app.yaml":                "telemetry:\n  service: todo\n  endpoint: \"\"",
		"config/prod.yaml.example":       "# endpoint: https://o2.example.com/api/default",
		"apps/web/src/routes/index.tsx":  `from "@todo/client/api"`, // 示例页面覆盖前端模板的首页
		"apps/web/src/api.ts":            `createClient({ baseURL: "/api", fetch: apiFetch })`,
		"internal/todo/todo.go":          `errorx.Define(errorx.NotFound, "todo.not_found", "待办事项不存在")`,
		"go.mod":                         "\nignore node_modules\n",
		".gitignore":                     "\ndist/\n# apps/dist 中提交了一个 .gitignore，见其中的说明\n!apps/dist/\n",
		"apps/dist/.gitignore":           "*\n!.gitignore\n",
		"./.gitignore":                   "\nconfig/local.yaml\n",
		"apps/apps.go":                   "//go:embed all:dist\nvar Dist embed.FS\n",
		"cmd/../package.json":            `"agents": "go tool tenon agents"`,
	} {
		if b, _ := os.ReadFile(filepath.Join(dir, f)); !strings.Contains(string(b), want) {
			t.Errorf("%s missing %q:\n%s", f, want, b)
		}
	}
	// 默认单语言项目不生成 locales 目录和 src/i18n.ts。
	for _, absent := range []string{"locales", "apps/web/src/i18n.ts"} {
		if _, err := os.Stat(filepath.Join(dir, absent)); err == nil {
			t.Errorf("single-language project should not create %s", absent)
		}
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "apps/web/src/router.tsx")); strings.Contains(string(b), "session") {
		t.Errorf("site frontend without login has a session:\n%s", b)
	}
	agentsMD, _ := os.ReadFile(filepath.Join(dir, "AGENTS.md"))
	for _, want := range []string{"# Todo\n", "go run ./cmd/todo routes", "bin/todo", "<!-- tenon:begin -->", "<!-- tenon:end -->"} {
		if !strings.Contains(string(agentsMD), want) {
			t.Errorf("AGENTS.md missing %q:\n%s", want, agentsMD)
		}
	}
	goMod, _ := os.ReadFile(filepath.Join(dir, "go.mod"))
	for _, want := range []string{"module example.com/todo\n", "go " + scaffold.GoVersion + "\n",
		"require github.com/go-tenon/tenon v0.0.0\n", "replace github.com/go-tenon/tenon => /src/tenon\n", "tool github.com/go-tenon/tenon/cmd/tenon\n"} {
		if !strings.Contains(string(goMod), want) {
			t.Errorf("go.mod missing %q:\n%s", want, goMod)
		}
	}
	main, _ := os.ReadFile(filepath.Join(dir, "cmd/todo/main.go"))
	for _, want := range []string{`"example.com/todo/internal/todo"`, `"example.com/todo/apps"`,
		"Logs       logging.Config", "Telemetry  telemetry.Config", "telemetry.Open(ctx, cfg.Telemetry)", "logging.New(cfg.Logs, logging.Export(tel.Logs))", "httpx.Telemetry(tel.Meters, tel.Tracers)",
		"MeterProvider: a.tel.Meters", "Telemetry:     a.cfg.Telemetry", "pages.SetAPI(router)", "httpcmd.ServeOptions(d)", "pages.Run(", "command.Serve(open, parts)"} {
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

// 多语言新项目生成 locales/embed.go、各语言翻译文件、src/i18n.ts，并在入口和前端接入 i18n。
func TestWriteMultiLanguage(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "todo")
	err := scaffold.Write(dir, scaffold.Options{
		Module: "example.com/todo", Name: "Todo", TenonVersion: "v0.0.0", Languages: []string{"zh-CN", "en"},
	})
	if err != nil {
		t.Fatal(err)
	}
	for f, want := range map[string]string{
		"locales/embed.go":               `//go:embed *.yaml`,
		"locales/en.yaml":                `todo.not_found: Todo not found`,
		"locales/web.zh-CN.yaml":         "app.web: \"Todo\"\ntodo.title: 待办\n",
		"locales/web.en.yaml":            "app.web: \"Todo\"\ntodo.title: Todos\n",
		"apps/web/src/i18n.ts":           `defaultLanguage: "zh-CN"`,
		"apps/web/src/router.tsx":        "i18n,",
		"apps/web/src/routes/__root.tsx": `{ title: i18n.t("app.web") }`,
		"apps/web/src/routes/index.tsx":  `t("todo.add")`,
		"apps/web/src/api.ts":            "fetch: apiFetch, headers: i18n.headers",
		"cmd/todo/main.go":               `httpx.Languages(i18n.MustNew("zh-CN", locales.FS))`,
	} {
		if b, _ := os.ReadFile(filepath.Join(dir, f)); !strings.Contains(string(b), want) {
			t.Errorf("%s missing %q:\n%s", f, want, b)
		}
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

// 生成的 package.json 中的 Bun 版本与框架的 CI 相同。
func TestBunVersionMatchesFramework(t *testing.T) {
	t.Parallel()
	b, err := os.ReadFile("../../../../.github/workflows/ci.yml")
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range regexp.MustCompile(`bun-version: (\S+)`).FindAllSubmatch(b, -1) {
		if string(m[1]) != scaffold.BunVersion {
			t.Errorf("framework CI bun %s, scaffold.BunVersion %s", m[1], scaffold.BunVersion)
		}
	}
}

// 生成的项目带 GitHub Actions 的 CI：两个任务 check、build，是合法的 YAML。
func TestWriteCI(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "todo")
	if err := scaffold.Write(dir, scaffold.Options{Module: "example.com/todo", Name: "Todo", TenonVersion: "v0.0.0"}); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(dir, ".github", "workflows", "ci.yml"))
	if err != nil {
		t.Fatal(err)
	}
	var ci struct {
		Jobs map[string]struct {
			Steps []map[string]any `yaml:"steps"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal(b, &ci); err != nil {
		t.Fatal(err)
	}
	if len(ci.Jobs) != 2 || len(ci.Jobs["check"].Steps) == 0 || len(ci.Jobs["build"].Steps) == 0 {
		t.Errorf("jobs %+v", ci.Jobs)
	}
	if pkg, _ := os.ReadFile(filepath.Join(dir, "package.json")); !strings.Contains(string(pkg), `"packageManager": "bun@`+scaffold.BunVersion+`"`) {
		t.Errorf("package.json:\n%s", pkg)
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
			"src/routes/_app.tsx":       `<RailLayout title={"后台"} user=`,
			"src/routes/_app/index.tsx": `createFileRoute("/_app/")`,
			"src/routes/login.tsx":      `<CenteredAuthLayout brand={"后台"} title={"登录"}>`,
			"src/auth.ts":               "createSession",
		}, []string{"src/routes/index.tsx", "src/api.ts", "src/i18n.ts", "../../locales/admin-console.zh-CN.yaml"}},
		{"frame", scaffold.Frontend{Layout: "frame"}, false, map[string]string{
			"src/theme.ts":        `preset: "compact", primary: "blue-gray", datetimeFormat: "MM/DD HH:mm"`,
			"src/routes/_app.tsx": `<FrameLayout title={"后台"} tabs user=`,
		}, nil},
		{"site", scaffold.Frontend{Layout: "site"}, true, map[string]string{
			"src/theme.ts":          `createUiTheme({ preset: "spacious", primary: "blue-gray"`,
			"src/routes/index.tsx":  `actions={<Button size="sm">{"开始使用"}</Button>}`,
			"src/routes/__root.tsx": `import "@fontsource-variable/inter/index.css"`,
			"package.json":          `"@fontsource-variable/inter"`,
			"vite.config.ts":        `tenon({ prerender: ["/"] })`,
		}, []string{"src/auth.ts", "src/routes/login.tsx"}},
		// 各个维度互相独立：后台的布局可以换登录页、去掉登录、改为服务端渲染，官网可以加上登录。
		{"rail split", scaffold.Frontend{Layout: "rail", Login: "split"}, false, map[string]string{
			"src/routes/login.tsx": `<SplitAuthLayout brand={"后台"} title={"登录"}>`,
		}, nil},
		{"frame no login", scaffold.Frontend{Layout: "frame", Login: "none"}, false, map[string]string{
			"src/routes/_app.tsx": `createFileRoute("/_app")({ component: Layout })`,
		}, []string{"src/auth.ts", "src/routes/login.tsx"}},
		{"frame ssr", scaffold.Frontend{Layout: "frame", Render: "ssr"}, true, map[string]string{
			"src/routes/_app.tsx": `({ ssr: false, beforeLoad: requireAuth, component: Layout })`,
			"vite.config.ts":      "plugins: [tenon()],",
		}, nil},
		{"site login", scaffold.Frontend{Layout: "site", Login: "centered"}, true, map[string]string{
			"src/routes/index.tsx": `<ButtonLink to="/login" size="sm">{"登录"}</ButtonLink>`,
			"src/router.tsx":       "session,",
			"src/auth.ts":          "createSession",
		}, nil},
		{"multi-language rail", scaffold.Frontend{Layout: "rail", Languages: []string{"zh-CN", "en"}}, false, map[string]string{
			"src/router.tsx":                         "id: \"admin-console\",\n    routeTree,\n    i18n,",
			"src/routes/_app.tsx":                    `<RailLayout title={t("app.admin-console")} user=`,
			"src/routes/login.tsx":                   `<CenteredAuthLayout brand={t("app.admin-console")} title={t("signin.title")}>`,
			"src/i18n.ts":                            `createI18n({ messages, defaultLanguage: "zh-CN" })`,
			"../../locales/admin-console.zh-CN.yaml": "app.admin-console: \"后台\"\nhome.title: 首页\nhome.start: 从这里开始。\nsignin.title: 登录\n",
			"../../locales/admin-console.en.yaml":    "app.admin-console: \"后台\"\nhome.title: Home\nhome.start: Start here.\nsignin.title: Sign in\n",
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
			"src/router.tsx": "id: \"admin-console\",\n    routeTree,",
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
		"clients/admin-desktop/assets/tray.png":      "\x89PNG", // 图标原样复制，不经过模板
		"clients/admin-desktop/build/icon.png":       "\x89PNG",
		"apps/admin/src/routes/oauth/consent.tsx":    `createFileRoute("/oauth/consent")`,
		"apps/admin/src/routes/login.tsx":            "<BrowserSignIn signIn={desktop.auth.signIn}",
		"apps/admin/src/auth.ts":                     "await host()?.auth.signOut()",
	} {
		b, err := os.ReadFile(filepath.Join(root, f))
		if err != nil || !strings.Contains(string(b), want) {
			t.Errorf("%s: want %q, err %v\n%s", f, want, err, b)
		}
	}

	// 多语言前端加桌面端时，授权页文字追加到各语言的翻译文件末尾。
	multiRoot := t.TempDir()
	langs := []string{"zh-CN", "en"}
	if err := scaffold.WriteFrontend(multiRoot, scaffold.Frontend{Scope: "todo", Name: "admin", Base: "/admin/", Title: "Admin", Layout: "frame", Languages: langs}); err != nil {
		t.Fatal(err)
	}
	if _, err := scaffold.WriteClient(multiRoot, scaffold.Client{Scope: "todo", Name: "admin-desktop", Kind: "electron", Frontend: "admin", Base: "/admin/", Title: "管理后台", Languages: langs}); err != nil {
		t.Fatal(err)
	}
	for f, want := range map[string]string{
		"locales/admin.zh-CN.yaml": "signin.title: 登录\n\n# 授权页（src/routes/oauth/consent.tsx）\nconsent.title: 授权\n",
		"locales/admin.en.yaml":    "signin.title: Sign in\n\n# OAuth consent page (src/routes/oauth/consent.tsx)\nconsent.title: Authorize\n",
	} {
		b, err := os.ReadFile(filepath.Join(multiRoot, f))
		if err != nil || !strings.Contains(string(b), want) {
			t.Errorf("multi %s: want %q, err %v\n%s", f, want, err, b)
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

// 模板中的名称（项目名、标题）不直接与汉字相连：名称是英文时中间要加空格，是中文时不加，模板无法兼顾，
// 所以写成不把名称嵌在中文句子中的形式，例如标题是名称，正文写“这是一个 Tenon 应用”。
func TestNamesStandApartFromChinese(t *testing.T) {
	t.Parallel()
	touching := regexp.MustCompile(`\p{Han}\[\[\.(Name|Title)\]\]|\[\[\.(Name|Title)\]\]\p{Han}`)
	templates := os.DirFS("template")
	err := fs.WalkDir(templates, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := fs.ReadFile(templates, p)
		if err != nil {
			return err
		}
		for i, line := range strings.Split(string(b), "\n") {
			if touching.MatchString(line) {
				t.Errorf("%s:%d: a name touches Chinese text: %s", p, i+1, line)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// TestDistIgnore 检查新项目的 apps/dist：模板中的 .gitignore 与 tenon build 写回的相同；git 提交它，
// 不提交目录中的清单和构建产物，刚 clone 的项目中 go:embed all:dist 也有文件可嵌入。
func TestDistIgnore(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "todo")
	if err := scaffold.Write(dir, scaffold.Options{Module: "example.com/todo", Name: "Todo", TenonVersion: "v0.0.0"}); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "apps/dist/.gitignore")); string(b) != dev.DistIgnore {
		t.Fatalf("apps/dist/.gitignore differs from dev.DistIgnore:\n%s", b)
	}
	for _, f := range []string{"apps/dist/frontends.json", "apps/dist/web/index.html", "apps/web/dist/client/a.js"} {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, f)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, f), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	git := func(args ...string) string {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return string(out)
	}
	git("init", "-q")
	got := git("ls-files", "--others", "--exclude-standard", "apps")
	if !strings.Contains(got, "apps/dist/.gitignore\n") || strings.Contains(got, "frontends.json") || strings.Contains(got, "index.html") || strings.Contains(got, "a.js") {
		t.Fatalf("files git would add under apps:\n%s", got)
	}
}

// generatedClient 是生成的客户端 api.ts 中与 FindClient 有关的部分：登录、退出在 auth 分组，读取当前用户在 user 分组。
const generatedClient = `export function createClient(options: ClientOptions) {
  const rt = new Runtime(options)
  return {
    /** operationId 以 auth- 开头的接口。 */
    auth: {
      /**
       * 用邮箱和密码登录
       *
       * POST /auth/login
       */
      login: (body: LoginBody): Promise<void> =>
        rt.request<void>({ method: "POST", path: ` + "`/auth/login`" + `, public: true, body }),
      /**
       * POST /auth/logout
       */
      logout: (): Promise<void> =>
        rt.request<void>({ method: "POST", path: ` + "`/auth/logout`" + ` }),
    },
    user: {
      /**
       * GET /me
       */
      me: (): Promise<Profile> =>
        rt.request<Profile>({ method: "GET", path: ` + "`/me`" + ` }),
    },
  }
}
`

func TestFindClient(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	if _, ok, err := scaffold.FindClient(root, "admin"); ok || err != nil {
		t.Fatalf("没有生成的客户端：ok=%v err=%v", ok, err)
	}
	dir := filepath.Join(root, "packages", "client", "src", "admin")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "api.ts"), []byte(generatedClient), 0o644); err != nil {
		t.Fatal(err)
	}
	calls, ok, err := scaffold.FindClient(root, "admin")
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	if want := (scaffold.AuthCalls{Login: "auth.login", Logout: "auth.logout", Me: "user.me"}); calls != want {
		t.Fatalf("%+v，期望 %+v", calls, want)
	}
}

func TestWriteFrontendWithClient(t *testing.T) {
	t.Parallel()
	read := func(dir, name string) string {
		b, _ := os.ReadFile(filepath.Join(dir, name))
		return string(b)
	}
	base := scaffold.Frontend{Scope: "helpdesk", Name: "admin", Base: "/admin/", Title: "后台", Layout: "frame", API: "/admin/api", Client: "admin"}

	// 客户端中有登录相关的接口：会话、客户端和 kit 都在 api.ts 中，auth.ts 调用生成的方法。
	root := t.TempDir()
	f := base
	f.Auth = scaffold.AuthCalls{Login: "auth.login", Logout: "auth.logout", Me: "user.me"}
	if err := scaffold.WriteFrontend(root, f); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, "apps", "admin")
	api, auth := read(dir, "src/api.ts"), read(dir, "src/auth.ts")
	for _, want := range []string{
		`import { createClient } from "@helpdesk/client/admin"`,
		`import { createKit } from "@helpdesk/client/admin/kit"`,
		"export const session = createSession({ load: async () => ({ account: (await api.user.me()).id }) })",
		`export const api = createClient({ baseURL: "/admin/api", fetch: apiFetch, session })`,
		`export const kit = createKit(api, { scope: session.scope, language: "zh-CN" })`,
	} {
		if !strings.Contains(api, want) {
			t.Errorf("api.ts missing %q:\n%s", want, api)
		}
	}
	for _, want := range []string{`import { api, session } from "./api"`, "api.user.me()", "api.auth.login(values)", "await api.auth.logout()"} {
		if !strings.Contains(auth, want) {
			t.Errorf("auth.ts missing %q:\n%s", want, auth)
		}
	}
	if strings.Contains(auth, "send(") || strings.HasPrefix(auth, "\n") || !strings.HasSuffix(auth, "}\n") {
		t.Errorf("auth.ts:\n%q", auth)
	}

	// 客户端中没有登录相关的接口：auth.ts 自己发请求，api.ts 用它的会话。
	root = t.TempDir()
	if err := scaffold.WriteFrontend(root, base); err != nil {
		t.Fatal(err)
	}
	dir = filepath.Join(root, "apps", "admin")
	api, auth = read(dir, "src/api.ts"), read(dir, "src/auth.ts")
	if !strings.Contains(api, `import { session } from "./auth"`) || strings.Contains(api, "createSession") {
		t.Errorf("api.ts:\n%s", api)
	}
	if !strings.Contains(auth, `send("POST", "/auth/login", values)`) || !strings.Contains(auth, "/admin/api${path}") {
		t.Errorf("auth.ts:\n%s", auth)
	}

	// 没有登录页：api.ts 中没有会话。
	root = t.TempDir()
	f = base
	f.Login = "none"
	if err := scaffold.WriteFrontend(root, f); err != nil {
		t.Fatal(err)
	}
	api = read(filepath.Join(root, "apps", "admin"), "src/api.ts")
	if strings.Contains(api, "session") || !strings.Contains(api, `createClient({ baseURL: "/admin/api", fetch: apiFetch })`) || !strings.Contains(api, `createKit(api, { language: "zh-CN" })`) {
		t.Errorf("api.ts without login:\n%s", api)
	}

	// 多语言前端：api.ts 接入 i18n.headers 和 i18n.language。
	root = t.TempDir()
	f = base
	f.Login = "none"
	f.Languages = []string{"zh-CN", "en"}
	if err := scaffold.WriteFrontend(root, f); err != nil {
		t.Fatal(err)
	}
	api = read(filepath.Join(root, "apps", "admin"), "src/api.ts")
	if !strings.Contains(api, `createClient({ baseURL: "/admin/api", fetch: apiFetch, headers: i18n.headers })`) || !strings.Contains(api, `createKit(api, { language: i18n.language })`) {
		t.Errorf("multi-language api.ts:\n%s", api)
	}
}

func TestWriteDeploy(t *testing.T) {
	t.Parallel()
	read := func(dir, name string) string {
		t.Helper()
		b, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(name)))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		return string(b)
	}

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".gitignore"), []byte("bin/\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	files, err := scaffold.WriteDeploy(dir, scaffold.Deploy{
		Slug:     "omirouter",
		Target:   "partner-acme",
		Instance: "acme-router",
		User:     "ops",
		Host:     "prod.example.com",
		HasSSR:   true,
	})
	if err != nil || len(files) != 5 {
		t.Fatalf("WriteDeploy: files=%v err=%v", files, err)
	}
	appYAML := read(dir, "deploy/app.yaml")
	for _, want := range []string{"app: omirouter", "has_ssr: true", "user: ops"} {
		if !strings.Contains(appYAML, want) {
			t.Errorf("deploy/app.yaml missing %q:\n%s", want, appYAML)
		}
	}
	targetYAML := read(dir, "deploy/targets/partner-acme/target.yaml")
	for _, want := range []string{"instance: acme-router", "user: ops", "- prod.example.com"} {
		if !strings.Contains(targetYAML, want) {
			t.Errorf("target.yaml missing %q:\n%s", want, targetYAML)
		}
	}
	targetCfg := read(dir, "deploy/targets/partner-acme/config.yaml")
	if !strings.Contains(targetCfg, "service: acme-router") {
		t.Errorf("config.yaml missing instance service:\n%s", targetCfg)
	}
	exampleTarget := read(dir, "deploy/example/target.yaml")
	if !strings.Contains(exampleTarget, "roles:") || !strings.Contains(exampleTarget, "instance:") {
		t.Errorf("example target.yaml:\n%s", exampleTarget)
	}
	if gi := read(dir, ".gitignore"); !strings.Contains(gi, "deploy/targets/") {
		t.Errorf(".gitignore missing deploy/targets/:\n%s", gi)
	}

	// 重复写入默认拒绝，传入 Force 允许覆盖。
	if _, err := scaffold.WriteDeploy(dir, scaffold.Deploy{Slug: "omirouter", Target: "partner-acme"}); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Errorf("second write without force: %v", err)
	}
	if _, err := scaffold.WriteDeploy(dir, scaffold.Deploy{Slug: "omirouter", Target: "partner-acme", Force: true}); err != nil {
		t.Fatalf("overwrite with force: %v", err)
	}
	if overwritten := read(dir, "deploy/app.yaml"); !strings.Contains(overwritten, "user: deploy") {
		t.Errorf("default user after overwrite:\n%s", overwritten)
	}
}
