package natspresence_test

import (
	"context"
	"crypto/rand"
	"testing"
	"time"

	"github.com/nats-io/nats.go/jetstream"

	"github.com/go-tenon/tenon/identity"
	"github.com/go-tenon/tenon/internal/natstest"
	"github.com/go-tenon/tenon/realtime"
	"github.com/go-tenon/tenon/realtime/natspresence"
	"github.com/go-tenon/tenon/realtime/realtimetest"
)

// newStore 创建测试独用的 bucket，测试结束时删除。
func newStore(t *testing.T) *natspresence.Store {
	t.Helper()
	nc := natstest.Connect(t)
	bucket := "test_presence_" + rand.Text()
	s, err := natspresence.New(context.Background(), nc, natspresence.Options{Bucket: bucket})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		js, err := jetstream.New(nc)
		if err != nil {
			t.Error(err)
			return
		}
		if err := js.DeleteKeyValue(context.Background(), bucket); err != nil {
			t.Error(err)
		}
	})
	return s
}

func TestStore(t *testing.T) {
	t.Parallel()
	realtimetest.TestPresenceStore(t, func(t *testing.T) realtime.PresenceStore { return newStore(t) })
}

// 频道名中的 ':' 和 '.' 不影响键：room 和 room.a 是两个频道，互不包含。
func TestChannelNamesAreOpaque(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := newStore(t)
	alice := identity.Ref{Type: "user", ID: "alice"}
	for _, ch := range []string{"room", "room.a", "room:a", "room.a.b"} {
		if err := s.Add(ctx, realtime.PresenceEntry{Channel: ch, Client: "c." + ch, Subject: alice}, time.Minute); err != nil {
			t.Fatal(err)
		}
	}
	for _, ch := range []string{"room", "room.a", "room:a", "room.a.b"} {
		got, err := s.List(ctx, ch)
		if err != nil || len(got) != 1 || got[0].Client != "c."+ch || got[0].Channel != ch {
			t.Fatalf("%s: %+v, %v", ch, got, err)
		}
	}
}

func TestAddRejectsTTLOutOfRange(t *testing.T) {
	t.Parallel()
	s := newStore(t)
	e := realtime.PresenceEntry{Channel: "room:1", Client: "c1", Subject: identity.Ref{Type: "user", ID: "alice"}}
	for _, ttl := range []time.Duration{0, -time.Second, natspresence.MaxTTL + time.Second} {
		if err := s.Add(context.Background(), e, ttl); err == nil {
			t.Errorf("ttl %s accepted", ttl)
		}
	}
}

func TestNewPanics(t *testing.T) {
	t.Parallel()
	nc := natstest.Connect(t)
	for name, f := range map[string]func(){
		"nil connection": func() { _, _ = natspresence.New(context.Background(), nil, natspresence.Options{Bucket: "b"}) },
		"empty bucket":   func() { _, _ = natspresence.New(context.Background(), nc, natspresence.Options{}) },
		"dot in bucket":  func() { _, _ = natspresence.New(context.Background(), nc, natspresence.Options{Bucket: "a.b"}) },
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
