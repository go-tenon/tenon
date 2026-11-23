package httpx_test

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-tenon/tenon/cleanup"
	"github.com/go-tenon/tenon/httpx"
)

func TestServeDrainsBeforeCleanup(t *testing.T) {
	t.Parallel()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var dbClosed atomic.Bool
	var sawRequest atomic.Bool
	release := make(chan struct{})

	mux := http.NewServeMux()
	mux.HandleFunc("/ping", func(w http.ResponseWriter, r *http.Request) {
		sawRequest.Store(true)
		<-release
		_, _ = io.WriteString(w, "pong")
	})

	var stack cleanup.Stack
	_ = stack.Add("db", func(context.Context) error {
		dbClosed.Store(true)
		return nil
	})

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		err := httpx.Serve(ctx, shutdownIn(time.Second), ln, mux)
		errCh <- stack.CloseWith(context.Background(), err)
	}()

	// 发起慢请求
	go func() {
		resp, err := http.Get("http://" + ln.Addr().String() + "/ping")
		if err == nil {
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
		}
	}()

	deadline := time.Now().Add(time.Second)
	for !sawRequest.Load() {
		if time.Now().After(deadline) {
			t.Fatal("request not seen")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if dbClosed.Load() {
		t.Fatal("db closed before shutdown started")
	}

	cancel()
	time.Sleep(30 * time.Millisecond)
	if dbClosed.Load() {
		t.Fatal("db must stay open until Shutdown finishes draining")
	}
	close(release)

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("serve did not stop")
	}
	if !dbClosed.Load() {
		t.Fatal("db should close after serve returns")
	}
}

// 长连接监听 DrainStarted 主动结束后，Serve 随即返回。
func TestDrainStartedEndsLongRequests(t *testing.T) {
	t.Parallel()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	entered := make(chan struct{})
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		select {
		case <-httpx.DrainStarted(r.Context()):
		case <-time.After(10 * time.Second):
		}
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- httpx.Serve(ctx, shutdownIn(5*time.Second), ln, handler) }()
	go func() {
		if resp, err := http.Get("http://" + ln.Addr().String()); err == nil {
			resp.Body.Close()
		}
	}()
	<-entered
	began := time.Now()
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if took := time.Since(began); took > time.Second {
		t.Fatalf("Serve took %v to stop", took)
	}
	if httpx.DrainStarted(context.Background()) != nil {
		t.Fatal("DrainStarted outside Serve should be nil")
	}
}

// 连接在排空之前已经被接收、请求在排空开始之后才到：请求照常处理。
// 不停机升级时旧进程直到交接那一刻都在接收连接。
func TestServeAnswersRequestsOnAcceptedConns(t *testing.T) {
	t.Parallel()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	accepted := make(chan struct{})
	counted := &countingListener{Listener: ln, accepted: accepted}
	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		errCh <- httpx.Serve(ctx, httpx.DefaultConfig(), counted, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(w, "ok")
		}))
	}()

	conn, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	<-accepted
	cancel()
	time.Sleep(50 * time.Millisecond) // 排空已经开始

	if _, err := io.WriteString(conn, "GET / HTTP/1.1\r\nHost: test\r\n\r\n"); err != nil {
		t.Fatal(err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	resp, err := io.ReadAll(conn)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(resp), "HTTP/1.1 200 OK") || !strings.HasSuffix(string(resp), "ok") {
		t.Fatalf("response = %q, want 200 ok", resp)
	}
	if err := <-errCh; err != nil {
		t.Fatalf("Serve = %v", err)
	}
}

type countingListener struct {
	net.Listener
	accepted chan struct{}
	once     atomic.Bool
}

func (l *countingListener) Accept() (net.Conn, error) {
	c, err := l.Listener.Accept()
	if err == nil && l.once.CompareAndSwap(false, true) {
		close(l.accepted)
	}
	return c, err
}

// Logger 选项让 Serve 的排空和停止记录写到指定的记录器。
func TestServeOptions(t *testing.T) {
	t.Parallel()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var logs strings.Builder
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err = httpx.Serve(ctx, httpx.DefaultConfig(), ln, http.NotFoundHandler(),
		httpx.Logger(slog.New(slog.NewTextHandler(&logs, nil))))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(logs.String(), "http server draining") || !strings.Contains(logs.String(), "http server stopped") {
		t.Fatalf("logs=%s", logs.String())
	}
}

