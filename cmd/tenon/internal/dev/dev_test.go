//go:build unix

package dev_test

import (
	"bytes"
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-tenon/tenon/cmd/tenon/internal/dev"
	"github.com/go-tenon/tenon/cmd/tenon/internal/project"
)

// fake 是伪造的命令：记录参数和所在的目录；监听模式（--watch、bun run dev）一直运行，直到收到 SIGINT。
// 信号的 trap 在写日志之前安装：测试看到日志就可能发出信号，先写日志会让信号落在 trap 安装之前，进程直接退出、
// 记不下 stopped。
// go build -mod=mod -o BIN 写出一个伪造的应用：serve 一直运行到 SIGTERM，其他子命令记录后退出；源码中有 BROKEN 时编译失败。
const fake = `#!/bin/sh
trap 'echo "stopped $(basename "$0")" >> "$DEVCMD_LOG"; exit 0' INT
echo "$(basename "$0") $* @$(basename "$PWD")" >> "$DEVCMD_LOG"
if [ "$1 $2" = "tool -n" ]; then echo /fake/templ; exit 0; fi
if [ "$1 $2" = "list -m" ]; then echo "$DEVCMD_FRAMEWORK"; exit 0; fi
if [ "$1 $2 $3" = "build -mod=mod -o" ]; then
	[ -n "$DEVCMD_BUILD_DELAY" ] && sleep "$DEVCMD_BUILD_DELAY"
	if grep -rqs BROKEN --include=*.go .; then echo "main.go:1: syntax error" >&2; exit 1; fi
	cat > "$4" <<'APP'
#!/bin/sh
trap 'echo "app stopped" >> "$DEVCMD_LOG"; exit 0' TERM
echo "app $* templ=$TEMPL_DEV_MODE" >> "$DEVCMD_LOG"
[ "$1" = "serve" ] || exit 0
while :; do sleep 0.02; done
APP
	chmod +x "$4"
	exit 0
fi
case "$*" in
*--watch*|"run dev"*--strictPort*)
	while :; do sleep 0.02; done ;;
esac
`

