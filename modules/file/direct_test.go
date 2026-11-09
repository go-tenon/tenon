package file_test

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
	"uuid"

	"github.com/johannesboyne/gofakes3"
	"github.com/johannesboyne/gofakes3/backend/s3mem"
	"github.com/uptrace/bun"

	"github.com/ryan053857/go-kit/database"
	"github.com/ryan053857/go-kit/database/dbtest"
	"github.com/ryan053857/go-kit/modules/file"
	"github.com/ryan053857/go-kit/storage"
)

func newDB(t *testing.T) *bun.DB {
	t.Helper()
	dcfg := database.DefaultConfig()
	dcfg.DSN = dbtest.DSN(t)
	db, err := database.Open(t.Context(), dcfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	dir := t.TempDir()
	if _, err := database.PublishMigrations(dir, file.Migrations); err != nil {
		t.Fatal(err)
	}
	if err := database.Migrate(t.Context(), db.DB, os.DirFS(dir)); err != nil {
		t.Fatal(err)
	}
	return db
}

func newS3(t *testing.T) storage.Storage {
	t.Helper()
	backend := s3mem.New()
	if err := backend.CreateBucket("test"); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(gofakes3.New(backend).Server())
	t.Cleanup(srv.Close)
	var cfg storage.Config
	cfg.Driver = storage.DriverS3
	cfg.S3 = storage.S3Config{Endpoint: srv.URL, Region: "us-east-1", Bucket: "test", AccessKeyID: "k", SecretAccessKey: "s", PathStyle: true}
	s, err := storage.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func put(t *testing.T, up *file.Upload, body []byte) {
	t.Helper()
	req, _ := http.NewRequest(up.Method, up.URL, bytes.NewReader(body))
	for k, v := range up.Header {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("PUT = %s", resp.Status)
	}
}

func TestDirectUpload(t *testing.T) {
	db := newDB(t)
	cfg := file.DefaultConfig()
	cfg.AllowedTypes = []string{"image/", "application/pdf"}
	cfg.MaxDirectSize = 1 << 20
	s := file.NewService(db, newS3(t), cfg)
	ctx := t.Context()
	owner := uuid.NewV7()
	img := pngBytes()

	if _, _, err := s.CreateUpload(ctx, owner, "a.png", "image/png", 2<<20); !errors.Is(err, file.ErrTooLarge) {
		t.Errorf("too large: %v", err)
	}
	if _, _, err := s.CreateUpload(ctx, owner, "a.exe", "application/x-msdownload", 10); !errors.Is(err, file.ErrTypeNotAllowed) {
		t.Errorf("declared type: %v", err)
	}

	f, up, err := s.CreateUpload(ctx, owner, "photo.png", "image/png", int64(len(img)))
	if err != nil {
		t.Fatal(err)
	}
	if f.Status != file.StatusPending || up.Method != "PUT" || up.Header["Content-Type"] != "image/png" {
		t.Fatalf("create = %+v %+v", f, up)
	}
	if _, err := s.Get(ctx, f.ID); !errors.Is(err, file.ErrNotFound) {
		t.Errorf("pending file visible: %v", err)
	}
	if _, err := s.CompleteUpload(ctx, owner, f.ID); !errors.Is(err, file.ErrUploadIncomplete) {
		t.Errorf("complete before upload: %v", err)
	}
	put(t, up, img)
	if _, err := s.CompleteUpload(ctx, uuid.NewV7(), f.ID); !errors.Is(err, file.ErrNotFound) {
		t.Errorf("complete by other user: %v", err)
	}
	done, err := s.CompleteUpload(ctx, owner, f.ID)
	if err != nil || done.Status != file.StatusReady || done.ContentType != "image/png" || !done.ExpiresAt.IsZero() {
		t.Fatalf("complete = %+v, %v", done, err)
	}
	if _, err := s.Get(ctx, f.ID); err != nil {
		t.Errorf("get after complete: %v", err)
	}

	// 声明为 PDF、实际是 HTML：完成时按内容识别，拒绝并删除。
	html := []byte("<!DOCTYPE html><script>alert(1)</script>")
	f2, up2, err := s.CreateUpload(ctx, owner, "doc.pdf", "application/pdf", int64(len(html)))
	if err != nil {
		t.Fatal(err)
	}
	put(t, up2, html)
	if _, err := s.CompleteUpload(ctx, owner, f2.ID); !errors.Is(err, file.ErrTypeNotAllowed) {
		t.Errorf("disguised html: %v", err)
	}
	if _, err := s.CompleteUpload(ctx, owner, f2.ID); !errors.Is(err, file.ErrNotFound) {
		t.Errorf("rejected file should be deleted: %v", err)
	}

	// 本地存储不支持直传。
	local := file.NewService(db, storage.NewLocal(t.TempDir()), cfg)
	if _, _, err := local.CreateUpload(ctx, owner, "a.png", "image/png", 10); !errors.Is(err, file.ErrNoDirectUpload) {
		t.Errorf("local: %v", err)
	}
}

func TestAttachAndCleanup(t *testing.T) {
	db := newDB(t)
	cfg := file.DefaultConfig()
	cfg.RequireAttach = true
	cfg.AttachTTL = time.Hour
	store := storage.NewLocal(t.TempDir())
	s := file.NewService(db, store, cfg)
	ctx := t.Context()
	owner := uuid.NewV7()

	kept, err := s.Upload(ctx, owner, "kept.txt", strings.NewReader("kept"))
	if err != nil {
		t.Fatal(err)
	}
	orphan, err := s.Upload(ctx, owner, "orphan.txt", strings.NewReader("orphan"))
	if err != nil {
		t.Fatal(err)
	}
	if kept.ExpiresAt.IsZero() {
		t.Fatal("RequireAttach upload has no deadline")
	}
	err = db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		return s.Attach(ctx, tx, kept.ID)
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Attach(ctx, db, kept.ID, uuid.NewV7()); !errors.Is(err, file.ErrNotFound) {
		t.Errorf("attach missing: %v", err)
	}

	// 未到期：不删除。
	if n, err := s.Cleanup(ctx); err != nil || n != 0 {
		t.Errorf("cleanup before deadline = %d, %v", n, err)
	}
	if _, err := db.NewUpdate().Model((*file.File)(nil)).Set("expires_at = ?", time.Now().Add(-time.Minute)).
		Where("id = ?", orphan.ID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if n, err := s.Cleanup(ctx); err != nil || n != 1 {
		t.Fatalf("cleanup = %d, %v", n, err)
	}
	if _, err := s.Get(ctx, orphan.ID); !errors.Is(err, file.ErrNotFound) {
		t.Errorf("orphan still exists: %v", err)
	}
	if _, err := store.Get(ctx, orphan.Key); !errors.Is(err, storage.ErrNotFound) {
		t.Errorf("orphan content still exists: %v", err)
	}
	if _, err := s.Get(ctx, kept.ID); err != nil {
		t.Errorf("attached file deleted: %v", err)
	}
	// 过期后不能再 Attach。
	if err := s.Attach(ctx, db, orphan.ID); !errors.Is(err, file.ErrNotFound) {
		t.Errorf("attach expired: %v", err)
	}
}
