package broadcast_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/go-tenon/tenon/broadcast"
	"github.com/go-tenon/tenon/broadcast/broadcasttest"
	"github.com/go-tenon/tenon/identity"
)

// signaled 检查 ch 在 wait 内收到（want 为 true）或没有收到信号。
func signaled(t *testing.T, ch <-chan struct{}, want bool, what string) {
	t.Helper()
	wait := 50 * time.Millisecond
	if want {
		wait = 2 * time.Second
	}
	select {
	case <-ch:
		if !want {
			t.Fatalf("%s: unexpected signal", what)
		}
	case <-time.After(wait):
		if want {
			t.Fatalf("%s: no signal", what)
		}
	}
}

// watchChan 把 Credentials.Watch 的回调转为单缓冲通道，便于测试断言。
func watchChan(ctx context.Context, creds *broadcast.Credentials, sub identity.Subject) <-chan struct{} {
	ch := make(chan struct{}, 1)
	creds.Watch(ctx, sub, func() {
		select {
		case ch <- struct{}{}:
		default:
		}
	})
	return ch
}

func TestCredentialsWatch(t *testing.T) {
	t.Parallel()
	hub := broadcast.New(broadcasttest.NewBackend())
	defer hub.Close()
	creds := broadcast.NewCredentials(hub)
	ctx, cancel := context.WithCancel(identity.WithTenant(context.Background(), "acme"))
	defer cancel()
	ch := watchChan(ctx, creds, identity.Subject{Type: "user", ID: "alice", Credential: "token:1", Impersonator: identity.Ref{Type: "staff", ID: "s1"}})

	bg := context.Background()
	for _, tc := range []struct {
		what    string
		recheck func() error
		want    bool
	}{
		{"same credential", func() error { return creds.RecheckCredential(bg, "token:1") }, true},
		{"other credential", func() error { return creds.RecheckCredential(bg, "token:2") }, false},
		{"same subject", func() error { return creds.RecheckSubject(bg, identity.Ref{Type: "user", ID: "alice"}) }, true},
		{"same impersonator", func() error { return creds.RecheckSubject(bg, identity.Ref{Type: "staff", ID: "s1"}) }, true},
		{"other impersonator", func() error { return creds.RecheckSubject(bg, identity.Ref{Type: "staff", ID: "s2"}) }, false},
		{"other subject", func() error { return creds.RecheckSubject(bg, identity.Ref{Type: "user", ID: "bob"}) }, false},
		{"same ID in another account system", func() error { return creds.RecheckSubject(bg, identity.Ref{Type: "staff", ID: "alice"}) }, false},
		{"request tenant", func() error { return creds.RecheckTenant(bg, "acme") }, true},
		{"other tenant", func() error { return creds.RecheckTenant(bg, "globex") }, false},
		{"platform", func() error { return creds.RecheckTenant(bg, "") }, false},
	} {
		if err := tc.recheck(); err != nil {
			t.Fatalf("%s: %v", tc.what, err)
		}
		signaled(t, ch, tc.want, tc.what)
	}

	// 读取前的多个提醒合并为一个信号。
	for range 3 {
		if err := creds.RecheckSubject(bg, identity.Ref{Type: "user", ID: "alice"}); err != nil {
			t.Fatal(err)
		}
	}
	signaled(t, ch, true, "first of merged")
	signaled(t, ch, false, "merged")

	// ID 必填。
	if creds.RecheckCredential(bg, "") == nil || creds.RecheckSubject(bg, identity.Ref{}) == nil {
		t.Fatal("empty id accepted")
	}

	// ctx 结束后停止关注。
	cancel()
	time.Sleep(20 * time.Millisecond)
	if err := creds.RecheckSubject(bg, identity.Ref{Type: "user", ID: "alice"}); err != nil {
		t.Fatal(err)
	}
	signaled(t, ch, false, "after ctx ended")
}

// lossyBackend 在进程内投递已订阅主题的消息，Lose 让 Hub 以为丢了消息。
type lossyBackend struct {
	mu      sync.Mutex
	deliver func(broadcast.Message)
	lost    func(error)
	topics  map[string]struct{}
}

func (b *lossyBackend) Publish(_ context.Context, msg broadcast.Message) error {
	b.mu.Lock()
	_, ok := b.topics[msg.Topic]
	deliver := b.deliver
	b.mu.Unlock()
	if ok && deliver != nil {
		deliver(msg)
	}
	return nil
}

func (b *lossyBackend) Open(deliver func(broadcast.Message), lost func(error)) broadcast.Session {
	b.mu.Lock()
	b.deliver, b.lost = deliver, lost
	b.topics = map[string]struct{}{}
	b.mu.Unlock()
	return &lossySession{b: b}
}

func (b *lossyBackend) Lose() {
	b.mu.Lock()
	lost := b.lost
	b.mu.Unlock()
	lost(errors.New("connection reset"))
}

type lossySession struct{ b *lossyBackend }

func (s *lossySession) Subscribe(topics ...string) error {
	s.b.mu.Lock()
	for _, topic := range topics {
		s.b.topics[topic] = struct{}{}
	}
	s.b.mu.Unlock()
	return nil
}

func (s *lossySession) Unsubscribe(topics ...string) error {
	s.b.mu.Lock()
	for _, topic := range topics {
		delete(s.b.topics, topic)
	}
	s.b.mu.Unlock()
	return nil
}

func (s *lossySession) Close() error { return nil }

// Backend 可能丢了消息时，Watch 发出一次提醒，之后的提醒照常送达。
func TestCredentialsWatchSignalsAfterLoss(t *testing.T) {
	t.Parallel()
	backend := &lossyBackend{}
	hub := broadcast.New(backend)
	defer hub.Close()
	creds := broadcast.NewCredentials(hub)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch := watchChan(ctx, creds, identity.Subject{Type: "user", ID: "alice"})

	backend.Lose()
	signaled(t, ch, true, "after loss")
	if err := creds.RecheckSubject(context.Background(), identity.Ref{Type: "user", ID: "alice"}); err != nil {
		t.Fatal(err)
	}
	signaled(t, ch, true, "after loss recovery")
}

// Hub 关闭后 Watch 不再收到提醒。
func TestCredentialsWatchStopsWhenHubCloses(t *testing.T) {
	t.Parallel()
	hub := broadcast.New(broadcasttest.NewBackend())
	creds := broadcast.NewCredentials(hub)
	ch := watchChan(context.Background(), creds, identity.Subject{Type: "user", ID: "alice"})
	if err := hub.Close(); err != nil {
		t.Fatal(err)
	}
	signaled(t, ch, false, "hub closed")
}
