package migrate

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
)

const testMigration = "-- +goose Up\nCREATE TABLE example (id integer);\n-- +goose Down\nDROP TABLE example;\n"

func TestPublishUpgradePreservesApplicationSQL(t *testing.T) {
	dir := t.TempDir()
	source := Source{Name: "files", FS: fstest.MapFS{
		"0001_create_files.sql": {Data: []byte(testMigration)},
	}}
	first, err := Publish(dir, source)
	if err != nil || len(first) != 1 {
		t.Fatalf("first=%v err=%v", first, err)
	}
	data, err := os.ReadFile(filepath.Join(dir, first[0]))
	if err != nil || !strings.HasPrefix(string(data), "-- mygoravel: files/0001_create_files\n") {
		t.Fatalf("data=%s err=%v", data, err)
	}
	// 应用可在首次部署前修改 SQL、重命名文件；来源标记使升级仍然只追加。
	custom := "-- mygoravel: files/0001_create_files\n" + testMigration + "-- application column additions\n"
	if err := os.Remove(filepath.Join(dir, first[0])); err != nil {
		t.Fatal(err)
	}
	renamed := filepath.Join(dir, "29990101000000_custom_files.sql")
	if err := os.WriteFile(renamed, []byte(custom), 0o600); err != nil {
		t.Fatal(err)
	}
	source.FS.(fstest.MapFS)["0002_add_field.sql"] = &fstest.MapFile{Data: []byte(testMigration)}
	second, err := Publish(dir, source)
	if err != nil || len(second) != 1 || !strings.HasPrefix(second[0], "29990101000001_files_0002_") {
		t.Fatalf("second=%v err=%v", second, err)
	}
	data, _ = os.ReadFile(renamed)
	if string(data) != custom {
		t.Fatal("application migration was overwritten")
	}
	again, err := Publish(dir, source)
	if err != nil || len(again) != 0 {
		t.Fatalf("again=%v err=%v", again, err)
	}
}

func TestPublishOrdersSourceVersionsNumerically(t *testing.T) {
	files := fstest.MapFS{
		"10_later.sql": {Data: []byte(testMigration)},
		"2_first.sql":  {Data: []byte(testMigration)},
	}
	names, err := Publish(t.TempDir(), Source{Name: "files", FS: files})
	if err != nil || len(names) != 2 || !strings.HasSuffix(names[0], "_2_first.sql") || !strings.HasSuffix(names[1], "_10_later.sql") {
		t.Fatalf("names=%v err=%v", names, err)
	}
}

func TestPublishRejectsSourceHistoryChanges(t *testing.T) {
	for _, tc := range []struct {
		name    string
		markers []string
		source  fstest.MapFS
		want    string
	}{
		{
			name: "renamed source", markers: []string{"files/0001_create_files"},
			source: fstest.MapFS{"0001_renamed.sql": {Data: []byte(testMigration)}}, want: "renames published",
		},
		{
			name: "inserted older version", markers: []string{"files/0002_existing"},
			source: fstest.MapFS{
				"0001_backfill.sql": {Data: []byte(testMigration)},
				"0003_new.sql":      {Data: []byte(testMigration)},
			}, want: "precedes published version",
		},
		{
			name: "duplicate historical version", markers: []string{"files/0001_create_files", "files/1_renamed"},
			source: fstest.MapFS{"0002_new.sql": {Data: []byte(testMigration)}}, want: "duplicate source version",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			for i, marker := range tc.markers {
				name := filepath.Join(dir, fmt.Sprintf("%d_application_name.sql", i+1))
				if err := os.WriteFile(name, []byte(sourceMarker+marker+"\n"+testMigration), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			written, err := Publish(dir, Source{Name: "files", FS: tc.source})
			if err == nil || !strings.Contains(err.Error(), tc.want) || len(written) != 0 {
				t.Fatalf("written=%v err=%v want=%q", written, err, tc.want)
			}
			names, err := filepath.Glob(filepath.Join(dir, "*.sql"))
			if err != nil || len(names) != len(tc.markers) {
				t.Fatalf("history check changed directory: %v err=%v", names, err)
			}
		})
	}
}

func TestPublishTimestampAndNumericVersions(t *testing.T) {
	for _, tc := range []struct {
		name     string
		existing int64
		want     int64
	}{
		{"minute", 29990101000059, 29990101000100},
		{"day", 29990101235959, 29990102000000},
		{"year", 29991231235959, 30000101000000},
		{"numeric", 30000000000000, 30000000000001},
		{"four digit year exhausted", 99991231235959, 99991231235960},
		{"largest version", math.MaxInt64 - 1, math.MaxInt64},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("%d_existing.sql", tc.existing)), []byte(testMigration), 0o600); err != nil {
				t.Fatal(err)
			}
			names, err := Publish(dir, Source{Name: "files", FS: fstest.MapFS{"1_new.sql": {Data: []byte(testMigration)}}})
			if err != nil || len(names) != 1 || names[0] != fmt.Sprintf("%d_files_1_new.sql", tc.want) {
				t.Fatalf("names=%v err=%v want=%d", names, err, tc.want)
			}
		})
	}
}

