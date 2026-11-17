package migrate_test

import (
	"context"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/go-tenon/tenon/database/dbtest"
	"github.com/go-tenon/tenon/database/migrate"
	"github.com/go-tenon/tenon/doctor"
)

func migration(table string) *fstest.MapFile {
	return &fstest.MapFile{Data: []byte("-- +goose Up\nCREATE TABLE " + table + " (id int);\n-- +goose Down\nDROP TABLE " + table + ";\n")}
}

func TestDoctor(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	older := fstest.MapFS{"2026_01_01_000001_a.sql": migration("a")}
	newer := fstest.MapFS{"2026_01_01_000001_a.sql": migration("a"), "2026_01_01_000002_b.sql": migration("b")}
	db := dbtest.Open(t, nil)
	check := func(fsys fstest.MapFS) doctor.DoctorResult {
		t.Helper()
		return doctor.Run(ctx, migrate.Doctor(db, fsys)).Results[0]
	}

	if res := check(newer); res.Status != doctor.Failed || !strings.Contains(res.Summary, "version 0, this program expects 2") ||
		!strings.Contains(res.Fix, "migrate up") {
		t.Errorf("before migrating: %+v", res)
	}
	p, err := migrate.NewProvider(db, newer)
	if err != nil {
		t.Fatal(err)
	}
	if err := migrate.Up(ctx, p); err != nil {
		t.Fatal(err)
	}
	if res := check(newer); res.Status != doctor.Passed {
		t.Errorf("after migrating: %+v", res)
	}
	// 回滚到旧版本的程序：数据库比程序新。
	if res := check(older); res.Status != doctor.Warning || !strings.Contains(res.Summary, "newer than this program") {
		t.Errorf("older program: %+v", res)
	}
}
