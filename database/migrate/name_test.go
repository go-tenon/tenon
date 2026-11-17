package migrate

import (
	"io/fs"
	"slices"
	"testing"
	"testing/fstest"
)

func TestGooseFSShowsVersionsToGoose(t *testing.T) {
	g := gooseFS{fstest.MapFS{
		"2026_11_17_123456_create_articles_table.sql": {Data: []byte("articles")},
		"2026_11_10_114451_create_files.sql":          {Data: []byte("files")},
		"embed.go":                                    {Data: []byte("package migrations")},
	}}
	names, err := fs.Glob(g, "*.sql")
	if err != nil || !slices.Equal(names, []string{"20261110114451_create_files.sql", "20261117123456_create_articles_table.sql"}) {
		t.Fatalf("names=%v err=%v", names, err)
	}
	for name, want := range map[string]string{"20261117123456_create_articles_table.sql": "articles", "20261110114451_create_files.sql": "files"} {
		data, err := fs.ReadFile(g, name)
		if err != nil || string(data) != want {
			t.Fatalf("%s: data=%q err=%v", name, data, err)
		}
	}
	entries, _ := fs.ReadDir(g, ".")
	for _, e := range entries {
		if info, err := e.Info(); err != nil || info.Name() != e.Name() {
			t.Fatalf("entry %s info=%v err=%v", e.Name(), info, err)
		}
	}
}

func TestFileName(t *testing.T) {
	for path, want := range map[string]string{
		"20261117123456_create_articles_table.sql":    "2026_11_17_123456_create_articles_table.sql",
		"dir/00010101000000_create_users_table.sql":   "0001_01_01_000000_create_users_table.sql",
		"0001_create_files.sql":                       "0001_create_files.sql",
		"2026_11_17_123456_create_articles_table.sql": "2026_11_17_123456_create_articles_table.sql",
	} {
		if got := FileName(path); got != want {
			t.Errorf("FileName(%q)=%q, want %q", path, got, want)
		}
	}
}

func TestCheckNames(t *testing.T) {
	ok := fstest.MapFS{"2026_11_17_123456_a.sql": {}, "embed.go": {}, "notes/readme.sql": {}}
	if err := checkNames(ok); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"0001_create_files.sql", "20261117123456_a.sql", "a.sql"} {
		if err := checkNames(fstest.MapFS{name: {}}); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
}
