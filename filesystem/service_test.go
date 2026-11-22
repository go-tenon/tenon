package filesystem_test

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
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