func TestPublishPlansAllVersionsBeforeWriting(t *testing.T) {
	dir := t.TempDir()
	name := filepath.Join(dir, fmt.Sprintf("%d_existing.sql", int64(math.MaxInt64-1)))
	if err := os.WriteFile(name, []byte(testMigration), 0o600); err != nil {
		t.Fatal(err)
	}
	written, err := Publish(dir, Source{Name: "files", FS: fstest.MapFS{
		"1_first.sql": {Data: []byte(testMigration)},
		"2_next.sql":  {Data: []byte(testMigration)},
	}})
	if err == nil || !strings.Contains(err.Error(), "version overflow") || len(written) != 0 {
		t.Fatalf("written=%v err=%v", written, err)
	}
	names, _ := filepath.Glob(filepath.Join(dir, "*.sql"))
	if len(names) != 1 {
		t.Fatalf("overflow published a partial batch: %v", names)
	}
}

func TestPublishPrevalidatesAllSources(t *testing.T) {
	valid := Source{Name: "files", FS: fstest.MapFS{"1_valid.sql": {Data: []byte(testMigration)}}}
	for _, bad := range []Source{
		{Name: "../escape", FS: valid.FS},
		{Name: "nil"},
		{Name: "files", FS: valid.FS},
		{Name: "duplicate", FS: fstest.MapFS{
			"01_first.sql": {Data: []byte(testMigration)},
			"1_second.sql": {Data: []byte(testMigration)},
		}},
		{Name: "invalid", FS: fstest.MapFS{"wrong.sql": {Data: []byte(testMigration)}}},
		{Name: "negative", FS: fstest.MapFS{"-1_wrong.sql": {Data: []byte(testMigration)}}},
		{Name: "zero", FS: fstest.MapFS{"0_wrong.sql": {Data: []byte(testMigration)}}},
		{Name: "missing_up", FS: fstest.MapFS{"1_wrong.sql": {Data: []byte("select 1;")}}},
	} {
		t.Run(bad.Name, func(t *testing.T) {
			dir := t.TempDir()
			if written, err := Publish(dir, valid, bad); err == nil || len(written) != 0 {
				t.Fatalf("written=%v err=%v", written, err)
			}
			entries, _ := os.ReadDir(dir)
			if len(entries) != 0 {
				t.Fatalf("prevalidation wrote %v", entries)
			}
		})
	}
}

func TestPublishRejectsAmbiguousApplicationHistory(t *testing.T) {
	for _, tc := range []struct {
		name  string
		files map[string]string
	}{
		{"version", map[string]string{"1_a.sql": testMigration, "01_b.sql": testMigration}},
		{"marker", map[string]string{
			"1_a.sql": "-- mygoravel: files/1_original\n" + testMigration,
			"2_b.sql": "-- mygoravel: files/1_original\n" + testMigration,
		}},
		{"overflow", map[string]string{"9223372036854775807_last.sql": testMigration}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			for name, data := range tc.files {
				if err := os.WriteFile(filepath.Join(dir, name), []byte(data), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			written, err := Publish(dir, Source{Name: "new", FS: fstest.MapFS{"1_new.sql": {Data: []byte(testMigration)}}})
			if err == nil || len(written) != 0 {
				t.Fatalf("written=%v err=%v", written, err)
			}
		})
	}
}

func TestPublishLockContentionAndRelease(t *testing.T) {
	dir := t.TempDir()
	lock, err := lockPublish(filepath.Join(dir, ".mygoravel-publish.lock"))
	if err != nil {
		t.Fatal(err)
	}
	source := Source{Name: "files", FS: fstest.MapFS{"1_create.sql": {Data: []byte(testMigration)}}}
	if written, err := Publish(dir, source); err == nil || len(written) != 0 {
		t.Fatalf("concurrent publication written=%v err=%v", written, err)
	}
	if err := lock.Close(); err != nil {
		t.Fatal(err)
	}
	if written, err := Publish(dir, source); err != nil || len(written) != 1 {
		t.Fatalf("released lock written=%v err=%v", written, err)
	}
}

func TestPublishFileNeverOverwrites(t *testing.T) {
	dir := t.TempDir()
	if err := publishFile(dir, "1_file.sql", []byte("original")); err != nil {
		t.Fatal(err)
	}
	if err := publishFile(dir, "1_file.sql", []byte("replacement")); err == nil {
		t.Fatal("expected exclusive creation failure")
	}
	data, _ := os.ReadFile(filepath.Join(dir, "1_file.sql"))
	if string(data) != "original" {
		t.Fatalf("data=%s", data)
	}
}
