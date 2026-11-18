//go:build unix

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fake 记录参数；go list -m 输出框架的目录；go build -mod=mod -o BIN（generate 编译应用）写出一个记录参数的伪造应用。
const fake = `#!/bin/sh
if [ "$1 $2" = "list -m" ]; then echo "$TENON_FRAMEWORK"; exit 0; fi
echo "$(basename "$0") $* @$(basename "$PWD")${GOOS:+ GOOS=$GOOS}${GOARCH:+ GOARCH=$GOARCH}" >> "$TENON_LOG"
if [ "$1 $2" = "env GOARCH" ]; then echo arm64; exit 0; fi
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

func TestBuild(t *testing.T) {
	log := newProject(t, map[string]string{"package.json": "{}", "apps/web/vite.config.ts": "", "apps/admin/vite.config.ts": ""}, "go", "bun")
	if err := execute(t, "build"); err != nil {
		t.Fatal(err)
	}
	// 在根目录安装一次，逐个编译前端的 SSR 代码，按 Go 的 GOARCH 构建一次共用的运行时，最后编译应用。
	l, _ := os.ReadFile(log)
	lines := strings.Split(strings.TrimSpace(string(l)), "\n")
	want := []string{"bun install --frozen-lockfile @", "go env GOARCH @", "bun run build @admin GOARCH=arm64",
		"bun run build @web GOARCH=arm64", "bun x tenon-start runtime --out ", "go build -trimpath -o bin/api ./cmd/api @"}
	if len(lines) != len(want) {
		t.Fatalf("log:\n%s", l)
	}
	for i, w := range want {
		if !strings.HasPrefix(lines[i], w) {
			t.Fatalf("log:\n%s", l)
		}
	}
}

// --arch 交叉构建：前端和应用都按目标架构编译（Linux），不读取本机的 GOARCH。
func TestBuildArch(t *testing.T) {
	log := newProject(t, map[string]string{"package.json": "{}", "apps/web/vite.config.ts": ""}, "go", "bun")
	if err := execute(t, "build", "--arch", "amd64"); err != nil {
		t.Fatal(err)
	}
	l, _ := os.ReadFile(log)
	lines := strings.Split(strings.TrimSpace(string(l)), "\n")
	want := []string{"bun install --frozen-lockfile @", "bun run build @web GOOS=linux GOARCH=amd64",
		"go build -trimpath -o bin/api ./cmd/api @", "GOOS=linux GOARCH=amd64"}
	if len(lines) != 4 || !strings.HasPrefix(lines[0], want[0]) || !strings.HasPrefix(lines[1], want[1]) ||
		!strings.Contains(lines[2], "tenon-start runtime --out ") || !strings.HasSuffix(lines[2], want[3]) ||
		!strings.HasPrefix(lines[3], want[2]) || !strings.HasSuffix(lines[3], want[3]) {
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
	if l, _ := os.ReadFile(log); !strings.HasPrefix(string(l), "go build -trimpath -o out/web ./cmd/web") || strings.Count(string(l), "\n") != 1 {
		t.Fatalf("log:\n%s", l)
	}
}

func TestGenerate(t *testing.T) {
	log := newProject(t, map[string]string{"apps/web/vite.config.ts": "", "packages/client/package.json": "{}",
		"tenon.json": `{"apis": {"/api/v1": {"name": "public", "go": "../api"}}}`}, "go")
	if err := execute(t, "generate"); err != nil {
		t.Fatal(err)
	}
	l, _ := os.ReadFile(log)
	for _, want := range []string{"app client --ts packages/client/src --name /api/v1=public\n", "app client --base-path /api/v1 --out ../api\n"} {
		if !strings.Contains(string(l), want) {
			t.Fatalf("log missing %q:\n%s", want, l)
		}
	}
	for _, f := range []string{"apps/web/ssr.js", "apps/ssr-runtime.bin", ".tenon/js/client/package.json"} {
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
	newProject(t, map[string]string{
		"go.mod":       "module example.com/helpdesk\n\ngo 1.27\n",
		"package.json": "{\n  \"name\": \"helpdesk\",\n  \"workspaces\": [\"apps/web\", \"packages/client\"]\n}\n",
	}, "go", "bun")
	cmd := root()
	var out bytes.Buffer
	cmd.SetArgs([]string{"add", "frontend", "admin"})
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	if err := cmd.Execute(); err != nil {
		t.Fatal(err, out.String())
	}
	for name, want := range map[string]string{
		"apps/admin/package.json":         `"name": "@helpdesk/admin"`,
		"apps/admin/vite.config.ts":       `base: "/admin/"`,
		"apps/admin/src/theme.ts":         `preset: "comfortable"`,
		"apps/admin/src/routes/login.tsx": "LoginForm",
		"package.json":                    `"apps/admin",`,
		".tenon/js/client/package.json":   `"name": "@go-tenon/client"`,
	} {
		if b, _ := os.ReadFile(name); !strings.Contains(string(b), want) {
			t.Errorf("%s missing %q:\n%s", name, want, b)
		}
	}
	if _, err := os.Stat("apps/admin/ssr.js"); err != nil {
		t.Errorf("placeholder ssr.js: %v", err)
	}
	for _, want := range []string{`adminPages, err := ssr.New(ssr.Options{Runtime: runtime, Bundle: admin.SSR, API: api, DevServer: cfg.DevServers["admin"]})`,
		`"/admin/*"`, `"example.com/helpdesk/apps/admin"`} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output missing %q:\n%s", want, out.String())
		}
	}
	for args, want := range map[string]string{
		"add frontend admin":              "already exists",
		"add frontend Admin":              "must match",
		"add frontend ops --base /ops":    "must start and end with /",
		"add frontend ops --layout antd":  `--layout "antd" is not one of`,
		"add frontend ops --preset dense": `--preset "dense" is not one of`,
	} {
		if err := execute(t, strings.Fields(args)...); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: err = %v", args, err)
		}
	}
}
