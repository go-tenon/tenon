//go:build unix

package main

import (
	"bytes"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-tenon/tenon/cmd/tenon/internal/dev"
)

// fake 记录参数；go list -m 输出框架的目录；go build -mod=mod -o BIN（generate 编译应用）写出一个记录参数的伪造应用；
// bun run build（vite build）写出浏览器的构建：页面外壳、脚本和 source map。
const fake = `#!/bin/sh
if [ "$1 $2" = "list -m" ]; then echo "$TENON_FRAMEWORK"; exit 0; fi
echo "$(basename "$0") $* @$(basename "$PWD")${GOOS:+ GOOS=$GOOS}${GOARCH:+ GOARCH=$GOARCH}" >> "$TENON_LOG"
if [ "$1 $2" = "env GOARCH" ]; then echo arm64; exit 0; fi
if [ "$1 $2" = "run build" ]; then
	mkdir -p dist/client/assets dist/client/.well-known
	echo shell > dist/client/_shell.html; echo js > dist/client/assets/a.js; echo map > dist/client/assets/a.js.map; echo x > dist/client/.well-known/x
fi
if [ "$1 $2 $3" = "build -mod=mod -o" ]; then
	printf '#!/bin/sh\necho "app $*" >> "$TENON_LOG"\n' > "$4"; chmod +x "$4"
fi
`

