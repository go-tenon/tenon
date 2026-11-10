//go:build integration

package files_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/johannesboyne/gofakes3"
	"github.com/johannesboyne/gofakes3/backend/s3mem"
	"github.com/uptrace/bun"

	"github.com/ryan053857/mygoravel/database/dbtest"
	"github.com/ryan053857/mygoravel/files"
	stores3 "github.com/ryan053857/mygoravel/files/s3"
)

type s3FileEnvironment struct {
	db     *bun.DB
	store  *stores3.Store
	svc    *files.Service
	cfg    files.Config
	client *http.Client
}

// newS3FileEnvironment 使用真实 PostgreSQL、AWS SDK 和 gofakes3 HTTP 服务。
// gofakes3 用于验证 S3 行为与请求兼容性，不提供 AWS IAM/签名鉴权的安全验证。
func newS3FileEnvironment(t *testing.T) s3FileEnvironment {
	t.Helper()
	db := dbtest.Open(t, files.Migrations())
	server := httptest.NewServer(gofakes3.New(s3mem.New()).Server())
	t.Cleanup(server.Close)
	client := awss3.New(awss3.Options{
		BaseEndpoint: aws.String(server.URL), Region: "us-east-1", UsePathStyle: true,
		Credentials: credentials.NewStaticCredentialsProvider("gofakes3", "gofakes3", ""),
		HTTPClient:  server.Client(),
	})
	const bucket = "files-integration"
	if _, err := client.CreateBucket(context.Background(), &awss3.CreateBucketInput{Bucket: aws.String(bucket)}); err != nil {
		t.Fatal(err)
	}
	store, err := stores3.Open(context.Background(), stores3.Config{
		Bucket: bucket, Region: "us-east-1", Endpoint: server.URL, PathStyle: true,
		AccessKey: "gofakes3", SecretKey: "gofakes3",
	})
	if err != nil {
		t.Fatal(err)
	}
	cfg := files.DefaultConfig()
	cfg.DefaultDisk, cfg.PartSize = "private", files.MinPartSize
	svc, err := files.New(db, map[string]files.Backend{"private": store}, cfg)
	if err != nil {
		t.Fatal(err)
	}
	httpClient := server.Client()
	httpClient.Timeout = 10 * time.Second
	return s3FileEnvironment{db: db, store: store, svc: svc, cfg: cfg, client: httpClient}
}

// requestSigned 实际执行签名中的方法、地址和请求头，并返回正文与响应 ETag。
func requestSigned(t *testing.T, client *http.Client, signed files.SignedRequest, data []byte) ([]byte, string) {
	t.Helper()
	var body io.Reader
	if data != nil {
		body = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(context.Background(), signed.Method, signed.URL, body)
	if err != nil {
		t.Fatal(err)
	}
	req.Header = signed.Headers.Clone()
	if host := req.Header.Get("Host"); host != "" {
		req.Host = host
		req.Header.Del("Host")
	}
	if req.URL.Query().Get("X-Amz-Signature") == "" {
		t.Fatal("request is missing its signature")
	}
	res, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	response, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		t.Fatalf("signed %s returned %d: %s", signed.Method, res.StatusCode, response)
	}
	return response, res.Header.Get("ETag")
}

func TestS3FileProxyLifecycle(t *testing.T) {
	e := newS3FileEnvironment(t)
	ctx := context.Background()
	for _, content := range []string{"", "proxied upload through the real SDK"} {
		r := prepareFile(t, e.svc, int64(len(content)))
		if r.Disk != "private" {
			t.Fatalf("wrong persisted disk: %q", r.Disk)
		}
		if _, err := e.svc.Put(ctx, fileOwner, r.ID, struct{ io.Reader }{strings.NewReader(content)}); err != nil {
			t.Fatal(err)
		}
		confirmed, err := e.svc.Confirm(ctx, fileOwner, r.ID)
		if err != nil || confirmed.Status != files.StatusReady {
			t.Fatalf("confirm=%+v err=%v", confirmed, err)
		}
		if got := string(readReadyFile(t, e.svc, r.ID)); got != content {
			t.Fatalf("proxy content=%q want=%q", got, content)
		}
		info, err := e.store.Stat(ctx, confirmed.Key)
		if err != nil || info.Size != int64(len(content)) || info.ContentType != "text/plain" {
			t.Fatalf("copied object=%+v err=%v", info, err)
		}
		for range 2 {
			if err := e.svc.Delete(ctx, fileOwner, r.ID); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := e.store.Stat(ctx, confirmed.Key); !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("deleted object still exists: %v", err)
		}
		if _, err := e.svc.Get(ctx, fileOwner, r.ID); !errors.Is(err, files.ErrNotFound) {
			t.Fatalf("deleted file still accessible: %v", err)
		}
	}
}

