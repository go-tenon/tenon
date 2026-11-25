//go:build unix

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// TestInitEndToEnd 用 init 生成项目（指向本地的框架），用 add frontend 加入三个前端，然后类型检查、tenon build
// （前端的静态文件和服务端渲染的 Bun 可执行文件嵌入程序）、运行二进制，通过 HTTP 检查服务端渲染和只在浏览器中渲染的页面、
// 挂载路径、静态资源和品牌色，走一遍待办事项的增删改查；再把全部前端改成只在浏览器中渲染，检查程序中不再带 Bun。
// 它需要 bun 和网络，编译、安装依赖很占 CPU，会干扰其他包中
// 计时的测试，所以只在设置了 TENON_SCAFFOLD_TEST=1 时运行（CI 的 scaffold 任务）：
//
//	TENON_SCAFFOLD_TEST=1 go test -run TestInitEndToEnd ./cmd/tenon/
func TestInitEndToEnd(t *testing.T) {
	if os.Getenv("TENON_SCAFFOLD_TEST") != "1" {
		t.Skip("set TENON_SCAFFOLD_TEST=1 to run")
	}
	framework, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "todo")
	var out bytes.Buffer
	cmd := root()
	cmd.SetArgs([]string{"init", dir, "--tenon", framework, "--module", "example.com/todo", "--name", "Todo"})
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("init: %v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "bun run dev") {
		t.Errorf("next steps missing:\n%s", out.String())
	}

	// shIn 在项目的子目录 sub 中运行命令，env 是追加的环境变量。
	shIn := func(sub string, env []string, name string, args ...string) {
		t.Helper()
		c := exec.Command(name, args...)
		c.Dir = filepath.Join(dir, sub)
		c.Env = append(os.Environ(), env...)
		if b, err := c.CombinedOutput(); err != nil {
			t.Fatalf("%s %v: %v\n%s", name, args, err, b)
		}
	}
	sh := func(name string, args ...string) {
		t.Helper()
		shIn(".", nil, name, args...)
	}
	// 三个前端：运营后台挂在 /admin/，控制台挂在 /console/（左右分栏的登录页），都只在浏览器中渲染；官网样式的 promo
	// 挂在 /promo/，带登录页，和 web 一样在服务端渲染。main.go 不用改：frontend.Host 按 apps/dist 中的清单托管全部前端。
	sh("go", "tool", "tenon", "add", "frontend", "admin", "--layout", "frame")
	sh("go", "tool", "tenon", "add", "frontend", "console", "--login", "split")
	sh("go", "tool", "tenon", "add", "frontend", "promo", "--layout", "site", "--login", "centered")
	// Electron 桌面端显示 admin：admin 中加上授权页，客户端的类型检查随项目的 typecheck 一起运行。
	sh("go", "tool", "tenon", "add", "client", "admin-desktop", "--kind", "electron", "--frontend", "admin", "--title", "Todo 后台")

	// add frontend、add client 之后装上了前端和 Electron 的技能；框架文档同步到了 tenon/docs，
	// 助手的 MCP 配置中有 tenon；README 中写着 gopls、Mantine、TanStack 的官方接入方法。
	for f, wants := range map[string][]string{
		"AGENTS.md":                {"# Todo\n", "`README.md` 的“AI 编程助手”一节", "## 代码约定"},
		"README.md":                {"# Todo\n", "<!-- tenon:deploy:begin -->", "go run ./cmd/todo config check --config config/prod.yaml", "gopls mcp", "@mantine/mcp-server", "@tanstack/intent"},
		".gitattributes":           {"tenon/** linguist-generated=true"},
		".github/workflows/ci.yml": {"go tool tenon generate", "go tool tenon check", "bun run typecheck", "bun run build"},
		// 单语言项目默认直写界面文字，不生成 messages.gen.ts；授权页直接写在 admin 中。
		"apps/web/src/routes/index.tsx":           {`"添加"`},
		"apps/admin/src/routes/oauth/consent.tsx": {`"允许"`},
		"apps/promo/src/routes/login.tsx":         {`"登录"`},
		"apps/web/src/routeTree.gen.ts":           {"declare module '@tanstack/react-start'"},
		"tenon/docs/README.md":                    {"# Tenon 文档"},
		".mcp.json":                               {`"tenon"`},
		".codex/config.toml":                      {"[mcp_servers.tenon]"},
		".cursor/mcp.json":                        {`"tenon"`},
		".agents/mcp_config.json":                 {`"tenon"`},
		".claude/skills/tenon-electron/SKILL.md":  {"name: tenon-electron"},
		".agents/skills/tenon-frontend/SKILL.md":  {"name: tenon-frontend"},
	} {
		b, err := os.ReadFile(filepath.Join(dir, f))
		if err != nil {
			t.Fatal(err)
		}
		for _, want := range wants {
			if !strings.Contains(string(b), want) {
				t.Errorf("%s missing %q:\n%s", f, want, b)
			}
		}
	}

	// 生成的项目通过 CI 的 tenon check（文字、时钟、错误码、颜色、依赖版本）。
	sh("go", "tool", "tenon", "check")

	// 助手按 .mcp.json 在项目的子目录中运行 go tool tenon mcp：它找到项目的根目录，检索文档，列出项目的前端。
	mcpCmd := exec.Command("go", "tool", "tenon", "mcp")
	mcpCmd.Dir = filepath.Join(dir, "internal")
	session, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil).Connect(context.Background(), &mcp.CommandTransport{Command: mcpCmd}, nil)
	if err != nil {
		t.Fatalf("tenon mcp: %v", err)
	}
	for _, c := range []struct {
		name string
		args map[string]any
		want string
	}{
		{"tenon_search_docs", map[string]any{"query": "访问策略"}, "tenon/docs/http/routing.md#access-policies"},
		{"tenon_app_info", map[string]any{}, `"base":"/admin/"`},
	} {
		name, want := c.name, c.want
		res, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: c.args})
		if err != nil {
			t.Fatal(err)
		}
		if text := res.Content[0].(*mcp.TextContent).Text; res.IsError || !strings.Contains(text, want) {
			t.Errorf("%s: want %q:\n%s", name, want, text)
		}
	}
	_ = session.Close()

	// 刚 clone 的项目没有 apps/dist（被 .gitignore 忽略）和生成的客户端：generate 先写前端的清单再编译应用。
	for _, p := range []string{"apps/dist", "packages/client/src/api"} {
		if err := os.RemoveAll(filepath.Join(dir, p)); err != nil {
			t.Fatal(err)
		}
	}
	// 框架的前端包从本地框架同步到 tenon/js，作为 workspace 成员，前端能导入它。
	// 生成的查询选项、校验规则、验证提示和权限从客户端包的子路径导入。
	imports := "import { chatTransport, createSession, describeError, uploadFile, validate } from \"@go-tenon/client\"\nimport { createQueries } from \"@todo/client/api/queries\"\nimport { schemas } from \"@todo/client/api/schemas\"\n" +
		"import { messages } from \"@todo/client/api/messages\"\nimport { permissions } from \"@todo/client/api/permissions\"\nimport { enums } from \"@todo/client/api/enums\"\n" +
		// 每个前端的 messages.gen.ts 登记自己的消息 ID，所以 admin 不导入 web 的文件，自己创建 /api 的客户端。
		"import { createClient } from \"@todo/client/api\"\n" +
		"const api = createClient({ baseURL: \"/api\", fetch })\n" +
		"const session = createSession({ load: async () => ({ account: \"a\" }) })\n" +
		// 浏览器的 fetch 和 File 可以直接交给按形状声明的参数；session.fetch(fetch) 仍是 typeof fetch，可以交给 AI SDK。
		"const authed: typeof fetch = session.fetch(fetch)\n" +
		"export const flows = [chatTransport(\"/api/chat\", authed), (file: File) => uploadFile(file, { prepare: async () => ({ id: \"f\" }), sign: async () => ({ url: \"\", method: \"PUT\" }) })]\n" +
		"export const probe = [createQueries(api, { scope: session.scope }).todo.list(), validate(schemas.todo.create.body, {}, { messages }), describeError(null), permissions, enums]\n"
	if err := os.WriteFile(filepath.Join(dir, "apps", "admin", "src", "framework.ts"), []byte(imports), 0o644); err != nil {
		t.Fatal(err)
	}
	// 带路径参数的页面。
	userPage := "import { createFileRoute } from \"@tanstack/react-router\"\n\n" +
		"export const Route = createFileRoute(\"/_app/users/$id\")({ staticData: { title: \"用户\" }, component: User })\n\n" +
		"function User() {\n  const { id } = Route.useParams()\n  return <p>user {id}</p>\n}\n"
	if err := os.MkdirAll(filepath.Join(dir, "apps", "admin", "src", "routes", "_app", "users"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "apps", "admin", "src", "routes", "_app", "users", "$id.tsx"), []byte(userPage), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(dir, "tenon", "js")); err != nil {
		t.Fatal(err)
	}
	sh("go", "tool", "tenon", "generate")
	if _, err := os.Stat(filepath.Join(dir, "tenon", "js", "start", "package.json")); err != nil {
		t.Fatalf("front-end packages not synced: %v", err)
	}
	sh("bun", "run", "typecheck")
	// typecheck 用 tenon-start routes 生成路由树：有新加的页面，也有 Start 的 Register 声明，与 tenon dev 生成的相同。
	if tree, err := os.ReadFile(filepath.Join(dir, "apps", "admin", "src", "routeTree.gen.ts")); err != nil ||
		!strings.Contains(string(tree), "'/_app/users/$id'") || !strings.Contains(string(tree), "declare module '@tanstack/react-start'") {
		t.Fatalf("admin route tree after typecheck: %v\n%s", err, tree)
	}
	sh("go", "vet", "./...")
	// 桌面端的主进程和 preload 打包成 dist/main.js、dist/preload.js。
	shIn("clients/admin-desktop", nil, "bun", "run", "bundle")
	for _, f := range []string{"main.js", "preload.js"} {
		if _, err := os.Stat(filepath.Join(dir, "clients", "admin-desktop", "dist", f)); err != nil {
			t.Fatal(err)
		}
	}
	// 为宿主构建时（TENON_RENDER=csr），服务端渲染的 promo 也只在浏览器中渲染：构建出页面外壳。之后的 tenon build 照常构建。
	shIn("apps/promo", []string{"TENON_RENDER=csr"}, "bun", "run", "build")
	if _, err := os.Stat(filepath.Join(dir, "apps", "promo", "dist", "client", "_shell.html")); err != nil {
		t.Fatalf("promo built for a host has no page shell: %v", err)
	}
	sh("go", "tool", "tenon", "build")
	// 两个 SSR 前端编进同一个 Bun 可执行文件：程序只比一份 Bun（约 100MB）大一点。
	if info, err := os.Stat(filepath.Join(dir, "bin", "todo")); err != nil || info.Size() > 180<<20 {
		t.Fatalf("bin/todo: %v, %d MB", err, info.Size()>>20)
	}

	// 运行构建出的二进制：页面由嵌入的 SSR 进程渲染，接口在 /api 下。
	port := freePort(t)
	if err := os.WriteFile(filepath.Join(dir, "config", "test.yaml"), []byte("http:\n  addr: 127.0.0.1:"+port+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	start := func() func() {
		app := exec.Command(filepath.Join(dir, "bin", "todo"), "serve", "--config", "config/app.yaml,config/test.yaml")
		app.Dir = dir
		if err := app.Start(); err != nil {
			t.Fatal(err)
		}
		stop := func() { _ = app.Process.Signal(os.Interrupt); _ = app.Wait() }
		t.Cleanup(func() { _ = app.Process.Kill() })
		return stop
	}
	stop := start()
	base := "http://127.0.0.1:" + port
	waitUp(t, base+"/api/todos")

	// 首页预渲染，带着本次请求的 nonce 和品牌色，文字取自翻译文件；不存在的页面由前端路由返回 404。
	page := getHTML(t, base+"/")
	if !strings.Contains(page, "<title>Todo</title>") || !strings.Contains(page, "添加") || strings.Contains(page, "todo.add") || !strings.Contains(page, "/assets/") || strings.Contains(page, "__tenon_csp_nonce__") ||
		!strings.Contains(strings.ToLower(page), "--mantine-color-brand-6: #777c91;") {
		t.Fatalf("page:\n%s", page)
	}
	// 只在浏览器中渲染的前端：Go 返回页面外壳，带本次请求的 nonce；服务端渲染的 promo 由它的 SSR 进程渲染。
	for url, title := range map[string]string{base + "/admin/": "admin", base + "/admin/users/42": "admin", base + "/console/login": "console",
		base + "/promo/": "promo", base + "/promo/login": "promo"} {
		page := getHTML(t, url)
		prefix := "/" + title + "/assets/"
		if !strings.Contains(page, "<title>"+title+"</title>") || !strings.Contains(page, prefix) || strings.Contains(page, "__tenon_csp_nonce__") {
			t.Fatalf("%s:\n%s", url, page)
		}
	}
	if res, err := http.Get(base + "/todos/42"); err != nil || res.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown page: %v %v", res, err)
	}
	// 构建产物中的每个静态文件都嵌入程序，由 Go 按挂载路径提供。
	assets, err := os.ReadDir(filepath.Join(dir, "apps", "admin", "dist", "client", "assets"))
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range assets {
		res, err := http.Get(base + "/admin/assets/" + a.Name())
		if err != nil {
			t.Fatal(err)
		}
		_ = res.Body.Close()
		if res.StatusCode != http.StatusOK || !strings.Contains(res.Header.Get("Cache-Control"), "immutable") {
			t.Errorf("/admin/assets/%s: %d %q", a.Name(), res.StatusCode, res.Header.Get("Cache-Control"))
		}
	}
	// 构建版本（tenon 插件中的 versionPlugin）只作用于浏览器的构建：version.json 在 dist/client 中，不缓存。
	if res, err := http.Get(base + "/admin/version.json"); err != nil {
		t.Fatal(err)
	} else {
		body, _ := io.ReadAll(res.Body)
		_ = res.Body.Close()
		var v struct{ Version string }
		if res.StatusCode != http.StatusOK || res.Header.Get("Cache-Control") != "no-cache" || json.Unmarshal(body, &v) != nil || v.Version == "" {
			t.Errorf("/admin/version.json: %d %q %s", res.StatusCode, res.Header.Get("Cache-Control"), body)
		}
		// 页面中注入的版本号与 version.json 相同，否则一打开页面就提示有新版本。
		// TanStack Start 在一次构建中会加载好几次 vite.config，每次都创建 versionPlugin。
		if !bundleContains(t, filepath.Join(dir, "apps", "admin", "dist", "client", "assets"), v.Version) {
			t.Errorf("version %q from version.json is not in the page bundle", v.Version)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "apps", "admin", "dist", "server", "version.json")); err == nil {
		t.Error("version.json written to the server build")
	}
	if res := send(t, http.MethodPost, base+"/api/todos", `{"title":"买牛奶"}`); res.status != http.StatusCreated || res.body["title"] != "买牛奶" {
		t.Fatalf("create: %+v", res)
	}
	if res := send(t, http.MethodPatch, base+"/api/todos/1", `{"done":true}`); res.status != http.StatusOK || res.body["done"] != true {
		t.Fatalf("update: %+v", res)
	}
	if list := get(t, base+"/api/todos"); !strings.Contains(list, `"done":true`) {
		t.Fatalf("list: %s", list)
	}
	if res := send(t, http.MethodDelete, base+"/api/todos/1", ""); res.status != http.StatusNoContent {
		t.Fatalf("delete: %+v", res)
	}
	if res := send(t, http.MethodDelete, base+"/api/todos/1", ""); res.status != http.StatusNotFound || res.body["code"] != "todo.not_found" {
		t.Fatalf("delete again: %+v", res)
	}
	stop()

	// 品牌色只写在前端的 theme.ts 中：改一次，重新构建后页面随之改变。
	themeTS := filepath.Join(dir, "apps", "web", "src", "theme.ts")
	b, _ := os.ReadFile(themeTS)
	if err := os.WriteFile(themeTS, []byte(strings.Replace(string(b), `"blue-gray"`, `"bright-red"`, 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	sh("go", "tool", "tenon", "build")
	stop = start()
	waitUp(t, base+"/api/todos")
	if page := strings.ToLower(getHTML(t, base+"/")); !strings.Contains(page, "--mantine-color-brand-6: #f0185c;") || strings.Contains(page, "--mantine-color-brand-6: #777c91;") {
		t.Fatalf("brand change not in page:\n%s", page)
	}
	stop()

	// 全部前端改成只在浏览器中渲染（package.json 的 tenon.ssr），去掉首页的预渲染（页面外壳由 / 渲染而来）：
	// 重新构建后程序中不带 Bun，页面仍然可以打开。
	for _, name := range []string{"web", "promo"} {
		for file, r := range map[string][2]string{"package.json": {`"ssr": true`, `"ssr": false`}, "vite.config.ts": {`{ prerender: ["/"] }`, ""}} {
			p := filepath.Join(dir, "apps", name, file)
			b, _ := os.ReadFile(p)
			if !strings.Contains(string(b), r[0]) {
				t.Fatalf("%s has no %q:\n%s", p, r[0], b)
			}
			if err := os.WriteFile(p, []byte(strings.Replace(string(b), r[0], r[1], 1)), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	sh("go", "tool", "tenon", "build")
	if _, err := os.Stat(filepath.Join(dir, "apps", "dist", "ssr")); err == nil {
		t.Fatal("apps/dist/ssr left after every frontend stopped server rendering")
	}
	if info, err := os.Stat(filepath.Join(dir, "bin", "todo")); err != nil || info.Size() > 80<<20 {
		t.Fatalf("bin/todo without SSR: %v, %d MB", err, info.Size()>>20)
	}
	start()
	waitUp(t, base+"/api/todos")
	for url, title := range map[string]string{base + "/": "Todo", base + "/todos/42": "Todo", base + "/promo/": "promo"} {
		if page := getHTML(t, url); !strings.Contains(page, "<title>"+title+"</title>") || strings.Contains(page, "__tenon_csp_nonce__") {
			t.Fatalf("%s without SSR:\n%s", url, page)
		}
	}

	// 开发时 tenon dev 运行每个前端的 bun run dev（vite dev，在 Node 中运行）：开发服务器要能加载
	// vite.config.ts（其中的框架插件由 Node 直接导入）并渲染页面。
	devPort := freePort(t)
	dev := exec.Command("bun", "run", "dev", "--port", devPort, "--strictPort")
	dev.Dir = filepath.Join(dir, "apps", "admin")
	dev.Env = append(os.Environ(), "API_ORIGIN="+base)
	var devOut syncBuffer
	dev.Stdout, dev.Stderr = &devOut, &devOut
	dev.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := dev.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = syscall.Kill(-dev.Process.Pid, syscall.SIGKILL); _ = dev.Wait() }()
	devURL := "http://localhost:" + devPort + "/admin/login"
	for deadline := time.Now().Add(60 * time.Second); ; time.Sleep(200 * time.Millisecond) {
		if res, err := http.Get(devURL); err == nil {
			body, _ := io.ReadAll(res.Body)
			_ = res.Body.Close()
			if res.StatusCode == http.StatusOK && strings.Contains(string(body), "<html") {
				break
			}
		}
		if time.Now().After(deadline) || dev.ProcessState != nil {
			t.Fatalf("dev server: %s", devOut.String())
		}
	}
}

// syncBuffer 收集子进程的输出，可以并发写入和读取。
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

type response struct {
	status int
	body   map[string]any
}

func send(t *testing.T, method, url, body string) response {
	t.Helper()
	req, _ := http.NewRequest(method, url, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(res.Body).Decode(&out)
	return response{res.StatusCode, out}
}

func get(t *testing.T, url string) string {
	t.Helper()
	res, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var b bytes.Buffer
	_, _ = b.ReadFrom(res.Body)
	return b.String()
}

// getHTML 像浏览器打开页面一样请求 url：前端路由的地址返回 index.html。
func getHTML(t *testing.T, url string) string {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, url, nil)
	req.Header.Set("Accept", "text/html")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var b bytes.Buffer
	_, _ = b.ReadFrom(res.Body)
	return b.String()
}

func waitUp(t *testing.T, url string) {
	t.Helper()
	for deadline := time.Now().Add(15 * time.Second); time.Now().Before(deadline); time.Sleep(100 * time.Millisecond) {
		if res, err := http.Get(url); err == nil {
			res.Body.Close()
			return
		}
	}
	t.Fatalf("%s did not come up", url)
}

func freePort(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	_, port, _ := net.SplitHostPort(ln.Addr().String())
	return port
}

// bundleContains 报告 dir 中是否有脚本包含 s。
func bundleContains(t *testing.T, dir, s string) bool {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(dir, "*.js"))
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(b), s) {
			return true
		}
	}
	return false
}
