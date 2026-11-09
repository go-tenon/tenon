package storage_test

import (
	"bytes"
	"errors"
	"io"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/johannesboyne/gofakes3"
	"github.com/johannesboyne/gofakes3/backend/s3mem"

	"github.com/ryan053857/go-kit/storage"
)

func TestValidKey(t *testing.T) {
	for key, want := range map[string]bool{
		"a.txt": true, "avatars/1.jpg": true, "a/b/c": true,
		"": false, "/abs": false, "a/../b": false, "../x": false, "a//b": false, `a\b`: false, "a/": false, "./a": false,
	} {
		if got := storage.ValidKey(key); got != want {
			t.Errorf("ValidKey(%q) = %v", key, got)
		}
	}
}

// testStorage 对任一实现执行相同的用例。
func testStorage(t *testing.T, s storage.Storage) {
	ctx := t.Context()
	if err := s.Put(ctx, "docs/a.txt", strings.NewReader("hello"), "text/plain"); err != nil {
		t.Fatal(err)
	}
	if err := s.Put(ctx, "docs/a.txt", bytes.NewReader([]byte("hello again")), "text/plain"); err != nil {
		t.Fatal(err) // 覆盖
	}
	rc, err := s.Get(ctx, "docs/a.txt")
	if err != nil {
		t.Fatal(err)
	}
	data, _ := io.ReadAll(rc)
	_ = rc.Close()
	if string(data) != "hello again" {
		t.Errorf("content = %q", data)
	}

	if _, err := s.Get(ctx, "docs/missing.txt"); !errors.Is(err, storage.ErrNotFound) {
		t.Errorf("missing: %v", err)
	}
	if err := s.Delete(ctx, "docs/a.txt"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get(ctx, "docs/a.txt"); !errors.Is(err, storage.ErrNotFound) {
		t.Errorf("after delete: %v", err)
	}
	if err := s.Delete(ctx, "docs/a.txt"); err != nil {
		t.Errorf("delete missing: %v", err)
	}
	if err := s.Put(ctx, "../escape", strings.NewReader("x"), ""); err == nil {
		t.Error("invalid key accepted")
	}
}

func TestLocal(t *testing.T) {
	dir := t.TempDir()
	s, err := storage.New(storage.Config{Local: storage.LocalConfig{Dir: dir}})
	if err != nil {
		t.Fatal(err)
	}
	testStorage(t, s)

	// 写入失败不会留下临时文件。
	entries, _ := os.ReadDir(filepath.Join(dir, "docs"))
	if len(entries) != 0 {
		t.Errorf("leftover files: %v", entries)
	}
}

func TestS3(t *testing.T) {
	backend := s3mem.New()
	if err := backend.CreateBucket("test"); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(gofakes3.New(backend).Server())
	defer srv.Close()

	var cfg storage.Config
	cfg.Driver = storage.DriverS3
	cfg.S3.Endpoint = srv.URL
	cfg.S3.Region = "us-east-1"
	cfg.S3.Bucket = "test"
	cfg.S3.AccessKeyID = "key"
	cfg.S3.SecretAccessKey = "secret"
	cfg.S3.PathStyle = true
	s, err := storage.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	testStorage(t, s)

	cfg.S3.Bucket = ""
	if _, err := storage.New(cfg); err == nil {
		t.Error("missing bucket accepted")
	}
}