// setup 在临时目录中准备一个项目和伪造的命令，返回目录和记录命令的日志。
func setup(t *testing.T, tools ...string) (dir, log string) {
	t.Helper()
	dir = t.TempDir()
	bin := filepath.Join(dir, ".bin")
	if err := os.Mkdir(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range tools {
		if err := os.WriteFile(filepath.Join(bin, name), []byte(fake), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	log = filepath.Join(dir, ".log")
	t.Setenv("DEVCMD_LOG", log)
	framework, _ := filepath.Abs("../../../..")
	t.Setenv("DEVCMD_FRAMEWORK", framework)
	t.Chdir(dir)
	t.Setenv("PATH", bin+":/usr/bin:/bin")
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir, log
}

func read(path string) string {
	b, _ := os.ReadFile(path)
	return string(b)
}

func waitFor(t *testing.T, what string, ok func() bool) {
	t.Helper()
	for deadline := time.Now().Add(15 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		if ok() {
			return
		}
	}
	t.Fatalf("timed out waiting for %s", what)
}

func load(t *testing.T) *project.Config {
	t.Helper()
	cfg, err := project.Load(".")
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

// start 运行 dev.Run，返回停止它并等待返回的函数。
func start(t *testing.T, cfg *project.Config, out, errOut *syncBuffer) func() error {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- dev.Run(ctx, cfg, out, errOut) }()
	return func() error {
		cancel()
		select {
		case err := <-done:
			return err
		case <-time.After(10 * time.Second):
			t.Fatal("dev did not stop")
			return nil
		}
	}
}

// 服务端渲染的页面：templ 省略 --cmd，应用带 TEMPL_DEV_MODE；静态文件变化时刷新浏览器；Tailwind 先复制样式源。
func TestDevPages(t *testing.T) {
	dir, log := setup(t, "go", "tailwindcss", "esbuild")
	var reloads atomic.Int32
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/_templ/reload/events" {
			reloads.Add(1)
		}
	}))
	defer proxy.Close()
	_, port, _ := net.SplitHostPort(strings.TrimPrefix(proxy.URL, "http://"))
	proxyPort, _ := strconv.Atoi(port)
	static := filepath.Join(dir, "static")
	if err := os.Mkdir(static, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "layout.templ"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := load(t)
	cfg.Templ.ProxyPort, cfg.Templ.Static = proxyPort, "static"
	cfg.Tailwind = &project.Tailwind{Input: "web/app.css", Output: "web/static/app.css"}
	cfg.Tools = []project.Tool{{Watch: []string{"esbuild", "--watch"}, Build: []string{"esbuild", "--minify"}}}
	var out, errOut syncBuffer
	stop := start(t, cfg, &out, &errOut)

	waitFor(t, "all watchers", func() bool {
		l := read(log)
		return strings.Contains(l, "go tool templ generate --watch") && strings.Contains(l, "tailwindcss -i") &&
			strings.Contains(l, "esbuild --watch") && strings.Contains(l, "app serve")
	})
	l := read(log)
	for _, want := range []string{
		"go tool -n templ",
		"--proxy=http://localhost:8080 --proxyport=" + port + " --open-browser=false @",
		"app serve --config config/app.yaml templ=true",
		"tailwindcss -i web/app.css -o web/static/app.css --watch=always",
	} {
		if !strings.Contains(l, want) {
			t.Errorf("log missing %q:\n%s", want, l)
		}
	}
	for _, f := range []string{"style-maia.css", "ui.css", "classes.txt"} {
		if _, err := os.Stat(filepath.Join(dir, "web", dev.UIDir, f)); err != nil {
			t.Errorf("ui styles: %v", err)
		}
	}
	if err := os.WriteFile(filepath.Join(static, "app.css"), []byte("body{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "browser reload", func() bool { return reloads.Load() >= 1 })

	if err := stop(); err != nil {
		t.Fatalf("dev returned %v", err)
	}
	l = read(log)
	for _, want := range []string{"stopped go", "app stopped", "stopped tailwindcss", "stopped esbuild", "go tool templ generate @", "--minify"} {
		if !strings.Contains(l, want) {
			t.Errorf("log missing %q after stop:\n%s", want, l)
		}
	}
	if !strings.Contains(out.String(), "http://localhost:"+port) {
		t.Errorf("output = %q", out.String())
	}
}

// 一个监听进程意外退出时，dev 停止其余进程并报错。
func TestDevStopsWhenAWatcherExits(t *testing.T) {
	dir, log := setup(t, "go", "esbuild")
	if err := os.WriteFile(filepath.Join(dir, ".bin", "esbuild"), []byte("#!/bin/sh\nuntil grep -q 'app serve' \"$DEVCMD_LOG\"; do sleep 0.02; done\nexit 3\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := load(t)
	cfg.Tools = []project.Tool{{Watch: []string{"esbuild", "--watch"}}}
	err := dev.Run(context.Background(), cfg, &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "esbuild") {
		t.Errorf("err = %v", err)
	}
	waitFor(t, "app stopped", func() bool { return strings.Contains(read(log), "app stopped") })
}

// 首次编译期间修改的 Go 代码在应用启动后触发一次重启，不会因为监听晚于编译开始而被漏掉。
func TestDevRestartsForChangesDuringFirstBuild(t *testing.T) {
	dir, log := setup(t, "go")
	t.Setenv("DEVCMD_BUILD_DELAY", "0.5")
	cfg := load(t)
	stop := start(t, cfg, &syncBuffer{}, &syncBuffer{})
	count := func(s string) int { return strings.Count(read(log), s) }
	waitFor(t, "first build", func() bool { return count("go build") == 1 })
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "restart", func() bool { return count("app stopped") == 1 && count("app serve") == 2 })
	if err := stop(); err != nil {
		t.Fatalf("dev returned %v", err)
	}
}

func TestDevMissingTools(t *testing.T) {
	setup(t, "go")
	cfg := load(t)
	for name, edit := range map[string]func(*project.Config){
		"tailwindcss not found": func(c *project.Config) { c.Tailwind = &project.Tailwind{Input: "a", Output: "b"} },
		"bun not found":         func(c *project.Config) { c.Frontends = []project.Frontend{{Name: "web", Dir: "apps/web"}} },
		"has no --config": func(c *project.Config) {
			c.Frontends, c.Serve = []project.Frontend{{Name: "web", Dir: "apps/web"}}, []string{"serve"}
		},
	} {
		c := *cfg
		edit(&c)
		if err := dev.Run(context.Background(), &c, &bytes.Buffer{}, &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), name) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}

// 单页应用：没有 node_modules 时在根目录安装，没有 dist 时写入占位页；为每个前端选端口运行 vite，把地址写入 .tenon/dev.yaml
// 交给应用；改了 Go 代码后重新编译、重启并生成客户端；编译失败时旧进程继续运行；测试文件和前端的变化不触发重新编译。
func TestDevSPA(t *testing.T) {
	defer dev.SetPollInterval(20 * time.Millisecond)()
	defer dev.SetFrontendWait(50 * time.Millisecond)() // 伪造的 vite 不监听端口
	dir, log := setup(t, "go", "bun")
	for name, content := range map[string]string{
		"package.json":                 "{}",
		"apps/web/vite.config.ts":      "",
		"apps/admin/vite.config.ts":    "",
		"packages/client/package.json": "{}",
	} {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cfg := load(t)
	var out, errOut syncBuffer
	stop := start(t, cfg, &out, &errOut)

	count := func(s string) int { return strings.Count(read(log), s) }
	waitFor(t, "app, generate and vite", func() bool {
		return count("app serve") == 1 && count("app client --ts packages/client/src") == 1 && count("bun run dev --port") == 2
	})
	l := read(log)
	for _, want := range []string{"bun install @" + filepath.Base(dir) + "\n", "app serve --config config/app.yaml --config .tenon/dev.yaml templ=\n",
		"--strictPort @web\n", "--strictPort @admin\n"} {
		if !strings.Contains(l, want) {
			t.Errorf("log missing %q:\n%s", want, l)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, ".tenon", "js", "client", "package.json")); err != nil {
		t.Errorf("front-end packages not synced: %v", err)
	}
	devYAML := read(filepath.Join(dir, ".tenon", "dev.yaml"))
	for _, f := range []string{"admin", "web"} {
		if !strings.Contains(devYAML, "  "+f+": http://localhost:") {
			t.Errorf("dev.yaml missing %s:\n%s", f, devYAML)
		}
		if b, err := os.ReadFile(filepath.Join(dir, "apps", f, dev.SSRBinary)); err != nil || len(b) != 0 {
			t.Errorf("%s ssr.bin placeholder = %q, %v", f, b, err)
		}
	}
	if count("bun run build") != 0 {
		t.Errorf("built before start:\n%s", l)
	}
	if strings.Contains(l, "go tool") {
		t.Errorf("templ used without .templ files:\n%s", l)
	}

	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "restart", func() bool { return count("app stopped") == 1 && count("app serve") == 2 && count("app client") == 2 })
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n\nBROKEN\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "build failure", func() bool { return strings.Contains(errOut.String(), "编译失败") })
	if count("app stopped") != 1 || count("app serve") != 2 {
		t.Fatalf("app restarted after a failed build:\n%s", read(log))
	}
	before := count("go build")
	for _, f := range []string{"main_test.go", "apps/web/src/app.tsx", "node_modules/x/x.go", ".tenon/x.go"} {
		p := filepath.Join(dir, f)
		_ = os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte("package main\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	time.Sleep(200 * time.Millisecond)
	if n := count("go build"); n != before {
		t.Fatalf("rebuilt %d times for non-Go changes", n-before)
	}

	if err := stop(); err != nil {
		t.Fatalf("dev returned %v", err)
	}
	l = read(log)
	if count("stopped bun") != 2 || count("app stopped") != 2 || count("bun run build") != 0 {
		t.Errorf("after stop:\n%s", l)
	}
	if !strings.Contains(out.String(), "dev: 打开 http://localhost:8080") {
		t.Errorf("output = %q", out.String())
	}
}

// SyncJS 把框架 js 中的包复制到 .tenon/js，跳过 node_modules；package.json 有变化时报告 changed。
func TestSyncJS(t *testing.T) {
	dir, _ := setup(t, "go")
	changed, err := dev.SyncJS(".")
	if err != nil || !changed {
		t.Fatalf("first sync: changed %v, %v", changed, err)
	}
	pkg := filepath.Join(dir, ".tenon", "js", "client", "package.json")
	if b, _ := os.ReadFile(pkg); !strings.Contains(string(b), `"name": "@go-tenon/client"`) {
		t.Fatalf("package.json:\n%s", b)
	}
	for _, p := range []string{".tenon/js/client/src/index.ts", ".tenon/js/shell/src/form.ts", ".tenon/.gitignore"} {
		if _, err := os.Stat(filepath.Join(dir, p)); err != nil {
			t.Error(err)
		}
	}
	// 测试文件只在框架中运行，不复制：它们可能依赖项目中没有的包（例如 antd）。
	for _, p := range []string{".tenon/js/node_modules", ".tenon/js/client/node_modules", ".tenon/js/package.json",
		".tenon/js/client/src/errors.test.ts", ".tenon/js/shell/src/form.test.tsx"} {
		if _, err := os.Stat(filepath.Join(dir, p)); !os.IsNotExist(err) {
			t.Errorf("%s copied", p)
		}
	}
	if changed, err := dev.SyncJS("."); err != nil || changed {
		t.Fatalf("second sync: changed %v, %v", changed, err)
	}
	// 项目中的副本被改动后恢复为框架的版本，并报告变化。
	if err := os.WriteFile(pkg, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if changed, err := dev.SyncJS("."); err != nil || !changed {
		t.Fatalf("after edit: changed %v, %v", changed, err)
	}

	// 包自己的 node_modules（Bun 的依赖链接）保留；包中多出的文件、框架中已经没有的包被删除。
	for p, content := range map[string]string{
		".tenon/js/shell/node_modules/react/package.json": "{}",
		".tenon/js/shell/src/stale.ts":                    "",
		".tenon/js/old/package.json":                      "{}",
	} {
		full := filepath.Join(dir, p)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := dev.SyncJS("."); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".tenon/js/shell/node_modules/react/package.json")); err != nil {
		t.Errorf("dependency links removed: %v", err)
	}
	for _, p := range []string{".tenon/js/shell/src/stale.ts", ".tenon/js/old"} {
		if _, err := os.Stat(filepath.Join(dir, p)); !os.IsNotExist(err) {
			t.Errorf("%s not removed", p)
		}
	}
}

// syncBuffer 是可以同时读写的 bytes.Buffer：dev 的子进程写入时，测试在读取。
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

func TestNeedsInstall(t *testing.T) {
	dir := t.TempDir()
	write := func(p, content string) {
		t.Helper()
		full := filepath.Join(dir, p)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if !dev.NeedsInstall(dir) {
		t.Error("no node_modules: want install")
	}
	write("node_modules/.keep", "")
	write(".tenon/js/client/package.json", `{"name":"@go-tenon/client"}`)
	write(".tenon/js/shell/package.json", `{"name":"@go-tenon/shell","peerDependencies":{"react":"^19"}}`)
	if !dev.NeedsInstall(dir) {
		t.Error("shell has dependencies but no node_modules: want install")
	}
	write(".tenon/js/shell/node_modules/react/package.json", "{}")
	if dev.NeedsInstall(dir) {
		t.Error("everything installed: want no install")
	}
}
