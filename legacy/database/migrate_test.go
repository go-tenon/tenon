package database_test

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/ryan053857/go-kit/database"
)

func TestPublishMigrations(t *testing.T) {
	dir := t.TempDir()
	// 产品已有一个版本号很大的迁移，发布的迁移必须排在它之后。
	if err := os.WriteFile(filepath.Join(dir, "29990101000000_create_orders.sql"), nil, 0o644); err != nil {
		t.Fatal(err)
	}

	account := database.MigrationSource{Name: "account", FS: fstest.MapFS{
		"0002_add_avatar.sql":   {Data: []byte("-- 2")},
		"0001_create_users.sql": {Data: []byte("-- 1")},
		"README.md":             {Data: []byte("ignored")},
	}}
	file := database.MigrationSource{Name: "file", FS: fstest.MapFS{
		"0001_create_files.sql": {Data: []byte("-- f")},
	}}

	written, err := database.PublishMigrations(dir, account, file)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"29990101000001_account_0001_create_users.sql",
		"29990101000002_account_0002_add_avatar.sql",
		"29990101000003_file_0001_create_files.sql",
	}
	if got := baseNames(written); !slices.Equal(got, want) {
		t.Fatalf("written = %v, want %v", got, want)
	}
	if data, _ := os.ReadFile(written[0]); string(data) != "-- 1" {
		t.Errorf("content = %q", data)
	}

	// 再次发布：没有新文件。产品修改过已发布的文件也不影响判断。
	_ = os.WriteFile(written[0], []byte("-- edited"), 0o644)
	again, err := database.PublishMigrations(dir, account, file)
	if err != nil || len(again) != 0 {
		t.Fatalf("second publish = %v, %v", again, err)
	}

	// 模块新增迁移后只发布新的那个。
	account.FS.(fstest.MapFS)["0003_add_locale.sql"] = &fstest.MapFile{Data: []byte("-- 3")}
	again, err = database.PublishMigrations(dir, account)
	if err != nil {
		t.Fatal(err)
	}
	if got := baseNames(again); !slices.Equal(got, []string{"29990101000004_account_0003_add_locale.sql"}) {
		t.Errorf("third publish = %v", got)
	}

	if _, err := database.PublishMigrations(dir, database.MigrationSource{Name: "Bad-Name", FS: fstest.MapFS{}}); err == nil {
		t.Error("invalid source name accepted")
	}
}

func TestCreateMigration(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "migrations") // 不存在时自动创建
	p1, err := database.CreateMigration(dir, "create_orders")
	if err != nil {
		t.Fatal(err)
	}
	p2, err := database.CreateMigration(dir, "add_order_note")
	if err != nil {
		t.Fatal(err)
	}
	b1, b2 := filepath.Base(p1), filepath.Base(p2)
	if !strings.HasSuffix(b1, "_create_orders.sql") || len(b1) != len("20261109120000_create_orders.sql") {
		t.Errorf("p1 = %s", b1)
	}
	if b2 <= b1 {
		t.Errorf("versions not increasing: %s, %s", b1, b2)
	}
	data, _ := os.ReadFile(p1)
	if !strings.Contains(string(data), "-- +goose Up") {
		t.Errorf("template = %q", data)
	}
	if _, err := database.CreateMigration(dir, "Bad Name"); err == nil {
		t.Error("invalid name accepted")
	}
}

func baseNames(paths []string) []string {
	var out []string
	for _, p := range paths {
		out = append(out, filepath.Base(p))
	}
	return out
}
