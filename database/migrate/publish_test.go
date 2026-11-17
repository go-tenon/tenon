package migrate

import (
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"testing/fstest"
)

const testMigration = "-- +goose Up\nCREATE TABLE example (id integer);\n-- +goose Down\nDROP TABLE example;\n"

func TestPublishCopiesNewMigrations(t *testing.T) {
	dir := t.TempDir()
	files := fstest.MapFS{
		"2026_11_10_000001_create_files.sql": {Data: []byte(testMigration)},
		"2026_11_10_000000_create_disks.sql": {Data: []byte(testMigration)},
	}
	first, err := Publish(dir, files)
	if err != nil || !slices.Equal(first, []string{"2026_11_10_000000_create_disks.sql", "2026_11_10_000001_create_files.sql"}) {
		t.Fatalf("first=%v err=%v", first, err)
	}
	data, err := os.ReadFile(filepath.Join(dir, first[1]))
	if err != nil || string(data) != testMigration {
		t.Fatalf("data=%s err=%v", data, err)
	}
	// 应用可以修改已发布的文件；升级后再次发布只复制新增的迁移，即使它比应用自己的迁移更早。
	custom := testMigration + "-- application column additions\n"
	if err := os.WriteFile(filepath.Join(dir, first[1]), []byte(custom), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "2026_12_01_000000_create_articles_table.sql"), []byte(testMigration), 0o600); err != nil {
		t.Fatal(err)
	}
	files["2026_11_20_000000_add_field.sql"] = &fstest.MapFile{Data: []byte(testMigration)}
	second, err := Publish(dir, files)
	if err != nil || !slices.Equal(second, []string{"2026_11_20_000000_add_field.sql"}) {
		t.Fatalf("second=%v err=%v", second, err)
	}
	if data, _ := os.ReadFile(filepath.Join(dir, first[1])); string(data) != custom {
		t.Fatal("application migration was overwritten")
	}
	if again, err := Publish(dir, files); err != nil || len(again) != 0 {
		t.Fatalf("again=%v err=%v", again, err)
	}
}

func TestPublishPrevalidatesAllSources(t *testing.T) {
	valid := fstest.MapFS{"2026_11_10_000000_valid.sql": {Data: []byte(testMigration)}}
	for name, bad := range map[string]fs.FS{
		"nil":            nil,
		"empty":          fstest.MapFS{},
		"numbered name":  fstest.MapFS{"0001_wrong.sql": {Data: []byte(testMigration)}},
		"goose name":     fstest.MapFS{"20261110000001_wrong.sql": {Data: []byte(testMigration)}},
		"missing up":     fstest.MapFS{"2026_11_10_000001_wrong.sql": {Data: []byte("select 1;")}},
		"same version":   fstest.MapFS{"2026_11_10_000000_other.sql": {Data: []byte(testMigration)}},
		"existing clash": fstest.MapFS{"2026_11_01_000000_other.sql": {Data: []byte(testMigration)}},
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "2026_11_01_000000_app.sql"), []byte(testMigration), 0o600); err != nil {
				t.Fatal(err)
			}
			if written, err := Publish(dir, valid, bad); err == nil || len(written) != 0 {
				t.Fatalf("written=%v err=%v", written, err)
			}
			if entries, _ := os.ReadDir(dir); len(entries) != 1 {
				t.Fatalf("prevalidation wrote %v", entries)
			}
		})
	}
}

func TestPublishRejectsInvalidApplicationDirectory(t *testing.T) {
	for _, name := range []string{"notes.sql", "20261117123456_old.sql"} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, name), []byte(testMigration), 0o600); err != nil {
				t.Fatal(err)
			}
			written, err := Publish(dir, fstest.MapFS{"2026_11_10_000000_new.sql": {Data: []byte(testMigration)}})
			if err == nil || len(written) != 0 {
				t.Fatalf("written=%v err=%v", written, err)
			}
		})
	}
}

func TestPublishFileNeverOverwrites(t *testing.T) {
	dir := t.TempDir()
	if err := publishFile(filepath.Join(dir, "1_file.sql"), []byte("original")); err != nil {
		t.Fatal(err)
	}
	if err := publishFile(filepath.Join(dir, "1_file.sql"), []byte("replacement")); err == nil {
		t.Fatal("expected exclusive creation failure")
	}
	data, _ := os.ReadFile(filepath.Join(dir, "1_file.sql"))
	if string(data) != "original" {
		t.Fatalf("data=%s", data)
	}
}
