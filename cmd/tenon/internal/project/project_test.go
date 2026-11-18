package project_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/go-tenon/tenon/cmd/tenon/internal/project"
)

func write(t *testing.T, dir, name, content string) {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// 没有 tenon.json 时全部用默认值；前端和客户端包按项目中的文件发现。
func TestDefaults(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "cmd/blog/main.go", "package main\n")
	cfg, err := project.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Main != "./cmd/blog" || strings.Join(cfg.Serve, " ") != "serve --config config/app.yaml" || cfg.URL != "http://localhost:8080" ||
		cfg.Frontends != nil || cfg.Client {
		t.Fatalf("empty project %+v", cfg)
	}

	write(t, dir, "config/local.yaml", "")
	write(t, dir, "apps/web/vite.config.ts", "")
	write(t, dir, "apps/web/package.json", `{"tenon": {"base": "/", "ssr": true}}`)
	write(t, dir, "apps/admin/vite.config.mjs", "")
	write(t, dir, "apps/admin/package.json", `{"tenon": {"base": "/admin/"}}`)
	write(t, dir, "apps/notes/README.md", "") // 没有 vite.config 的目录不是前端
	write(t, dir, "apps/.cache/vite.config.ts", "")
	write(t, dir, "packages/client/package.json", "{}")
	cfg, err = project.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	want := []project.Frontend{{Name: "admin", Dir: "apps/admin", Base: "/admin/"}, {Name: "web", Dir: "apps/web", Base: "/", SSR: true}}
	if strings.Join(cfg.Serve, " ") != "serve --config config/app.yaml,config/local.yaml" ||
		!slices.Equal(cfg.Frontends, want) || !cfg.Client {
		t.Fatalf("project %+v", cfg)
	}

	// package.json 的 tenon 字段：挂载路径必须以 / 开头和结尾，两个前端不能挂在同一个路径。
	write(t, dir, "apps/shop/vite.config.ts", "")
	for content, want := range map[string]string{
		`{}`:                             "tenon.base",
		`{"tenon": {"base": "/shop"}}`:   "must start and end with /",
		`{"tenon": {"base": "/admin/"}}`: "both mounted at /admin/",
	} {
		write(t, dir, "apps/shop/package.json", content)
		if _, err := project.Load(dir); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: err = %v, want %q", content, err, want)
		}
	}
	if err := os.RemoveAll(filepath.Join(dir, "apps", "shop")); err != nil {
		t.Fatal(err)
	}

	write(t, dir, "apps/Admin_2/vite.config.ts", "")
	if _, err := project.Load(dir); err == nil || !strings.Contains(err.Error(), "Admin_2") {
		t.Fatalf("invalid frontend name: %v", err)
	}
}

func TestConfigFile(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "tenon.json", `{"$schema": "x", "main": "./cmd/web", "url": "http://localhost:3000",
		"apis": {"/api/v1": {"name": "public", "go": "../api"}},
		"tools": [{"watch": ["esbuild", "--watch"]}], "agents": {"targets": ["codex"]}}`)
	cfg, err := project.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Main != "./cmd/web" || cfg.APIs["/api/v1"].Name != "public" || cfg.APIs["/api/v1"].Go != "../api" ||
		len(cfg.Tools) != 1 || !slices.Equal(cfg.AgentTargets(), []string{"codex"}) {
		t.Fatalf("%+v", cfg)
	}
	// agents.targets 省略时是默认的助手，空数组时不接入任何助手。
	for content, want := range map[string][]string{
		``:                            project.DefaultTargets,
		`, "agents": {}`:              project.DefaultTargets,
		`, "agents": {"targets": []}`: {},
		`, "agents": {"skip": ["tenon-electron"]}`: project.DefaultTargets,
	} {
		write(t, dir, "tenon.json", `{"main": "./cmd/web"`+content+`}`)
		cfg, err := project.Load(dir)
		if err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(cfg.AgentTargets(), want) {
			t.Errorf("%s: targets %v, want %v", content, cfg.AgentTargets(), want)
		}
	}
	for name, content := range map[string]string{
		"unknown field": `{"mian": "./cmd/api"}`,
		"old frontend":  `{"frontend": {"dir": "web"}}`,
		"bad url":       `{"url": "localhost:8080"}`,
		"templ":         `{"templ": {"proxyPort": 7331}}`,
		"empty tool":    `{"tools": [{"build": ["x"]}]}`,
		"api key":       `{"apis": {"api/v1": {"name": "v1"}}}`,
		"api name":      `{"apis": {"/api/v1": {"name": "V1"}}}`,
		"empty api":     `{"apis": {"/api/v1": null}}`,
		"agent":         `{"agents": {"targets": ["copilot"]}}`,
	} {
		write(t, dir, "tenon.json", content)
		if _, err := project.Load(dir); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}

func TestHasConfigFlag(t *testing.T) {
	for args, want := range map[string]bool{
		"serve --config config/app.yaml": true,
		"serve --config=config/app.yaml": true,
		"serve --addr :8080":             false,
	} {
		if got := project.HasConfigFlag(strings.Fields(args)); got != want {
			t.Errorf("%q: %v", args, got)
		}
	}
}

