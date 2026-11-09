package database_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ryan053857/mygoravel/database"
	"github.com/ryan053857/mygoravel/database/migrate"
)

func TestConfigValidate(t *testing.T) {
	cfg := database.DefaultConfig()
	if err := cfg.Validate(); err == nil {
		t.Fatal("empty dsn should fail")
	}
}

func TestCheckPublishedDetectsModification(t *testing.T) {
	dir := t.TempDir()
	base := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "00001_a.sql"), []byte("v1"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(base, "00001_a.sql"), []byte("v1"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := migrate.CheckPublished(dir, base); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "00001_a.sql"), []byte("changed"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := migrate.CheckPublished(dir, base)
	if err == nil || !strings.Contains(err.Error(), "modified") {
		t.Fatalf("got %v", err)
	}
}
