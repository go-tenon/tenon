package frontend

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"testing/fstest"
	"time"

	"github.com/go-chi/chi/v5"
	metricnoop "go.opentelemetry.io/otel/metric/noop"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/go-tenon/tenon/httpx"
	"github.com/go-tenon/tenon/logging"
)

// 测试二进制自己充当 SSR 进程（FAKE_SSR）和托管它的 Go 进程（FAKE_HOST），测试不依赖 Bun。
func TestMain(m *testing.M) {
	switch {
	case os.Getenv("FAKE_SSR") != "":
		fakeSSR()
	case os.Getenv("FAKE_HOST") != "":
		fakeHost()
	default:
		os.Exit(m.Run())
	}
}

// fakeSSR 监听 SSR_SOCKET，模拟 Bun 的 SSR 服务。FAKE_SSR=silent 时不监听，用于就绪超时。
func fakeSSR() {
	if os.Getenv("FAKE_SSR") == "silent" {
		time.Sleep(time.Hour)
	}
	journal := func(s string) {
		if p := os.Getenv("FAKE_LOG"); p != "" {
			f, _ := os.OpenFile(p, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
			fmt.Fprintln(f, s)
			f.Close()
		}
	}
	api := &http.Client{Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", os.Getenv(EnvAPISocket))
	}}}
	mux := http.NewServeMux()
	echo := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set(HeaderRoute, "/echo/$id")
		json.NewEncoder(w).Encode(map[string]string{
			"nonce": r.Header.Get(HeaderNonce), "request_id": r.Header.Get("X-Request-ID"),
			"traceparent": r.Header.Get("Traceparent"), "host": r.Host, "env": os.Getenv("FAKE_EXTRA"),
			"frontend": os.Getenv(EnvFrontend), "path": r.URL.Path,
		})
	}
	// 页面请求带着前端的挂载路径：shop 挂在 /shop/。
	mux.HandleFunc("/echo/{id}", echo)
	mux.HandleFunc("/shop/echo/{id}", echo)
	mux.HandleFunc("/stream", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		io.WriteString(w, "first\n")
		w.(http.Flusher).Flush()
		time.Sleep(400 * time.Millisecond)
		io.WriteString(w, "second\n")
	})
	mux.HandleFunc("/api", func(w http.ResponseWriter, r *http.Request) {
		req, _ := http.NewRequestWithContext(r.Context(), http.MethodGet, "http://go/ping", nil)
		req.Header.Set("X-Request-ID", r.Header.Get("X-Request-ID"))
		res, err := api.Do(req)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		defer res.Body.Close()
		io.Copy(w, res.Body)
	})
	mux.HandleFunc("POST /__tenon/print/{name}", func(w http.ResponseWriter, r *http.Request) {
		if r.PathValue("name") != "invoice" {
			http.NotFound(w, r)
			return
		}
		var data map[string]any
		json.NewDecoder(r.Body).Decode(&data)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprintf(w, "<!doctype html><title>%v</title><p>%s</p>", data["no"], r.Header.Get("X-Request-ID"))
	})
	mux.HandleFunc("/crash", func(http.ResponseWriter, *http.Request) { os.Exit(3) })
	mux.HandleFunc("/slow", func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(400 * time.Millisecond)
		journal("done")
		io.WriteString(w, "slow done")
	})
	ln, err := net.Listen("unix", os.Getenv(EnvSocket))
	if err != nil {
		fmt.Fprintln(os.Stderr, "listen:", err)
		os.Exit(1)
	}
	srv := &http.Server{Handler: mux}
	term := make(chan os.Signal, 1)
	signal.Notify(term, syscall.SIGTERM)
	go func() {
		<-term
		journal("term")
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		srv.Shutdown(ctx)
	}()
	fmt.Println("fake ssr listening")
	fmt.Fprintln(os.Stderr, "fake ssr warning")
	srv.Serve(ln)
	os.Exit(0)
}

// fakeHost 托管 fakeSSR，就绪后打印子进程的 pid，然后一直运行，等测试把它强制结束。
func fakeHost() {
	h, err := New(ssrDist(Entry{Name: "web", Base: "/", SSR: true}), Options{API: http.NotFoundHandler(), Env: []string{"FAKE_SSR=1"}})
	if err != nil {
		panic(err)
	}
	h.Run(context.Background(), func(ctx context.Context) error {
		fmt.Printf("child %d\n", h.app("web").pid.Load())
		<-ctx.Done()
		return nil
	})
}

// self 是测试二进制自己，充当 SSR 可执行文件：环境变量 FAKE_SSR 让它运行 fakeSSR。
var self = sync.OnceValue(func() string {
	b, err := os.ReadFile("/proc/self/exe")
	if err != nil {
		panic(err)
	}
	return string(b)
})

