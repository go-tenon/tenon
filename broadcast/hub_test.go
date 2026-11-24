package broadcast_test

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/go-tenon/tenon/broadcast"
	"github.com/go-tenon/tenon/broadcast/broadcasttest"
)

func TestHubDeliversAcrossInstances(t *testing.T) {
	t.Parallel()
	backend := broadcasttest.NewBackend()
	h := broadcast.New(backend, nil)
	defer h.Close()
	sub := h.Subscribe("room:1")
	if err := h.PublishJSON(context.Background(), "room:1", map[string]string{"type": "message"}); err != nil {
		t.Fatal(err)
	}
	var ev map[string]string
	if err := json.Unmarshal(broadcasttest.Receive(t, sub).Data, &ev); err != nil || ev["type"] != "message" {
		t.Fatalf("event=%v err=%v", ev, err)
	}
	// 同一个 Backend 上的另一个 Hub 相当于另一个实例。
	other := broadcast.New(backend, nil)
	defer other.Close()
	otherSub := other.Subscribe("room:2")
	_ = h.Publish(context.Background(), "room:2", []byte("x"))
	if got := broadcasttest.Receive(t, otherSub); string(got.Data) != "x" {
		t.Fatalf("other instance received %+v", got)
	}
}

func TestBackendContract(t *testing.T) {
	t.Parallel()
	broadcasttest.TestBackend(t, func(*testing.T) (broadcast.Backend, broadcast.Backend) {
		b := broadcasttest.NewBackend()
		return b, b
	})
}

func TestSlowSubscriberEnds(t *testing.T) {
	t.Parallel()
	h := broadcast.New(broadcasttest.NewBackend(), nil)
	defer h.Close()
	slow, fast := h.Subscribe("room:1"), h.Subscribe("room:1")
	ctx := context.Background()
	for range 100 {
		_ = h.Publish(ctx, "room:1", []byte("x"))
		for len(fast.C()) > 0 {
			<-fast.C()
		}
	}
	n := 0
	for range slow.C() {
		n++
	}
	if !errors.Is(slow.Err(), broadcast.ErrSlow) || n != 64 {
		t.Fatalf("slow: err=%v buffered=%d", slow.Err(), n)
	}
	if fast.Err() != nil {
		t.Fatalf("fast subscription ended: %v", fast.Err())
	}
}

// lossy 是一个可以手动报告丢失的 Backend。
type lossy struct {
	mu   sync.Mutex
	lost func(error)
}

func (l *lossy) Publish(context.Context, broadcast.Message) error { return nil }

func (l *lossy) Open(_ func(broadcast.Message), lost func(error)) broadcast.Session {
	l.mu.Lock()
	l.lost = lost
	l.mu.Unlock()
	return noopSession{}
}

func (l *lossy) Lose(err error) {
	l.mu.Lock()
	fn := l.lost
	l.mu.Unlock()
	fn(err)
}

type noopSession struct{}

func (noopSession) Subscribe(...string) error   { return nil }
func (noopSession) Unsubscribe(...string) error { return nil }
func (noopSession) Close() error                { return nil }

func TestLostEndsAllSubscriptions(t *testing.T) {
	t.Parallel()
	backend := &lossy{}
	h := broadcast.New(backend, nil)
	defer h.Close()
	a, b := h.Subscribe("room:1"), h.Subscribe("room:2", "room:3")
	cause := errors.New("connection reset")
	backend.Lose(cause)
	for _, sub := range []*broadcast.Subscription{a, b} {
		if _, ok := <-sub.C(); ok || !errors.Is(sub.Err(), broadcast.ErrLost) {
			t.Fatalf("err=%v", sub.Err())
		}
	}
	// 之后的订阅照常工作。
	if h.Subscribe("room:1").Err() != nil {
		t.Fatal("new subscription ended")
	}
}

func TestTopics(t *testing.T) {
	t.Parallel()
	for _, topic := range []string{"room:42", "room.42", "a_b-c", "x"} {
		if err := broadcast.ValidateTopic(topic); err != nil {
			t.Errorf("%q: %v", topic, err)
		}
	}
	for _, topic := range []string{"", "room 42", "room*", "room>", ".room", "room.", "a..b", "房间", string(make([]byte, 201))} {
		if broadcast.ValidateTopic(topic) == nil {
			t.Errorf("%q accepted", topic)
		}
	}
	h := broadcast.New(broadcasttest.NewBackend(), nil)
	defer h.Close()
	if h.Publish(context.Background(), "bad topic", nil) == nil {
		t.Error("Publish accepted an invalid topic")
	}
	for name, f := range map[string]func(){
		"no topics":     func() { h.Subscribe() },
		"invalid topic": func() { h.Subscribe("bad topic") },
		"nil backend":   func() { broadcast.New(nil, nil) },
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

func TestSubscribeAfterClose(t *testing.T) {
	t.Parallel()
	h := broadcast.New(broadcasttest.NewBackend(), nil)
	_ = h.Close()
	sub := h.Subscribe("room:1")
	if _, ok := <-sub.C(); ok || !errors.Is(sub.Err(), broadcast.ErrClosed) {
		t.Fatalf("err=%v", sub.Err())
	}
	sub.Close() // 重复关闭无害
}

func TestReceiverSubscribesOnDemand(t *testing.T) {
	t.Parallel()
	h := broadcast.New(broadcasttest.NewBackend(), nil)
	defer h.Close()
	var got []string
	r := h.Receiver(func(m broadcast.Message) { got = append(got, m.Topic+"="+string(m.Data)) }, nil)
	defer r.Close()
	if err := r.Subscribe("room:1"); err != nil {
		t.Fatal(err)
	}
	sub := h.Subscribe("room:1")
	for _, topic := range []string{"room:1", "room:2"} {
		if err := h.Publish(context.Background(), topic, []byte("x")); err != nil {
			t.Fatal(err)
		}
	}
	// 只收到自己订阅的 room:1，不收到未订阅的 room:2。
	if want := []string{"room:1=x"}; !slices.Equal(got, want) {
		t.Fatalf("receiver got %v, want %v", got, want)
	}
	broadcasttest.Receive(t, sub)

	if err := r.Unsubscribe("room:1"); err != nil {
		t.Fatal(err)
	}
	_ = h.Publish(context.Background(), "room:1", []byte("y"))
	if len(got) != 1 {
		t.Fatalf("receiver got %v after unsubscribe", got)
	}

	r.Close()
	r.Close()
	if err := r.Subscribe("room:1"); !errors.Is(err, broadcast.ErrClosed) {
		t.Fatalf("subscribe after close: %v", err)
	}
}

func TestReceiverIsToldAboutLostMessages(t *testing.T) {
	t.Parallel()
	backend := &lossy{}
	h := broadcast.New(backend, nil)
	defer h.Close()
	lost := make(chan error, 1)
	r := h.Receiver(func(broadcast.Message) {}, func(err error) { lost <- err })
	defer r.Close()
	cause := errors.New("connection reset")
	backend.Lose(cause)
	select {
	case err := <-lost:
		if !errors.Is(err, cause) {
			t.Fatalf("lost called with %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("lost was not called")
	}
}
