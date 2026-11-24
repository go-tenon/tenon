package natsbroadcast_test

import (
	"bytes"
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
	h := broadcast.New(natsbroadcast.New(nc, prefix()))
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
	h := broadcast.New(natsbroadcast.New(nc, prefix()))
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

// 超过 max_payload 的消息分片发送，订阅方收到原样的整条消息；两个连接同时发送的大消息各自拼回。
func TestLargeMessages(t *testing.T) {
	t.Parallel()
	p := prefix()
	nc := natstest.Connect(t)
	h := broadcast.New(natsbroadcast.New(nc, p))
	defer h.Close()
	other := broadcast.New(natsbroadcast.New(natstest.Connect(t), p))
	defer other.Close()
	sub := h.Subscribe("room:1")
	broadcasttest.Ready(t, h)

	big := func(b byte) []byte { return bytes.Repeat([]byte{b}, int(nc.MaxPayload())*3+123) }
	ctx := context.Background()
	if err := h.Publish(ctx, "room:1", big('a')); err != nil {
		t.Fatal(err)
	}
	if err := other.Publish(ctx, "room:1", big('b')); err != nil {
		t.Fatal(err)
	}
	if err := h.Publish(ctx, "room:1", []byte("small")); err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for range 3 {
		select {
		case m := <-sub.C():
			switch {
			case bytes.Equal(m.Data, big('a')):
				got["a"] = true
			case bytes.Equal(m.Data, big('b')):
				got["b"] = true
			case string(m.Data) == "small":
				got["small"] = true
			default:
				t.Fatalf("unexpected message of %d bytes", len(m.Data))
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("timed out, got %v", got)
		}
	}
	if len(got) != 3 {
		t.Fatalf("got %v", got)
	}
}

// 未订阅大消息主题的实例不会收到分片流量，只收到自己订阅的主题。
func TestUnsubscribedInstanceIsolatedFromLargeMessages(t *testing.T) {
	t.Parallel()
	p := prefix()
	pubNC := natstest.Connect(t)
	subNC := natstest.Connect(t)
	pub := broadcast.New(natsbroadcast.New(pubNC, p))
	defer pub.Close()
	subHub := broadcast.New(natsbroadcast.New(subNC, p))
	defer subHub.Close()

	sub := subHub.Subscribe("room:small")
	before := subNC.Statistics.InBytes
	ctx := context.Background()
	big := bytes.Repeat([]byte("x"), int(pubNC.MaxPayload())*3)
	if err := pub.Publish(ctx, "room:big", big); err != nil {
		t.Fatal(err)
	}
	if err := pub.Publish(ctx, "room:small", []byte("ok")); err != nil {
		t.Fatal(err)
	}
	if m := broadcasttest.Receive(t, sub); string(m.Data) != "ok" {
		t.Fatalf("got %+v", m)
	}
	if delta := subNC.Statistics.InBytes - before; delta > uint64(pubNC.MaxPayload()) {
		t.Fatalf("unsubscribed instance received %d bytes from room:big", delta)
	}
}
