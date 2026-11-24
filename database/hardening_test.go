package database_test

import (
	"context"
	"testing"

	"github.com/go-tenon/tenon/database"
	"github.com/go-tenon/tenon/database/dbtest"
)

func TestEscapeLike(t *testing.T) {
	t.Parallel()
	if got, want := database.EscapeLike(`100%_a\b`), `100\%\_a\\b`; got != want {
		t.Fatalf("EscapeLike = %q, want %q", got, want)
	}
}

func TestTrackPrimary(t *testing.T) {
	t.Parallel()
	base := dbtest.Open(t)
	ctx := t.Context()
	if _, err := base.ExecContext(ctx, "CREATE TABLE notes (body text)"); err != nil {
		t.Fatal(err)
	}
	dsn := dbtest.DSN(t, base)
	db, err := database.Open(ctx, database.Config{
		DSN:      withParam(t, dsn, "application_name", "primary"),
		Replicas: []string{withParam(t, dsn, "application_name", "replica")},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	server := func(ctx context.Context) string {
		t.Helper()
		var name string
		if err := db.NewSelect().ColumnExpr("current_setting('application_name')").Scan(ctx, &name); err != nil {
			t.Fatal(err)
		}
		return name
	}

	tracked := database.TrackPrimary(ctx)
	if got := server(tracked); got != "replica" {
		t.Fatalf("before write: got %q, want replica", got)
	}

	// RunInTx 提交后，同一 tracked ctx 的后续只读查询自动路由到主库。
	if err := database.RunInTx(tracked, db, nil, func(ctx context.Context, tx database.Tx) error {
		_, err := tx.NewInsert().Model(&note{Body: "hello"}).Exec(ctx)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if got := server(tracked); got != "primary" {
		t.Fatalf("after tx commit: got %q, want primary", got)
	}
}
