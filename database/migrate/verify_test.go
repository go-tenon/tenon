package migrate_test

import (
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"

	"github.com/go-tenon/tenon/database/migrate"
)

func TestVerify(t *testing.T) {
	t.Parallel()
	const (
		f1 = "2026_11_10_114451_create_files.sql"
		f2 = "2026_11_11_090000_add_file_meta.sql"
	)
	sql1 := []byte("-- +goose Up\nCREATE TABLE files (id uuid PRIMARY KEY);\n-- +goose Down\nDROP TABLE files;\n")
	sql2 := []byte("-- +goose Up\nALTER TABLE files ADD COLUMN meta jsonb;\n-- +goose Down\nALTER TABLE files DROP COLUMN meta;\n")

	src1 := fstest.MapFS{f1: {Data: sql1}}
	src12 := fstest.MapFS{f1: {Data: sql1}, f2: {Data: sql2}}

	dir := filepath.Join(t.TempDir(), "migrations")
	// 尚未发布任何迁移时报告 missing。
	issues, err := migrate.Verify(dir, src1)
	if err != nil || len(issues) != 1 || issues[0].Kind != "missing" || issues[0].File != f1 {
		t.Fatalf("empty dir: issues=%+v err=%v", issues, err)
	}

	// 发布并加上应用自己的迁移后检查通过。
	if _, err := migrate.Publish(dir, src1); err != nil {
		t.Fatal(err)
	}
	appFile := filepath.Join(dir, "2026_11_12_100000_create_articles.sql")
	if err := os.WriteFile(appFile, []byte("-- +goose Up\nCREATE TABLE articles (id uuid PRIMARY KEY);\n-- +goose Down\nDROP TABLE articles;\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if issues, err := migrate.Verify(dir, src1); err != nil || len(issues) != 0 {
		t.Fatalf("in sync: issues=%+v err=%v", issues, err)
	}

	// 框架新增迁移但应用未运行 migrate publish 时报告 missing。
	issues, err = migrate.Verify(dir, src12)
	if err != nil || len(issues) != 1 || issues[0].Kind != "missing" || issues[0].File != f2 {
		t.Fatalf("missing new migration: issues=%+v err=%v", issues, err)
	}

	// 已发布文件内容被修改时报告 modified。
	if err := os.WriteFile(filepath.Join(dir, f1), append(sql1, []byte("-- changed\n")...), 0o644); err != nil {
		t.Fatal(err)
	}
	issues, err = migrate.Verify(dir, src1)
	if err != nil || len(issues) != 1 || issues[0].Kind != "modified" || issues[0].File != f1 {
		t.Fatalf("modified migration: issues=%+v err=%v", issues, err)
	}
}
