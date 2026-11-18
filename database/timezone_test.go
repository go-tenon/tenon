package database_test

import (
	"context"
	"testing"
	"time"

	"github.com/go-tenon/tenon/database/dbtest"
)

// 读出的 timestamptz 在 UTC 中，与进程的本地时区无关。修改了 time.Local，串行运行。
func TestScansTimesInUTC(t *testing.T) {
	local := time.Local
	time.Local = time.FixedZone("UTC+8", 8*3600)
	t.Cleanup(func() { time.Local = local })

	db := dbtest.Open(t)
	ctx := context.Background()
	at := time.Date(2026, 1, 2, 3, 4, 5, 6000, time.UTC)
	if _, err := db.ExecContext(ctx, "CREATE TABLE events (at timestamptz NOT NULL)"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.NewRaw("INSERT INTO events VALUES (?)", at.In(time.Local)).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	var raw time.Time
	if err := db.NewRaw("SELECT at FROM events").Scan(ctx, &raw); err != nil {
		t.Fatal(err)
	}
	var row struct {
		At time.Time `bun:"at"`
	}
	if err := db.NewSelect().Table("events").Scan(ctx, &row); err != nil {
		t.Fatal(err)
	}
	for name, v := range map[string]time.Time{"raw": raw, "model": row.At} {
		if v.Location() != time.UTC || !v.Equal(at) {
			t.Errorf("%s = %v, want %v in UTC", name, v, at)
		}
	}
}
