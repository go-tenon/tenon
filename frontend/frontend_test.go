package frontend

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/go-tenon/tenon/httpx"
)

// testDist 返回构建产物：清单是 entries，files 的路径相对 dist。
func testDist(entries []Entry, files map[string]string) fstest.MapFS {
	m, _ := json.Marshal(entries)
	fsys := fstest.MapFS{DistDir + "/" + ManifestFile: {Data: m}}
	for name, data := range files {
		fsys[DistDir+"/"+name] = &fstest.MapFile{Data: []byte(data)}
	}
	return fsys
}

const shellPage = `<!doctype html><script nonce="__tenon_csp_nonce__">boot()</script>`

// spaDist 是两个只在浏览器中渲染的前端：web 挂在 /，admin 挂在 /admin/。
func spaDist() fstest.MapFS {
	return testDist([]Entry{{Name: "web", Base: "/"}, {Name: "admin", Base: "/admin/"}}, map[string]string{
		"web/_shell.html":           shellPage,
		"web/index.html":            `<h1 nonce="__tenon_csp_nonce__">home</h1>`,
		"web/docs/intro/index.html": "<h1>intro</h1>",
		"web/assets/app-1.js":       "console.log(1)",
		"web/version.json":          `{"version":"v1"}`,
		"admin/_shell.html":         "<!doctype html><title>admin</title>",
		"admin/assets/admin-1.js":   "admin()",
	})
}

type result struct {
	status int
	header http.Header
	body   string
}

func do(t *testing.T, h http.Handler, method, target string) result {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(method, target, nil))
	return result{rec.Code, rec.Header(), rec.Body.String()}
}

// nonceOf 从 CSP 中取出本次请求的 nonce。
func nonceOf(csp string) string {
	_, after, _ := strings.Cut(csp, "'nonce-")
	nonce, _, _ := strings.Cut(after, "'")
	return nonce
}

// Options.CSP 按页面请求给出策略：页面外壳和预渲染的页面都把这次请求和它的 nonce 交给它，不同路径的页面得到不同的策略。
func TestCSPPerRequest(t *testing.T) {
	t.Parallel()
	h, err := New(spaDist(), Options{CSP: func(r *http.Request, nonce string) string {
		if strings.HasPrefix(r.URL.Path, "/admin/") {
			return "script-src 'nonce-" + nonce + "'; frame-ancestors 'none'"
		}
		return "script-src 'nonce-" + nonce + "'; frame-ancestors *"
	}})
	if err != nil {
		t.Fatal(err)
	}
	handler := h.Handler()
	for _, c := range []struct {
		target, ancestors string
		nonceInBody       bool // 页面中有占位 nonce，换成了策略中的那一个
	}{
		{"/todos/42", "frame-ancestors *", true},          // web 的页面外壳
		{"/", "frame-ancestors *", true},                  // web 预渲染的页面
		{"/admin/users", "frame-ancestors 'none'", false}, // admin 的页面外壳
	} {
		r := do(t, handler, http.MethodGet, c.target)
		csp := r.header.Get("Content-Security-Policy")
		nonce := nonceOf(csp)
		if nonce == "" || !strings.HasSuffix(csp, c.ancestors) || c.nonceInBody && !strings.Contains(r.body, nonce) {
			t.Errorf("%s: csp %q, body %q", c.target, csp, r.body)
		}
	}
}

