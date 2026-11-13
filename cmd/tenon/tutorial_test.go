//go:build unix

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/go-tenon/tenon/database"
)

// tutorialFile 是教程中一个完整文件的代码块：```go title="internal/article/article.go"。
var tutorialFile = regexp.MustCompile("(?ms)^```\\w+ title=\"([^\"]+)\"\\n(.*?)^```")

// migrationFile 是教程中的迁移文件名，时间戳之后是 migrate make 的名称。
var migrationFile = regexp.MustCompile(`^migrations/\d+_(\w+)\.sql$`)

// TestTutorial 按 docs/tutorial 的章节做一遍教程：用 init 生成项目，按顺序把每章的完整文件写进去，
// 执行正文中的命令（迁移用 migrate make 生成文件名，与读者的做法相同），每章结束时编译、生成客户端、
// 类型检查；最后构建、运行，用 HTTP 走一遍登录、写草稿、发布和授权。
//
// 它需要 Docker（启动教程的 compose.yaml）、bun 和网络，只在设置了 TENON_SCAFFOLD_TEST=1 时运行。
func TestTutorial(t *testing.T) {
	if os.Getenv("TENON_SCAFFOLD_TEST") != "1" {
		t.Skip("set TENON_SCAFFOLD_TEST=1 to run")
	}
	framework, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "blog")
	var out bytes.Buffer
	cmd := root()
	cmd.SetArgs([]string{"init", dir, "--tenon", framework, "--module", "example.com/blog"})
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("init: %v\n%s", err, out.String())
	}

	p := &tutorial{t: t, dir: dir}
	chapters, err := filepath.Glob(filepath.Join(framework, "docs", "tutorial", "[0-9]*.md"))
	if err != nil || len(chapters) == 0 {
		t.Fatalf("no chapters: %v", err)
	}
	for _, chapter := range chapters {
		name := filepath.Base(chapter)
		t.Log(name)
		doc, err := os.ReadFile(chapter)
		if err != nil {
			t.Fatal(err)
		}
		var migrations [][2]string
		for _, m := range tutorialFile.FindAllSubmatchIndex(doc, -1) {
			path, body := string(doc[m[2]:m[3]]), string(doc[m[4]:m[5]])
			// 正文要在代码块之前写出文件的路径，读者才知道写到哪里
			if !bytes.Contains(doc[:m[0]], []byte("`"+path+"`")) && !migrationFile.MatchString(path) {
				t.Errorf("%s: %s is not named before its code block", name, path)
			}
			if mm := migrationFile.FindStringSubmatch(path); mm != nil {
				migrations = append(migrations, [2]string{mm[1], body})
				continue
			}
			p.write(path, body)
		}
		p.chapter(name, migrations)
	}
	p.run()
}

type tutorial struct {
	t   *testing.T
	dir string
	env []string
}

func (p *tutorial) write(path, body string) {
	p.t.Helper()
	full := filepath.Join(p.dir, path)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		p.t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
		p.t.Fatal(err)
	}
}

// sh 在项目目录中运行命令，失败时结束测试；返回标准输出。
func (p *tutorial) sh(name string, args ...string) string {
	p.t.Helper()
	c := exec.Command(name, args...)
	c.Dir = p.dir
	c.Env = append(os.Environ(), p.env...)
	var stdout, stderr bytes.Buffer
	c.Stdout, c.Stderr = &stdout, &stderr
	if err := c.Run(); err != nil {
		p.t.Fatalf("%s %s: %v\n%s%s", name, strings.Join(args, " "), err, stdout.String(), stderr.String())
	}
	return stdout.String()
}

func (p *tutorial) api(args ...string) string {
	p.t.Helper()
	return p.sh("go", append([]string{"run", "./cmd/api"}, args...)...)
}

// chapter 执行一章正文中的命令，然后检查项目。
func (p *tutorial) chapter(name string, migrations [][2]string) {
	p.t.Helper()
	p.sh("go", "mod", "tidy")
	switch {
	case strings.HasPrefix(name, "02-"):
		p.sh("docker", "compose", "up", "-d", "--wait")
		p.database()
	case strings.HasPrefix(name, "04-"):
		if err := os.RemoveAll(filepath.Join(p.dir, "internal", "todo")); err != nil {
			p.t.Fatal(err)
		}
	case strings.HasPrefix(name, "06-"):
		p.write(".env", "APP_KEY="+strings.TrimSpace(p.sh("openssl", "rand", "-base64", "32"))+"\n")
		p.api("migrate", "publish")
	}
	for _, m := range migrations {
		// migrate make 输出新文件的路径
		path := strings.TrimSpace(p.api("migrate", "make", m[0]))
		p.write(path, m[1])
	}
	if len(migrations) > 0 {
		p.api("migrate", "up")
	}
	if strings.HasPrefix(name, "06-") {
		p.api("user", "create", "--name", "小明", "--email", "ming@example.com", "--password", "secret123")
	}
	p.sh("go", "vet", "./...")
	p.sh("go", "tool", "tenon", "generate")
	// 第 4 章删除了待办接口，页面在第 5 章才改写
	if !strings.HasPrefix(name, "04-") {
		p.sh("bun", "run", "typecheck")
	}
}

