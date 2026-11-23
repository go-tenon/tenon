package identity_test

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/go-tenon/tenon/errorx"
	"github.com/go-tenon/tenon/identity"
)

var support = identity.Ref{Type: "staff", ID: "support"}

var (
	errDown      = errorx.Define(errorx.Unavailable, "test.down")
	errDisabled  = errorx.Define(errorx.Unauthenticated, "test.disabled")
	errNotMember = errorx.Define(errorx.NotFound, "test.not_member")
)

func allowSupport(perms ...string) *identity.Impersonation {
	return &identity.Impersonation{
		Actors: identity.Accounts{
			"staff": func(ctx context.Context, sub identity.Subject) (identity.Subject, error) {
				if identity.TenantFromContext(ctx) != "" {
					return identity.Subject{}, errors.New("actor resolved outside platform tenant")
				}
				sub.Permissions = perms
				return sub, nil
			},
		},
		Permissions: []string{"users:impersonate"},
	}
}

func TestEstablish(t *testing.T) {
	t.Parallel()
	cred := identity.Subject{Type: "user", ID: "alice", Method: "bearer", Credential: "tok1", Scopes: []string{"articles:*"}}
	impCred := identity.Subject{Type: "user", ID: "alice", Method: "bearer", Credential: "tok2", Scopes: []string{"articles:*"}, Impersonator: support}
	as := func(sub identity.Subject, err error) identity.Authenticator {
		return func(context.Context) (identity.Subject, error) { return sub, err }
	}
	grant := func(perms ...string) identity.Accounts {
		return identity.Accounts{"user": func(ctx context.Context, sub identity.Subject) (identity.Subject, error) {
			sub.Tenant, sub.Permissions = identity.TenantFromContext(ctx), perms
			return sub, nil
		}}
	}
	fail := func(err error) identity.Accounts {
		return identity.Accounts{"user": func(context.Context, identity.Subject) (identity.Subject, error) {
			return identity.Subject{}, err
		}}
	}
	plain := errors.New("connection refused")
	for name, tc := range map[string]struct {
		tenant string
		auth   identity.Authenticator
		policy identity.Policy
		perms  []string
		want   error // nil 表示通过
		failed bool
		public map[string]any
	}{
		"credential only":             {auth: as(cred, nil)},
		"no credential":               {auth: as(identity.Subject{}, nil), want: identity.ErrUnauthenticated},
		"invalid credential":          {auth: as(identity.Subject{}, errDisabled.New()), want: identity.ErrUnauthenticated},
		"invalid with other kind":     {auth: as(identity.Subject{}, errNotMember.New()), want: identity.ErrUnauthenticated},
		"backend down":                {auth: as(identity.Subject{}, errDown.New()), want: errDown, failed: true},
		"backend plain error":         {auth: as(identity.Subject{}, plain), want: plain, failed: true},
		"bound to another tenant":     {tenant: "globex", auth: as(identity.Subject{Type: "user", ID: "alice", BoundTenant: "acme"}, nil), policy: identity.Policy{Accounts: grant("*")}, want: identity.ErrForbidden},
		"bound to this tenant":        {tenant: "acme", auth: as(identity.Subject{Type: "user", ID: "alice", BoundTenant: "acme"}, nil), policy: identity.Policy{Accounts: grant("*")}},
		"bound allowed in self scope": {tenant: "", auth: as(identity.Subject{Type: "user", ID: "alice", BoundTenant: "acme"}, nil), policy: identity.Policy{Accounts: grant("*"), AccountScope: true}},
		"bound blocked in platform":   {tenant: "", auth: as(identity.Subject{Type: "user", ID: "alice", BoundTenant: "acme"}, nil), policy: identity.Policy{Accounts: grant("*")}, want: identity.ErrForbidden},
		"impersonation fail closed":   {auth: as(impCred, nil), policy: identity.Policy{Accounts: grant("*")}, want: identity.ErrUnauthenticated},
		"impersonation allowed":       {auth: as(impCred, nil), policy: identity.Policy{Accounts: grant("articles:read"), Impersonation: allowSupport("users:impersonate")}, perms: []string{"articles:read"}},
		"impersonation actor missing": {auth: as(impCred, nil), policy: identity.Policy{Accounts: grant("*"), Impersonation: allowSupport("users:read")}, want: identity.ErrUnauthenticated},
		"impersonation direct only":   {auth: as(impCred, nil), policy: identity.Policy{Accounts: grant("*"), Impersonation: allowSupport("users:impersonate"), DirectOnly: true}, want: identity.ErrImpersonationForbidden},
		"impersonation actor down": {
			auth: as(impCred, nil),
			policy: identity.Policy{
				Accounts: grant("*"),
				Impersonation: &identity.Impersonation{
					Actors: identity.Accounts{"staff": func(context.Context, identity.Subject) (identity.Subject, error) {
						return identity.Subject{}, errDown.New()
					}},
				},
			},
			want: errDown, failed: true,
		},
		"resolver down":              {auth: as(cred, nil), policy: identity.Policy{Accounts: fail(errDown.New())}, want: errDown, failed: true},
		"resolver plain error":       {auth: as(cred, nil), policy: identity.Policy{Accounts: fail(plain)}, want: plain, failed: true},
		"resolver disables":          {auth: as(cred, nil), policy: identity.Policy{Accounts: fail(errDisabled.New())}, want: identity.ErrUnauthenticated},
		"resolver returns nobody":    {auth: as(cred, nil), policy: identity.Policy{Accounts: fail(nil)}, want: identity.ErrUnauthenticated},
		"resolver rejects":           {auth: as(cred, nil), policy: identity.Policy{Accounts: fail(errNotMember.New())}, want: errNotMember},
		"permission granted":         {auth: as(cred, nil), policy: identity.Policy{Accounts: grant("articles:write")}, perms: []string{"articles:write"}},
		"permission missing":         {auth: as(cred, nil), policy: identity.Policy{Accounts: grant("articles:read")}, perms: []string{"articles:read", "articles:write"}, want: identity.ErrForbidden, public: map[string]any{"permission": "articles:write"}},
		"permission outside scopes":  {auth: as(cred, nil), policy: identity.Policy{Accounts: grant("*")}, perms: []string{"users:read"}, want: identity.ErrForbidden, public: map[string]any{"permission": "users:read"}},
		"permission without resolve": {auth: as(cred, nil), perms: []string{"articles:read"}, want: identity.ErrForbidden},
	} {
		t.Run(name, func(t *testing.T) {
			ctx := identity.WithTenant(context.Background(), tc.tenant)
			sub, err := identity.Establish(ctx, tc.auth, tc.policy, tc.perms...)
			if identity.Failed(err) != tc.failed {
				t.Fatalf("Failed(%v) = %v", err, !tc.failed)
			}
			if tc.want == nil {
				if err != nil || sub.ID != "alice" {
					t.Fatalf("sub %+v err %v", sub, err)
				}
				return
			}
			if !errors.Is(err, tc.want) || sub.ID != "" {
				t.Fatalf("sub %+v err %v, want %v", sub, err, tc.want)
			}
			if tc.public != nil && !reflect.DeepEqual(errorx.PublicOf(err), tc.public) {
				t.Fatalf("public %v", errorx.PublicOf(err))
			}
		})
	}
}