// ssrDist 返回构建产物：清单是 entries，SSR 可执行文件是测试二进制自己；web 有一个预渲染的首页，
// 只在浏览器中渲染的前端有页面外壳。
func ssrDist(entries ...Entry) fstest.MapFS {
	files := map[string]string{ExecutableFile: self(), "web/index.html": "<h1>prerendered</h1>"}
	for _, e := range entries {
		if !e.SSR {
			files[e.Name+"/"+ShellFile] = "<title>" + e.Name + "</title>"
		}
	}
	return testDist(entries, files)
}

// app 返回名为 name 的前端。
func (h *Host) app(name string) *app {
	for _, a := range h.apps {
		if a.Name == name {
			return a
		}
	}
	panic("no frontend " + name)
}

// client 不复用连接：DefaultClient 可能预先建立一条连接却不发请求，服务器关停时要等满 5 秒才把它当作空闲连接。
var client = &http.Client{Transport: &http.Transport{DisableKeepAlives: true}}

type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}
func (s *syncBuffer) String() string { s.mu.Lock(); defer s.mu.Unlock(); return s.b.String() }

func TestHost(t *testing.T) {
	restartBackoff = 20 * time.Millisecond
	journal := t.TempDir() + "/journal"
	logs := &syncBuffer{}
	spans := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(spans))
	reader := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))

	api := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "pong "+logging.RequestID(r.Context()))
	})
	h, err := New(ssrDist(Entry{Name: "web", Base: "/", SSR: true}, Entry{Name: "admin", Base: "/admin/"}), Options{
		API:    httpx.AssignRequestID(httpx.DefaultRequestIDConfig())(api),
		Env:    []string{"FAKE_SSR=1", "FAKE_LOG=" + journal, "FAKE_EXTRA=extra"},
		Logger: slog.New(slog.NewTextHandler(logs, nil)), MeterProvider: mp,
	})
	if err != nil {
		t.Fatal(err)
	}
	router := chi.NewRouter()
	router.Use(httpx.Telemetry(metricnoop.NewMeterProvider(), tp))
	router.Use(httpx.AssignRequestID(httpx.DefaultRequestIDConfig()))
	router.Handle("/*", h.Handler())
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	base := "http://" + ln.Addr().String()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	served := make(chan struct{})
	runErr := make(chan error, 1)
	go func() {
		runErr <- h.Run(ctx, func(ctx context.Context) error {
			close(served)
			srv := &http.Server{Handler: router}
			go srv.Serve(ln)
			<-ctx.Done()
			sctx, c := context.WithTimeout(context.Background(), 5*time.Second)
			defer c()
			return srv.Shutdown(sctx)
		})
	}()
	select {
	case <-served:
	case err := <-runErr:
		t.Fatalf("Run returned before serving: %v", err)
	case <-time.After(30 * time.Second):
		t.Fatal("ssr not ready")
	}
	if !h.Ready() {
		t.Fatal("serve started before the process was ready")
	}

	t.Run("nonce, CSP, request ID, trace and route", func(t *testing.T) {
		req, _ := http.NewRequest(http.MethodGet, base+"/echo/7", nil)
		req.Header.Set("X-Request-ID", "REQ-1")
		req.Header.Set(HeaderNonce, "forged")
		res, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		var got map[string]string
		json.NewDecoder(res.Body).Decode(&got)
		if got["nonce"] == "" || got["nonce"] == "forged" {
			t.Fatalf("nonce %q", got["nonce"])
		}
		if csp := res.Header.Get("Content-Security-Policy"); csp != DefaultCSP(got["nonce"]) {
			t.Fatalf("csp %q", csp)
		}
		if got["request_id"] != "REQ-1" || got["env"] != "extra" || !strings.HasPrefix(got["host"], "127.0.0.1:") ||
			got["frontend"] != "web" || got["path"] != "/echo/7" {
			t.Fatalf("forwarded %v", got)
		}
		if res.Header.Get(HeaderRoute) != "" {
			t.Fatal("route header leaked to the client")
		}
		// 客户端读完响应时，服务端的中间件可能还没有结束 span。
		var span sdktrace.ReadOnlySpan
		for deadline := time.Now().Add(2 * time.Second); span == nil && time.Now().Before(deadline); time.Sleep(5 * time.Millisecond) {
			for _, s := range spans.Ended() {
				if s.Name() == "GET /echo/$id" {
					span = s
				}
			}
		}
		if span == nil {
			t.Fatalf("no span named after the frontend route; spans: %v", spanNames(spans))
		}
		if !strings.Contains(got["traceparent"], span.SpanContext().TraceID().String()) {
			t.Fatalf("traceparent %q, trace %s", got["traceparent"], span.SpanContext().TraceID())
		}
	})

	t.Run("static files and other frontends are served by Go", func(t *testing.T) {
		for path, want := range map[string]string{"/": "<h1>prerendered</h1>", "/admin/users": "<title>admin</title>"} {
			res, err := client.Get(base + path)
			if err != nil {
				t.Fatal(err)
			}
			b, _ := io.ReadAll(res.Body)
			res.Body.Close()
			if res.StatusCode != http.StatusOK || string(b) != want {
				t.Fatalf("%s: %d %q", path, res.StatusCode, b)
			}
		}
	})

	t.Run("streams without buffering", func(t *testing.T) {
		start := time.Now()
		res, err := client.Get(base + "/stream")
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		r := bufio.NewReader(res.Body)
		first, _ := r.ReadString('\n')
		firstAt := time.Since(start)
		second, _ := r.ReadString('\n')
		if first != "first\n" || second != "second\n" || firstAt > 300*time.Millisecond {
			t.Fatalf("first %q after %s, second %q", first, firstAt, second)
		}
	})

	t.Run("calls back the internal API", func(t *testing.T) {
		req, _ := http.NewRequest(http.MethodGet, base+"/api", nil)
		req.Header.Set("X-Request-ID", "REQ-2")
		res, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(res.Body)
		res.Body.Close()
		if string(b) != "pong REQ-2" {
			t.Fatalf("api %q", b)
		}
	})

	t.Run("logs process output", func(t *testing.T) {
		out := logs.String()
		if !strings.Contains(out, `level=INFO msg="fake ssr listening" component=ssr frontend=web`) ||
			!strings.Contains(out, `level=WARN msg="fake ssr warning" component=ssr frontend=web`) {
			t.Fatalf("logs:\n%s", out)
		}
	})

	t.Run("restarts after a crash", func(t *testing.T) {
		web := h.app("web")
		pid := web.pid.Load()
		res, err := client.Get(base + "/crash")
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != http.StatusBadGateway || res.Header.Get("Retry-After") == "" {
			t.Fatalf("status %d", res.StatusCode)
		}
		deadline := time.Now().Add(10 * time.Second)
		for !h.Ready() || web.pid.Load() == pid {
			if time.Now().After(deadline) {
				t.Fatalf("not restarted, pid %d -> %d, ready %v; logs:\n%s", pid, web.pid.Load(), h.Ready(), logs.String())
			}
			time.Sleep(10 * time.Millisecond)
		}
		res, err = client.Get(base + "/echo/1")
		if err != nil || res.StatusCode != http.StatusOK {
			t.Fatalf("after restart: %v %v", res, err)
		}
		// 重启后的进程从同一个内存文件启动，渲染同一个前端。
		var got map[string]string
		json.NewDecoder(res.Body).Decode(&got)
		res.Body.Close()
		if got["frontend"] != "web" {
			t.Fatalf("frontend after restart: %q", got["frontend"])
		}
		var rm metricdata.ResourceMetrics
		reader.Collect(context.Background(), &rm)
		if n := counter(rm, "ssr.restarts"); n != 1 {
			t.Fatalf("ssr.restarts = %d", n)
		}
	})

	t.Run("drains pages before stopping the process", func(t *testing.T) {
		pid := int(h.app("web").pid.Load())
		body := make(chan string, 1)
		go func() {
			res, err := client.Get(base + "/slow")
			if err != nil {
				body <- err.Error()
				return
			}
			b, _ := io.ReadAll(res.Body)
			res.Body.Close()
			body <- string(b)
		}()
		time.Sleep(100 * time.Millisecond)
		cancel()
		if got := <-body; got != "slow done" {
			t.Fatalf("in-flight page: %q", got)
		}
		if err := <-runErr; err != nil {
			t.Fatalf("Run: %v", err)
		}
		b, _ := os.ReadFile(journal)
		if string(b) != "done\nterm\n" {
			t.Fatalf("journal %q: the process must stop after in-flight pages finish", b)
		}
		if alive(pid) {
			t.Fatal("ssr process still running after Run returned")
		}
		if c, err := dialSocket(context.Background(), h.api); err == nil {
			c.Close()
			t.Fatal("internal API still listening")
		}
	})
}

