package local_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/ryan053857/mygoravel/files"
	"github.com/ryan053857/mygoravel/files/local"
)

func openStore(t *testing.T) (*local.Store, string) {
	t.Helper()
	dir := t.TempDir()
	s, err := local.Open(local.Config{Root: dir})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s, dir
}

func readObject(t *testing.T, s *local.Store, key string) []byte {
	t.Helper()
	r, err := s.Open(context.Background(), key)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	data, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestObjectsPreservePreviousContentOnFailure(t *testing.T) {
	s, dir := openStore(t)
	ctx := context.Background()
	if err := s.Put(ctx, "images/a.txt", "text/plain", strings.NewReader("original"), 8); err != nil {
		t.Fatal(err)
	}
	for _, size := range []int64{2, 20, -1} {
		if err := s.Put(ctx, "images/a.txt", "", strings.NewReader("broken"), size); !errors.Is(err, files.ErrInvalid) {
			t.Fatalf("size %d: %v", size, err)
		}
		if got := string(readObject(t, s, "images/a.txt")); got != "original" {
			t.Fatalf("failed write replaced old content: %q", got)
		}
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if err := s.Put(canceled, "images/a.txt", "", strings.NewReader("new"), 3); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled put: %v", err)
	}
	if err := s.Copy(ctx, "images/a.txt", "copies/a.txt"); err != nil {
		t.Fatal(err)
	}
	info, err := s.Stat(ctx, "copies/a.txt")
	if err != nil || info.Size != 8 {
		t.Fatalf("stat=%+v err=%v", info, err)
	}
	if err := s.Put(ctx, "images/a.txt", "", strings.NewReader("changed"), 7); err != nil {
		t.Fatal(err)
	}
	if got := string(readObject(t, s, "copies/a.txt")); got != "original" {
		t.Fatalf("copy followed source rewrite: %q", got)
	}
	for range 2 {
		if err := s.Delete(ctx, "copies/a.txt"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.Open(ctx, "copies/a.txt"); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("deleted object: %v", err)
	}
	if err := filepath.WalkDir(dir, func(path string, entry fs.DirEntry, err error) error {
		if err == nil && strings.HasPrefix(entry.Name(), ".tmp-") {
			t.Errorf("temporary file leaked: %s", path)
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
}

func TestMultipartResumesAfterReopenAndValidatesCompletion(t *testing.T) {
	s, dir := openStore(t)
	ctx := context.Background()
	id, err := s.CreateMultipart(ctx, "large/file.bin", "application/octet-stream")
	if err != nil {
		t.Fatal(err)
	}
	first := bytes.Repeat([]byte("a"), int(files.MinPartSize))
	tail := []byte("tail")
	p2, err := s.UploadPart(ctx, "large/file.bin", id, 2, bytes.NewReader(tail), int64(len(tail)))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = local.Open(local.Config{Root: dir})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	parts, err := s.ListParts(ctx, "large/file.bin", id)
	if err != nil || len(parts) != 1 || parts[0] != p2 {
		t.Fatalf("resumed parts=%+v err=%v", parts, err)
	}
	p1, err := s.UploadPart(ctx, "large/file.bin", id, 1, bytes.NewReader(first), int64(len(first)))
	if err != nil {
		t.Fatal(err)
	}
	badTag := p1
	badTag.ETag = "wrong"
	for _, bad := range [][]files.Part{{}, {p1, p1}, {badTag, p2}, {{Number: 3, ETag: p2.ETag}}} {
		if err := s.CompleteMultipart(ctx, "large/file.bin", id, bad); err == nil {
			t.Fatalf("invalid completion succeeded: %+v", bad)
		}
		if _, err := s.Stat(ctx, "large/file.bin"); !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("failed completion published an object: %v", err)
		}
	}
	if err := s.CompleteMultipart(ctx, "large/file.bin", id, []files.Part{p2, p1}); err != nil {
		t.Fatal(err)
	}
	want := append(first, tail...)
	got := readObject(t, s, "large/file.bin")
	if sha256.Sum256(got) != sha256.Sum256(want) {
		t.Fatal("assembled content mismatch")
	}
	if _, err := s.ListParts(ctx, "large/file.bin", id); !errors.Is(err, files.ErrUploadNotFound) {
		t.Fatalf("completed session remained: %v", err)
	}
	if err := s.AbortMultipart(ctx, "large/file.bin", id); err != nil {
		t.Fatalf("abort completed upload: %v", err)
	}
}

func TestMultipartRetryAndAbort(t *testing.T) {
	s, _ := openStore(t)
	ctx := context.Background()
	id, err := s.CreateMultipart(ctx, "target", "")
	if err != nil {
		t.Fatal(err)
	}
	old, err := s.UploadPart(ctx, "target", id, 1, strings.NewReader("old"), 3)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.UploadPart(ctx, "target", id, 1, strings.NewReader("partial"), 100); !errors.Is(err, files.ErrInvalid) {
		t.Fatalf("incomplete part: %v", err)
	}
	parts, err := s.ListParts(ctx, "target", id)
	if err != nil || len(parts) != 1 || parts[0] != old {
		t.Fatalf("failed retry corrupted previous part: %+v %v", parts, err)
	}
	updated, err := s.UploadPart(ctx, "target", id, 1, strings.NewReader("new"), 3)
	if err != nil || updated.ETag == old.ETag {
		t.Fatalf("retry ETag=%q old=%q err=%v", updated.ETag, old.ETag, err)
	}
	if err := s.CompleteMultipart(ctx, "target", id, []files.Part{old}); !errors.Is(err, files.ErrInvalid) {
		t.Fatalf("stale ETag accepted: %v", err)
	}
	second, err := s.UploadPart(ctx, "target", id, 2, strings.NewReader("x"), 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.CompleteMultipart(ctx, "target", id, []files.Part{updated, second}); !errors.Is(err, files.ErrInvalid) {
		t.Fatalf("undersized nonfinal part accepted: %v", err)
	}
	for range 2 {
		if err := s.AbortMultipart(ctx, "target", id); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.UploadPart(ctx, "target", id, 1, strings.NewReader("x"), 1); !errors.Is(err, files.ErrUploadNotFound) {
		t.Fatalf("aborted session accepted part: %v", err)
	}
}

func TestPathAndSessionIsolation(t *testing.T) {
	s, dir := openStore(t)
	ctx := context.Background()
	for _, key := range []string{"", ".", "..", "../escape", "/absolute", "x/../escape", "x//y", `x\y`, "nul\x00byte"} {
		if err := s.Put(ctx, key, "", strings.NewReader("x"), 1); !errors.Is(err, files.ErrInvalid) {
			t.Errorf("unsafe key %q: %v", key, err)
		}
	}
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(dir, "objects", "escape")); err != nil {
		t.Fatal(err)
	}
	if err := s.Put(ctx, "escape/forbidden", "", strings.NewReader("x"), 1); err == nil {
		t.Fatal("write through external symlink succeeded")
	}
	if _, err := os.Stat(filepath.Join(outside, "forbidden")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("outside root changed: %v", err)
	}
	id, err := s.CreateMultipart(ctx, "target", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.UploadPart(ctx, "another", id, 1, strings.NewReader("x"), 1); !errors.Is(err, files.ErrInvalid) {
		t.Fatalf("session rebound: %v", err)
	}
	if err := s.AbortMultipart(ctx, "another", id); !errors.Is(err, files.ErrInvalid) {
		t.Fatalf("wrong key aborted session: %v", err)
	}
	if _, err := s.ListParts(ctx, "target", "../../objects"); !errors.Is(err, files.ErrInvalid) {
		t.Fatalf("invalid session accepted: %v", err)
	}
	if err := os.Symlink("../multipart", filepath.Join(dir, "objects", "sessions")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Open(ctx, "sessions/"+id+"/meta.json"); err == nil {
		t.Fatal("object reader accessed private multipart metadata")
	}
}

func TestConcurrentPartsAreCompleteAndReplaceable(t *testing.T) {
	s, _ := openStore(t)
	ctx := context.Background()
	id, err := s.CreateMultipart(ctx, "concurrent", "")
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := range 32 {
		wg.Go(func() {
			data := bytes.Repeat([]byte{byte(i)}, 8192)
			// 两个并发请求可以覆盖同一片号，但最终内容必须来自完整的一次写入。
			if _, err := s.UploadPart(ctx, "concurrent", id, int32(i%16+1), bytes.NewReader(data), int64(len(data))); err != nil {
				t.Errorf("upload part: %v", err)
			}
		})
	}
	wg.Wait()
	parts, err := s.ListParts(ctx, "concurrent", id)
	if err != nil || len(parts) != 16 {
		t.Fatalf("parts=%+v err=%v", parts, err)
	}
	for i, p := range parts {
		if p.Number != int32(i+1) || p.Size != 8192 || len(p.ETag) != 64 {
			t.Fatalf("invalid stored part: %+v", p)
		}
	}
	if err := s.CompleteMultipart(ctx, "concurrent", id, parts[:1]); err != nil {
		t.Fatal(err)
	}
	data := readObject(t, s, "concurrent")
	if !bytes.Equal(data, bytes.Repeat(data[:1], len(data))) {
		t.Fatal("concurrent retry mixed bodies")
	}
}

func TestCorruptPartIsNeverPublished(t *testing.T) {
	s, dir := openStore(t)
	ctx := context.Background()
	id, err := s.CreateMultipart(ctx, "corrupt", "")
	if err != nil {
		t.Fatal(err)
	}
	p, err := s.UploadPart(ctx, "corrupt", id, 1, strings.NewReader("valid"), 5)
	if err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(filepath.Join(dir, "multipart", id, "00001.part"), os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.WriteAt([]byte("wrong"), 65)
	f.Close()
	if err != nil {
		t.Fatal(err)
	}
	if err := s.CompleteMultipart(ctx, "corrupt", id, []files.Part{p}); !errors.Is(err, files.ErrInvalid) {
		t.Fatalf("corrupt part accepted: %v", err)
	}
	if _, err := s.Stat(ctx, "corrupt"); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("corrupt object published: %v", err)
	}
}

func TestCompletedUploadRecoversAfterPartialCleanup(t *testing.T) {
	s, dir := openStore(t)
	ctx := context.Background()
	const key = "uploads/cleanup-interrupted"
	id, err := s.CreateMultipart(ctx, key, "application/octet-stream")
	if err != nil {
		t.Fatal(err)
	}
	first := bytes.Repeat([]byte("a"), int(files.MinPartSize))
	p1, err := s.UploadPart(ctx, key, id, 1, bytes.NewReader(first), int64(len(first)))
	if err != nil {
		t.Fatal(err)
	}
	p2, err := s.UploadPart(ctx, key, id, 2, strings.NewReader("end"), 3)
	if err != nil {
		t.Fatal(err)
	}
	// 模拟合并对象和完成标记已落盘，RemoveAll 删掉第一片后进程退出。
	complete := append(first, "end"...)
	if err := s.Put(ctx, key, "", bytes.NewReader(complete), int64(len(complete))); err != nil {
		t.Fatal(err)
	}
	meta, err := json.Marshal(map[string]any{"key": key, "completed": true})
	if err != nil {
		t.Fatal(err)
	}
	uploadDir := filepath.Join(dir, "multipart", id)
	if err := os.WriteFile(filepath.Join(uploadDir, "meta.json"), meta, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(uploadDir, "00001.part")); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	resumed, err := local.Open(local.Config{Root: dir})
	if err != nil {
		t.Fatal(err)
	}
	defer resumed.Close()
	if _, err := resumed.ListParts(ctx, key, id); !errors.Is(err, files.ErrUploadNotFound) {
		t.Fatalf("completed upload must trigger object recovery, got %v", err)
	}
	if err := resumed.CompleteMultipart(ctx, key, id, []files.Part{p1, p2}); !errors.Is(err, files.ErrUploadNotFound) {
		t.Fatalf("completed upload must not reassemble missing parts: %v", err)
	}
	if _, err := resumed.UploadPart(ctx, key, id, 2, strings.NewReader("new"), 3); !errors.Is(err, files.ErrUploadNotFound) {
		t.Fatalf("completed upload accepted a replacement part: %v", err)
	}
	if err := resumed.AbortMultipart(ctx, "wrong-key", id); !errors.Is(err, files.ErrInvalid) {
		t.Fatalf("completed metadata lost key binding: %v", err)
	}
	if err := resumed.AbortMultipart(ctx, key, id); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(uploadDir); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("partial cleanup left a directory: %v", err)
	}
	if got := readObject(t, resumed, key); !bytes.Equal(got, complete) {
		t.Fatal("recovery changed the completed object")
	}
}

func TestAbortRecoversWhenCleanupAlreadyDeletedMetadata(t *testing.T) {
	s, dir := openStore(t)
	ctx := context.Background()
	id, err := s.CreateMultipart(ctx, "uploads/aborted", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.UploadPart(ctx, "uploads/aborted", id, 1, strings.NewReader("retained"), 8); err != nil {
		t.Fatal(err)
	}
	uploadDir := filepath.Join(dir, "multipart", id)
	// 模拟 RemoveAll 先删除元数据，随后在删分片时失败。
	if err := os.Remove(filepath.Join(uploadDir, "meta.json")); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := s.AbortMultipart(ctx, "uploads/aborted", id); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := os.Stat(uploadDir); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("missing metadata caused successful-but-incomplete cleanup: %v", err)
	}
}