// Resolver 加载权限和租户；主体本身、认证方式、凭据、租户边界、凭据的限制、代为登录者和验证情况总是取自凭据。
func TestEstablishKeepsCredentialFields(t *testing.T) {
	t.Parallel()
	signedIn := identity.Auth{Methods: []string{"password"}, Time: time.Date(2026, 11, 13, 8, 0, 0, 0, time.UTC)}
	cred := identity.Subject{
		ID: "alice", Type: "user", Method: "bearer", Credential: "tok1", BoundTenant: "acme",
		Scopes: []string{"articles:read"}, Impersonator: support, Auth: signedIn,
	}
	rebuilt := identity.Accounts{"user": func(context.Context, identity.Subject) (identity.Subject, error) {
		return identity.Subject{
			Type: "staff", ID: "mallory", Tenant: "acme", BoundTenant: "forged", Permissions: []string{"*"}, Method: "forged", Credential: "other",
			Impersonator: identity.Ref{Type: "staff", ID: "nobody"}, Auth: identity.Auth{Methods: []string{"password", "totp"}, Time: time.Now()},
		}, nil
	}}
	auth := func(context.Context) (identity.Subject, error) { return cred, nil }
	policy := identity.Policy{Accounts: rebuilt, Impersonation: allowSupport("users:impersonate")}
	ctx := identity.WithTenant(context.Background(), "acme")
	sub, err := identity.Establish(ctx, auth, policy, "articles:read")
	if err != nil {
		t.Fatal(err)
	}
	want := identity.Subject{
		Type: "user", ID: "alice", Tenant: "acme", BoundTenant: "acme", Permissions: []string{"*"},
		Method: "bearer", Credential: "tok1", Scopes: []string{"articles:read"}, Impersonator: support, Auth: signedIn,
	}
	if !reflect.DeepEqual(sub, want) {
		t.Fatalf("%+v", sub)
	}
	// 凭据的限制仍然生效：Resolver 丢掉 Scopes，权限照样受它约束。
	if _, err := identity.Establish(ctx, auth, policy, "articles:write"); !errors.Is(err, identity.ErrForbidden) {
		t.Fatalf("scopes ignored: %v", err)
	}
}

