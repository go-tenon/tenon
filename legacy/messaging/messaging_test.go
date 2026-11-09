package messaging_test

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cockroachdb/errors"

	"github.com/uptrace/bun"

	"github.com/ryan053857/go-kit/database"
	"github.com/ryan053857/go-kit/database/dbtest"
	"github.com/ryan053857/go-kit/httpx"
	"github.com/ryan053857/go-kit/logger"
	"github.com/ryan053857/go-kit/messaging"
)

type event struct {
	N int `json:"n"`
}

func setup(t *testing.T) (*bun.DB, *messaging.Bus) {
	t.Helper()
	cfg := database.DefaultConfig()
	cfg.DSN = dbtest.DSN(t)
	db, err := database.Open(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	bus, err := messaging.New(db.DB)
	if err != nil {
		t.Fatal(err)
	}
	return db, bus
}

func run(t *testing.T, bus *messaging.Bus) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- bus.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil {
			t.Errorf("Run: %v", err)
		}
	})
	select {
	case <-bus.Running():
	case <-time.After(10 * time.Second):
		t.Fatal("bus not running")
	}
}

func TestPublishTx(t *testing.T) {
	db, bus := setup(t)
	got := make(chan event, 10)
	err := messaging.Handle(bus, "test_event", func(ctx context.Context, e event) error {
		got <- e
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := messaging.Handle(bus, "test_event", func(context.Context, event) error { return nil }); err == nil {
		t.Error("second handler for the same topic accepted")
	}

	ctx := t.Context()
	// 回滚的事务中发布的消息不会被投递。
	_ = db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if err := messaging.PublishTx(ctx, tx.Tx, "test_event", event{N: 1}); err != nil {
			t.Fatal(err)
		}
		return errors.New("rollback")
	})
	// 提交的事务中发布的消息会被投递。
	err = db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		return messaging.PublishTx(ctx, tx.Tx, "test_event", event{N: 2})
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := bus.Publish(ctx, "test_event", event{N: 3}); err != nil {
		t.Fatal(err)
	}

	run(t, bus)
	for _, want := range []int{2, 3} {
		select {
		case e := <-got:
			if e.N != want {
				t.Errorf("got %d, want %d", e.N, want)
			}
		case <-time.After(10 * time.Second):
			t.Fatalf("timeout waiting for event %d", want)
		}
	}
	select {
	case e := <-got:
		t.Errorf("unexpected event %d", e.N)
	case <-time.After(1500 * time.Millisecond):
	}

	// 处理成功的消息已删除。
	var n int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM watermill_test_event`).Scan(&n); err != nil || n != 0 {
		t.Errorf("remaining messages = %d, %v", n, err)
	}
}

func TestRequestID(t *testing.T) {
	_, bus := setup(t)

	// 处理函数中的日志会带上发布方请求的 request_id。
	var buf syncBuffer
	old := slog.Default()
	slog.SetDefault(slog.New(logger.NewContextHandler(slog.NewTextHandler(&buf, nil))))
	t.Cleanup(func() { slog.SetDefault(old) })

	done := make(chan struct{})
	err := messaging.Handle(bus, "with_request", func(ctx context.Context, _ event) error {
		slog.InfoContext(ctx, "handled")
		close(done)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	h := httpx.RequestID(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := bus.Publish(r.Context(), "with_request", event{N: 1}); err != nil {
			t.Error(err)
		}
	}))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set(httpx.RequestIDHeader, "req-123")
	h.ServeHTTP(httptest.NewRecorder(), req)

	run(t, bus)
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("timeout")
	}
	if s := buf.String(); !strings.Contains(s, "msg=handled") || !strings.Contains(s, "request_id=req-123") || !strings.Contains(s, "topic=with_request") {
		t.Errorf("log = %s", s)
	}
}

type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func TestPoison(t *testing.T) {
	db, bus := setup(t)
	var attempts atomic.Int32
	err := messaging.Handle(bus, "always_fails", func(context.Context, event) error {
		attempts.Add(1)
		return errors.New("boom")
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := bus.Publish(t.Context(), "always_fails", event{N: 1}); err != nil {
		t.Fatal(err)
	}
	run(t, bus)

	// 1 次执行 + 3 次重试（1s、2s、4s）后移入 poison。
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		var n int
		err := db.QueryRowContext(t.Context(), `SELECT count(*) FROM watermill_poison`).Scan(&n)
		if err == nil && n == 1 {
			if got := attempts.Load(); got != 4 {
				t.Errorf("attempts = %d, want 4", got)
			}
			var left int
			_ = db.QueryRowContext(t.Context(), `SELECT count(*) FROM watermill_always_fails`).Scan(&left)
			if left != 0 {
				t.Errorf("failed message still queued: %d", left)
			}
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("message not moved to poison queue, attempts = %d", attempts.Load())
}

func TestRunWithoutHandlers(t *testing.T) {
	_, bus := setup(t)
	run(t, bus) // 不阻塞、ctx 取消后返回 nil
}
