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
	cfg, err := project.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Main != "./cmd/api" || strings.Join(cfg.Serve, " ") != "serve --config config/app.yaml" || cfg.URL != "http://localhost:8080" ||
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
		"tools": [{"watch": ["esbuild", "--watch"]}]}`)
	cfg, err := project.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Main != "./cmd/web" || cfg.APIs["/api/v1"].Name != "public" || cfg.APIs["/api/v1"].Go != "../api" ||
		len(cfg.Tools) != 1 {
		t.Fatalf("%+v", cfg)
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
