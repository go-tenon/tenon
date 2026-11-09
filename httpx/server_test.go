package httpx

import (
	"context"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/cockroachdb/errors"
)

type testServer struct {
	url    string
	cancel context.CancelFunc
	done   chan error
}

func startServer(t *testing.T, s *Server) *testServer {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	ts := &testServer{url: "http://" + ln.Addr().String(), cancel: cancel, done: make(chan error, 1)}
	go func() { ts.done <- s.Serve(ctx, ln) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-ts.done:
		case <-time.After(5 * time.Second):
			t.Error("server did not stop")
		}
	})
	return ts
}

func get(t *testing.T, url string) (int, string) {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, strings.TrimSpace(string(b))
}

func TestServerHealth(t *testing.T) {
	var ready error
	s := NewServer(DefaultConfig(), http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "app")
	}))
	s.ReadyCheck = func(context.Context) error { return ready }
	ts := startServer(t, s)

	if code, body := get(t, ts.url+"/healthz"); code != 200 || body != "ok" {
		t.Errorf("/healthz = %d %q", code, body)
	}
	if code, body := get(t, ts.url+"/readyz"); code != 200 || body != "ok" {
		t.Errorf("/readyz = %d %q", code, body)
	}
	ready = errors.New("db down")
	if code, _ := get(t, ts.url+"/readyz"); code != 503 {
		t.Errorf("/readyz with failing check = %d, want 503", code)
	}
	if code, body := get(t, ts.url+"/other"); code != 200 || body != "app" {
		t.Errorf("/other = %d %q", code, body)
	}
}

func TestServerGracefulShutdown(t *testing.T) {
	cfg := DefaultConfig()
	cfg.ShutdownDelay = 300 * time.Millisecond

	started := make(chan struct{})
	release := make(chan struct{})
	s := NewServer(cfg, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/slow" {
			close(started)
			<-release
		}
		_, _ = io.WriteString(w, "done")
	}))
	ts := startServer(t, s)

	slow := make(chan string, 1)
	go func() {
		_, body := get(t, ts.url+"/slow")
		slow <- body
	}()
	<-started

	ts.cancel()

	// During ShutdownDelay readiness fails but new requests are still served.
	time.Sleep(50 * time.Millisecond)
	if code, _ := get(t, ts.url+"/readyz"); code != 503 {
		t.Errorf("/readyz while draining = %d, want 503", code)
	}
	if code, body := get(t, ts.url+"/fast"); code != 200 || body != "done" {
		t.Errorf("/fast while draining = %d %q", code, body)
	}

	// The in-flight request completes before Serve returns.
	select {
	case err := <-ts.done:
		t.Fatalf("Serve returned before in-flight request finished: %v", err)
	case <-time.After(400 * time.Millisecond):
	}
	close(release)
	if body := <-slow; body != "done" {
		t.Errorf("slow body = %q", body)
	}
	if err := <-ts.done; err != nil {
		t.Errorf("Serve = %v, want nil", err)
	}
	ts.done <- nil // let Cleanup finish
}

func TestServerShutdownTimeout(t *testing.T) {
	cfg := DefaultConfig()
	cfg.ShutdownTimeout = 100 * time.Millisecond

	started := make(chan struct{})
	s := NewServer(cfg, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-r.Context().Done() // canceled when the connection is force-closed
	}))
	ts := startServer(t, s)

	go func() {
		resp, err := http.Get(ts.url + "/stuck")
		if err == nil {
			resp.Body.Close()
		}
	}()
	<-started
	ts.cancel()

	err := <-ts.done
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("Serve = %v, want deadline exceeded", err)
	}
	ts.done <- nil
}

func TestServerRunListenError(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Addr = "256.0.0.1:0"
	if err := NewServer(cfg, http.NotFoundHandler()).Run(context.Background()); err == nil {
		t.Fatal("expected listen error")
	}
}