func TestReadyTimeout(t *testing.T) {
	h, err := New(ssrDist(Entry{Name: "web", Base: "/", SSR: true}), Options{API: http.NotFoundHandler(), Env: []string{"FAKE_SSR=silent"},
		ReadyTimeout: 300 * time.Millisecond, StopTimeout: time.Second, Logger: slog.New(slog.DiscardHandler)})
	if err != nil {
		t.Fatal(err)
	}
	called := false
	err = h.Run(context.Background(), func(context.Context) error { called = true; return nil })
	if err == nil || called || !strings.Contains(err.Error(), "not ready") {
		t.Fatalf("err %v, serve called %v", err, called)
	}
}

// Go 进程被强制结束时，SSR 进程随之结束（Pdeathsig）。
func TestParentDeathKillsProcess(t *testing.T) {
	cmd := exec.Command(os.Args[0])
	cmd.Env = append(os.Environ(), "FAKE_HOST=1")
	out, _ := cmd.StdoutPipe()
	cmd.Stderr = io.Discard
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	line, err := bufio.NewReader(out).ReadString('\n')
	if err != nil {
		cmd.Process.Kill()
		t.Fatal(err)
	}
	pid, _ := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(line, "child ")))
	if !alive(pid) {
		t.Fatalf("child %d not running", pid)
	}
	cmd.Process.Kill()
	cmd.Wait()
	deadline := time.Now().Add(5 * time.Second)
	for alive(pid) {
		if time.Now().After(deadline) {
			syscall.Kill(pid, syscall.SIGKILL)
			t.Fatal("ssr process outlived its parent")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// Render 用打印模板渲染数据：没有内部接口（队列 worker）也能运行；对外的请求访问不到打印模板。
func TestRender(t *testing.T) {
	h, err := New(ssrDist(Entry{Name: "web", Base: "/", SSR: true}), Options{Env: []string{"FAKE_SSR=1"}})
	if err != nil {
		t.Fatal(err)
	}
	ctx := logging.WithRequestID(context.Background(), "PRINT-1")
	if _, err := h.Render(ctx, "web", "invoice", nil); err == nil {
		t.Fatal("rendered before the process was ready")
	}
	err = h.Run(context.Background(), func(context.Context) error {
		html, err := h.Render(ctx, "web", "invoice", map[string]any{"no": "INV-7"})
		if err != nil || string(html) != "<!doctype html><title>INV-7</title><p>PRINT-1</p>" {
			return fmt.Errorf("render: %q %v", html, err)
		}
		if _, err := h.Render(ctx, "web", "missing", nil); !errors.Is(err, ErrUnknownTemplate) {
			return fmt.Errorf("unknown template: %v", err)
		}
		if _, err := h.Render(ctx, "web", "../secret", nil); err == nil {
			return errors.New("accepted a name with a slash")
		}
		srv := httptest.NewServer(h.Handler())
		defer srv.Close()
		res, err := client.Post(srv.URL+PrintPath+"invoice", "application/json", strings.NewReader(`{"no":"x"}`))
		if err != nil {
			return err
		}
		res.Body.Close()
		if res.StatusCode != http.StatusNotFound {
			return fmt.Errorf("public print request: %d", res.StatusCode)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// 两个 SSR 前端共用一个可执行文件，各自一个进程：请求按挂载路径到达各自的进程，Run 结束后全部停止。
func TestTwoSSRFrontends(t *testing.T) {
	h, err := New(ssrDist(Entry{Name: "web", Base: "/", SSR: true}, Entry{Name: "shop", Base: "/shop/", SSR: true}),
		Options{Env: []string{"FAKE_SSR=1"}, Logger: slog.New(slog.DiscardHandler)})
	if err != nil {
		t.Fatal(err)
	}
	err = h.Run(context.Background(), func(context.Context) error {
		if !h.Ready() {
			return errors.New("not all processes are ready")
		}
		if h.app("web").pid.Load() == h.app("shop").pid.Load() {
			return errors.New("frontends share a process")
		}
		srv := httptest.NewServer(h.Handler())
		defer srv.Close()
		for path, want := range map[string]string{"/echo/1": "web", "/shop/echo/2": "shop"} {
			res, err := client.Get(srv.URL + path)
			if err != nil {
				return err
			}
			var got map[string]string
			json.NewDecoder(res.Body).Decode(&got)
			res.Body.Close()
			if got["frontend"] != want {
				return fmt.Errorf("%s reached %q", path, got["frontend"])
			}
		}
		return nil
	})
	if err != nil || h.Ready() {
		t.Fatalf("run: %v, ready after Run %v", err, h.Ready())
	}
}

func alive(pid int) bool {
	b, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return false
	}
	// 已经退出、等待回收的进程状态是 Z。
	f := strings.Fields(string(b[bytes.LastIndexByte(b, ')')+1:]))
	return len(f) > 0 && f[0] != "Z"
}

func counter(rm metricdata.ResourceMetrics, name string) int64 {
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if s, ok := m.Data.(metricdata.Sum[int64]); ok && m.Name == name {
				var n int64
				for _, dp := range s.DataPoints {
					n += dp.Value
				}
				return n
			}
		}
	}
	return 0
}

func spanNames(r *tracetest.SpanRecorder) []string {
	var names []string
	for _, s := range r.Ended() {
		names = append(names, s.Name())
	}
	return names
}