func newProject(t *testing.T, files map[string]string, tools ...string) string {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, ".bin")
	_ = os.Mkdir(bin, 0o755)
	for _, name := range tools {
		if err := os.WriteFile(filepath.Join(bin, name), []byte(fake), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	files = maps.Clone(files)
	if files == nil {
		files = map[string]string{}
	}
	// 应用的入口：tenon.json 没有写 main 时是 cmd 下唯一的目录。
	if !strings.Contains(files["tenon.json"], `"main"`) {
		files["cmd/blog/main.go"] = "package main\n"
	}
	for name, content := range files {
		p := filepath.Join(dir, name)
		_ = os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	log := filepath.Join(dir, ".log")
	t.Setenv("TENON_LOG", log)
	framework, _ := filepath.Abs("../..")
	t.Setenv("TENON_FRAMEWORK", framework)
	t.Setenv("PATH", bin+":/usr/bin:/bin")
	t.Chdir(dir)
	return log
}

func execute(t *testing.T, args ...string) error {
	t.Helper()
	cmd := root()
	cmd.SetArgs(args)
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	return cmd.Execute()
}

// frontends 是两个前端：web 挂在 / 并在服务端渲染，admin 挂在 /admin/ 只在浏览器中渲染。
var frontends = map[string]string{
	"package.json":              "{}",
	"apps/web/vite.config.ts":   "",
	"apps/web/package.json":     `{"tenon": {"base": "/", "ssr": true}}`,
	"apps/admin/vite.config.ts": "",
	"apps/admin/package.json":   `{"tenon": {"base": "/admin/"}}`,
}

func TestBuild(t *testing.T) {
	log := newProject(t, frontends, "go", "bun")
	if err := os.MkdirAll("apps/dist/old", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := execute(t, "build"); err != nil {
		t.Fatal(err)
	}
	// 在根目录安装一次，逐个构建前端，按 Go 的 GOARCH 把 SSR 前端编译成一个可执行文件，最后编译应用并注入版本元数据。
	l, _ := os.ReadFile(log)
	lines := strings.Split(strings.TrimSpace(string(l)), "\n")
	dir, _ := os.Getwd()
	want := []string{
		"go env GOARCH @",
		"bun install --frozen-lockfile @",
		"bun run build @admin",
		"bun run build @web",
		"bun x tenon-start compile --out " + dir + "/apps/dist/ssr web=" + dir + "/apps/web/dist/server/server.js @web GOARCH=arm64",
		"go build -trimpath -ldflags ",
	}
	if len(lines) != len(want) {
		t.Fatalf("log:\n%s", l)
	}
	for i, w := range want {
		if !strings.HasPrefix(lines[i], w) {
			t.Fatalf("line %d: want %q\nlog:\n%s", i, w, l)
		}
	}
	if !strings.Contains(lines[5], "-o bin/blog ./cmd/blog") || !strings.Contains(lines[5], "command.buildVersion=") || !strings.Contains(lines[5], "telemetry.buildVersionOverride=") {
		t.Fatalf("go build line missing ldflags/output:\n%s", lines[5])
	}
	// 每个前端的浏览器构建复制到 apps/dist/<名称>，不带 source map；apps/dist 每次从头生成。
	for _, f := range []string{"apps/dist/admin/_shell.html", "apps/dist/admin/assets/a.js", "apps/dist/admin/.well-known/x", "apps/dist/web/assets/a.js"} {
		if _, err := os.Stat(f); err != nil {
			t.Error(err)
		}
	}
	for _, f := range []string{"apps/dist/admin/assets/a.js.map", "apps/dist/old"} {
		if _, err := os.Stat(f); err == nil {
			t.Errorf("%s should not exist", f)
		}
	}
	manifest, _ := os.ReadFile("apps/dist/frontends.json")
	if !strings.Contains(string(manifest), `"name": "admin",`) || !strings.Contains(string(manifest), `"ssr": true`) {
		t.Errorf("manifest:\n%s", manifest)
	}
	// 提交在 apps/dist 中的 .gitignore 在清空之后写回去。
	if b, _ := os.ReadFile("apps/dist/.gitignore"); string(b) != dev.DistIgnore {
		t.Errorf("apps/dist/.gitignore:\n%s", b)
	}
}

// 没有服务端渲染的前端：不编译 Bun 可执行文件，程序中不带 Bun；二次构建命中指纹缓存跳过前端重建，--no-cache 强制重建。
func TestBuildWithoutSSR(t *testing.T) {
	log := newProject(t, map[string]string{"package.json": "{}", "apps/admin/vite.config.ts": "", "apps/admin/package.json": `{"tenon": {"base": "/admin/"}}`}, "go", "bun")
	if err := execute(t, "build", "--version", "v1.2.3"); err != nil {
		t.Fatal(err)
	}
	l, _ := os.ReadFile(log)
	lines := strings.Split(strings.TrimSpace(string(l)), "\n")
	want := []string{"bun install --frozen-lockfile @", "bun run build @admin", "go build -trimpath -ldflags "}
	if len(lines) != len(want) {
		t.Fatalf("log:\n%s", l)
	}
	for i, w := range want {
		if !strings.HasPrefix(lines[i], w) {
			t.Fatalf("log:\n%s", l)
		}
	}
	if !strings.Contains(lines[2], "command.buildVersion=v1.2.3") || !strings.Contains(lines[2], "-o bin/blog ./cmd/blog") {
		t.Fatalf("go build line: %s", lines[2])
	}
	if _, err := os.Stat("apps/dist/ssr"); err == nil {
		t.Error("ssr executable built without an SSR frontend")
	}

	// 二次构建未改前端源码，应命中指纹缓存，仅执行 go build。
	_ = os.WriteFile(log, nil, 0o644)
	if err := execute(t, "build", "--version", "v1.2.3"); err != nil {
		t.Fatal(err)
	}
	l2, _ := os.ReadFile(log)
	lines2 := strings.Split(strings.TrimSpace(string(l2)), "\n")
	if len(lines2) != 1 || !strings.HasPrefix(lines2[0], "go build -trimpath -ldflags ") {
		t.Fatalf("expected cache hit with only go build, got:\n%s", l2)
	}

	// 修改前端源码后指纹失效，自动重建前端。
	if err := os.WriteFile("apps/admin/vite.config.ts", []byte("// changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(log, nil, 0o644)
	if err := execute(t, "build", "--version", "v1.2.4"); err != nil {
		t.Fatal(err)
	}
	l3, _ := os.ReadFile(log)
	if !strings.Contains(string(l3), "bun run build @admin") {
		t.Fatalf("expected frontend rebuild after source change, got:\n%s", l3)
	}

	// --no-cache 强制忽略缓存重建前端。
	_ = os.WriteFile(log, nil, 0o644)
	if err := execute(t, "build", "--no-cache", "--version", "v1.2.4"); err != nil {
		t.Fatal(err)
	}
	l4, _ := os.ReadFile(log)
	if !strings.Contains(string(l4), "bun run build @admin") {
		t.Fatalf("expected frontend rebuild with --no-cache, got:\n%s", l4)
	}
}

// --arch 交叉构建：SSR 可执行文件和应用都按目标架构编译（Linux），不读取本机的 GOARCH。
func TestBuildArch(t *testing.T) {
	log := newProject(t, map[string]string{"package.json": "{}", "apps/web/vite.config.ts": "", "apps/web/package.json": `{"tenon": {"base": "/", "ssr": true}}`}, "go", "bun")
	if err := execute(t, "build", "--arch", "amd64"); err != nil {
		t.Fatal(err)
	}
	l, _ := os.ReadFile(log)
	lines := strings.Split(strings.TrimSpace(string(l)), "\n")
	if len(lines) != 4 || !strings.HasPrefix(lines[0], "bun install --frozen-lockfile @") || lines[1] != "bun run build @web" ||
		!strings.Contains(lines[2], "tenon-start compile --out ") || !strings.HasSuffix(lines[2], "GOOS=linux GOARCH=amd64") ||
		!strings.HasPrefix(lines[3], "go build -trimpath -ldflags ") || !strings.Contains(lines[3], "-o bin/blog ./cmd/blog") || !strings.HasSuffix(lines[3], "GOOS=linux GOARCH=amd64") {
		t.Fatalf("log:\n%s", l)
	}
	if err := execute(t, "build", "--arch", "386"); err == nil || !strings.Contains(err.Error(), "amd64 or arm64") {
		t.Fatalf("bad arch: %v", err)
	}
}

func TestBuildWithoutFrontend(t *testing.T) {
	log := newProject(t, map[string]string{"tenon.json": `{"main": "./cmd/web"}`}, "go")
	if err := execute(t, "build", "-o", "out/web"); err != nil {
		t.Fatal(err)
	}
	if l, _ := os.ReadFile(log); !strings.HasPrefix(string(l), "go build -trimpath -ldflags ") || !strings.Contains(string(l), "-o out/web ./cmd/web") || strings.Count(string(l), "\n") != 1 {
		t.Fatalf("log:\n%s", l)
	}
}

func TestGenerate(t *testing.T) {
	log := newProject(t, map[string]string{"apps/web/vite.config.ts": "", "apps/web/package.json": `{"tenon": {"base": "/"}}`,
		"packages/client/package.json": "{}", "tenon.json": `{"apis": {"/api/v1": {"name": "public", "go": "../api"}}}`}, "go")
	if err := execute(t, "generate"); err != nil {
		t.Fatal(err)
	}
	l, _ := os.ReadFile(log)
	for _, want := range []string{"app client --ts packages/client/src --name /api/v1=public\n", "app client --base-path /api/v1 --out ../api\n"} {
		if !strings.Contains(string(l), want) {
			t.Fatalf("log missing %q:\n%s", want, l)
		}
	}
	for _, f := range []string{"apps/dist/frontends.json", "apps/dist/.gitignore", "tenon/js/client/package.json"} {
		if _, err := os.Stat(f); err != nil {
			t.Errorf("%s: %v", f, err)
		}
	}
	newProject(t, nil, "go")
	if err := execute(t, "generate"); err == nil || !strings.Contains(err.Error(), "nothing to generate") {
		t.Fatalf("err = %v", err)
	}
}

func TestAddFrontend(t *testing.T) {
	log := newProject(t, map[string]string{
		"package.json":            "{\n  \"name\": \"helpdesk\",\n  \"workspaces\": [\"apps/web\", \"packages/client\"]\n}\n",
		"apps/web/vite.config.ts": "",
		"apps/web/package.json":   `{"tenon": {"base": "/", "ssr": true}}`,
	}, "go", "bun")
	cmd := root()
	var out bytes.Buffer
	cmd.SetArgs([]string{"add", "frontend", "admin", "--api", "/admin/api"})
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	if err := cmd.Execute(); err != nil {
		t.Fatal(err, out.String())
	}
	for name, want := range map[string]string{
		"apps/admin/package.json":                `"tenon": { "base": "/admin/", "ssr": false },`,
		"apps/dist/frontends.json":               `"base": "/admin/",`,
		"apps/admin/src/theme.ts":                `preset: "comfortable"`,
		"apps/admin/src/routes/login.tsx":        "LoginForm",
		"apps/admin/src/auth.ts":                 "session.fetch(fetch)(`/admin/api${path}`",
		"package.json":                           `"apps/admin",`,
		"tenon/js/client/package.json":           `"name": "@go-tenon/client"`,
		"tenon/docs/README.md":                   "# Tenon 文档",
		".mcp.json":                              `"tenon"`,
		".claude/skills/tenon-frontend/SKILL.md": "name: tenon-frontend",
		".agents/skills/tenon-action/SKILL.md":   "name: tenon-action",
	} {
		if b, _ := os.ReadFile(name); !strings.Contains(string(b), want) {
			t.Errorf("%s missing %q:\n%s", name, want, b)
		}
	}
	// 安装依赖之后为新的前端生成路由树。
	if b, _ := os.ReadFile(log); !strings.Contains(string(b), "bun x tenon-start routes @admin") {
		t.Errorf("route tree not generated:\n%s", b)
	}
	if !strings.Contains(out.String(), "已生成 apps/admin，挂在 /admin/，页面只在浏览器中渲染") {
		t.Errorf("output:\n%s", out.String())
	}
	for args, want := range map[string]string{
		"add frontend admin":              "already exists",
		"add frontend Admin":              "must match",
		"add frontend ops --base /ops":    "must start and end with /",
		"add frontend ops --base /":       "apps/web is already mounted at /",
		"add frontend ops --layout antd":  `layout "antd" is not one of`,
		"add frontend ops --login modal":  `login "modal" is not one of`,
		"add frontend ops --preset dense": `preset "dense" is not one of`,
		"add frontend ops --render spa":   `render "spa" is not one of`,
		"add frontend ops --api ops/api":  `api "ops/api" must start with /`,
		"add frontend ops --api /ops/":    `api "/ops/" must start with / and not end with /`,
	} {
		if err := execute(t, strings.Fields(args)...); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: err = %v", args, err)
		}
	}
}

func TestCheck(t *testing.T) {
	newProject(t, map[string]string{
		"apps/web/vite.config.ts":    "",
		"apps/web/package.json":      `{"tenon": {"base": "/"}}`,
		"apps/web/src/page.tsx":      `export const title = "写死的中文"; export const color = "#ff0000"`,
		"migrations/0001_create.sql": `CREATE TABLE items (id uuid PRIMARY KEY, created_at timestamptz DEFAULT NOW());`,
	})
	// 默认单语言项目跳过 text，报告 colors 与 clock（2 项）。
	if err := execute(t, "check"); err == nil || !strings.Contains(err.Error(), "2 check violations") {
		t.Fatalf("single-language check = %v", err)
	}
	// 配置多种语言后自动启用 text 检查（3 项）。
	if err := os.WriteFile("tenon.json", []byte(`{"languages": ["zh-CN", "en"]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := execute(t, "check"); err == nil || !strings.Contains(err.Error(), "3 check violations") {
		t.Fatalf("multi-language check = %v", err)
	}
	if err := execute(t, "check", "--only", "clock"); err == nil || !strings.Contains(err.Error(), "1 check violations") {
		t.Fatalf("check --only clock = %v", err)
	}
	if err := execute(t, "check", "--only", "errors"); err != nil {
		t.Fatalf("check --only errors = %v", err)
	}
}

func TestAddLanguage(t *testing.T) {
	newProject(t, map[string]string{
		"AGENTS.md":               "# Blog\n\n<!-- tenon:begin -->\n<!-- tenon:end -->\n",
		"apps/web/vite.config.ts": "",
		"apps/web/package.json":   `{"tenon": {"base": "/"}}`,
	}, "go")
	cmd := root()
	var out bytes.Buffer
	cmd.SetArgs([]string{"add", "language", "en"})
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	if err := cmd.Execute(); err != nil {
		t.Fatal(err, out.String())
	}
	for name, want := range map[string]string{
		"tenon.json":                   `"languages": [`,
		"locales/embed.go":             `//go:embed *.yaml`,
		"locales/en.yaml":              `服务端 en 翻译文件`,
		"locales/web.zh-CN.yaml":       `app.web: "web"`,
		"locales/web.en.yaml":          `app.web: "web"`,
		"apps/web/src/i18n.ts":         `defaultLanguage: "zh-CN"`,
		"apps/web/src/messages.gen.ts": `"en":`,
		"AGENTS.md":                    "页面用 `useT`",
	} {
		b, err := os.ReadFile(name)
		if err != nil || !strings.Contains(string(b), want) {
			t.Errorf("%s: err=%v want %q\n%s", name, err, want, b)
		}
	}
	if err := execute(t, "add", "language", "en"); err == nil || !strings.Contains(err.Error(), "already configured") {
		t.Fatalf("duplicate language = %v", err)
	}
	if err := execute(t, "add", "language", "not a lang!"); err == nil || !strings.Contains(err.Error(), "not a valid language tag") {
		t.Fatalf("invalid language = %v", err)
	}
}

func TestAddDeployAndDryRun(t *testing.T) {
	newProject(t, map[string]string{
		"go.mod":                  "module example.com/blog\n\ngo 1.27.4\n",
		"package.json":            `{"name": "blog", "packageManager": "bun@1.4.4"}`,
		"apps/web/vite.config.ts": "",
		"apps/web/package.json":   `{"tenon": {"base": "/", "ssr": false}}`,
	})
	if err := os.WriteFile("README.md", []byte("# App\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := root()
	var out bytes.Buffer
	cmd.SetArgs([]string{"add", "deploy", "--user", "ops", "--host", "prod.example.com"})
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	if err := cmd.Execute(); err != nil {
		t.Fatal(err, out.String())
	}
	if !strings.Contains(out.String(), "deploy.yaml") {
		t.Errorf("output:\n%s", out.String())
	}
	for name, want := range map[string]string{
		"deploy.yaml":  "- prod.example.com",
		".env.example": "DATABASE_URL=",
		"README.md":    "<!-- tenon:deploy:begin -->",
	} {
		b, err := os.ReadFile(name)
		if err != nil || !strings.Contains(string(b), want) {
			t.Errorf("%s: err=%v want %q\n%s", name, err, want, b)
		}
	}
	if err := execute(t, "add", "deploy"); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("second add deploy without --force = %v", err)
	}
	if err := execute(t, "add", "deploy", "--force"); err != nil {
		t.Fatalf("add deploy --force = %v", err)
	}

	// 验证生成的 deploy.yaml 可直接驱动 tenon deploy --dry-run（text 与 json 双模）与 setup --dry-run。
	out.Reset()
	dry := root()
	dry.SetArgs([]string{"deploy", "--dry-run"})
	dry.SetOut(&out)
	dry.SetErr(&out)
	if err := dry.Execute(); err != nil {
		t.Fatalf("deploy --dry-run: %v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "[dry-run] 环境 production") {
		t.Errorf("dry-run output:\n%s", out.String())
	}

	out.Reset()
	dryJSON := root()
	dryJSON.SetArgs([]string{"deploy", "--dry-run", "--format", "json"})
	dryJSON.SetOut(&out)
	dryJSON.SetErr(&out)
	if err := dryJSON.Execute(); err != nil {
		t.Fatalf("deploy --dry-run --format json: %v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), `"dry_run": true`) || !strings.Contains(out.String(), `"waves"`) {
		t.Errorf("dry-run json output:\n%s", out.String())
	}

	out.Reset()
	setupDry := root()
	setupDry.SetArgs([]string{"deploy", "setup", "--dry-run", "--format", "json"})
	setupDry.SetOut(&out)
	setupDry.SetErr(&out)
	if err := setupDry.Execute(); err != nil {
		t.Fatalf("deploy setup --dry-run: %v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), `"unit_path"`) {
		t.Errorf("setup dry-run output:\n%s", out.String())
	}
}
