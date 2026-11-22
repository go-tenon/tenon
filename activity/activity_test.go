package activity_test

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/activity"
	"github.com/go-tenon/tenon/database/dbtest"
	"github.com/go-tenon/tenon/identity"
	"github.com/go-tenon/tenon/logging"
	"github.com/go-tenon/tenon/page"
)

var alice = identity.Subject{ID: "u1", Type: "user", Tenant: "acme", Permissions: []string{"*"}}

func record(t *testing.T, db bun.IDB, actor identity.Subject, a activity.Activity) {
	t.Helper()
	if err := activity.Record(context.Background(), db, actor, a); err != nil {
		t.Fatal(err)
	}
}

func events(list []activity.Activity) string {
	var out []string
	for _, a := range list {
		out = append(out, a.Event)
	}
	return strings.Join(out, ",")
}

func TestRecordFillsActorTenantAndRequestID(t *testing.T) {
	t.Parallel()
	db := dbtest.Open(t, activity.Migrations())
	ctx := logging.WithAttrs(context.Background(), slog.String(logging.AttrRequestID, "req-1"))
	before := time.Now().UTC().Add(-time.Second)
	err := activity.Record(ctx, db, alice, activity.Activity{
		Event: "article.published", TargetType: "article", TargetID: "a1",
		Properties: map[string]any{"title": "Hello", "tags": []any{"go"}},
		// 由 Record 填写的字段会被覆盖。
		Tenant: "other", ActorID: "forged", ImpersonatorID: "forged", RequestID: "forged",
	})
	if err != nil {
		t.Fatal(err)
	}
	res, err := activity.List(context.Background(), db, activity.Query{Tenant: "acme"})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Items) != 1 || res.Total != 1 || res.Page != 1 || res.PerPage != activity.DefaultPerPage {
		t.Fatalf("res=%+v", res)
	}
	a := res.Items[0]
	if a.ID.Version() != 7 || a.Tenant != "acme" || a.ActorType != "user" || a.ActorID != "u1" || a.ImpersonatorID != "" ||
		a.Event != "article.published" || a.TargetType != "article" || a.TargetID != "a1" || a.RequestID != "req-1" {
		t.Fatalf("activity=%+v", a)
	}
	if a.Properties["title"] != "Hello" || len(a.Properties["tags"].([]any)) != 1 {
		t.Fatalf("properties=%#v", a.Properties)
	}
	if a.CreatedAt.Before(before) || a.CreatedAt.After(time.Now().Add(time.Second)) {
		t.Fatalf("created_at=%v", a.CreatedAt)
	}
}

// 代为登录时，操作人是被代为登录的用户，Impersonator 记下实际操作的人。
func TestRecordImpersonation(t *testing.T) {
	t.Parallel()
	db := dbtest.Open(t, activity.Migrations())
	asAlice := alice
	staff := identity.Ref{Type: "staff", ID: "s1"}
	asAlice.Impersonator = staff
	record(t, db, asAlice, activity.Activity{Event: "article.published"})
	res, err := activity.List(context.Background(), db, activity.Query{Tenant: "acme"})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Items) != 1 || res.Items[0].Actor() != alice.Ref() || res.Items[0].Impersonator() != staff {
		t.Fatalf("res=%+v", res)
	}
}

func TestRecordWithoutActorAndProperties(t *testing.T) {
	t.Parallel()
	db := dbtest.Open(t, activity.Migrations())
	record(t, db, identity.Subject{Tenant: "acme"}, activity.Activity{Event: "articles.scheduled_published"})
	res, err := activity.List(context.Background(), db, activity.Query{Tenant: "acme"})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Items) != 1 || res.Items[0].ActorID != "" || res.Items[0].Properties == nil || len(res.Items[0].Properties) != 0 {
		t.Fatalf("res=%+v", res)
	}
}

