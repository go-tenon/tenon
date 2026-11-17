package dev

import (
	"github.com/go-tenon/tenon/cmd/tenon/internal/project"

	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestHostPort(t *testing.T) {
	for raw, want := range map[string]string{
		"http://localhost:8080":   "localhost:8080",
		"http://127.0.0.1":        "127.0.0.1:80",
		"https://dev.example.com": "dev.example.com:443",
		"http://[::1]:9000":       "[::1]:9000",
	} {
		if got := hostPort(raw); got != want {
			t.Errorf("hostPort(%q) = %q", raw, got)
		}
	}
}

func TestWatchersAndBuilds(t *testing.T) {
	cfg := &project.Config{
		URL:       "http://localhost:8080",
		Frontends: []project.Frontend{{Name: "admin", Dir: "apps/admin"}, {Name: "web", Dir: "apps/web"}},
		Templ:     &project.Templ{ProxyPort: 7331},
		Tailwind:  &project.Tailwind{Input: "web/app.css", Output: "web/static/app.css"},
		Tools:     []project.Tool{{Watch: []string{"esbuild", "--watch"}, Build: []string{"esbuild"}}},
	}
	d := dev{cfg: cfg, servers: map[string]string{"admin": "http://localhost:5174", "web": "http://localhost:5173"}}
	front := []string{"API_ORIGIN=http://localhost:8080"}
	want := []watcher{
		{"apps/admin", []string{"bun", "run", "dev", "--port", "5174", "--strictPort"}, front},
		{"apps/web", []string{"bun", "run", "dev", "--port", "5173", "--strictPort"}, front},
		{".", []string{"go", "tool", "templ", "generate", "--watch", "--proxy=http://localhost:8080", "--proxyport=7331", "--open-browser=false"}, nil},
		{".", []string{"tailwindcss", "-i", "web/app.css", "-o", "web/static/app.css", "--watch=always"}, nil},
		{".", []string{"esbuild", "--watch"}, nil},
	}
	same := func(a, b watcher) bool {
		return a.dir == b.dir && slices.Equal(a.args, b.args) && slices.Equal(a.env, b.env)
	}
	if got := d.watchers(); !slices.EqualFunc(got, want, same) {
		t.Errorf("watchers = %q", got)
	}
	// 停止后不再构建前端：SSR 可执行文件由 go tool tenon build 编译。
	wantBuilds := []watcher{
		{".", []string{"go", "tool", "templ", "generate"}, nil},
		{".", []string{"tailwindcss", "-i", "web/app.css", "-o", "web/static/app.css", "--minify"}, nil},
		{".", []string{"esbuild"}, nil},
	}
	if got := d.builds(); !slices.EqualFunc(got, wantBuilds, same) {
		t.Errorf("builds = %q", got)
	}
}

// 有前端时追加 --config .tenon/dev.yaml，其中按名称写着开发服务器的地址；没有前端时 serve 的参数不变。
func TestDevConfig(t *testing.T) {
	t.Chdir(t.TempDir())
	cfg := &project.Config{Serve: []string{"serve", "--config", "config/app.yaml"}}
	if got := (dev{cfg: cfg}).serveArgs(); strings.Join(got, " ") != "serve --config config/app.yaml" {
		t.Errorf("without frontends: %q", got)
	}
	cfg.Frontends = []project.Frontend{{Name: "web", Dir: "apps/web"}, {Name: "admin", Dir: "apps/admin"}}
	servers, err := devServers(cfg.Frontends)
	if err != nil || len(servers) != 2 || servers["web"] == servers["admin"] || !strings.HasPrefix(servers["web"], "http://localhost:") {
		t.Fatalf("servers %q, %v", servers, err)
	}
	d := dev{cfg: cfg, servers: servers}
	if got := d.serveArgs(); strings.Join(got, " ") != "serve --config config/app.yaml --config "+DevConfig {
		t.Errorf("serve args %q", got)
	}
	if strings.Join(cfg.Serve, " ") != "serve --config config/app.yaml" {
		t.Error("serveArgs changed cfg.Serve")
	}
	if err := writeDevConfig(servers); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(DevConfig)
	want := "dev_servers:\n  admin: " + servers["admin"] + "\n  web: " + servers["web"] + "\n"
	if !strings.HasSuffix(string(b), want) {
		t.Errorf("dev.yaml:\n%s", b)
	}
	if b, _ := os.ReadFile(filepath.Join(filepath.Dir(DevConfig), ".gitignore")); string(b) != "*\n" {
		t.Errorf(".gitignore %q", b)
	}
}

func TestGenerations(t *testing.T) {
	cfg := &project.Config{Client: true, APIs: map[string]*project.API{
		"/api/v1":    {Name: "public", Go: "../api"},
		"/api/admin": {Go: "../admin"},
	}}
	got := Generations(cfg)
	want := [][]string{
		{"client", "--ts", "packages/client/src", "--name", "/api/v1=public"},
		{"client", "--base-path", "/api/admin", "--out", "../admin"},
		{"client", "--base-path", "/api/v1", "--out", "../api"},
	}
	if !slices.EqualFunc(got, want, slices.Equal) {
		t.Errorf("generations = %q", got)
	}
	if got := Generations(&project.Config{}); got != nil {
		t.Errorf("without client package: %q", got)
	}
}

// 应用的源码：.go（测试除外）、go.mod、go.sum；跳过隐藏目录、node_modules、testdata、dist。
func TestGoSourceSnapshot(t *testing.T) {
	dir := t.TempDir()
	for _, f := range []string{"main.go", "go.mod", "go.sum", "a_test.go", "README.md", "internal/x.go",
		".git/x.go", "apps/web/node_modules/y.go", "testdata/z.go", "apps/web/dist/w.go", "apps/web/frontend.go"} {
		path := filepath.Join(dir, f)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	snap := snapshot(dir, goSource)
	var files []string
	for _, line := range strings.Split(strings.TrimSpace(snap), "\n") {
		rel, _ := filepath.Rel(dir, strings.Fields(line)[0])
		files = append(files, rel)
	}
	want := []string{"apps/web/frontend.go", "go.mod", "go.sum", "internal/x.go", "main.go"}
	if !slices.Equal(files, want) {
		t.Errorf("files = %q", files)
	}
}

func TestWatch(t *testing.T) {
	dir := t.TempDir()
	var n atomic.Int32
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error)
	state := func() string { return snapshot(filepath.Join(dir, "static"), nil) }
	go func() { done <- watch(ctx, state, 10*time.Millisecond, func() { n.Add(1) }) }()

	time.Sleep(40 * time.Millisecond)
	if n.Load() != 0 {
		t.Fatal("changed without a change")
	}
	// 目录稍后才出现：新文件算一次变化。
	if err := os.MkdirAll(filepath.Join(dir, "static", "css"), 0o755); err != nil {
		t.Fatal(err)
	}
	css := filepath.Join(dir, "static", "css", "app.css")
	if err := os.WriteFile(css, []byte("a"), 0o644); err != nil {
		t.Fatal(err)
	}
	// WriteFile 先清空再写入，轮询可能落在两步之间，把一次写入算成两次变化。
	waitUntil(t, func() bool { return n.Load() >= 1 })
	before := n.Load()
	if err := os.WriteFile(css, []byte("ab"), 0o644); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, func() bool { return n.Load() > before })
	cancel()
	if err := <-done; err != context.Canceled {
		t.Errorf("watch returned %v", err)
	}
}

func waitUntil(t *testing.T, ok func() bool) {
	t.Helper()
	for deadline := time.Now().Add(3 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		if ok() {
			return
		}
	}
	t.Fatal("timed out")
}