func TestS3FileSignedUploadKeepsConfirmedContentIndependent(t *testing.T) {
	e := newS3FileEnvironment(t)
	ctx := context.Background()
	r := prepareFile(t, e.svc, 4)
	signed, err := e.svc.SignPut(ctx, fileOwner, r.ID)
	if err != nil {
		t.Fatal(err)
	}
	frozen, err := e.svc.Get(ctx, fileOwner, r.ID)
	if err != nil || frozen.UploadMode != files.UploadModeSingle {
		t.Fatalf("signature did not freeze single-upload mode: %+v %v", frozen, err)
	}
	if _, err := e.svc.CreateMultipart(ctx, fileOwner, r.ID); !errors.Is(err, files.ErrConflict) {
		t.Fatalf("unused single-upload URL allowed switching to multipart: %v", err)
	}
	if _, err := e.svc.SignPut(ctx, fileOwner, r.ID); err != nil {
		t.Fatal(err)
	}
	again, err := e.svc.Get(ctx, fileOwner, r.ID)
	if err != nil || !again.UpdatedAt.Equal(frozen.UpdatedAt) {
		t.Fatalf("signing again changed stable file state: before=%s after=%s err=%v", frozen.UpdatedAt, again.UpdatedAt, err)
	}
	if signed.Method != http.MethodPut || signed.Headers.Get("Content-Length") != "4" || signed.Headers.Get("Content-Type") != "text/plain" {
		t.Fatalf("unexpected signed upload method or headers: %s %v", signed.Method, signed.Headers)
	}
	if _, etag := requestSigned(t, e.client, signed, []byte("safe")); etag == "" {
		t.Fatal("S3 upload did not return an ETag")
	}
	completed, err := e.svc.Complete(ctx, fileOwner, r.ID)
	if err != nil || completed.UploadedAt == nil || completed.Status != files.StatusTemporary {
		t.Fatalf("complete=%+v err=%v", completed, err)
	}
	confirmed, err := e.svc.Confirm(ctx, fileOwner, r.ID)
	if err != nil || confirmed.Key == r.Key {
		t.Fatalf("confirm=%+v err=%v", confirmed, err)
	}
	// 同一旧 URL 仍能改写上传键，业务确认必须使用独立副本。
	requestSigned(t, e.client, signed, []byte("evil"))
	source, err := e.store.Open(ctx, r.Key)
	if err != nil {
		t.Fatal(err)
	}
	changed, err := io.ReadAll(source)
	source.Close()
	if err != nil || string(changed) != "evil" {
		t.Fatalf("old URL did not exercise source overwrite: %q %v", changed, err)
	}
	if got := string(readReadyFile(t, e.svc, r.ID)); got != "safe" {
		t.Fatalf("old URL changed confirmed content: %q", got)
	}
	download, err := e.svc.SignGet(ctx, fileOwner, r.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := requestSigned(t, e.client, download, nil); string(got) != "safe" {
		t.Fatalf("signed download=%q", got)
	}
	expireFile(t, e.db, r.ID)
	if n, err := e.svc.Cleanup(ctx, 10); err != nil || n != 1 {
		t.Fatalf("cleanup n=%d err=%v", n, err)
	}
	if _, err := e.store.Stat(ctx, r.Key); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("upload source survived cleanup: %v", err)
	}
	if got := string(readReadyFile(t, e.svc, r.ID)); got != "safe" {
		t.Fatalf("cleanup removed confirmed content: %q", got)
	}
}