// database 为这次测试新建一个数据库，教程中的命令通过 DATABASE_URL 使用它，测试结束时删除。
func (p *tutorial) database() {
	p.t.Helper()
	ctx := context.Background()
	admin, err := database.Open(ctx, database.Config{DSN: "postgres://blog:blog@localhost:5432/postgres?sslmode=disable"})
	if err != nil {
		p.t.Fatal(err)
	}
	name := fmt.Sprintf("tutorial_%d", time.Now().UnixNano())
	if _, err := admin.ExecContext(ctx, "CREATE DATABASE "+name); err != nil {
		p.t.Fatal(err)
	}
	p.t.Cleanup(func() {
		_, _ = admin.ExecContext(context.Background(), "DROP DATABASE "+name+" WITH (FORCE)")
		_ = admin.Close()
	})
	p.env = append(p.env, "DATABASE_URL=postgres://blog:blog@localhost:5432/"+name+"?sslmode=disable")
}

// run 构建并运行教程的博客，用 HTTP 检查各章的规则。
func (p *tutorial) run() {
	t := p.t
	p.sh("go", "tool", "tenon", "build")
	p.api("user", "create", "--name", "小红", "--email", "hong@example.com", "--password", "secret123")

	port := freePort(t)
	p.write("config/test.yaml", "http:\n  addr: 127.0.0.1:"+port+"\n")
	app := exec.Command(filepath.Join(p.dir, "bin", "api"), "serve", "--config", "config/app.yaml,config/test.yaml")
	app.Dir = p.dir
	app.Env = append(os.Environ(), p.env...)
	var logs bytes.Buffer
	app.Stdout, app.Stderr = &logs, &logs
	if err := app.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = app.Process.Kill()
		_ = app.Wait()
		if t.Failed() {
			t.Log(logs.String())
		}
	})
	base := "http://127.0.0.1:" + port
	waitUp(t, base+"/readyz")

	if page := get(t, base+"/"); !strings.Contains(page, "/assets/") {
		t.Fatalf("page:\n%s", page)
	}
	if res := call(t, "", http.MethodPost, base+"/api/articles", `{"title":"x","slug":"x","content":""}`); res.status != http.StatusUnauthorized {
		t.Fatalf("create without login: %+v", res)
	}
	if res := call(t, "", http.MethodPost, base+"/api/auth/login", `{"email":"ming@example.com","password":"wrong"}`); res.status != http.StatusUnauthorized {
		t.Fatalf("wrong password: %+v", res)
	}
	ming, hong := login(t, base, "ming@example.com"), login(t, base, "hong@example.com")

	res := call(t, ming, http.MethodPost, base+"/api/articles", `{"title":"你好","slug":"hello","content":"第一篇"}`)
	if res.status != http.StatusCreated {
		t.Fatalf("create: %+v", res)
	}
	id, _ := res.body["id"].(string)
	if res := call(t, ming, http.MethodPost, base+"/api/articles", `{"title":"x","slug":"hello","content":""}`); res.status != http.StatusConflict || res.body["detail"] != "slug“hello”已被使用" {
		t.Fatalf("duplicate slug: %+v", res)
	}
	if res := call(t, "", http.MethodGet, base+"/api/articles", ""); res.body["total"] != float64(0) {
		t.Fatalf("draft is public: %+v", res)
	}
	if res := call(t, hong, http.MethodPut, base+"/api/articles/"+id, `{"title":"改","slug":"hello","content":""}`); res.status != http.StatusForbidden {
		t.Fatalf("other author updated: %+v", res)
	}
	if res := call(t, ming, http.MethodPost, base+"/api/articles/"+id+"/publish", ""); res.status != http.StatusOK {
		t.Fatalf("publish: %+v", res)
	}
	if res := call(t, "", http.MethodGet, base+"/api/articles/"+id, ""); res.status != http.StatusOK || res.body["title"] != "你好" {
		t.Fatalf("published article: %+v", res)
	}
	mine := call(t, hong, http.MethodGet, base+"/api/me/articles", "")
	if mine.body["total"] != float64(0) {
		t.Fatalf("hong sees ming's articles: %+v", mine)
	}
}

// login 用邮箱和密码登录，返回 access token；refresh token 在 cookie 中。
func login(t *testing.T, base, email string) string {
	t.Helper()
	res := call(t, "", http.MethodPost, base+"/api/auth/login", `{"email":"`+email+`","password":"secret123"}`)
	token, _ := res.body["access_token"].(string)
	if res.status != http.StatusOK || token == "" || !slices.ContainsFunc(res.cookies, func(c *http.Cookie) bool { return c.Name == "blog_refresh" && c.HttpOnly }) {
		t.Fatalf("login %s: %+v", email, res)
	}
	return token
}

type apiResponse struct {
	status  int
	body    map[string]any
	cookies []*http.Cookie
}

func call(t *testing.T, token, method, url, body string) apiResponse {
	t.Helper()
	req, _ := http.NewRequest(method, url, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept-Language", "zh-CN")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	out := apiResponse{status: res.StatusCode, cookies: res.Cookies()}
	_ = json.NewDecoder(res.Body).Decode(&out.body)
	return out
}
