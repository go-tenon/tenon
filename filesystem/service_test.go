package filesystem_test

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/google/uuid"
	"github.com/johannesboyne/gofakes3"
	"github.com/johannesboyne/gofakes3/backend/s3mem"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/database/dbtest"
	"github.com/go-tenon/tenon/filesystem"
	"github.com/go-tenon/tenon/filesystem/local"
	s3disk "github.com/go-tenon/tenon/filesystem/s3"
	"github.com/go-tenon/tenon/identity"
)

var (
	alice = identity.Subject{ID: "alice", Type: "user"}
	bob   = identity.Subject{ID: "bob", Type: "user"}
)

type env struct {
	db    *bun.DB
	svc   *filesystem.Service
	local *local.Disk
	s3    *s3disk.Disk
}

func newEnv(t *testing.T) env {
	t.Helper()
	db := dbtest.Open(t, filesystem.Migrations())
	localDisk, err := local.Open(filesystem.LocalConfig{Root: filepath.Join(t.TempDir(), "files"), PublicURL: "/storage"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = localDisk.Close() })

	server := httptest.NewServer(gofakes3.New(s3mem.New()).Server())
	t.Cleanup(server.Close)
	client := awss3.New(awss3.Options{
		BaseEndpoint: aws.String(server.URL), Region: "us-east-1", UsePathStyle: true,
		Credentials: credentials.NewStaticCredentialsProvider("k", "s", ""),
	})
	if _, err := client.CreateBucket(context.Background(), &awss3.CreateBucketInput{Bucket: aws.String("test")}); err != nil {
		t.Fatal(err)
	}
	remote, err := s3disk.New(client, "test", "https://cdn.example.com")
	if err != nil {
		t.Fatal(err)
	}
	var cfg filesystem.Config
	cfg.SetDefaults()
	svc, err := filesystem.New(db, cfg, map[string]filesystem.Disk{"local": localDisk, "s3": remote})
	if err != nil {
		t.Fatal(err)
	}
	return env{db: db, svc: svc, local: localDisk, s3: remote}
}