// 只在浏览器中渲染的前端：静态文件、预渲染的页面和页面外壳都由 Go 返回，HTML 中的占位 nonce 换成本次请求的。
func TestStatic(t *testing.T) {
	t.Parallel()
	h, err := New(spaDist(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	handler := h.Handler()

	shell := do(t, handler, http.MethodGet, "/todos/42")
	nonce := nonceOf(shell.header.Get("Content-Security-Policy"))
	if shell.status != http.StatusOK || nonce == "" || shell.body != `<!doctype html><script nonce="`+nonce+`">boot()</script>` ||
		shell.header.Get("Content-Type") != "text/html; charset=utf-8" || shell.header.Get("Cache-Control") != "no-cache" {
		t.Fatalf("shell: %+v", shell)
	}
	if again := do(t, handler, http.MethodGet, "/todos/42"); nonceOf(again.header.Get("Content-Security-Policy")) == nonce {
		t.Fatal("nonce reused across requests")
	}

	for target, want := range map[string]string{
		"/":            "home",
		"/docs/intro":  "<h1>intro</h1>",
		"/docs/intro/": "<h1>intro</h1>",
		"/admin":       "<!doctype html><title>admin</title>",
		"/admin/users": "<!doctype html><title>admin</title>",
	} {
		if r := do(t, handler, http.MethodGet, target); r.status != http.StatusOK || !strings.Contains(r.body, want) || r.header.Get("Content-Security-Policy") == "" {
			t.Errorf("%s: %+v", target, r)
		}
	}
	if r := do(t, handler, http.MethodGet, "/"); strings.Contains(r.body, PrerenderNonce) {
		t.Errorf("placeholder nonce left in the prerendered page: %s", r.body)
	}

	asset := do(t, handler, http.MethodGet, "/admin/assets/admin-1.js")
	if asset.status != http.StatusOK || asset.body != "admin()" || !strings.Contains(asset.header.Get("Cache-Control"), "immutable") ||
		!strings.HasPrefix(asset.header.Get("Content-Type"), "text/javascript") {
		t.Fatalf("asset: %+v", asset)
	}
	if r := do(t, handler, http.MethodGet, "/version.json"); r.status != http.StatusOK || r.header.Get("Cache-Control") != "no-cache" {
		t.Fatalf("version.json: %+v", r)
	}
	// 挂载路径最长的前端优先：/admin/assets 下的文件不会落到 web。
	for _, target := range []string{"/assets/missing.js", "/admin/assets/app-1.js", "/__tenon/print/invoice"} {
		if r := do(t, handler, http.MethodGet, target); r.status != http.StatusNotFound {
			t.Errorf("%s: %+v", target, r)
		}
	}
	if r := do(t, handler, http.MethodHead, "/todos"); r.status != http.StatusOK || r.body != "" {
		t.Fatalf("HEAD: %+v", r)
	}
	if r := do(t, handler, http.MethodPost, "/todos"); r.status != http.StatusMethodNotAllowed || r.header.Get("Allow") != "GET, HEAD" {
		t.Fatalf("POST: %+v", r)
	}

	// 没有 SSR 前端：Run 只运行 serve，Ready 为 true、Ping 返回 nil，Render 报错。
	ran := false
	if err := h.Run(context.Background(), func(context.Context) error { ran = true; return nil }); err != nil || !ran || !h.Ready() || h.Ping(context.Background()) != nil {
		t.Fatalf("run: %v %v", err, ran)
	}
	if _, err := h.Render(context.Background(), "web", "invoice", nil); err == nil || !strings.Contains(err.Error(), "server rendering") {
		t.Fatalf("render: %v", err)
	}
	if _, err := h.Render(context.Background(), "shop", "invoice", nil); err == nil || !strings.Contains(err.Error(), "unknown frontend") {
		t.Fatalf("render unknown frontend: %v", err)
	}
}

// 没有前端挂在 / 时，其他路径返回 404。
func TestNotMounted(t *testing.T) {
	t.Parallel()
	h, err := New(testDist([]Entry{{Name: "admin", Base: "/admin/"}}, map[string]string{"admin/_shell.html": "admin"}), Options{})
	if err != nil {
		t.Fatal(err)
	}
	if r := do(t, h.Handler(), http.MethodGet, "/administrator"); r.status != http.StatusNotFound {
		t.Fatalf("%+v", r)
	}
}

// 构建产物不完整时 New 报错，提示运行 tenon build。
func TestNewChecksDist(t *testing.T) {
	t.Parallel()
	for name, c := range map[string]struct {
		fsys fstest.MapFS
		opts Options
		want string
	}{
		"no manifest":        {fstest.MapFS{}, Options{}, "frontends.json"},
		"no shell":           {testDist([]Entry{{Name: "web", Base: "/"}}, nil), Options{}, "_shell.html"},
		"no ssr executable":  {testDist([]Entry{{Name: "web", Base: "/", SSR: true}}, nil), Options{}, "dist/ssr"},
		"bad base":           {testDist([]Entry{{Name: "web", Base: "/web"}}, map[string]string{"web/_shell.html": ""}), Options{}, "must start and end with /"},
		"missing dev server": {testDist([]Entry{{Name: "web", Base: "/"}}, nil), Options{DevServers: map[string]string{"admin": "http://localhost:5173"}}, "invalid dev server"},
		"bad dev server":     {testDist([]Entry{{Name: "web", Base: "/"}}, nil), Options{DevServers: map[string]string{"web": "localhost:5173"}}, "invalid dev server"},
	} {
		if _, err := New(c.fsys, c.opts); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: err = %v, want %q", name, err, c.want)
		}
	}
}

// 开发模式：每个前端的请求转给它的开发服务器，不找静态文件，不设置 CSP；Render 请求开发服务器。
func TestDevServers(t *testing.T) {
	t.Parallel()
	var got http.Header
	dev := func(name string) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			got = r.Header.Clone()
			b, _ := io.ReadAll(r.Body)
			w.Header().Set("Content-Type", "text/html")
			io.WriteString(w, name+" "+r.Method+" "+r.URL.Path+" "+string(b))
		}))
	}
	web, admin := dev("web"), dev("admin")
	defer web.Close()
	defer admin.Close()
	fsys := testDist([]Entry{{Name: "web", Base: "/", SSR: true}, {Name: "admin", Base: "/admin/"}}, nil)
	h, err := New(fsys, Options{DevServers: map[string]string{"web": web.URL, "admin": admin.URL}})
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(httpx.AssignRequestID(httpx.DefaultRequestIDConfig())(h.Handler()))
	defer srv.Close()
	for path, want := range map[string]string{"/hello": "web GET /hello ", "/admin/users": "admin GET /admin/users ", "/assets/a.js": "web GET /assets/a.js "} {
		req, _ := http.NewRequest(http.MethodGet, srv.URL+path, nil)
		req.Header.Set("X-Request-ID", "DEV-1")
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(res.Body)
		res.Body.Close()
		if string(b) != want || res.Header.Get("Content-Security-Policy") != "" || got.Get("X-Request-ID") != "DEV-1" || got.Get(HeaderNonce) != "" {
			t.Fatalf("%s: body %q csp %q headers %v", path, b, res.Header.Get("Content-Security-Policy"), got)
		}
	}
	if !h.Ready() {
		t.Fatal("dev mode is always ready")
	}
	html, err := h.Render(context.Background(), "web", "invoice", map[string]int{"n": 1})
	if err != nil || string(html) != `web POST /__tenon/print/invoice {"n":1}` {
		t.Fatalf("render: %q %v", html, err)
	}
	ran := false
	if err := h.Run(context.Background(), func(context.Context) error { ran = true; return nil }); err != nil || !ran {
		t.Fatalf("dev Run: %v %v", err, ran)
	}
	if err := h.Run(context.Background(), func(context.Context) error { return nil }); err == nil {
		t.Fatal("Run twice")
	}
}

