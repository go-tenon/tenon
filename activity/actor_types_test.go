package activity_test

import (
	"testing"

	"github.com/go-tenon/tenon/activity"
	"github.com/go-tenon/tenon/database/dbtest"
	"github.com/go-tenon/tenon/identity"
)

func TestListActorTypes(t *testing.T) {
	t.Parallel()
	db := dbtest.Open(t, activity.Migrations())
	ctx := t.Context()

	admin := identity.Subject{Type: "platform_admin", ID: "a1"}
	member := identity.Subject{Type: "member", ID: "m1"}
	sys := identity.Subject{Type: identity.System, ID: "system"}

	for _, tc := range []struct {
		actor identity.Subject
		event string
	}{
		{admin, "tenant.created"},
		{member, "member.profile_updated"},
		{sys, "system.migrated"},
	} {
		if err := activity.Record(ctx, db, tc.actor, activity.Activity{Event: tc.event}); err != nil {
			t.Fatal(err)
		}
	}

	got, err := activity.List(ctx, db, activity.Query{
		Tenant:     "",
		ActorTypes: []identity.SubjectType{"platform_admin", identity.System},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Items) != 2 {
		t.Fatalf("got %d items, want 2: %+v", len(got.Items), got.Items)
	}
	for _, item := range got.Items {
		if item.ActorType == "member" {
			t.Fatalf("unexpected member activity in platform query: %+v", item)
		}
	}
}
