package apitoken_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/go-tenon/tenon/apitoken"
	"github.com/go-tenon/tenon/database"
	"github.com/go-tenon/tenon/database/dbtest"
	"github.com/go-tenon/tenon/identity"
)

// fakeRechecker 记录 apitoken 自动发出的凭据复查事件。
type fakeRechecker struct {
	mu          sync.Mutex
	credentials []string
	subjects    []identity.Ref
}

// RecheckCredential 记录凭据级复查。
func (f *fakeRechecker) RecheckCredential(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.credentials = append(f.credentials, id)
	return nil
}

// RecheckSubject 记录主体级复查。
func (f *fakeRechecker) RecheckSubject(_ context.Context, ref identity.Ref) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.subjects = append(f.subjects, ref)
	return nil
}

// RecheckTenant 记录租户级复查。
func (f *fakeRechecker) RecheckTenant(_ context.Context, _ string) error { return nil }

func TestCredentialsAndWithDB(t *testing.T) {
	t.Parallel()
	db := dbtest.Open(t, apitoken.Migrations())
	rec := &fakeRechecker{}
	store := apitoken.NewStore(db, apitoken.Config{
		Lifetime:    24 * time.Hour,
		IdleTimeout: time.Hour,
	}, apitoken.Credentials(rec))
	ctx := t.Context()

	user := identity.Subject{Type: "member", ID: "m1"}
	plain1, err := store.Login(ctx, user, apitoken.Device{UserAgent: "ua1"})
	if err != nil {
		t.Fatal(err)
	}
	sub1, err := store.Validate(ctx, plain1)
	if err != nil {
		t.Fatal(err)
	}
	plain2, err := store.Login(ctx, user, apitoken.Device{UserAgent: "ua2"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Validate(ctx, plain2); err != nil {
		t.Fatal(err)
	}

	// 回滚时不发复查通知。
	_ = database.RunInTx(ctx, db, nil, func(ctx context.Context, tx database.Tx) error {
		if err := store.WithDB(tx).Revoke(ctx, sub1, sub1.Credential); err != nil {
			t.Fatal(err)
		}
		return context.Canceled
	})
	if len(rec.credentials) != 0 {
		t.Fatalf("expected no recheck on rollback, got %+v", rec.credentials)
	}

	// 提交时发 RecheckCredential。
	if err := database.RunInTx(ctx, db, nil, func(ctx context.Context, tx database.Tx) error {
		return store.WithDB(tx).Revoke(ctx, sub1, sub1.Credential)
	}); err != nil {
		t.Fatal(err)
	}
	if len(rec.credentials) != 1 || rec.credentials[0] != sub1.Credential {
		t.Fatalf("credentials = %+v, want [%s]", rec.credentials, sub1.Credential)
	}

	// RevokeSubject 发 RecheckSubject。
	if _, err := store.RevokeSubject(ctx, user.Ref()); err != nil {
		t.Fatal(err)
	}
	if len(rec.subjects) != 1 || rec.subjects[0] != user.Ref() {
		t.Fatalf("subjects = %+v, want [%v]", rec.subjects, user.Ref())
	}
}
