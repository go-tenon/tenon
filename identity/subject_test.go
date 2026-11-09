package identity_test

import (
	"context"
	"testing"

	"github.com/ryan053857/mygoravel/identity"
)

func TestSubjectContextAndScope(t *testing.T) {
	sub := identity.Subject{ID: "u1", Type: identity.SubjectUser, Scopes: []string{"orders"}}
	ctx := identity.WithSubject(context.Background(), sub)
	got, ok := identity.FromContext(ctx)
	if !ok || got.ID != "u1" || !got.HasScope("orders") || got.HasScope("admin") {
		t.Fatalf("%+v ok=%v", got, ok)
	}
}