// schema.json 与 Config 的字段一致：编辑器的补全与 tenon 读取的字段相同。
func TestSchemaMatchesConfig(t *testing.T) {
	data, err := os.ReadFile("../../schema.json")
	if err != nil {
		t.Fatal(err)
	}
	var schema map[string]any
	if err := json.Unmarshal(data, &schema); err != nil {
		t.Fatal(err)
	}
	var compare func(where string, typ reflect.Type, props map[string]any)
	compare = func(where string, typ reflect.Type, props map[string]any) {
		var fields []string
		for i := range typ.NumField() {
			f := typ.Field(i)
			name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
			if name == "-" {
				continue
			}
			fields = append(fields, name)
			p, _ := props[name].(map[string]any)
			if p == nil {
				t.Errorf("%s.%s missing from schema.json", where, name)
				continue
			}
			ft := f.Type
			for ft.Kind() == reflect.Pointer || (ft.Kind() == reflect.Slice || ft.Kind() == reflect.Map) && ft.Elem().Kind() != reflect.String {
				switch ft.Kind() {
				case reflect.Slice:
					p, _ = p["items"].(map[string]any)
				case reflect.Map:
					p, _ = p["additionalProperties"].(map[string]any)
				}
				ft = ft.Elem()
			}
			if ft.Kind() == reflect.Struct {
				sub, _ := p["properties"].(map[string]any)
				compare(where+"."+name, ft, sub)
			}
		}
		for name := range props {
			if !slices.Contains(fields, name) {
				t.Errorf("%s.%s in schema.json is not a Config field", where, name)
			}
		}
	}
	compare("tenon", reflect.TypeFor[project.Config](), schema["properties"].(map[string]any))
}

// clients 中带 tenon.json 的目录是客户端；种类和前端要存在。
func TestClients(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	write(t, dir, "cmd/blog/main.go", "package main\n")
	write(t, dir, "apps/admin/vite.config.ts", "")
	write(t, dir, "apps/admin/package.json", `{"tenon": {"base": "/admin/"}}`)
	write(t, dir, "clients/admin-desktop/tenon.json", `{"kind": "electron", "frontend": "admin"}`)
	write(t, dir, "clients/notes/README.md", "") // 没有 tenon.json 的目录不是客户端
	cfg, err := project.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	want := []project.Client{{Name: "admin-desktop", Dir: "clients/admin-desktop", Kind: "electron", Frontend: "admin"}}
	if !slices.Equal(cfg.Clients, want) {
		t.Fatalf("clients %+v", cfg.Clients)
	}
	if cl, ok := cfg.FindClient("admin-desktop"); !ok || cl.Kind != "electron" {
		t.Fatalf("FindClient: %+v %v", cl, ok)
	}
	if f, ok := cfg.FindFrontend("admin"); !ok || f.Base != "/admin/" {
		t.Fatalf("FindFrontend: %+v %v", f, ok)
	}
	for content, want := range map[string]string{
		`{"kind": "swiftui"}`:                               `kind "swiftui" is not one of`,
		`{"kind": "electron", "frontend": "shop"}`:          `frontend "shop" is not in apps`,
		`{"kind": "electron", "frontend": "admin", "x": 1}`: "unknown field",
	} {
		write(t, dir, "clients/admin-desktop/tenon.json", content)
		if _, err := project.Load(dir); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: err = %v, want %q", content, err, want)
		}
	}
}

// 没有写 url 时，应用的地址按 tenon dev 运行应用时加载的配置中的 http.addr 推出。
func TestURLFromAppConfig(t *testing.T) {
	t.Setenv("TENON_TEST_DEV_PORT", "")
	os.Unsetenv("TENON_TEST_DEV_PORT") // 由下面的 .env 提供；测试结束时恢复
	t.Setenv("APP_CONFIG", "")
	load := func(dir string) string {
		t.Helper()
		cfg, err := project.Load(dir)
		if err != nil {
			t.Fatal(err)
		}
		return cfg.URL
	}
	dir := t.TempDir()
	write(t, dir, "cmd/blog/main.go", "package main\n")
	if got := load(dir); got != "http://localhost:8080" {
		t.Fatalf("no config: %s", got)
	}
	write(t, dir, "config/app.yaml", "http:\n  addr: \":8081\"\n")
	if got := load(dir); got != "http://localhost:8081" {
		t.Fatalf("app.yaml: %s", got)
	}
	write(t, dir, ".env", "TENON_TEST_DEV_PORT=9100\n")
	write(t, dir, "config/local.yaml", "http:\n  addr: 0.0.0.0:${TENON_TEST_DEV_PORT}\n")
	if got := load(dir); got != "http://localhost:9100" {
		t.Fatalf("local.yaml with .env: %s", got)
	}
	// serve 指定了配置文件时按它读取；tenon.json 写了 url 时用它。
	write(t, dir, "config/dev.yaml", "http:\n  addr: :7000\n")
	write(t, dir, "tenon.json", `{"serve": ["serve", "--config=config/app.yaml,config/dev.yaml"]}`)
	if got := load(dir); got != "http://localhost:7000" {
		t.Fatalf("serve --config: %s", got)
	}
	write(t, dir, "tenon.json", `{"url": "https://app.test"}`)
	if got := load(dir); got != "https://app.test" {
		t.Fatalf("url in tenon.json: %s", got)
	}
	write(t, dir, "tenon.json", `{}`)
	write(t, dir, "config/local.yaml", "http:\n  addr: :0\n")
	if _, err := project.Load(dir); err == nil || !strings.Contains(err.Error(), "set url in tenon.json") {
		t.Fatalf("random port: %v", err)
	}
}