func TestS3FileMultipartResumeWithProxyAndSignedParts(t *testing.T) {
	for _, mode := range []string{"proxy", "signed"} {
		t.Run(mode, func(t *testing.T) {
			e := newS3FileEnvironment(t)
			ctx := context.Background()
			first := bytes.Repeat([]byte("a"), int(e.cfg.PartSize))
			tail := []byte("small final part")
			r := prepareFile(t, e.svc, int64(len(first)+len(tail)))
			created, err := e.svc.CreateMultipart(ctx, fileOwner, r.ID)
			if err != nil {
				t.Fatal(err)
			}
			// 模拟客户端保存 Number/ETag，不要求客户端保存或回传 Size。
			upload := func(svc *files.Service, number int32, data []byte) files.Part {
				t.Helper()
				if mode == "proxy" {
					p, err := svc.UploadPart(ctx, fileOwner, r.ID, created.UploadID, number, bytes.NewReader(data))
					if err != nil {
						t.Fatal(err)
					}
					return files.Part{Number: p.Number, ETag: p.ETag}
				}
				signed, err := svc.SignPart(ctx, fileOwner, r.ID, created.UploadID, number)
				if err != nil {
					t.Fatal(err)
				}
				_, etag := requestSigned(t, e.client, signed, data)
				if etag == "" {
					t.Fatal("signed part did not return an ETag")
				}
				return files.Part{Number: number, ETag: etag}
			}
			part2 := upload(e.svc, 2, tail)
			cfg := e.cfg
			cfg.PartSize = 8 << 20
			resumed, err := files.New(e.db, map[string]files.Backend{"private": e.store}, cfg)
			if err != nil {
				t.Fatal(err)
			}
			again, err := resumed.CreateMultipart(ctx, fileOwner, r.ID)
			if err != nil || again.UploadID != created.UploadID {
				t.Fatalf("resumed session=%+v err=%v", again, err)
			}
			listed, err := resumed.ListParts(ctx, fileOwner, r.ID, created.UploadID)
			if err != nil || len(listed) != 1 || listed[0].Number != 2 || listed[0].ETag != part2.ETag || listed[0].Size != int64(len(tail)) {
				t.Fatalf("resumed parts=%+v err=%v", listed, err)
			}
			part1 := upload(resumed, 1, first)
			bad := []files.Part{{Number: 1, ETag: "wrong"}, part2}
			if _, err := resumed.CompleteMultipart(ctx, fileOwner, r.ID, created.UploadID, bad); !errors.Is(err, files.ErrInvalid) {
				t.Fatalf("accepted mismatched manifest: %v", err)
			}
			manifest := []files.Part{part2, part1}
			for range 2 {
				completed, err := resumed.CompleteMultipart(ctx, fileOwner, r.ID, created.UploadID, manifest)
				if err != nil || completed.UploadedAt == nil {
					t.Fatalf("complete=%+v err=%v", completed, err)
				}
			}
			if _, err := resumed.Confirm(ctx, fileOwner, r.ID); err != nil {
				t.Fatal(err)
			}
			want := sha256.Sum256(append(first, tail...))
			if got := sha256.Sum256(readReadyFile(t, resumed, r.ID)); got != want {
				t.Fatal("S3 assembled content differs")
			}
			expireFile(t, e.db, r.ID)
			if n, err := resumed.Cleanup(ctx, 10); err != nil || n != 1 {
				t.Fatalf("completed session cleanup n=%d err=%v", n, err)
			}
			if got := sha256.Sum256(readReadyFile(t, resumed, r.ID)); got != want {
				t.Fatal("cleanup changed multipart ready content")
			}
		})
	}
}

func TestS3FileAbortCancellationAndExpiredCleanup(t *testing.T) {
	e := newS3FileEnvironment(t)
	ctx := context.Background()
	r := prepareFile(t, e.svc, 4)
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := e.svc.Put(canceled, fileOwner, r.ID, strings.NewReader("data")); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled put=%v", err)
	}
	if _, err := e.store.Stat(ctx, r.Key); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("canceled request wrote an object: %v", err)
	}
	created, err := e.svc.CreateMultipart(ctx, fileOwner, r.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.UploadPart(ctx, fileOwner, r.ID, created.UploadID, 1, strings.NewReader("data")); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := e.svc.AbortMultipart(ctx, fileOwner, r.ID, created.UploadID); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := e.store.ListParts(ctx, r.Key, created.UploadID); !errors.Is(err, files.ErrUploadNotFound) {
		t.Fatalf("aborted S3 session still exists: %v", err)
	}
	newUpload, err := e.svc.CreateMultipart(ctx, fileOwner, r.ID)
	if err != nil || newUpload.UploadID == created.UploadID {
		t.Fatalf("new session=%+v err=%v", newUpload, err)
	}
	signed, err := e.svc.SignPart(ctx, fileOwner, r.ID, newUpload.UploadID, 1)
	if err != nil {
		t.Fatal(err)
	}
	requestSigned(t, e.client, signed, []byte("data"))
	expireFile(t, e.db, r.ID)
	if _, err := e.svc.SignPart(ctx, fileOwner, r.ID, newUpload.UploadID, 1); !errors.Is(err, files.ErrNotFound) {
		t.Fatalf("signed expired upload: %v", err)
	}
	if n, err := e.svc.Cleanup(ctx, 10); err != nil || n != 1 {
		t.Fatalf("expired S3 cleanup n=%d err=%v", n, err)
	}
	if _, err := e.store.ListParts(ctx, r.Key, newUpload.UploadID); !errors.Is(err, files.ErrUploadNotFound) {
		t.Fatalf("expired S3 session still exists: %v", err)
	}
	if _, err := e.svc.Get(ctx, fileOwner, r.ID); !errors.Is(err, files.ErrNotFound) {
		t.Fatalf("expired record still accessible: %v", err)
	}
}
