package httpcmd_test

import (
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/go-tenon/tenon/command"
	"github.com/go-tenon/tenon/command/httpcmd"
	"github.com/go-tenon/tenon/httpx"
)

// healthConfig 是 health 测试的应用配置。
type healthConfig struct {
	HTTP httpx.Config `yaml:"http"`
}

// serveLive 在 127.0.0.1 的随机端口上提供 /livez，返回 status，结束时关闭；返回端口。
func serveLive(t *testing.T, status int) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != httpcmd.LivePath {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(status)
	})}
	go srv.Serve(ln)
	t.Cleanup(func() { srv.Close() })
	_, port, _ := net.SplitHostPort(ln.Addr().String())
	return port
}

// health 用监听地址 addr 执行 health 命令。
func health(t *testing.T, addr string) error {
	t.Helper()
	path := filepath.Join(t.TempDir(), "app.yaml")
	if err := os.WriteFile(path, []byte("http:\n  addr: \""+addr+"\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	root := newRoot(httpcmd.Health(func(c healthConfig) httpx.Config { return c.HTTP }))
	_, _, err := execute(root, "health", "--config", path)
	return err
}

func TestHealth(t *testing.T) {
	t.Parallel()
	// 监听所有地址时请求 127.0.0.1。
	if err := health(t, ":"+serveLive(t, http.StatusNoContent)); err != nil {
		t.Fatalf("live: %v", err)
	}
	if err := health(t, "127.0.0.1:"+serveLive(t, http.StatusServiceUnavailable)); command.ExitCode(err) != command.ExitFailure {
		t.Fatalf("503: exit=%d err=%v", command.ExitCode(err), err)
	}
	// 没有服务在监听。
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()
	if err := health(t, addr); command.ExitCode(err) != command.ExitFailure {
		t.Fatalf("refused: exit=%d err=%v", command.ExitCode(err), err)
	}
}