// TestServeOnReady 验证 OnReady 在服务器开始接收连接后被调用。
func TestServeOnReady(t *testing.T) {
	t.Parallel()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ready := make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- httpx.Serve(ctx, httpx.DefaultConfig(), ln, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(w, "ready")
		}), httpx.OnReady(func() { close(ready) }))
	}()
	select {
	case <-ready:
	case <-time.After(2 * time.Second):
		t.Fatal("OnReady not called")
	}
	resp, err := http.Get("http://" + ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(body) != "ready" {
		t.Fatalf("body = %q, want ready", body)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

// TestServeDrainDelayAndConnectionClose 验证 DrainDelay 期间就绪检查返回 503、业务请求照常返回且带 Connection: close，
// 以及 SkipDrainDelay 为真时立即跳过摘流等待。
func TestServeDrainDelayAndConnectionClose(t *testing.T) {
	t.Parallel()
	t.Run("waits drain delay and sets connection close", func(t *testing.T) {
		t.Parallel()
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		mux := http.NewServeMux()
		mux.Handle("/readyz", httpx.Ready(httpx.ReadyOptions{}))
		mux.HandleFunc("/ping", func(w http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(w, "pong")
		})
		cfg := httpx.DefaultConfig()
		cfg.DrainDelay = 200 * time.Millisecond
		ready := make(chan struct{})
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() {
			done <- httpx.Serve(ctx, cfg, ln, mux, httpx.OnReady(func() { close(ready) }))
		}()
		<-ready

		began := time.Now()
		cancel()
		time.Sleep(30 * time.Millisecond)

		// 摘流期间 /readyz 返回 503，普通请求照常返回 200 且响应头带 Connection: close。
		r1, err := http.Get("http://" + ln.Addr().String() + "/readyz")
		if err != nil {
			t.Fatal(err)
		}
		r1.Body.Close()
		if r1.StatusCode != http.StatusServiceUnavailable {
			t.Fatalf("/readyz status = %d, want 503", r1.StatusCode)
		}
		conn, err := net.Dial("tcp", ln.Addr().String())
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		if _, err := io.WriteString(conn, "GET /ping HTTP/1.1\r\nHost: test\r\n\r\n"); err != nil {
			t.Fatal(err)
		}
		_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
		raw, err := io.ReadAll(conn)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(raw), "Connection: close") || !strings.HasSuffix(string(raw), "pong") {
			t.Fatalf("draining response = %q, want Connection: close and pong", raw)
		}
		if err := <-done; err != nil {
			t.Fatal(err)
		}
		if took := time.Since(began); took < 180*time.Millisecond {
			t.Fatalf("Serve returned after %v, want >= DrainDelay", took)
		}
	})

	t.Run("skips drain delay on handover", func(t *testing.T) {
		t.Parallel()
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		cfg := httpx.DefaultConfig()
		cfg.DrainDelay = 2 * time.Second
		ready := make(chan struct{})
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() {
			done <- httpx.Serve(ctx, cfg, ln, http.NotFoundHandler(),
				httpx.OnReady(func() { close(ready) }),
				httpx.SkipDrainDelay(func() bool { return true }),
			)
		}()
		<-ready
		began := time.Now()
		cancel()
		if err := <-done; err != nil {
			t.Fatal(err)
		}
		if took := time.Since(began); took > 500*time.Millisecond {
			t.Fatalf("Serve took %v, expected SkipDrainDelay to skip 2s wait", took)
		}
	})
}

// TestServeGracefulKeepAliveDrain 验证停机交接时：
//  1. 排空开始前已进入处理器的在途请求，写出响应时自动附带 Connection: close；
//  2. 排空开始瞬间处于空闲的 keep-alive 连接，在短暂宽限期内发来的下一个请求仍正常返回 200 并附带 Connection: close，避免反代报 502。
func TestServeGracefulKeepAliveDrain(t *testing.T) {
	t.Parallel()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	entered := make(chan struct{})
	release := make(chan struct{})
	mux := http.NewServeMux()
	mux.HandleFunc("/slow", func(w http.ResponseWriter, _ *http.Request) {
		close(entered)
		<-release
		_, _ = io.WriteString(w, "slow")
	})
	mux.HandleFunc("/fast", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "fast")
	})

	ready := make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- httpx.Serve(ctx, httpx.DefaultConfig(), ln, mux, httpx.OnReady(func() { close(ready) }))
	}()
	<-ready

	// 连接 1：先发一次请求后保持空闲，模拟反向代理的 keep-alive 空闲连接池。
	idleConn, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer idleConn.Close()
	if _, err := io.WriteString(idleConn, "GET /fast HTTP/1.1\r\nHost: test\r\n\r\n"); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 512)
	_ = idleConn.SetReadDeadline(time.Now().Add(2 * time.Second))
	n, err := idleConn.Read(buf)
	if err != nil || !strings.Contains(string(buf[:n]), "fast") {
		t.Fatalf("warmup idle conn = %q, %v", string(buf[:n]), err)
	}

	// 连接 2：排空前进入处理器，排空后才写响应。
	slowConn, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer slowConn.Close()
	if _, err := io.WriteString(slowConn, "GET /slow HTTP/1.1\r\nHost: test\r\n\r\n"); err != nil {
		t.Fatal(err)
	}
	<-entered

	// 开始排空（模拟热重载交接：无 DrainDelay）。
	cancel()
	time.Sleep(50 * time.Millisecond)

	// 放行在途慢请求：响应应附带 Connection: close。
	close(release)
	_ = slowConn.SetReadDeadline(time.Now().Add(2 * time.Second))
	slowResp, err := io.ReadAll(slowConn)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(slowResp), "Connection: close") || !strings.HasSuffix(string(slowResp), "slow") {
		t.Fatalf("in-flight response = %q, want Connection: close and slow", slowResp)
	}

	// 在短暂宽限期内复用空闲 keep-alive 连接发送 POST 请求：仍应成功返回 200 并附带 Connection: close。
	if _, err := io.WriteString(idleConn, "POST /fast HTTP/1.1\r\nHost: test\r\nContent-Length: 0\r\n\r\n"); err != nil {
		t.Fatal(err)
	}
	_ = idleConn.SetReadDeadline(time.Now().Add(2 * time.Second))
	idleResp, err := io.ReadAll(idleConn)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(idleResp), "HTTP/1.1 200 OK") || !strings.Contains(string(idleResp), "Connection: close") || !strings.HasSuffix(string(idleResp), "fast") {
		t.Fatalf("reused idle conn response = %q, want 200 + Connection: close + fast", idleResp)
	}

	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
