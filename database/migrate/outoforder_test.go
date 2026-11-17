package migrate_test

import (
	"context"
	"testing"
	"testing/fstest"

	"github.com/go-tenon/tenon/database/dbtest"
	"github.com/go-tenon/tenon/database/migrate"
)

// 与 Laravel 相同，后加入的更早的迁移（例如升级框架后发布的迁移）也会执行。
func TestUpRunsEarlierMigrationsAddedLater(t *testing.T) {
	ctx := context.Background()
	files := fstest.MapFS{"2026_01_01_000002_create_b.sql": {Data: []byte("-- +goose Up\nCREATE TABLE b (id int);\n-- +goose Down\nDROP TABLE b;\n")}}
	db := dbtest.Open(t, files)
	files["2026_01_01_000001_create_a.sql"] = &fstest.MapFile{Data: []byte("-- +goose Up\nCREATE TABLE a (id int);\n-- +goose Down\nDROP TABLE a;\n")}
	p, err := migrate.NewProvider(db, files)
	if err != nil {
		t.Fatal(err)
	}
	if err := migrate.Up(ctx, p); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, "SELECT FROM a"); err != nil {
		t.Fatal(err)
	}
	// down 回滚最后执行的迁移，也就是 a。
	if _, err := p.Down(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, "SELECT FROM a"); err == nil {
		t.Fatal("a was not rolled back")
	}
}
