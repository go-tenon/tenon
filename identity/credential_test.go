package identity_test

import (
	"context"
	"errors"
	"testing"

	"github.com/go-tenon/tenon/errorx"
	"github.com/go-tenon/tenon/identity"
)

func TestCheckCredential(t *testing.T) {
	t.Parallel()
	errRevoked := errorx.Define(errorx.Unauthenticated, "auth.unauthenticated")
	errUnavailable := errorx.Define(errorx.Unavailable, "users.unavailable")

	ctx := context.Background()
	if err := identity.CheckCredential(ctx); err != nil {
		t.Fatalf("no checks: %v", err)
	}
	ok := identity.WithCredentialCheck(ctx, func(context.Context) error { return nil })
	if err := identity.CheckCredential(ok); err != nil {
		t.Fatalf("passing check: %v", err)
	}
	// 暂时无法判断（系统故障、普通错误）不算失效。
	for _, err := range []error{errUnavailable.New(), errors.New("db down")} {
		c := identity.WithCredentialCheck(ok, func(context.Context) error { return err })
		if got := identity.CheckCredential(c); got != nil {
			t.Fatalf("%v treated as revoked", err)
		}
	}
	// 多个校验依次运行，返回第一个失效的错误；派生出的 context 相互独立。
	revoked := identity.WithCredentialCheck(ok, func(context.Context) error { return errRevoked.New() })
	if err := identity.CheckCredential(revoked); !errors.Is(err, errRevoked) {
		t.Fatalf("revoked: %v", err)
	}
	if err := identity.CheckCredential(ok); err != nil {
		t.Fatalf("parent context changed: %v", err)
	}
}

type watcher struct{ ch chan struct{} }

func (w watcher) Watch(context.Context, identity.Subject) <-chan struct{} { return w.ch }

func TestWatchCredential(t *testing.T) {
	t.Parallel()
	w := watcher{ch: make(chan struct{})}
	ctx := context.Background()
	if identity.WatchCredential(identity.WithCredentialWatcher(ctx, w)) != nil {
		t.Fatal("watched without a subject")
	}
	if identity.WatchCredential(identity.WithSubject(ctx, identity.Subject{ID: "alice"})) != nil {
		t.Fatal("watched without a watcher")
	}
	both := identity.WithCredentialWatcher(identity.WithSubject(ctx, identity.Subject{ID: "alice"}), w)
	if got := identity.WatchCredential(both); got != (<-chan struct{})(w.ch) {
		t.Fatal("watcher channel not returned")
	}
}
