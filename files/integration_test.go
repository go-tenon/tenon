//go:build integration

package files_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/uptrace/bun"

	"github.com/ryan053857/mygoravel/database/dbtest"
	"github.com/ryan053857/mygoravel/files"
	"github.com/ryan053857/mygoravel/files/local"
	"github.com/ryan053857/mygoravel/identity"
)

var fileOwner = identity.Subject{ID: "opaque-owner", Type: identity.SubjectUser}

type fileEnvironment struct {
	db    *bun.DB
	store *local.Store
	svc   *files.Service
	dir   string
	cfg   files.Config
}

func newFileEnvironment(t *testing.T) fileEnvironment {
	t.Helper()
	db := dbtest.Open(t, files.Migrations())
	dir := t.TempDir()
	store, err := local.Open(local.Config{Root: dir})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	cfg := files.DefaultConfig()
	cfg.PartSize = files.MinPartSize
	svc, err := files.New(db, map[string]files.Backend{"local": store}, cfg)
	if err != nil {
		t.Fatal(err)
	}
	return fileEnvironment{db: db, store: store, svc: svc, dir: dir, cfg: cfg}
}

func prepareFile(t *testing.T, svc *files.Service, size int64) files.Record {
	t.Helper()
	r, err := svc.Prepare(context.Background(), fileOwner, files.PrepareInput{Filename: "report.txt", ContentType: "text/plain", Size: size})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func expireFile(t *testing.T, db *bun.DB, id uuid.UUID) {
	t.Helper()
	_, err := db.NewUpdate().Model((*files.Record)(nil)).Set("expires_at = ?", time.Now().Add(-time.Minute)).Where("id = ?", id).Exec(context.Background())
	if err != nil {
		t.Fatal(err)
	}
}

func readReadyFile(t *testing.T, svc *files.Service, id uuid.UUID) []byte {
	t.Helper()
	body, record, err := svc.Open(context.Background(), fileOwner, id)
	if err != nil {
		t.Fatal(err)
	}
	defer body.Close()
	if record.Status != files.StatusReady {
		t.Fatalf("read nonready record: %+v", record)
	}
	data, err := io.ReadAll(body)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestFileLifecycleAndApplicationColumns(t *testing.T) {
	e := newFileEnvironment(t)
	ctx := context.Background()
	// 消费项目增加列后，框架查询仍只扫描自己的模型列。
	if _, err := e.db.ExecContext(ctx, "ALTER TABLE files ADD COLUMN product_note text NOT NULL DEFAULT 'application'"); err != nil {
		t.Fatal(err)
	}
	for _, content := range []string{"", "hello files"} {
		t.Run("bytes="+strconv.Itoa(len(content)), func(t *testing.T) {
			r := prepareFile(t, e.svc, int64(len(content)))
			if r.Status != files.StatusTemporary || r.Disk != "local" || r.ExpiresAt == nil {
				t.Fatalf("prepared record: %+v", r)
			}
			if _, err := e.svc.Confirm(ctx, fileOwner, r.ID); !errors.Is(err, files.ErrConflict) {
				t.Fatalf("confirmed missing upload: %v", err)
			}
			if body, _, err := e.svc.Open(ctx, fileOwner, r.ID); !errors.Is(err, files.ErrConflict) || body != nil {
				t.Fatalf("opened temporary file: %v", err)
			}
			if _, err := e.svc.Put(ctx, fileOwner, r.ID, strings.NewReader(content+"extra")); !errors.Is(err, files.ErrInvalid) {
				t.Fatalf("accepted oversized body: %v", err)
			}
			uploaded, err := e.svc.Put(ctx, fileOwner, r.ID, strings.NewReader(content))
			if err != nil || uploaded.UploadedAt == nil || uploaded.Status != files.StatusTemporary {
				t.Fatalf("put=%+v err=%v", uploaded, err)
			}
			if _, err := e.svc.Complete(ctx, fileOwner, r.ID); err != nil {
				t.Fatal(err)
			}
			if _, err := e.svc.Put(ctx, fileOwner, r.ID, strings.NewReader(content)); !errors.Is(err, files.ErrConflict) {
				t.Fatalf("overwrote completed upload: %v", err)
			}
			confirmed, err := e.svc.Confirm(ctx, fileOwner, r.ID)
			if err != nil || confirmed.Status != files.StatusReady || confirmed.Key == r.Key {
				t.Fatalf("confirm=%+v err=%v", confirmed, err)
			}
			again, err := e.svc.Confirm(ctx, fileOwner, r.ID)
			if err != nil || again.Key != confirmed.Key {
				t.Fatalf("repeat confirm=%+v err=%v", again, err)
			}
			if got := string(readReadyFile(t, e.svc, r.ID)); got != content {
				t.Fatalf("got %q want %q", got, content)
			}
			before, err := e.svc.Get(ctx, fileOwner, r.ID)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := e.svc.Complete(ctx, fileOwner, r.ID); err != nil {
				t.Fatalf("complete retry after confirmation: %v", err)
			}
			if _, err := e.svc.Confirm(ctx, fileOwner, r.ID); err != nil {
				t.Fatal(err)
			}
			after, err := e.svc.Get(ctx, fileOwner, r.ID)
			if err != nil || !after.UpdatedAt.Equal(before.UpdatedAt) {
				t.Fatalf("idempotent request changed updated_at: before=%s after=%s err=%v", before.UpdatedAt, after.UpdatedAt, err)
			}
			for _, stranger := range []identity.Subject{
				{ID: "another", Type: identity.SubjectUser},
				{ID: fileOwner.ID, Type: identity.SubjectService},
			} {
				if _, err := e.svc.Get(ctx, stranger, r.ID); !errors.Is(err, files.ErrNotFound) {
					t.Fatalf("cross-owner read: %v", err)
				}
				if err := e.svc.Delete(ctx, stranger, r.ID); !errors.Is(err, files.ErrNotFound) {
					t.Fatalf("cross-owner delete: %v", err)
				}
			}
			for range 2 {
				if err := e.svc.Delete(ctx, fileOwner, r.ID); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := e.svc.Get(ctx, fileOwner, r.ID); !errors.Is(err, files.ErrNotFound) {
				t.Fatalf("deleted file remained available: %v", err)
			}
			if _, err := e.store.Stat(ctx, confirmed.Key); !errors.Is(err, fs.ErrNotExist) {
				t.Fatalf("deleted content remained: %v", err)
			}
		})
	}
}

func TestFileCompleteVerifiesActualObjectSize(t *testing.T) {
	e := newFileEnvironment(t)
	ctx := context.Background()
	r := prepareFile(t, e.svc, 4)
	// 模拟客户端直传：对象已经存在，但数据库尚未确认上传结束。
	if err := e.store.Put(ctx, r.Key, "", strings.NewReader("bad"), 3); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Complete(ctx, fileOwner, r.ID); !errors.Is(err, files.ErrInvalid) {
		t.Fatalf("accepted wrong actual size: %v", err)
	}
	got, err := e.svc.Get(ctx, fileOwner, r.ID)
	if err != nil || got.UploadedAt != nil {
		t.Fatalf("incorrect upload was recorded as complete: %+v %v", got, err)
	}
	if err := e.store.Put(ctx, r.Key, "", strings.NewReader("good"), 4); err != nil {
		t.Fatal(err)
	}
	if got, err := e.svc.Complete(ctx, fileOwner, r.ID); err != nil || got.UploadedAt == nil {
		t.Fatalf("complete direct upload=%+v err=%v", got, err)
	}
	if _, err := e.svc.Confirm(ctx, fileOwner, r.ID); err != nil {
		t.Fatal(err)
	}
	if got := string(readReadyFile(t, e.svc, r.ID)); got != "good" {
		t.Fatalf("confirmed direct content=%q", got)
	}
}

func TestFileMultipartResumeAndParallelParts(t *testing.T) {
	e := newFileEnvironment(t)
	ctx := context.Background()
	first := bytes.Repeat([]byte("a"), int(e.cfg.PartSize))
	tail := []byte("small final part")
	r := prepareFile(t, e.svc, int64(len(first)+len(tail)))
	created, err := e.svc.CreateMultipart(ctx, fileOwner, r.ID)
	if err != nil || created.UploadID == "" {
		t.Fatalf("create=%+v err=%v", created, err)
	}
	again, err := e.svc.CreateMultipart(ctx, fileOwner, r.ID)
	if err != nil || again.UploadID != created.UploadID {
		t.Fatalf("created duplicate session: %+v %v", again, err)
	}
	part2, err := e.svc.UploadPart(ctx, fileOwner, r.ID, created.UploadID, 2, bytes.NewReader(tail))
	if err != nil {
		t.Fatal(err)
	}
	if err := e.store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := local.Open(local.Config{Root: e.dir})
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	// 新配置不能改变已创建会话的分片大小。
	changed := e.cfg
	changed.PartSize = 8 << 20
	barrier := &partBarrier{Backend: reopened, entered: make(chan struct{}, 2), release: make(chan struct{})}
	svc, err := files.New(e.db, map[string]files.Backend{"local": barrier}, changed)
	if err != nil {
		t.Fatal(err)
	}
	listed, err := svc.ListParts(ctx, fileOwner, r.ID, created.UploadID)
	if err != nil || len(listed) != 1 || listed[0] != part2 {
		t.Fatalf("resume=%+v err=%v", listed, err)
	}
	for _, number := range []int32{0, 3, files.MaxParts + 1} {
		if _, err := svc.UploadPart(ctx, fileOwner, r.ID, created.UploadID, number, strings.NewReader("x")); !errors.Is(err, files.ErrInvalid) {
			t.Fatalf("accepted part %d: %v", number, err)
		}
	}
	parts := make([]files.Part, 2)
	errs := make([]error, 2)
	parallelCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var wg sync.WaitGroup
	for i, data := range [][]byte{first, tail} {
		wg.Go(func() {
			parts[i], errs[i] = svc.UploadPart(parallelCtx, fileOwner, r.ID, created.UploadID, int32(i+1), bytes.NewReader(data))
		})
	}
	// 两个请求必须同时进入存储层，证明数据库共享锁允许并行分片。
	entered := 0
	for range 2 {
		select {
		case <-barrier.entered:
			entered++
		case <-parallelCtx.Done():
		}
	}
	close(barrier.release)
	wg.Wait()
	if entered != 2 {
		t.Fatalf("multipart requests were serialized: %d entered, errors=%v", entered, errs)
	}
	for _, err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if _, err := svc.CompleteMultipart(ctx, fileOwner, r.ID, created.UploadID, parts[:1]); !errors.Is(err, files.ErrInvalid) {
		t.Fatalf("accepted missing part: %v", err)
	}
	if _, err := svc.CompleteMultipart(ctx, fileOwner, r.ID, created.UploadID, []files.Part{parts[0], parts[0]}); !errors.Is(err, files.ErrInvalid) {
		t.Fatalf("accepted repeated part: %v", err)
	}
	for range 2 {
		// 浏览器只提交编号和 ETag，实际大小必须由服务端查询并传给后端。
		manifest := []files.Part{{Number: parts[1].Number, ETag: parts[1].ETag}, {Number: parts[0].Number, ETag: parts[0].ETag}}
		completed, err := svc.CompleteMultipart(ctx, fileOwner, r.ID, created.UploadID, manifest)
		if err != nil || completed.UploadID != created.UploadID || completed.UploadedAt == nil {
			t.Fatalf("complete=%+v err=%v", completed, err)
		}
	}
	if _, err := svc.Confirm(ctx, fileOwner, r.ID); err != nil {
		t.Fatal(err)
	}
	before, err := svc.Get(ctx, fileOwner, r.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after, err := svc.CompleteMultipart(ctx, fileOwner, r.ID, created.UploadID, parts); err != nil || after.Status != files.StatusReady || !after.UpdatedAt.Equal(before.UpdatedAt) {
		t.Fatalf("complete retry after confirm=%+v err=%v", after, err)
	}
	if _, err := svc.CompleteMultipart(ctx, fileOwner, r.ID, created.UploadID, make([]files.Part, files.MaxParts+1)); !errors.Is(err, files.ErrInvalid) {
		t.Fatalf("oversized manifest accepted: %v", err)
	}
	if got, want := sha256.Sum256(readReadyFile(t, svc, r.ID)), sha256.Sum256(append(first, tail...)); got != want {
		t.Fatal("assembled file differs")
	}
}

func TestFileMultipartAbortAndEmptyFile(t *testing.T) {
	e := newFileEnvironment(t)
	ctx := context.Background()
	r := prepareFile(t, e.svc, 0)
	first, err := e.svc.CreateMultipart(ctx, fileOwner, r.ID)
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := e.svc.AbortMultipart(ctx, fileOwner, r.ID, first.UploadID); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := e.svc.Put(ctx, fileOwner, r.ID, strings.NewReader("")); !errors.Is(err, files.ErrConflict) {
		t.Fatalf("aborted multipart switched to single PUT: %v", err)
	}
	if _, err := e.svc.SignPut(ctx, fileOwner, r.ID); !errors.Is(err, files.ErrConflict) {
		t.Fatalf("aborted multipart switched to single signing: %v", err)
	}
	second, err := e.svc.CreateMultipart(ctx, fileOwner, r.ID)
	if err != nil || second.UploadID == first.UploadID {
		t.Fatalf("recreated session=%+v err=%v", second, err)
	}
	if _, err := e.svc.UploadPart(ctx, fileOwner, r.ID, first.UploadID, 1, strings.NewReader("")); !errors.Is(err, files.ErrConflict) {
		t.Fatalf("old request uploaded into new session: %v", err)
	}
	if _, err := e.svc.ListParts(ctx, fileOwner, r.ID, first.UploadID); !errors.Is(err, files.ErrConflict) {
		t.Fatalf("old request listed new session: %v", err)
	}
	if err := e.svc.AbortMultipart(ctx, fileOwner, r.ID, first.UploadID); !errors.Is(err, files.ErrConflict) {
		t.Fatalf("old request aborted new session: %v", err)
	}
	if _, err := e.svc.CompleteMultipart(ctx, fileOwner, r.ID, first.UploadID, nil); !errors.Is(err, files.ErrConflict) {
		t.Fatalf("old request completed new session: %v", err)
	}
	part, err := e.svc.UploadPart(ctx, fileOwner, r.ID, second.UploadID, 1, strings.NewReader(""))
	if err != nil || part.Size != 0 {
		t.Fatalf("empty part=%+v err=%v", part, err)
	}
	if _, err := e.svc.CompleteMultipart(ctx, fileOwner, r.ID, second.UploadID, []files.Part{part}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Confirm(ctx, fileOwner, r.ID); err != nil {
		t.Fatal(err)
	}
	if got := readReadyFile(t, e.svc, r.ID); len(got) != 0 {
		t.Fatalf("empty file contains %q", got)
	}
}

func TestFileExpirationAndCleanupPreserveReadyContent(t *testing.T) {
	e := newFileEnvironment(t)
	ctx := context.Background()
	ready := prepareFile(t, e.svc, 4)
	if _, err := e.svc.Put(ctx, fileOwner, ready.ID, strings.NewReader("safe")); err != nil {
		t.Fatal(err)
	}
	confirmed, err := e.svc.Confirm(ctx, fileOwner, ready.ID)
	if err != nil {
		t.Fatal(err)
	}
	// 模拟旧上传地址改写上传键；正式键必须保持独立。
	if err := e.store.Put(ctx, ready.Key, "", strings.NewReader("evil"), 4); err != nil {
		t.Fatal(err)
	}
	if got := string(readReadyFile(t, e.svc, ready.ID)); got != "safe" {
		t.Fatalf("upload key changed ready object: %q", got)
	}
	expired := prepareFile(t, e.svc, 1)
	created, err := e.svc.CreateMultipart(ctx, fileOwner, expired.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.UploadPart(ctx, fileOwner, expired.ID, created.UploadID, 1, strings.NewReader("x")); err != nil {
		t.Fatal(err)
	}
	deleted := prepareFile(t, e.svc, 1)
	if err := e.svc.Delete(ctx, fileOwner, deleted.ID); err != nil {
		t.Fatal(err)
	}
	// 已删除记录到期前仍须保留回收信息，以处理旧地址重建的上传对象。
	if err := e.store.Put(ctx, deleted.Key, "", strings.NewReader("x"), 1); err != nil {
		t.Fatal(err)
	}
	for _, r := range []files.Record{ready, expired, deleted} {
		expireFile(t, e.db, r.ID)
	}
	if _, err := e.svc.Get(ctx, fileOwner, expired.ID); !errors.Is(err, files.ErrNotFound) {
		t.Fatalf("expired file remained available: %v", err)
	}
	if _, err := e.svc.UploadPart(ctx, fileOwner, expired.ID, created.UploadID, 1, strings.NewReader("x")); !errors.Is(err, files.ErrNotFound) {
		t.Fatalf("expired upload accepted: %v", err)
	}
	if _, err := e.svc.CreateMultipart(ctx, fileOwner, expired.ID); !errors.Is(err, files.ErrNotFound) {
		t.Fatalf("expired session recreated: %v", err)
	}
	count, err := e.svc.Cleanup(ctx, 10)
	if err != nil || count != 3 {
		t.Fatalf("cleanup count=%d err=%v", count, err)
	}
	for _, key := range []string{ready.Key, expired.Key, deleted.Key} {
		if _, err := e.store.Stat(ctx, key); !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("cleanup retained %q: %v", key, err)
		}
	}
	if _, err := e.store.ListParts(ctx, expired.Key, created.UploadID); !errors.Is(err, files.ErrUploadNotFound) {
		t.Fatalf("expired multipart remained: %v", err)
	}
	got, err := e.svc.Get(ctx, fileOwner, ready.ID)
	if err != nil || got.Status != files.StatusReady || got.ExpiresAt != nil || got.Key != confirmed.Key {
		t.Fatalf("ready after cleanup=%+v err=%v", got, err)
	}
	if content := string(readReadyFile(t, e.svc, ready.ID)); content != "safe" {
		t.Fatalf("cleanup changed ready object: %q", content)
	}
	if count, err := e.svc.Cleanup(ctx, 10); err != nil || count != 0 {
		t.Fatalf("repeat cleanup count=%d err=%v", count, err)
	}
}

var errLostStorageResponse = errors.New("simulated response loss after storage succeeded")

type partBarrier struct {
	files.Backend
	entered chan struct{}
	release chan struct{}
}

func (b *partBarrier) UploadPart(ctx context.Context, key, uploadID string, number int32, body io.Reader, size int64) (files.Part, error) {
	select {
	case b.entered <- struct{}{}:
	case <-ctx.Done():
		return files.Part{}, ctx.Err()
	}
	select {
	case <-b.release:
		return b.Backend.UploadPart(ctx, key, uploadID, number, body, size)
	case <-ctx.Done():
		return files.Part{}, ctx.Err()
	}
}

type lostCompleteResponse struct {
	files.Backend
	failed bool
}

func (b *partBarrier) CompleteMultipart(ctx context.Context, key, uploadID string, parts []files.Part) error {
	// 与 S3 后端相同，非末片必须带有满足下限的实际尺寸。
	for i, part := range parts {
		if i < len(parts)-1 && part.Size < files.MinPartSize {
			return files.ErrInvalid
		}
	}
	return b.Backend.CompleteMultipart(ctx, key, uploadID, parts)
}

func (b *lostCompleteResponse) CompleteMultipart(ctx context.Context, key, uploadID string, parts []files.Part) error {
	if err := b.Backend.CompleteMultipart(ctx, key, uploadID, parts); err != nil {
		return err
	}
	if !b.failed {
		b.failed = true
		return errLostStorageResponse
	}
	return nil
}

func TestFileMultipartRecoversAfterStorageResponseLoss(t *testing.T) {
	e := newFileEnvironment(t)
	ctx := context.Background()
	svc, err := files.New(e.db, map[string]files.Backend{"local": &lostCompleteResponse{Backend: e.store}}, e.cfg)
	if err != nil {
		t.Fatal(err)
	}
	r := prepareFile(t, svc, 4)
	created, err := svc.CreateMultipart(ctx, fileOwner, r.ID)
	if err != nil {
		t.Fatal(err)
	}
	part, err := svc.UploadPart(ctx, fileOwner, r.ID, created.UploadID, 1, strings.NewReader("data"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CompleteMultipart(ctx, fileOwner, r.ID, created.UploadID, []files.Part{part}); !errors.Is(err, errLostStorageResponse) {
		t.Fatalf("lost response failure=%v", err)
	}
	rolledBack, err := svc.Get(ctx, fileOwner, r.ID)
	if err != nil || rolledBack.UploadedAt != nil || rolledBack.UploadID != created.UploadID {
		t.Fatalf("database did not roll back: %+v %v", rolledBack, err)
	}
	completed, err := svc.CompleteMultipart(ctx, fileOwner, r.ID, created.UploadID, []files.Part{part})
	if err != nil || completed.UploadedAt == nil || completed.UploadID != created.UploadID {
		t.Fatalf("retry failed to recover stored object: %+v %v", completed, err)
	}
	if _, err := svc.Confirm(ctx, fileOwner, r.ID); err != nil {
		t.Fatal(err)
	}
	if got := string(readReadyFile(t, svc, r.ID)); got != "data" {
		t.Fatalf("recovered wrong content: %q", got)
	}
}

type lostCopyResponse struct {
	files.Backend
	failDelete bool
}

func (b *lostCopyResponse) Copy(ctx context.Context, source, destination string) error {
	if err := b.Backend.Copy(ctx, source, destination); err != nil {
		return err
	}
	return errLostStorageResponse
}

func (b *lostCopyResponse) Delete(ctx context.Context, key string) error {
	if b.failDelete && strings.HasPrefix(key, "objects/") {
		b.failDelete = false
		return errLostStorageResponse
	}
	return b.Backend.Delete(ctx, key)
}

func TestFileCleanupRetriesRemovalOfRolledBackConfirm(t *testing.T) {
	e := newFileEnvironment(t)
	ctx := context.Background()
	svc, err := files.New(e.db, map[string]files.Backend{"local": &lostCopyResponse{Backend: e.store, failDelete: true}}, e.cfg)
	if err != nil {
		t.Fatal(err)
	}
	r := prepareFile(t, svc, 4)
	if _, err := svc.Put(ctx, fileOwner, r.ID, strings.NewReader("data")); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Confirm(ctx, fileOwner, r.ID); !errors.Is(err, errLostStorageResponse) {
		t.Fatalf("confirm response=%v", err)
	}
	rolledBack, err := svc.Get(ctx, fileOwner, r.ID)
	if err != nil || rolledBack.Status != files.StatusTemporary || rolledBack.Key != r.Key {
		t.Fatalf("confirm database state did not roll back: %+v %v", rolledBack, err)
	}
	readyKey := "objects/" + r.ID.String()
	if _, err := e.store.Stat(ctx, readyKey); err != nil {
		t.Fatalf("failure injection did not create ready copy: %v", err)
	}
	expireFile(t, e.db, r.ID)
	if count, err := svc.Cleanup(ctx, 10); !errors.Is(err, errLostStorageResponse) || count != 0 {
		t.Fatalf("failed cleanup count=%d err=%v", count, err)
	}
	var retryable files.Record
	if err := e.db.NewSelect().Model(&retryable).Where("id = ?", r.ID).Scan(ctx); err != nil {
		t.Fatal(err)
	}
	if retryable.ExpiresAt == nil || retryable.Status != files.StatusTemporary {
		t.Fatalf("failed cleanup lost retry state: %+v", retryable)
	}
	if _, err := e.store.Stat(ctx, r.Key); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("failed cleanup did not exercise partial deletion: %v", err)
	}
	if _, err := e.store.Stat(ctx, readyKey); err != nil {
		t.Fatalf("failed cleanup unexpectedly removed ready copy: %v", err)
	}
	if count, err := svc.Cleanup(ctx, 10); err != nil || count != 1 {
		t.Fatalf("cleanup count=%d err=%v", count, err)
	}
	for _, key := range []string{r.Key, readyKey} {
		if _, err := e.store.Stat(ctx, key); !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("orphan %q remained: %v", key, err)
		}
	}
}

type lostDeleteResponse struct {
	files.Backend
	key    string
	failed bool
}

func (b *lostDeleteResponse) Delete(ctx context.Context, key string) error {
	if err := b.Backend.Delete(ctx, key); err != nil {
		return err
	}
	if key == b.key && !b.failed {
		b.failed = true
		return errLostStorageResponse
	}
	return nil
}

func TestFileDeletePersistsRetryBeforeRemovingStorage(t *testing.T) {
	for _, failure := range []string{"storage response", "database update"} {
		t.Run(failure, func(t *testing.T) {
			e := newFileEnvironment(t)
			ctx := context.Background()
			r := prepareFile(t, e.svc, 4)
			if _, err := e.svc.Put(ctx, fileOwner, r.ID, strings.NewReader("data")); err != nil {
				t.Fatal(err)
			}
			ready, err := e.svc.Confirm(ctx, fileOwner, r.ID)
			if err != nil {
				t.Fatal(err)
			}
			expireFile(t, e.db, r.ID)
			if n, err := e.svc.Cleanup(ctx, 10); err != nil || n != 1 {
				t.Fatalf("initial cleanup n=%d err=%v", n, err)
			}
			before, err := e.svc.Get(ctx, fileOwner, r.ID)
			if err != nil || before.ExpiresAt != nil {
				t.Fatalf("expected ready record without pending cleanup: %+v %v", before, err)
			}
			svc := e.svc
			if failure == "storage response" {
				svc, err = files.New(e.db, map[string]files.Backend{"local": &lostDeleteResponse{Backend: e.store, key: ready.Key}}, e.cfg)
				if err != nil {
					t.Fatal(err)
				}
			} else {
				// 首次提交 deleted 允许成功；实际对象删完后的数据库更新失败。
				if _, err := e.db.ExecContext(ctx, `CREATE FUNCTION reject_delete_completion() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF OLD.status = 'deleted' AND NEW.expires_at IS NULL THEN
        RAISE EXCEPTION 'simulated database update failure';
    END IF;
    RETURN NEW;
END $$`); err != nil {
					t.Fatal(err)
				}
				if _, err := e.db.ExecContext(ctx, `CREATE TRIGGER reject_delete_completion BEFORE UPDATE ON files
FOR EACH ROW EXECUTE FUNCTION reject_delete_completion()`); err != nil {
					t.Fatal(err)
				}
			}
			if err := svc.Delete(ctx, fileOwner, r.ID); err == nil {
				t.Fatal("expected injected deletion failure")
			}
			if _, err := e.store.Stat(ctx, ready.Key); !errors.Is(err, fs.ErrNotExist) {
				t.Fatalf("failure injection did not remove actual object: %v", err)
			}
			if _, err := svc.Get(ctx, fileOwner, r.ID); !errors.Is(err, files.ErrNotFound) {
				t.Fatalf("failed deletion left a visible file without content: %v", err)
			}
			var retry files.Record
			if err := e.db.NewSelect().Model(&retry).Where("id = ?", r.ID).Scan(ctx); err != nil {
				t.Fatal(err)
			}
			if retry.Status != files.StatusDeleted || retry.ExpiresAt == nil {
				t.Fatalf("deletion lost durable retry state: %+v", retry)
			}
			if failure == "database update" {
				if _, err := e.db.ExecContext(ctx, "DROP TRIGGER reject_delete_completion ON files"); err != nil {
					t.Fatal(err)
				}
			}
			if n, err := svc.Cleanup(ctx, 10); err != nil || n != 1 {
				t.Fatalf("retry cleanup n=%d err=%v", n, err)
			}
			if err := svc.Delete(ctx, fileOwner, r.ID); err != nil {
				t.Fatalf("repeat delete: %v", err)
			}
		})
	}
}

type failingDeleteKey struct {
	files.Backend
	key string
}

func (b *failingDeleteKey) Delete(ctx context.Context, key string) error {
	if key == b.key {
		return errLostStorageResponse
	}
	return b.Backend.Delete(ctx, key)
}

func TestFileCleanupFailureDoesNotStarveOtherRecords(t *testing.T) {
	for _, failure := range []string{"storage", "database"} {
		t.Run(failure, func(t *testing.T) {
			e := newFileEnvironment(t)
			ctx := context.Background()
			first := prepareFile(t, e.svc, 0)
			second := prepareFile(t, e.svc, 0)
			expireFile(t, e.db, first.ID)
			expireFile(t, e.db, second.ID)
			// 固定初始尝试顺序，避免依赖测试运行速度或时钟精度。
			for i, id := range []uuid.UUID{first.ID, second.ID} {
				if _, err := e.db.NewUpdate().Model((*files.Record)(nil)).Set("updated_at = ?", time.Now().Add(time.Duration(i-2)*time.Hour)).Where("id = ?", id).Exec(ctx); err != nil {
					t.Fatal(err)
				}
			}
			svc := e.svc
			if failure == "storage" {
				var err error
				svc, err = files.New(e.db, map[string]files.Backend{"local": &failingDeleteKey{Backend: e.store, key: first.Key}}, e.cfg)
				if err != nil {
					t.Fatal(err)
				}
			} else {
				if _, err := e.db.ExecContext(ctx, fmt.Sprintf(`CREATE FUNCTION reject_one_cleanup() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.id = '%s'::uuid AND NEW.status = 'deleted' THEN
        RAISE EXCEPTION 'simulated cleanup state update failure';
    END IF;
    RETURN NEW;
END $$`, first.ID)); err != nil {
					t.Fatal(err)
				}
				if _, err := e.db.ExecContext(ctx, `CREATE TRIGGER reject_one_cleanup BEFORE UPDATE ON files
FOR EACH ROW EXECUTE FUNCTION reject_one_cleanup()`); err != nil {
					t.Fatal(err)
				}
			}
			if n, err := svc.Cleanup(ctx, 1); err == nil || n != 0 {
				t.Fatalf("expected first record to fail: n=%d err=%v", n, err)
			}
			if n, err := svc.Cleanup(ctx, 1); err != nil || n != 1 {
				t.Fatalf("failed record starved the next one: n=%d err=%v", n, err)
			}
			var row files.Record
			if err := e.db.NewSelect().Model(&row).Where("id = ?", second.ID).Scan(ctx); err != nil {
				t.Fatal(err)
			}
			if row.Status != files.StatusDeleted || row.ExpiresAt != nil {
				t.Fatalf("second record was not cleaned: %+v", row)
			}
		})
	}
}

func TestFileMetadataAndDeletionIntentDoNotRequireStoredDisk(t *testing.T) {
	e := newFileEnvironment(t)
	ctx := context.Background()
	r := prepareFile(t, e.svc, 1)
	cfg := e.cfg
	cfg.DefaultDisk = "replacement"
	svc, err := files.New(e.db, map[string]files.Backend{"replacement": e.store}, cfg)
	if err != nil {
		t.Fatal(err)
	}
	got, err := svc.Get(ctx, fileOwner, r.ID)
	if err != nil || got.ID != r.ID || got.Disk != "local" {
		t.Fatalf("metadata requires missing disk: %+v %v", got, err)
	}
	if err := svc.Delete(ctx, fileOwner, r.ID); err == nil {
		t.Fatal("expected missing disk to prevent physical cleanup")
	}
	var deleted files.Record
	if err := e.db.NewSelect().Model(&deleted).Where("id = ?", r.ID).Scan(ctx); err != nil {
		t.Fatal(err)
	}
	if deleted.Status != files.StatusDeleted || deleted.ExpiresAt == nil {
		t.Fatalf("missing disk prevented durable deletion intent: %+v", deleted)
	}
}
