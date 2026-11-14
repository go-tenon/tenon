//go:build unix

package main

import (
	"bytes"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestInitEndToEnd 用 init 生成项目（指向本地的框架），用 add frontend 加入第二个前端并挂载，然后类型检查、
// tenon build、运行二进制，通过 HTTP 检查两个前端的页面和深链接，走一遍待办事项的增删改查。它需要 bun 和网络，编译、安装依赖很占 CPU，会干扰其他包中
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

	sh := func(name string, args ...string) {
		t.Helper()
		c := exec.Command(name, args...)
		c.Dir = dir
		if b, err := c.CombinedOutput(); err != nil {
			t.Fatalf("%s %v: %v\n%s", name, args, err, b)
		}
	}
	// 第二个前端挂在 /admin/：按 add frontend 的提示修改路由。
	sh("go", "tool", "tenon", "add", "frontend", "admin")
	mainGo := filepath.Join(dir, "cmd", "api", "main.go")
	src, err := os.ReadFile(mainGo)
	if err != nil {
		t.Fatal(err)
	}
	mount := `	router.Handle("/admin/*", httpx.NewSPA(admin.Dist, httpx.SPAOptions{Base: "/admin/", DevServer: cfg.DevServers["admin"]}))` + "\n"
	code := strings.Replace(string(src), `	router.Handle("/*"`, mount+`	router.Handle("/*"`, 1)
	code = strings.Replace(code, `	"example.com/todo/apps/web"`, "\t\"example.com/todo/apps/admin\"\n\t\"example.com/todo/apps/web\"", 1)
	if err := os.WriteFile(mainGo, []byte(code), 0o644); err != nil {
		t.Fatal(err)
	}

	// 刚 clone 的项目没有 dist（被 .gitignore 忽略）和生成的客户端：generate 先写占位页再编译应用。
	for _, p := range []string{"apps/web/dist", "apps/admin/dist", "packages/client/src/api"} {
		if err := os.RemoveAll(filepath.Join(dir, p)); err != nil {
			t.Fatal(err)
		}
	}
	// 框架的前端包从本地框架复制到 .tenon/js，作为 workspace 成员，前端能导入它。
	// 生成的查询选项、校验规则、验证提示和权限从客户端包的子路径导入。
	imports := "import \"@go-tenon/client\"\nimport { createQueries } from \"@todo/client/api/queries\"\nimport { schemas } from \"@todo/client/api/schemas\"\n" +
		"import { messages } from \"@todo/client/api/messages\"\nimport { permissions } from \"@todo/client/api/permissions\"\nimport { enums } from \"@todo/client/api/enums\"\n" +
		"import { api } from \"../../web/src/client\"\nexport const probe = [createQueries(api).todo.list(), schemas, messages, permissions, enums]\n"
	if err := os.WriteFile(filepath.Join(dir, "apps", "admin", "src", "framework.ts"), []byte(imports), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(dir, ".tenon")); err != nil {
		t.Fatal(err)
	}
	sh("go", "tool", "tenon", "generate")
	if _, err := os.Stat(filepath.Join(dir, ".tenon", "js", "client", "package.json")); err != nil {
		t.Fatalf("front-end packages not synced: %v", err)
	}
	sh("bun", "run", "typecheck")
	sh("go", "vet", "./...")
	sh("go", "tool", "tenon", "build")

	// 运行构建出的二进制：页面来自嵌入的 dist，接口在 /api 下。
	port := freePort(t)
	if err := os.WriteFile(filepath.Join(dir, "config", "test.yaml"), []byte("http:\n  addr: 127.0.0.1:"+port+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	app := exec.Command(filepath.Join(dir, "bin", "api"), "serve", "--config", "config/app.yaml,config/test.yaml")
	app.Dir = dir
	if err := app.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = app.Process.Kill(); _ = app.Wait() })
	base := "http://127.0.0.1:" + port
	waitUp(t, base+"/api/todos")

	page := get(t, base+"/")
	if !strings.Contains(page, "<title>Todo</title>") || !strings.Contains(page, "/assets/") {
		t.Fatalf("page:\n%s", page)
	}
	for _, url := range []string{base + "/admin/", base + "/admin/users/42"} {
		if page := getHTML(t, url); !strings.Contains(page, "<title>admin</title>") || !strings.Contains(page, "/admin/assets/") {
			t.Fatalf("%s:\n%s", url, page)
		}
	}
	if page := getHTML(t, base+"/todos/42"); !strings.Contains(page, "<title>Todo</title>") {
		t.Fatalf("deep link:\n%s", page)
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