func TestRecordRollsBackWithTransaction(t *testing.T) {
	t.Parallel()
	db := dbtest.Open(t, activity.Migrations())
	ctx := context.Background()
	err := db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if err := activity.Record(ctx, tx, alice, activity.Activity{Event: "article.deleted"}); err != nil {
			return err
		}
		return context.Canceled
	})
	if err != context.Canceled {
		t.Fatal(err)
	}
	res, err := activity.List(ctx, db, activity.Query{Tenant: "acme"})
	if err != nil || len(res.Items) != 0 || res.Total != 0 {
		t.Fatalf("res=%+v err=%v", res, err)
	}
}

func TestRecordRejectsInvalid(t *testing.T) {
	t.Parallel()
	db := dbtest.Open(t, activity.Migrations())
	for name, c := range map[string]struct {
		actor identity.Subject
		a     activity.Activity
	}{
		"empty event":        {alice, activity.Activity{}},
		"id without type":    {alice, activity.Activity{Event: "e", TargetID: "1"}},
		"unencodable values": {alice, activity.Activity{Event: "e", Properties: map[string]any{"f": func() {}}}},
	} {
		if err := activity.Record(context.Background(), db, c.actor, c.a); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
}

func TestListFiltersAndPages(t *testing.T) {
	t.Parallel()
	db := dbtest.Open(t, activity.Migrations())
	bob := identity.Subject{ID: "u2", Type: "user", Tenant: "acme"}
	record(t, db, alice, activity.Activity{Event: "article.created", TargetType: "article", TargetID: "a1"})
	record(t, db, bob, activity.Activity{Event: "article.updated", TargetType: "article", TargetID: "a1"})
	record(t, db, alice, activity.Activity{Event: "article.created", TargetType: "article", TargetID: "a2"})
	record(t, db, alice, activity.Activity{Event: "member.invited", TargetType: "user", TargetID: "a1"})
	record(t, db, identity.Subject{ID: "u1", Tenant: "globex"}, activity.Activity{Event: "article.created", TargetType: "article", TargetID: "a1"})
	record(t, db, identity.Subject{ID: "ops"}, activity.Activity{Event: "tenant.suspended", TargetType: "tenant", TargetID: "acme"})

	ctx := context.Background()
	for name, c := range map[string]struct {
		q     activity.Query
		want  string
		total int64
	}{
		"tenant":   {activity.Query{Tenant: "acme"}, "member.invited,article.created,article.updated,article.created", 4},
		"platform": {activity.Query{}, "tenant.suspended", 1},
		"target":   {activity.Query{Tenant: "acme", TargetType: "article", TargetID: "a1"}, "article.updated,article.created", 2},
		"type":     {activity.Query{Tenant: "acme", TargetType: "article"}, "article.created,article.updated,article.created", 3},
		"actor":    {activity.Query{Tenant: "acme", Actor: alice.Ref()}, "member.invited,article.created,article.created", 3},
		"event":    {activity.Query{Tenant: "acme", Event: "article.created"}, "article.created,article.created", 2},
		"per_page": {activity.Query{Tenant: "acme", Page: page.OffsetParams{Page: 1, PerPage: 2}}, "member.invited,article.created", 4},
	} {
		res, err := activity.List(ctx, db, c.q)
		if err != nil {
			t.Fatal(err)
		}
		if got := events(res.Items); got != c.want || res.Total != c.total {
			t.Errorf("%s: got %s (total=%d) want %s (total=%d)", name, got, res.Total, c.want, c.total)
		}
	}

	var pages []string
	for p := 1; p <= 3; p++ {
		res, err := activity.List(ctx, db, activity.Query{Tenant: "acme", Page: page.OffsetParams{Page: p, PerPage: 3}})
		if err != nil {
			t.Fatal(err)
		}
		if res.Total != 4 || res.Page != p || res.PerPage != 3 {
			t.Fatalf("page %d: res=%+v", p, res)
		}
		if len(res.Items) > 0 {
			pages = append(pages, events(res.Items))
		}
	}
	if got := strings.Join(pages, "|"); got != "member.invited,article.created,article.updated|article.created" {
		t.Fatalf("pages=%s", got)
	}

	if _, err := activity.List(ctx, db, activity.Query{TargetID: "a1"}); err == nil {
		t.Fatal("expected error for target id without type")
	}

	// 操作人按类型和 ID 一起区分：另一套账号体系中的 u1 是另一个人。
	record(t, db, identity.Subject{ID: "u1", Type: "user", Tenant: "initech"}, activity.Activity{Event: "by.user"})
	record(t, db, identity.Subject{ID: "u1", Type: "staff", Tenant: "initech"}, activity.Activity{Event: "by.staff"})
	res, err := activity.List(ctx, db, activity.Query{Tenant: "initech", Actor: identity.Ref{Type: "staff", ID: "u1"}})
	if err != nil || events(res.Items) != "by.staff" || res.Total != 1 {
		t.Fatalf("staff actor: %s total=%d err=%v", events(res.Items), res.Total, err)
	}
}

func TestCleanupDeletesOldRecords(t *testing.T) {
	t.Parallel()
	db := dbtest.Open(t, activity.Migrations())
	ctx := context.Background()
	for range 3 {
		record(t, db, alice, activity.Activity{Event: "old"})
	}
	record(t, db, alice, activity.Activity{Event: "new"})
	_, err := db.NewUpdate().Model((*activity.Activity)(nil)).
		Set("created_at = ?", time.Now().Add(-48*time.Hour).UTC()).Where("event = ?", "old").Exec(ctx)
	if err != nil {
		t.Fatal(err)
	}
	cleanup := activity.Cleanup(db, 24*time.Hour)
	for _, want := range []int{2, 1, 0} {
		n, err := cleanup(ctx, 2)
		if err != nil || n != want {
			t.Fatalf("n=%d want=%d err=%v", n, want, err)
		}
	}
	res, err := activity.List(ctx, db, activity.Query{Tenant: "acme"})
	if err != nil || events(res.Items) != "new" || res.Total != 1 {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if _, err := cleanup(ctx, 0); err == nil {
		t.Fatal("expected error for non-positive limit")
	}
}

// exported 把 Export 的结果编码为 JSON。
func exported(t *testing.T) func(v any, err error) string {
	return func(v any, err error) string {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		data, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}
}

func contains(t *testing.T, got string, want ...string) {
	t.Helper()
	for _, w := range want {
		if !strings.Contains(got, w) {
			t.Errorf("%s does not contain %s", got, w)
		}
	}
}

func excludes(t *testing.T, got string, unwanted ...string) {
	t.Helper()
	for _, w := range unwanted {
		if strings.Contains(got, w) {
			t.Errorf("%s contains %s", got, w)
		}
	}
}

func TestExport(t *testing.T) {
	t.Parallel()
	db := dbtest.Open(t, activity.Migrations())
	ctx := context.Background()
	record(t, db, alice, activity.Activity{Event: "article.published", TargetType: "article", TargetID: "a1", Properties: map[string]any{"title": "Hello"}})
	asAlice := alice
	asAlice.Tenant, asAlice.Impersonator = "globex", identity.Ref{Type: "staff", ID: "s1"}
	record(t, db, asAlice, activity.Activity{Event: "article.deleted"})
	record(t, db, identity.Subject{ID: "u2", Type: "user", Tenant: "acme"}, activity.Activity{Event: "bob.event"})
	got := exported(t)(activity.Export(ctx, db, alice.Ref()))
	contains(t, got,
		`{"tenant":"acme","event":"article.published","target_type":"article","target_id":"a1","properties":{"title":"Hello"},"created_at":`,
		`{"tenant":"globex","event":"article.deleted","impersonated":true,"created_at":`)
	excludes(t, got, "bob.event", "s1")
	if _, err := activity.Export(ctx, db, identity.Ref{}); err == nil {
		t.Fatal("expected error for empty subject")
	}
}
