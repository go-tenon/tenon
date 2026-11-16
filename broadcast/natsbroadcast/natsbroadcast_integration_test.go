package natsbroadcast_test

import (
	"context"
	"crypto/rand"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/go-tenon/tenon/broadcast"
	"github.com/go-tenon/tenon/broadcast/broadcasttest"
	"github.com/go-tenon/tenon/broadcast/natsbroadcast"
	"github.com/go-tenon/tenon/internal/natstest"
)

// prefix 返回测试独用的 subject 前缀。
func prefix() string { return "test.broadcast." + strings.ToLower(rand.Text()) }

func TestBackend(t *testing.T) {
	t.Parallel()
	broadcasttest.TestBackend(t, func(t *testing.T) (broadcast.Backend, broadcast.Backend) {
		p := prefix()
		return natsbroadcast.New(natstest.Connect(t), p), natsbroadcast.New(natstest.Connect(t), p)
	})
}

// 连接断开后订阅以 ErrLost 结束；NATS 客户端重新连上后，新的订阅照常收到消息。
func TestReconnectEndsSubscriptions(t *testing.T) {
	t.Parallel()
	nc := natstest.Connect(t)
	h := broadcast.New(natsbroadcast.New(nc, prefix()), nil)
	defer h.Close()
	sub := h.Subscribe("room:1")
	broadcasttest.Ready(t, h)

	if err := nc.ForceReconnect(); err != nil {
		t.Fatal(err)
	}
	expectLost(t, sub)
	expectRecovered(t, h)
}

// expectRecovered 检查新的订阅照常收到消息。重新连上时 Backend 还会报告一次丢失，这个通知是异步的，
// 可能结束刚建立的订阅；这时换一个新订阅再试。
func expectRecovered(t *testing.T, h *broadcast.Hub) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		again := h.Subscribe("room:1")
		if err := h.Publish(context.Background(), "room:1", []byte("back")); err != nil {
			t.Fatal(err)
		}
		select {
		case m, ok := <-again.C():
			if ok && string(m.Data) == "back" {
				again.Close()
				return
			}
		case <-time.After(500 * time.Millisecond):
		}
		again.Close()
	}
	t.Fatal("hub did not recover within 10s")
}

// 连接被关闭后订阅以 ErrLost 结束，发布返回错误。
func TestClosedConnection(t *testing.T) {
	t.Parallel()
	nc := natstest.Connect(t)
	h := broadcast.New(natsbroadcast.New(nc, prefix()), nil)
	defer h.Close()
	sub := h.Subscribe("room:1")
	broadcasttest.Ready(t, h)

	nc.Close()
	expectLost(t, sub)
	if err := h.Publish(context.Background(), "room:1", []byte("x")); err == nil {
		t.Fatal("publish on a closed connection succeeded")
	}
}

func expectLost(t *testing.T, sub *broadcast.Subscription) {
	t.Helper()
	select {
	case _, ok := <-sub.C():
		if ok || !errors.Is(sub.Err(), broadcast.ErrLost) {
			t.Fatalf("ok=%v err=%v", ok, sub.Err())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("subscription not ended")
	}
}

func TestNewPanics(t *testing.T) {
	t.Parallel()
	nc := natstest.Connect(t)
	for name, f := range map[string]func(){
		"nil connection": func() { natsbroadcast.New(nil, "app.broadcast") },
		"empty prefix":   func() { natsbroadcast.New(nc, "") },
		"empty token":    func() { natsbroadcast.New(nc, "app..broadcast") },
		"trailing dot":   func() { natsbroadcast.New(nc, "app.") },
		"wildcard":       func() { natsbroadcast.New(nc, "app.*") },
		"full wildcard":  func() { natsbroadcast.New(nc, "app.>") },
		"space":          func() { natsbroadcast.New(nc, "app broadcast") },
	} {
		t.Run(name, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Fatal("expected panic")
				}
			}()
			f()
		})
	}
}
