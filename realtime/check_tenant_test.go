package realtime_test

import (
	"context"
	"net/http"
	"sync/atomic"
	"testing"

	"github.com/go-tenon/tenon/broadcast"
	"github.com/go-tenon/tenon/errorx"
	"github.com/go-tenon/tenon/identity"
	"github.com/go-tenon/tenon/realtime"
)

type staleSnapshotKey struct{}

var errTenantDisabled = errorx.Define(errorx.PermissionDenied, "test.tenant_disabled")

func TestCheckIsolatesHandshakeContextAndCheckTenant(t *testing.T) {
	t.Parallel()
	b := &bus{}
	hub := broadcast.New(b.backend(), nil)
	t.Cleanup(func() { _ = hub.Close() })
	creds := broadcast.NewCredentials(hub)

	var disabled atomic.Bool
	var sawStaleOnRecheck atomic.Bool

	opts := realtime.Options{
		Authenticate: func(_ context.Context, token string) (identity.Subject, error) {
			if token == "u1" {
				return identity.Subject{Type: "user", ID: "u1"}, nil
			}
			return identity.Subject{}, errRevoked.New()
		},
		Accounts: identity.Accounts{
			"user": func(ctx context.Context, sub identity.Subject) (identity.Subject, error) {
				if identity.Rechecking(ctx) && ctx.Value(staleSnapshotKey{}) != nil {
					sawStaleOnRecheck.Store(true)
				}
				sub.Tenant = identity.TenantFromContext(ctx)
				return sub, nil
			},
		},
		Tenant: func(_ *http.Request) string { return "t1" },
		CheckTenant: func(_ context.Context, tenant string) error {
			if tenant == "t1" && disabled.Load() {
				return errTenantDisabled.New()
			}
			return nil
		},
		Credentials: creds,
	}

	wrap := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := context.WithValue(r.Context(), staleSnapshotKey{}, "handshake-snapshot")
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}

	n := start(t, b.backend(), opts, wrap)
	c, f := connect(t, n.url, "u1", nil)
	if f.Error != nil {
		t.Fatalf("connect error: %+v", f.Error)
	}
	defer c.close()

	// 停用租户并通知该租户复查，连接必须立即断开且复查未见到握手期快照。
	disabled.Store(true)
	if err := creds.RecheckTenant(t.Context(), "t1"); err != nil {
		t.Fatal(err)
	}
	if code := c.closed(); code != 3500 {
		t.Fatalf("close code = %d, want 3500", code)
	}
	if sawStaleOnRecheck.Load() {
		t.Fatal("recheck saw stale handshake context value")
	}
}