// TestCSPPolicy 验证结构化 CSP 与 DefaultCSP 一致，并支持去重追加来源与提取 origin。
func TestCSPPolicy(t *testing.T) {
	t.Parallel()
	const nonce = "abc123"
	p := DefaultPolicy(nonce)
	if got, want := p.String(), DefaultCSP(nonce); got != want {
		t.Fatalf("DefaultPolicy = %q, want %q", got, want)
	}
	p.AddImgSrc("blob:", Origin("https://cdn.example.com/files"), Origin("/storage"), "blob:", "")
	p.AddConnectSrc(Origin("https://o2.example.com/ingest"), "https://o2.example.com")
	p.FrameAncestors = []string{"https://shop.example.com"}
	got := p.String()
	for _, want := range []string{
		"img-src 'self' data: blob: https://cdn.example.com",
		"connect-src 'self' https://o2.example.com",
		"frame-ancestors https://shop.example.com",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("csp %q missing %q", got, want)
		}
	}
	if Origin("http://img.local:8080/path?x=1") != "http://img.local:8080" || Origin("ftp://x.com") != "" || Origin("/storage") != "" {
		t.Fatalf("unexpected Origin results")
	}
}

// TestSetAPI 验证延迟绑定 API 处理器在 Run 之前生效，Run 之后调用触发 panic。
func TestSetAPI(t *testing.T) {
	t.Parallel()
	h, err := New(spaDist(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	called := false
	h.SetAPI(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusAccepted)
	}))
	if err := h.Run(context.Background(), func(context.Context) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if h.opts.API == nil {
		t.Fatal("api was not set")
	}
	rec := httptest.NewRecorder()
	h.opts.API.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/ping", nil))
	if !called || rec.Code != http.StatusAccepted {
		t.Fatalf("api handler not invoked: called=%v code=%d", called, rec.Code)
	}
	defer func() {
		if recover() == nil {
			t.Fatal("SetAPI after Run should panic")
		}
	}()
	h.SetAPI(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
}