func TestReestablish(t *testing.T) {
	t.Parallel()
	was := identity.Subject{Type: "user", ID: "alice", Impersonator: support}
	now := was
	var authErr, resolveErr error
	perms := []string{"events"}
	actorPerms := []string{"users:impersonate"}
	auth := func(ctx context.Context) (identity.Subject, error) {
		if !identity.Rechecking(ctx) {
			t.Error("authenticator: not rechecking")
		}
		return now, authErr
	}
	resolve := identity.Accounts{"user": func(ctx context.Context, sub identity.Subject) (identity.Subject, error) {
		if !identity.Rechecking(ctx) {
			t.Error("resolver: not rechecking")
		}
		sub.Permissions = perms
		return sub, resolveErr
	}}
	imp := &identity.Impersonation{
		Actors: identity.Accounts{"staff": func(ctx context.Context, sub identity.Subject) (identity.Subject, error) {
			if !identity.Rechecking(ctx) {
				t.Error("actor resolver: not rechecking")
			}
			sub.Permissions = actorPerms
			return sub, nil
		}},
		Permissions: []string{"users:impersonate"},
	}
	policy := identity.Policy{Accounts: resolve, Impersonation: imp}
	check := func() error {
		_, err := identity.Reestablish(context.Background(), was, auth, policy, "events")
		return err
	}
	if identity.Rechecking(context.Background()) {
		t.Fatal("plain context is rechecking")
	}

	if err := check(); err != nil {
		t.Fatalf("unchanged: %v", err)
	}
	// 操作者失去代为登录权限后立即失效。
	actorPerms = nil
	if err := check(); !errors.Is(err, identity.ErrUnauthenticated) {
		t.Fatalf("actor revoked: %v", err)
	}
	actorPerms = []string{"users:impersonate"}

	// 暂时无法判断时不是失效。
	authErr = errDown.New()
	if err := check(); !identity.Failed(err) {
		t.Fatalf("backend down: %v", err)
	}
	authErr, resolveErr = nil, errDown.New()
	if err := check(); !identity.Failed(err) {
		t.Fatalf("resolver down: %v", err)
	}
	resolveErr = errDisabled.New()
	if err := check(); !errors.Is(err, identity.ErrUnauthenticated) {
		t.Fatalf("disabled: %v", err)
	}
	resolveErr, perms = nil, nil
	if err := check(); !errors.Is(err, identity.ErrForbidden) {
		t.Fatalf("permission removed: %v", err)
	}
	perms = []string{"events"}
	// 凭据换了主体，或者代为登录已经结束。
	now = identity.Subject{Type: "user", ID: "bob", Impersonator: support}
	if err := check(); !errors.Is(err, identity.ErrUnauthenticated) {
		t.Fatalf("another subject: %v", err)
	}
	now = identity.Subject{Type: "staff", ID: "alice", Impersonator: support}
	if err := check(); !errors.Is(err, identity.ErrUnauthenticated) {
		t.Fatalf("same ID in another account system: %v", err)
	}
	now = identity.Subject{Type: "user", ID: "alice"}
	if err := check(); !errors.Is(err, identity.ErrUnauthenticated) {
		t.Fatalf("another impersonator: %v", err)
	}
	now = identity.Subject{}
	if err := check(); !errors.Is(err, identity.ErrUnauthenticated) {
		t.Fatalf("revoked: %v", err)
	}
}
