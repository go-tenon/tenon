package migrate

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestCreateGuessesTableFromName(t *testing.T) {
	for _, tc := range []struct{ name, want string }{
		{"create_articles_table", "CREATE TABLE articles (\n"},
		{"create_article_tags", "CREATE TABLE article_tags (\n"},
		{"add_published_at_to_articles_table", "ALTER TABLE articles;\n"},
		{"drop_summary_from_articles", "ALTER TABLE articles;\n"},
		{"backfill_slugs", "-- +goose Up\n\n-- +goose Down\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path, err := Create(t.TempDir(), tc.name)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.HasSuffix(path, "_"+tc.name+".sql") {
				t.Fatalf("path=%s", path)
			}
			data, err := os.ReadFile(path)
			if err != nil || !strings.HasPrefix(string(data), "-- +goose Up\n") || !strings.Contains(string(data), tc.want) {
				t.Fatalf("data=%s err=%v", data, err)
			}
		})
	}
	data, _ := os.ReadFile(must(Create(t.TempDir(), "create_articles_table")))
	for _, want := range []string{
		"DROP TABLE articles;",
		"COMMENT ON TABLE articles IS '';",
		"COMMENT ON COLUMN articles.id IS '主键';",
		"COMMENT ON COLUMN articles.created_at IS '创建时间';",
		"COMMENT ON COLUMN articles.updated_at IS '更新时间';",
	} {
		if !strings.Contains(string(data), want) {
			t.Fatalf("data=%s\nmissing %q", data, want)
		}
	}
}

func TestCreateUsesPostgresTypes(t *testing.T) {
	data, _ := os.ReadFile(must(Create(t.TempDir(), "create_articles_table")))
	if !strings.Contains(string(data), "id         uuid PRIMARY KEY") || strings.Contains(string(data), "DEFAULT") {
		t.Fatalf("data=%s", data)
	}
}

func TestCreateVersionFollowsExistingMigrations(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "migrations") // 目录不存在时自动创建
	first, err := Create(dir, "create_articles_table")
	if err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`^\d{4}_\d{2}_\d{2}_\d{6}_create_articles_table\.sql$`).MatchString(filepath.Base(first)) {
		t.Fatalf("first=%s", first)
	}
	if err := os.WriteFile(filepath.Join(dir, "2999_01_01_000000_future.sql"), []byte(testMigration), 0o600); err != nil {
		t.Fatal(err)
	}
	second, err := Create(dir, "add_status_to_articles_table")
	if err != nil || filepath.Base(second) != "2999_01_01_000001_add_status_to_articles_table.sql" {
		t.Fatalf("first=%s second=%s err=%v", first, second, err)
	}
}

func TestCreateRejectsOtherFileNames(t *testing.T) {
	for _, name := range []string{"20261117123456_goose.sql", "notes.sql", "0000_00_00_000000_zero.sql", "9999_99_99_999999_last.sql"} {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(testMigration), 0o600); err != nil {
			t.Fatal(err)
		}
		if path, err := Create(dir, "create_articles_table"); err == nil {
			t.Errorf("%s: created %s", name, path)
		}
	}
}

func TestCreateRejectsNonSnakeCaseNames(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"", "CreateArticles", "create-articles", "create articles", "_create", "create__articles", "create_articles_", "1_create", "../create"} {
		if _, err := Create(dir, name); err == nil {
			t.Errorf("Create(%q) succeeded", name)
		}
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Fatalf("files written: %v", entries)
	}
}

func must(path string, err error) string {
	if err != nil {
		panic(err)
	}
	return path
}

func TestNextVersion(t *testing.T) {
	for _, tc := range []struct{ existing, want int64 }{
		{29990101000059, 29990101000100}, // 晚于现在：下一秒，按时间进位
		{30000000000000, 30000000000001}, // 不是合法的时间：加一
		{lastVersion - 1, lastVersion},
	} {
		if got, err := nextVersion(tc.existing); err != nil || got != tc.want {
			t.Errorf("nextVersion(%d)=%d, %v, want %d", tc.existing, got, err, tc.want)
		}
	}
	if _, err := nextVersion(lastVersion); err == nil {
		t.Error("nextVersion(lastVersion) succeeded")
	}
}