func (e env) expire(t *testing.T, id uuid.UUID) {
	t.Helper()
	if _, err := e.db.NewUpdate().Model((*filesystem.File)(nil)).Set("expires_at = ?", time.Now().Add(-time.Second)).
		Where("id = ?", id).Exec(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func (e env) rows(t *testing.T) int {
	t.Helper()
	n, err := e.db.NewSelect().Model((*filesystem.File)(nil)).Count(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return int(n)
}

func upload(t *testing.T, svc *filesystem.Service, owner identity.Subject, body string) filesystem.File {
	t.Helper()
	r, err := svc.Upload(context.Background(), owner, filesystem.FileInput{Filename: "Photo.JPG", ContentType: "image/jpeg", Size: int64(len(body))}, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestUploadConfirmGetOpen(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	e := newEnv(t)
	r := upload(t, e.svc, alice, "hello")
	if r.Status != filesystem.StatusTemporary || r.ExpiresAt == nil || !strings.HasSuffix(r.Key, ".jpg") {
		t.Fatalf("uploaded = %+v", r)
	}
	if r.URL != "/storage/"+r.Key {
		t.Fatalf("URL = %s", r.URL)
	}
	if _, err := e.svc.Confirm(ctx, bob, r.ID, filesystem.Accept{}); !errors.Is(err, filesystem.ErrNotFound) {
		t.Fatalf("other owner confirmed: %v", err)
	}
	confirmed, err := e.svc.Confirm(ctx, alice, r.ID, filesystem.Accept{})
	if err != nil || confirmed.Status != filesystem.StatusReady || confirmed.ExpiresAt != nil || confirmed.URL != r.URL {
		t.Fatalf("confirm = %+v %v", confirmed, err)
	}
	if again, err := e.svc.Confirm(ctx, alice, r.ID, filesystem.Accept{}); err != nil || again.Status != filesystem.StatusReady {
		t.Fatalf("repeat confirm = %+v %v", again, err)
	}
	got, err := e.svc.Get(ctx, r.ID)
	if err != nil || got.Status != filesystem.StatusReady || got.URL != r.URL {
		t.Fatalf("get = %+v %v", got, err)
	}
	body, _, err := e.svc.Open(ctx, r.ID)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := io.ReadAll(body)
	_ = body.Close()
	if string(data) != "hello" {
		t.Fatalf("open = %q", data)
	}
	if _, err := e.svc.Get(ctx, uuid.Must(uuid.NewV7())); !errors.Is(err, filesystem.ErrNotFound) {
		t.Fatalf("missing get = %v", err)
	}
}

func TestUploadValidation(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	e := newEnv(t)
	cases := map[string]struct {
		in   filesystem.FileInput
		body string
		want error
	}{
		"short":    {filesystem.FileInput{Filename: "a.txt", Size: 10}, "abc", filesystem.ErrInvalid},
		"long":     {filesystem.FileInput{Filename: "a.txt", Size: 2}, "abc", filesystem.ErrInvalid},
		"too big":  {filesystem.FileInput{Filename: "a.txt", Size: int64(2 * filesystem.GiB)}, "", filesystem.ErrTooLarge},
		"no name":  {filesystem.FileInput{Filename: " ", Size: 0}, "", filesystem.ErrInvalid},
		"bad disk": {filesystem.FileInput{Disk: "nope", Filename: "a", Size: 0}, "", filesystem.ErrInvalid},
	}
	for name, c := range cases {
		if _, err := e.svc.Upload(ctx, alice, c.in, strings.NewReader(c.body)); !errors.Is(err, c.want) {
			t.Errorf("%s: err = %v, want %v", name, err, c.want)
		}
	}
	if n := e.rows(t); n != 0 {
		t.Fatalf("failed uploads left %d rows", n)
	}
	empty := upload(t, e.svc, alice, "")
	if _, err := e.svc.Confirm(ctx, alice, empty.ID, filesystem.Accept{}); err != nil {
		t.Fatalf("empty file: %v", err)
	}
}

func TestConfirmRequiresUploadedObject(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	e := newEnv(t)
	r, err := e.svc.Prepare(ctx, alice, filesystem.FileInput{Filename: "a.txt", Size: 3})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Confirm(ctx, alice, r.ID, filesystem.Accept{}); !errors.Is(err, filesystem.ErrNotUploaded) {
		t.Fatalf("confirm without object = %v", err)
	}
	if err := e.local.Put(ctx, r.Key, "", strings.NewReader("ab"), 2); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Confirm(ctx, alice, r.ID, filesystem.Accept{}); !errors.Is(err, filesystem.ErrNotUploaded) {
		t.Fatalf("confirm with wrong size = %v", err)
	}
	if _, err := e.svc.SignPut(ctx, alice, r.ID); !errors.Is(err, filesystem.ErrUnsupported) {
		t.Fatalf("local SignPut = %v", err)
	}
	e.expire(t, r.ID)
	if _, err := e.svc.Confirm(ctx, alice, r.ID, filesystem.Accept{}); !errors.Is(err, filesystem.ErrNotFound) {
		t.Fatalf("expired confirm = %v", err)
	}
}

func TestConcurrentConfirm(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	e := newEnv(t)
	r := upload(t, e.svc, alice, "data")
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for range 8 {
		wg.Go(func() {
			got, err := e.svc.Confirm(ctx, alice, r.ID, filesystem.Accept{})
			if err == nil && got.Status != filesystem.StatusReady {
				err = errors.New("not ready")
			}
			errs <- err
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestDeleteAndCleanup(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	e := newEnv(t)

	ready := upload(t, e.svc, alice, "keep")
	if _, err := e.svc.Confirm(ctx, alice, ready.ID, filesystem.Accept{}); err != nil {
		t.Fatal(err)
	}
	stale := upload(t, e.svc, alice, "stale")
	e.expire(t, stale.ID)
	fresh := upload(t, e.svc, alice, "fresh")

	n, err := e.svc.Cleanup(ctx, 10)
	if err != nil || n != 1 {
		t.Fatalf("cleanup = %d %v", n, err)
	}
	if _, err := e.local.Stat(ctx, stale.Key); err == nil {
		t.Fatal("stale object kept")
	}
	if _, err := e.svc.Get(ctx, stale.ID); !errors.Is(err, filesystem.ErrNotFound) {
		t.Fatalf("stale record = %v", err)
	}
	for _, r := range []filesystem.File{ready, fresh} {
		if _, err := e.svc.Get(ctx, r.ID); err != nil {
			t.Fatalf("%s removed: %v", r.Status, err)
		}
	}

	if err := e.svc.Delete(ctx, ready.ID); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.Delete(ctx, ready.ID); err != nil {
		t.Fatalf("repeat delete = %v", err)
	}
	if err := e.svc.Delete(ctx, uuid.Must(uuid.NewV7())); err != nil {
		t.Fatalf("delete missing = %v", err)
	}
	if _, err := e.svc.Get(ctx, ready.ID); !errors.Is(err, filesystem.ErrNotFound) {
		t.Fatalf("deleted file visible: %v", err)
	}
	if _, err := e.local.Stat(ctx, ready.Key); err != nil {
		t.Fatal("object should wait for cleanup")
	}
	if n, err := e.svc.Cleanup(ctx, 10); err != nil || n != 1 {
		t.Fatalf("cleanup after delete = %d %v", n, err)
	}
	if _, err := e.local.Stat(ctx, ready.Key); err == nil {
		t.Fatal("deleted object kept after cleanup")
	}
	if n := e.rows(t); n != 1 {
		t.Fatalf("rows = %d, want only fresh", n)
	}
}

func TestDeleteInTransaction(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	e := newEnv(t)
	kept := upload(t, e.svc, alice, "kept")
	gone := upload(t, e.svc, alice, "gone")

	rollback := errors.New("rollback")
	err := e.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if err := e.svc.WithDB(tx).Delete(ctx, kept.ID); err != nil {
			return err
		}
		return rollback
	})
	if !errors.Is(err, rollback) {
		t.Fatal(err)
	}
	if _, err := e.svc.Get(ctx, kept.ID); err != nil {
		t.Fatalf("rolled back delete removed file: %v", err)
	}
	if _, err := e.local.Stat(ctx, kept.Key); err != nil {
		t.Fatalf("rolled back delete removed object: %v", err)
	}

	if err := e.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		return e.svc.WithDB(tx).Delete(ctx, gone.ID)
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Get(ctx, gone.ID); !errors.Is(err, filesystem.ErrNotFound) {
		t.Fatalf("deleted file visible: %v", err)
	}
	if _, err := e.local.Stat(ctx, gone.Key); err != nil {
		t.Fatal("object should wait for cleanup after commit")
	}
	if n, err := e.svc.Cleanup(ctx, 10); err != nil || n != 1 {
		t.Fatalf("cleanup = %d %v", n, err)
	}
	if _, err := e.local.Stat(ctx, gone.Key); err == nil {
		t.Fatal("object kept after cleanup")
	}
}

func sendSigned(t *testing.T, req filesystem.SignedRequest, body []byte) string {
	t.Helper()
	status, etag, msg := trySigned(t, req, body)
	if status != http.StatusOK {
		t.Fatalf("status %d: %s", status, msg)
	}
	return etag
}

// trySigned 发送签名请求，返回状态码、ETag 和响应体。
func trySigned(t *testing.T, req filesystem.SignedRequest, body []byte) (status int, etag, msg string) {
	t.Helper()
	r, err := http.NewRequest(req.Method, req.URL, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range req.Headers {
		r.Header[k] = v
	}
	resp, err := http.DefaultClient.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, resp.Header.Get("ETag"), string(data)
}

func TestS3DirectPut(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	e := newEnv(t)
	r, err := e.svc.Prepare(ctx, alice, filesystem.FileInput{Disk: "s3", Filename: "a.txt", ContentType: "text/plain", Size: 5})
	if err != nil {
		t.Fatal(err)
	}
	if r.URL != "https://cdn.example.com/"+r.Key {
		t.Fatalf("URL = %s", r.URL)
	}
	if _, err := e.svc.SignPut(ctx, bob, r.ID); !errors.Is(err, filesystem.ErrNotFound) {
		t.Fatalf("other owner signed: %v", err)
	}
	req, err := e.svc.SignPut(ctx, alice, r.ID)
	if err != nil {
		t.Fatal(err)
	}
	sendSigned(t, req, []byte("hello"))
	if _, err := e.svc.Confirm(ctx, alice, r.ID, filesystem.Accept{}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.SignPut(ctx, alice, r.ID); !errors.Is(err, filesystem.ErrInvalid) {
		t.Fatalf("signing confirmed file = %v", err)
	}
	// 确认之后，还在有效期内的上传地址不能替换已经校验过的内容。
	if status, _, msg := trySigned(t, req, []byte("HACKD")); status != http.StatusPreconditionFailed {
		t.Fatalf("overwrite with the old upload URL: %d %s", status, msg)
	}
	body, _, err := e.svc.Open(ctx, r.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer body.Close()
	if got, _ := io.ReadAll(body); string(got) != "hello" {
		t.Fatalf("content replaced: %q", got)
	}
}

func TestS3Multipart(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	e := newEnv(t)
	data := bytes.Repeat([]byte("x"), 16<<20+100)
	r, err := e.svc.Prepare(ctx, alice, filesystem.FileInput{Disk: "s3", Filename: "big.bin", Size: int64(len(data))})
	if err != nil {
		t.Fatal(err)
	}
	up, err := e.svc.CreateMultipart(ctx, alice, r.ID)
	if err != nil || up.PartSize != 16<<20 || up.PartCount != 2 {
		t.Fatalf("multipart = %+v %v", up, err)
	}
	if _, err := e.svc.SignPart(ctx, alice, r.ID, up.UploadID, 3); !errors.Is(err, filesystem.ErrInvalid) {
		t.Fatalf("part 3 = %v", err)
	}
	var parts []filesystem.Part
	for n := 1; n <= up.PartCount; n++ {
		req, err := e.svc.SignPart(ctx, alice, r.ID, up.UploadID, int32(n))
		if err != nil {
			t.Fatal(err)
		}
		chunk := data[int64(n-1)*up.PartSize : min(int64(n)*up.PartSize, int64(len(data)))]
		parts = append(parts, filesystem.Part{Number: int32(n), ETag: sendSigned(t, req, chunk)})
	}
	if _, err := e.svc.CompleteMultipart(ctx, alice, r.ID, up.UploadID, parts[:1]); !errors.Is(err, filesystem.ErrInvalid) {
		t.Fatalf("missing part = %v", err)
	}
	if _, err := e.svc.Confirm(ctx, alice, r.ID, filesystem.Accept{}); !errors.Is(err, filesystem.ErrNotUploaded) {
		t.Fatalf("confirm before complete = %v", err)
	}
	for range 2 { // 第二次模拟响应丢失后的重试
		if _, err := e.svc.CompleteMultipart(ctx, alice, r.ID, up.UploadID, []filesystem.Part{parts[1], parts[0]}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := e.svc.Confirm(ctx, alice, r.ID, filesystem.Accept{}); err != nil {
		t.Fatal(err)
	}
	info, err := e.s3.Stat(ctx, r.Key)
	if err != nil || info.Size != int64(len(data)) {
		t.Fatalf("stat = %+v %v", info, err)
	}
}

func TestS3CleanupAbortsMultipart(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	e := newEnv(t)
	r, err := e.svc.Prepare(ctx, alice, filesystem.FileInput{Disk: "s3", Filename: "big.bin", Size: 20 << 20})
	if err != nil {
		t.Fatal(err)
	}
	up, err := e.svc.CreateMultipart(ctx, alice, r.ID)
	if err != nil {
		t.Fatal(err)
	}
	e.expire(t, r.ID)
	if n, err := e.svc.Cleanup(ctx, 10); err != nil || n != 1 {
		t.Fatalf("cleanup = %d %v", n, err)
	}
	if err := e.s3.CompleteMultipart(ctx, r.Key, up.UploadID, []filesystem.Part{{Number: 1, ETag: "x"}}); err == nil {
		t.Fatal("session survived cleanup")
	}
	if n := e.rows(t); n != 0 {
		t.Fatalf("rows = %d", n)
	}
}

// Accept 按文件内容识别真实类型：它必须被允许，并与声明的类型和扩展名一致；大小不超过 MaxSize。
// 不符合时文件保持 temporary；已确认的文件再次确认时同样检查。
func TestConfirmAccept(t *testing.T) {
	t.Parallel()
	const png = "\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR"
	const html = "<!DOCTYPE html><script>alert(1)</script>"
	images := filesystem.Accept{Types: []string{"image/*"}, MaxSize: 1 << 20}
	ctx := context.Background()
	e := newEnv(t)
	put := func(filename, contentType, body string) filesystem.File {
		t.Helper()
		r, err := e.svc.Upload(ctx, alice, filesystem.FileInput{Filename: filename, ContentType: contentType, Size: int64(len(body))}, strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	for name, tc := range map[string]struct {
		file   filesystem.File
		accept filesystem.Accept
		ok     bool
	}{
		"png":                   {put("cover.png", "image/png", png), images, true},
		"exact type":            {put("cover.png", "image/png", png), filesystem.Accept{Types: []string{"IMAGE/PNG"}}, true},
		"no extension":          {put("cover", "image/png", png), images, true},
		"zero accept":           {put("page.html", "text/html", html), filesystem.Accept{}, true},
		"html disguised as png": {put("cover.png", "image/png", html), images, false},
		"png named html":        {put("cover.html", "image/png", png), images, false},
		"declared jpeg":         {put("cover.png", "image/jpeg", png), images, false},
		"type not allowed":      {put("cover.png", "image/png", png), filesystem.Accept{Types: []string{"application/pdf"}}, false},
		"too large":             {put("cover.png", "image/png", png), filesystem.Accept{MaxSize: 4}, false},
	} {
		got, err := e.svc.Confirm(ctx, alice, tc.file.ID, tc.accept)
		if tc.ok != (err == nil) || (!tc.ok && !errors.Is(err, filesystem.ErrNotAccepted)) {
			t.Errorf("%s: err=%v", name, err)
			continue
		}
		want := filesystem.StatusTemporary
		if tc.ok {
			want = filesystem.StatusReady
		}
		if r, _ := e.svc.Get(ctx, tc.file.ID); r.Status != want || (tc.ok && got.Status != want) {
			t.Errorf("%s: status=%s want %s", name, r.Status, want)
		}
	}

	ready := put("page.html", "text/html", html)
	if _, err := e.svc.Confirm(ctx, alice, ready.ID, filesystem.Accept{}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Confirm(ctx, alice, ready.ID, images); !errors.Is(err, filesystem.ErrNotAccepted) {
		t.Fatalf("reconfirm ready file with images: %v", err)
	}
}

// zipBytes 构造只包含给定文件名和内容的内存 ZIP 压缩包，用于测试 OOXML 与 ZIP 识别。
func zipBytes(t *testing.T, entries map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, content := range entries {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// TestConfirmAcceptDocuments 验证办公文档（OOXML/OLE2）、纯文本和 JSON 的真实内容识别与伪装拦截，
// 以及客户端只给兜底类型 application/octet-stream 时确认后写回规范 MIME 类型。
func TestConfirmAcceptDocuments(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	e := newEnv(t)
	putBytes := func(filename, contentType string, body []byte) filesystem.File {
		t.Helper()
		r, err := e.svc.Upload(ctx, alice, filesystem.FileInput{
			Filename: filename, ContentType: contentType, Size: int64(len(body)),
		}, bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		return r
	}

	docx := zipBytes(t, map[string]string{"[Content_Types].xml": "<Types/>", "word/document.xml": "<w:document/>"})
	xlsx := zipBytes(t, map[string]string{"[Content_Types].xml": "<Types/>", "xl/workbook.xml": "<workbook/>"})
	pptx := zipBytes(t, map[string]string{"[Content_Types].xml": "<Types/>", "ppt/presentation.xml": "<presentation/>"})
	plainZip := zipBytes(t, map[string]string{"readme.txt": "hello"})
	ole2 := append([]byte{0xD0, 0xCF, 0x11, 0xE0, 0xA1, 0xB1, 0x1A, 0xE1}, make([]byte, 64)...)

	docs := filesystem.Accept{
		Types: []string{
			"application/pdf",
			"application/zip",
			"application/vnd.openxmlformats-officedocument.wordprocessingml.document",
			"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
			"application/vnd.openxmlformats-officedocument.presentationml.presentation",
			"application/msword",
			"application/vnd.ms-excel",
			"application/vnd.ms-powerpoint",
			"text/plain",
			"text/csv",
			"text/markdown",
			"application/json",
		},
	}

	for name, tc := range map[string]struct {
		file     filesystem.File
		ok       bool
		wantMIME string
	}{
		"docx": {
			file:     putBytes("spec.docx", "application/vnd.openxmlformats-officedocument.wordprocessingml.document", docx),
			ok:       true,
			wantMIME: "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
		},
		"docx with octet-stream": {
			file:     putBytes("spec.docx", "application/octet-stream", docx),
			ok:       true,
			wantMIME: "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
		},
		"xlsx": {
			file:     putBytes("sheet.xlsx", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", xlsx),
			ok:       true,
			wantMIME: "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
		},
		"pptx": {
			file:     putBytes("deck.pptx", "application/vnd.openxmlformats-officedocument.presentationml.presentation", pptx),
			ok:       true,
			wantMIME: "application/vnd.openxmlformats-officedocument.presentationml.presentation",
		},
		"zip disguised as docx": {
			file: putBytes("fake.docx", "application/vnd.openxmlformats-officedocument.wordprocessingml.document", plainZip),
			ok:   false,
		},
		"xlsx disguised as docx": {
			file: putBytes("wrong.docx", "application/vnd.openxmlformats-officedocument.wordprocessingml.document", xlsx),
			ok:   false,
		},
		"doc ole2": {
			file:     putBytes("legacy.doc", "application/msword", ole2),
			ok:       true,
			wantMIME: "application/msword",
		},
		"fake doc": {
			file: putBytes("fake.doc", "application/msword", []byte("not ole2")),
			ok:   false,
		},
		"csv windows excel mime": {
			file:     putBytes("orders.csv", "application/vnd.ms-excel", []byte("id,amount\n1,100\n")),
			ok:       true,
			wantMIME: "application/vnd.ms-excel",
		},
		"markdown octet-stream": {
			file:     putBytes("notes.md", "application/octet-stream", []byte("# 标题\n内容\n")),
			ok:       true,
			wantMIME: "text/markdown",
		},
		"log empty mime": {
			file:     putBytes("app.log", "", []byte("2026-11-22 INFO ok\n")),
			ok:       true,
			wantMIME: "text/plain",
		},
		"json valid": {
			file:     putBytes("data.json", "application/json", []byte(`{"ok":true}`)),
			ok:       true,
			wantMIME: "application/json",
		},
		"json invalid": {
			file: putBytes("bad.json", "application/json", []byte(`{not json`)),
			ok:   false,
		},
		"html disguised as txt": {
			file: putBytes("xss.txt", "text/plain", []byte("<html><script>alert(1)</script></html>")),
			ok:   false,
		},
		"svg disguised as md": {
			file: putBytes("xss.md", "text/markdown", []byte("<svg onload=alert(1)>")),
			ok:   false,
		},
		"binary disguised as txt": {
			file: putBytes("bin.txt", "text/plain", []byte{'a', 'b', 0x00, 'c'}),
			ok:   false,
		},
		"zip valid": {
			file:     putBytes("archive.zip", "application/zip", plainZip),
			ok:       true,
			wantMIME: "application/zip",
		},
	} {
		got, err := e.svc.Confirm(ctx, alice, tc.file.ID, docs)
		if tc.ok != (err == nil) || (!tc.ok && !errors.Is(err, filesystem.ErrNotAccepted)) {
			t.Errorf("%s: err=%v, want ok=%v", name, err, tc.ok)
			continue
		}
		if tc.ok && got.ContentType != tc.wantMIME {
			t.Errorf("%s: content_type=%q, want %q", name, got.ContentType, tc.wantMIME)
		}
	}
}

// TestGetMany 验证批量按 ID 查询未删除文件，跨磁盘（local 与 s3）正确生成公开地址，已删除与不存在的 ID 不在结果中。
func TestGetMany(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	e := newEnv(t)

	empty, err := e.svc.GetMany(ctx, nil)
	if err != nil || len(empty) != 0 {
		t.Fatalf("empty GetMany = %v %v", empty, err)
	}

	localFile := upload(t, e.svc, alice, "local-data")
	deletedFile := upload(t, e.svc, alice, "gone")
	if err := e.svc.Delete(ctx, deletedFile.ID); err != nil {
		t.Fatal(err)
	}
	s3File, err := e.svc.Upload(ctx, alice, filesystem.FileInput{
		Disk: "s3", Filename: "doc.pdf", ContentType: "application/pdf", Size: 4,
	}, strings.NewReader("%PDF"))
	if err != nil {
		t.Fatal(err)
	}
	missing := uuid.Must(uuid.NewV7())

	got, err := e.svc.GetMany(ctx, []uuid.UUID{localFile.ID, s3File.ID, deletedFile.ID, missing})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("len(got) = %d, want 2", len(got))
	}
	if got[localFile.ID].URL != "/storage/"+localFile.Key {
		t.Errorf("local URL = %q", got[localFile.ID].URL)
	}
	if got[s3File.ID].URL != "https://cdn.example.com/"+s3File.Key {
		t.Errorf("s3 URL = %q", got[s3File.ID].URL)
	}
}

// TestConfirmImageMeta 验证 Confirm 自动解析 PNG/JPEG/GIF/WebP 宽高并持久化到 files.meta，
// Get/GetMany 原样读出，MaxDimension 正确拦截超限尺寸和损坏图片。
func TestConfirmImageMeta(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	e := newEnv(t)

	encodePNG := func(w, h int) []byte {
		var buf bytes.Buffer
		img := image.NewRGBA(image.Rect(0, 0, w, h))
		img.Set(0, 0, color.RGBA{R: 10, G: 20, B: 30, A: 255})
		if err := png.Encode(&buf, img); err != nil {
			t.Fatal(err)
		}
		return buf.Bytes()
	}
	encodeJPEG := func(w, h int) []byte {
		var buf bytes.Buffer
		img := image.NewRGBA(image.Rect(0, 0, w, h))
		if err := jpeg.Encode(&buf, img, nil); err != nil {
			t.Fatal(err)
		}
		return buf.Bytes()
	}
	encodeGIF := func(w, h int) []byte {
		var buf bytes.Buffer
		img := image.NewPaletted(image.Rect(0, 0, w, h), color.Palette{color.Black, color.White})
		if err := gif.Encode(&buf, img, nil); err != nil {
			t.Fatal(err)
		}
		return buf.Bytes()
	}
	makeWebPVP8 := func(w, h int) []byte {
		b := make([]byte, 34)
		copy(b[0:4], "RIFF")
		binary.LittleEndian.PutUint32(b[4:8], uint32(len(b)-8))
		copy(b[8:12], "WEBP")
		copy(b[12:16], "VP8 ")
		binary.LittleEndian.PutUint32(b[16:20], 14)
		b[23], b[24], b[25] = 0x9d, 0x01, 0x2a
		binary.LittleEndian.PutUint16(b[26:28], uint16(w))
		binary.LittleEndian.PutUint16(b[28:30], uint16(h))
		return b
	}
	makeWebPVP8L := func(w, h int) []byte {
		b := make([]byte, 32)
		copy(b[0:4], "RIFF")
		binary.LittleEndian.PutUint32(b[4:8], uint32(len(b)-8))
		copy(b[8:12], "WEBP")
		copy(b[12:16], "VP8L")
		binary.LittleEndian.PutUint32(b[16:20], 12)
		b[20] = 0x2f
		binary.LittleEndian.PutUint32(b[21:25], uint32(w-1)|(uint32(h-1)<<14))
		return b
	}
	makeWebPVP8X := func(w, h int) []byte {
		b := make([]byte, 32)
		copy(b[0:4], "RIFF")
		binary.LittleEndian.PutUint32(b[4:8], uint32(len(b)-8))
		copy(b[8:12], "WEBP")
		copy(b[12:16], "VP8X")
		binary.LittleEndian.PutUint32(b[16:20], 10)
		wm, hm := uint32(w-1), uint32(h-1)
		b[24], b[25], b[26] = byte(wm), byte(wm>>8), byte(wm>>16)
		b[27], b[28], b[29] = byte(hm), byte(hm>>8), byte(hm>>16)
		return b
	}
	put := func(name, ct string, data []byte) filesystem.File {
		t.Helper()
		f, err := e.svc.Upload(ctx, alice, filesystem.FileInput{Filename: name, ContentType: ct, Size: int64(len(data))}, bytes.NewReader(data))
		if err != nil {
			t.Fatal(err)
		}
		return f
	}

	accept := filesystem.Accept{Types: []string{"image/*"}, MaxDimension: 500}
	cases := []struct {
		name  string
		ct    string
		data  []byte
		wantW int
		wantH int
	}{
		{"a.png", "image/png", encodePNG(120, 80), 120, 80},
		{"b.jpg", "image/jpeg", encodeJPEG(64, 48), 64, 48},
		{"c.gif", "image/gif", encodeGIF(32, 24), 32, 24},
		{"d.webp", "image/webp", makeWebPVP8(200, 150), 200, 150},
		{"e.webp", "image/webp", makeWebPVP8L(320, 240), 320, 240},
		{"f.webp", "image/webp", makeWebPVP8X(400, 300), 400, 300},
	}
	var ids []uuid.UUID
	for _, tc := range cases {
		f := put(tc.name, tc.ct, tc.data)
		confirmed, err := e.svc.Confirm(ctx, alice, f.ID, accept)
		if err != nil {
			t.Fatalf("%s: confirm err=%v", tc.name, err)
		}
		if confirmed.Meta.Width != tc.wantW || confirmed.Meta.Height != tc.wantH {
			t.Fatalf("%s: meta=%+v, want %dx%d", tc.name, confirmed.Meta, tc.wantW, tc.wantH)
		}
		got, err := e.svc.Get(ctx, f.ID)
		if err != nil || got.Meta != confirmed.Meta {
			t.Fatalf("%s: Get meta=%+v err=%v", tc.name, got.Meta, err)
		}
		ids = append(ids, f.ID)
	}
	many, err := e.svc.GetMany(ctx, ids)
	if err != nil || len(many) != len(ids) || many[ids[0]].Meta.Width != 120 || many[ids[4]].Meta.Height != 240 {
		t.Fatalf("GetMany meta = %+v %v", many, err)
	}

	// 超过 MaxDimension 的图片被拒绝，文件保持 temporary
	tooBig := put("big.png", "image/png", encodePNG(600, 100))
	if _, err := e.svc.Confirm(ctx, alice, tooBig.ID, accept); !errors.Is(err, filesystem.ErrNotAccepted) {
		t.Fatalf("too big image: err=%v, want ErrNotAccepted", err)
	}
	if rec, _ := e.svc.Get(ctx, tooBig.ID); rec.Status != filesystem.StatusTemporary {
		t.Fatalf("too big image status=%s, want temporary", rec.Status)
	}

	// 截断/损坏的图片在设置了 MaxDimension 时被拒绝
	broken := put("broken.png", "image/png", []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR"))
	if _, err := e.svc.Confirm(ctx, alice, broken.ID, accept); !errors.Is(err, filesystem.ErrNotAccepted) {
		t.Fatalf("broken png with MaxDimension: err=%v, want ErrNotAccepted", err)
	}

	// 非图片文件的 Meta 保持零值
	txt := put("note.txt", "text/plain", []byte("hello"))
	confirmedTxt, err := e.svc.Confirm(ctx, alice, txt.ID, filesystem.Accept{Types: []string{"text/plain"}, MaxDimension: 500})
	if err != nil || confirmedTxt.Meta != (filesystem.Meta{}) {
		t.Fatalf("text meta=%+v err=%v", confirmedTxt.Meta, err)
	}
}

// TestConfirmAcceptMedia 验证常见音视频容器的真实内容识别、多语义扩展名兼容与伪装拦截。
func TestConfirmAcceptMedia(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	e := newEnv(t)
	putBytes := func(filename, contentType string, body []byte) filesystem.File {
		t.Helper()
		r, err := e.svc.Upload(ctx, alice, filesystem.FileInput{
			Filename: filename, ContentType: contentType, Size: int64(len(body)),
		}, bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		return r
	}

	makeFtyp := func(major string, compat ...string) []byte {
		size := 16 + 4*len(compat)
		b := make([]byte, size+8)
		binary.BigEndian.PutUint32(b[0:4], uint32(size))
		copy(b[4:8], "ftyp")
		copy(b[8:12], major)
		for i, c := range compat {
			copy(b[16+4*i:20+4*i], c)
		}
		return b
	}
	mp4 := makeFtyp("isom", "iso2", "mp41")
	mov := makeFtyp("qt  ", "qt  ")
	m4a := makeFtyp("M4A ", "isom", "mp42")
	webmVideo := append([]byte{0x1A, 0x45, 0xDF, 0xA3, 0x01, 0x42, 0x82}, []byte("webm...V_VP9...A_OPUS")...)
	webmAudio := append([]byte{0x1A, 0x45, 0xDF, 0xA3, 0x01, 0x42, 0x82}, []byte("webm...A_OPUS")...)
	oggOpus := append(append([]byte("OggS"), make([]byte, 24)...), []byte("OpusHead\x01\x01")...)
	wav := append([]byte("RIFF\x24\x00\x00\x00WAVEfmt "), make([]byte, 20)...)
	mp3ID3 := append([]byte("ID3\x03\x00\x00\x00\x00\x00\x00"), make([]byte, 16)...)
	mp3Raw := append([]byte{0xFF, 0xFB, 0x90, 0x64}, make([]byte, 16)...)
	aac := append([]byte{0xFF, 0xF1, 0x50, 0x80, 0x02, 0x1F, 0xFC}, make([]byte, 16)...)
	flac := append([]byte("fLaC"), make([]byte, 24)...)

	media := filesystem.Accept{
		Types: []string{
			"video/mp4",
			"video/quicktime",
			"video/webm",
			"audio/ogg",
			"audio/mpeg",
			"audio/mp4",
			"audio/webm",
			"audio/wav",
			"audio/aac",
			"audio/flac",
		},
	}

	for name, tc := range map[string]struct {
		file     filesystem.File
		ok       bool
		wantMIME string
	}{
		"mp4": {
			file:     putBytes("clip.mp4", "video/mp4", mp4),
			ok:       true,
			wantMIME: "video/mp4",
		},
		"mp4 octet-stream": {
			file:     putBytes("clip.mp4", "application/octet-stream", mp4),
			ok:       true,
			wantMIME: "video/mp4",
		},
		"mov": {
			file:     putBytes("iphone.mov", "video/quicktime", mov),
			ok:       true,
			wantMIME: "video/quicktime",
		},
		"m4a": {
			file:     putBytes("memo.m4a", "audio/x-m4a", m4a),
			ok:       true,
			wantMIME: "audio/x-m4a",
		},
		"webm video": {
			file:     putBytes("screen.webm", "video/webm", webmVideo),
			ok:       true,
			wantMIME: "video/webm",
		},
		"webm audio": {
			file:     putBytes("voice.webm", "audio/webm", webmAudio),
			ok:       true,
			wantMIME: "audio/webm",
		},
		"ogg opus telegram voice": {
			file:     putBytes("voice.ogg", "audio/ogg", oggOpus),
			ok:       true,
			wantMIME: "audio/ogg",
		},
		"opus ext octet-stream": {
			file:     putBytes("voice.opus", "application/octet-stream", oggOpus),
			ok:       true,
			wantMIME: "audio/ogg",
		},
		"wav wave alias": {
			file:     putBytes("note.wav", "audio/wave", wav),
			ok:       true,
			wantMIME: "audio/wave",
		},
		"mp3 id3": {
			file:     putBytes("song.mp3", "audio/mpeg", mp3ID3),
			ok:       true,
			wantMIME: "audio/mpeg",
		},
		"mp3 raw frame": {
			file:     putBytes("raw.mp3", "audio/mp3", mp3Raw),
			ok:       true,
			wantMIME: "audio/mp3",
		},
		"aac adts": {
			file:     putBytes("clip.aac", "audio/aac", aac),
			ok:       true,
			wantMIME: "audio/aac",
		},
		"flac": {
			file:     putBytes("lossless.flac", "audio/flac", flac),
			ok:       true,
			wantMIME: "audio/flac",
		},
		"html disguised as mp4": {
			file: putBytes("fake.mp4", "video/mp4", []byte("<!DOCTYPE html><script>alert(1)</script>")),
			ok:   false,
		},
		"ogg named mp4": {
			file: putBytes("wrong.mp4", "audio/ogg", oggOpus),
			ok:   false,
		},
	} {
		got, err := e.svc.Confirm(ctx, alice, tc.file.ID, media)
		if tc.ok != (err == nil) || (!tc.ok && !errors.Is(err, filesystem.ErrNotAccepted)) {
			t.Errorf("%s: err=%v, want ok=%v", name, err, tc.ok)
			continue
		}
		if tc.ok && got.ContentType != tc.wantMIME {
			t.Errorf("%s: content_type=%q, want %q", name, got.ContentType, tc.wantMIME)
		}
	}
}

// TestUploadUnknownSize 验证流式上传未知长度正文（UnknownSize）：写入后回填真实字节数并可正常确认，
// 超出 max_upload_size 时返回 ErrTooLarge 且不留下孤儿记录或对象。
func TestUploadUnknownSize(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	e := newEnv(t)

	for _, disk := range []string{"local", "s3"} {
		body := "streamed-media-payload-" + disk
		r, err := e.svc.Upload(ctx, alice, filesystem.FileInput{
			Disk: disk, Filename: "clip.txt", ContentType: "text/plain", Size: filesystem.UnknownSize,
		}, strings.NewReader(body))
		if err != nil {
			t.Fatalf("%s unknown size upload: %v", disk, err)
		}
		if r.Size != int64(len(body)) {
			t.Fatalf("%s size=%d, want %d", disk, r.Size, len(body))
		}
		confirmed, err := e.svc.Confirm(ctx, alice, r.ID, filesystem.Accept{Types: []string{"text/plain"}})
		if err != nil || confirmed.Size != int64(len(body)) {
			t.Fatalf("%s confirm: %+v %v", disk, confirmed, err)
		}
	}

	// 限制 max_upload_size 为 10 字节，验证未知长度流超限时立即失败且不残留记录。
	cfg := filesystem.Config{DefaultDisk: "local", MaxUploadSize: 10, TempTTL: time.Hour}
	smallSvc, err := filesystem.New(e.db, cfg, map[string]filesystem.Disk{"local": e.local})
	if err != nil {
		t.Fatal(err)
	}
	before := e.rows(t)
	if _, err := smallSvc.Upload(ctx, alice, filesystem.FileInput{
		Filename: "big.txt", ContentType: "text/plain", Size: filesystem.UnknownSize,
	}, strings.NewReader("0123456789EXTRA")); !errors.Is(err, filesystem.ErrTooLarge) {
		t.Fatalf("over limit err=%v, want ErrTooLarge", err)
	}
	if after := e.rows(t); after != before {
		t.Fatalf("over limit left %d rows", after-before)
	}
}

// TestUploadAcceptImmediateValidation 验证 Upload 指定 Accept 时在写入后立即校验类型与体积，不合格立即删除对象与记录。
func TestUploadAcceptImmediateValidation(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	e := newEnv(t)
	accept := filesystem.Accept{Types: []string{"image/png"}, MaxSize: 512, MaxDimension: 128}
	before := e.rows(t)

	// HTML 伪装成 PNG 在 Upload 阶段即被拦截并清理。
	htmlPayload := "<!doctype html>alert"
	if _, err := e.svc.Upload(ctx, alice, filesystem.FileInput{
		Filename: "evil.png", ContentType: "image/png", Size: int64(len(htmlPayload)), Accept: &accept,
	}, strings.NewReader(htmlPayload)); !errors.Is(err, filesystem.ErrNotAccepted) {
		t.Fatalf("disguised html err=%v, want ErrNotAccepted", err)
	}
	if after := e.rows(t); after != before {
		t.Fatalf("disguised html left %d rows", after-before)
	}

	// UnknownSize 超出 Accept.MaxSize 立即清理并返回 ErrNotAccepted。
	if _, err := e.svc.Upload(ctx, alice, filesystem.FileInput{
		Filename: "big.png", ContentType: "image/png", Size: filesystem.UnknownSize, Accept: &accept,
	}, bytes.NewReader(bytes.Repeat([]byte{'a'}, 600))); !errors.Is(err, filesystem.ErrNotAccepted) {
		t.Fatalf("oversized stream err=%v, want ErrNotAccepted", err)
	}
	if after := e.rows(t); after != before {
		t.Fatalf("oversized stream left %d rows", after-before)
	}

	// 合法 PNG 通过校验并预先写入图片尺寸，且状态仍为 temporary 等待业务 Confirm。
	var pngBuf bytes.Buffer
	img := image.NewRGBA(image.Rect(0, 0, 16, 12))
	if err := png.Encode(&pngBuf, img); err != nil {
		t.Fatal(err)
	}
	f, err := e.svc.Upload(ctx, alice, filesystem.FileInput{
		Filename: "ok.png", ContentType: "application/octet-stream", Size: int64(pngBuf.Len()), Accept: &accept,
	}, bytes.NewReader(pngBuf.Bytes()))
	if err != nil {
		t.Fatalf("valid upload err=%v", err)
	}
	if f.Status != filesystem.StatusTemporary || f.ContentType != "image/png" || f.Meta.Width != 16 || f.Meta.Height != 12 {
		t.Fatalf("uploaded file = %+v", f)
	}
}

// TestConfirmAMRAndServe 验证 AMR 音频探测与 filesystem.Serve 的安全响应头、附件下载、Range 206 及 S3 签名跳转。
func TestConfirmAMRAndServe(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	e := newEnv(t)

	amrBody := append([]byte("#!AMR\n"), make([]byte, 20)...)
	amrFile, err := e.svc.Upload(ctx, alice, filesystem.FileInput{
		Filename: "voice.amr", ContentType: "application/octet-stream", Size: int64(len(amrBody)),
	}, bytes.NewReader(amrBody))
	if err != nil {
		t.Fatal(err)
	}
	confirmed, err := e.svc.Confirm(ctx, alice, amrFile.ID, filesystem.Accept{Types: []string{"audio/amr"}})
	if err != nil || confirmed.ContentType != "audio/amr" {
		t.Fatalf("confirm amr = %+v %v", confirmed, err)
	}

	// 本地磁盘支持 HTTP Range 206 与安全头。
	req := httptest.NewRequest(http.MethodGet, "/download", nil)
	req.Header.Set("Range", "bytes=0-4")
	rec := httptest.NewRecorder()
	if err := filesystem.Serve(rec, req, e.svc, amrFile.ID, filesystem.ServeAttachment()); err != nil {
		t.Fatal(err)
	}
	if rec.Code != http.StatusPartialContent || rec.Body.String() != "#!AMR" ||
		rec.Header().Get("X-Content-Type-Options") != "nosniff" ||
		!strings.Contains(rec.Header().Get("Content-Disposition"), "attachment") {
		t.Fatalf("local range serve: code=%d body=%q headers=%v", rec.Code, rec.Body.String(), rec.Header())
	}

	// 活跃脚本类型（HTML）强制加上 sandbox 与 attachment。
	htmlFile, err := e.svc.Upload(ctx, alice, filesystem.FileInput{
		Filename: "page.html", ContentType: "text/html", Size: 13,
	}, strings.NewReader("<h1>evil</h1>"))
	if err != nil {
		t.Fatal(err)
	}
	recHTML := httptest.NewRecorder()
	if err := filesystem.Serve(recHTML, httptest.NewRequest(http.MethodGet, "/html", nil), e.svc, htmlFile.ID); err != nil {
		t.Fatal(err)
	}
	if recHTML.Header().Get("Content-Security-Policy") != "sandbox" || !strings.Contains(recHTML.Header().Get("Content-Disposition"), "attachment") {
		t.Fatalf("active type headers = %v", recHTML.Header())
	}

	// S3 磁盘启用 ServeRedirectSigned 时返回 302 与 nosniff。
	s3File, err := e.svc.Upload(ctx, alice, filesystem.FileInput{
		Disk: "s3", Filename: "doc.txt", ContentType: "text/plain", Size: 5,
	}, strings.NewReader("hello"))
	if err != nil {
		t.Fatal(err)
	}
	recS3 := httptest.NewRecorder()
	if err := filesystem.Serve(recS3, httptest.NewRequest(http.MethodGet, "/s3", nil), e.svc, s3File.ID, filesystem.ServeRedirectSigned(5*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if recS3.Code != http.StatusFound || recS3.Header().Get("Location") == "" || recS3.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("s3 redirect = %d %v", recS3.Code, recS3.Header())
	}
}
