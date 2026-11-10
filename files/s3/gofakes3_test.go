package s3_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"io/fs"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/johannesboyne/gofakes3"
	"github.com/johannesboyne/gofakes3/backend/s3mem"

	"github.com/ryan053857/mygoravel/files"
	stores3 "github.com/ryan053857/mygoravel/files/s3"
)

// newFakeStore 通过真实 SDK 创建桶，并让驱动连接内存 S3 服务。
func newFakeStore(t *testing.T) *stores3.Store {
	t.Helper()
	server := httptest.NewServer(gofakes3.New(s3mem.New()).Server())
	t.Cleanup(server.Close)
	client := awss3.New(awss3.Options{
		BaseEndpoint: aws.String(server.URL), Region: "us-east-1", UsePathStyle: true,
		Credentials:      credentials.NewStaticCredentialsProvider("test-key", "test-secret", ""),
		RetryMaxAttempts: 1,
	})
	if _, err := client.CreateBucket(context.Background(), &awss3.CreateBucketInput{Bucket: aws.String("test-bucket")}); err != nil {
		t.Fatal(err)
	}
	store, err := stores3.Open(context.Background(), stores3.Config{
		Bucket: "test-bucket", Region: "us-east-1", Endpoint: server.URL, PathStyle: true,
		AccessKey: "test-key", SecretKey: "test-secret",
	})
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func readFakeObject(t *testing.T, store *stores3.Store, key string) []byte {
	t.Helper()
	body, err := store.Open(context.Background(), key)
	if err != nil {
		t.Fatal(err)
	}
	defer body.Close()
	data, err := io.ReadAll(body)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestFakeS3ObjectLifecycle(t *testing.T) {
	t.Parallel()
	store := newFakeStore(t)
	ctx := context.Background()
	for _, tc := range []struct{ key, body string }{
		{"uploads/empty", ""},
		{"uploads/中文 空格 ?#.txt", "actual object content"},
		{"uploads/literal+plus%2B.txt", "plus and percent must remain literal"},
	} {
		t.Run(tc.key, func(t *testing.T) {
			if _, err := store.Stat(ctx, tc.key); !errors.Is(err, fs.ErrNotExist) {
				t.Fatalf("stat missing object: %v", err)
			}
			if _, err := store.Open(ctx, tc.key); !errors.Is(err, fs.ErrNotExist) {
				t.Fatalf("open missing object: %v", err)
			}
			if err := store.Put(ctx, tc.key, "text/plain", struct{ io.Reader }{strings.NewReader(tc.body)}, int64(len(tc.body))); err != nil {
				t.Fatal(err)
			}
			info, err := store.Stat(ctx, tc.key)
			if err != nil || info.Size != int64(len(tc.body)) || info.ContentType != "text/plain" || info.ETag == "" {
				t.Fatalf("stat=%+v err=%v", info, err)
			}
			if got := string(readFakeObject(t, store, tc.key)); got != tc.body {
				t.Fatalf("got %q, want %q", got, tc.body)
			}
			// 上传失败不能改掉已有对象。
			if err := store.Put(ctx, tc.key, "text/plain", strings.NewReader("too long"), 1); !errors.Is(err, files.ErrInvalid) {
				t.Fatalf("size mismatch: %v", err)
			}
			copyKey := "objects/" + strings.TrimPrefix(tc.key, "uploads/")
			if err := store.Copy(ctx, tc.key, copyKey); err != nil {
				t.Fatal(err)
			}
			if err := store.Delete(ctx, tc.key); err != nil {
				t.Fatal(err)
			}
			if got := string(readFakeObject(t, store, copyKey)); got != tc.body {
				t.Fatalf("copied content=%q", got)
			}
			for range 2 {
				if err := store.Delete(ctx, copyKey); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := store.Stat(ctx, copyKey); !errors.Is(err, fs.ErrNotExist) {
				t.Fatalf("object remained after deletion: %v", err)
			}
		})
	}
}

func TestFakeS3MultipartResumeAndRetransmit(t *testing.T) {
	t.Parallel()
	store := newFakeStore(t)
	ctx := context.Background()
	key := "uploads/multipart"
	id, err := store.CreateMultipart(ctx, key, "application/octet-stream")
	if err != nil {
		t.Fatal(err)
	}
	old, err := store.UploadPart(ctx, key, id, 2, strings.NewReader("old"), 3)
	if err != nil {
		t.Fatal(err)
	}
	first := bytes.Repeat([]byte("a"), int(files.MinPartSize))
	p1, err := store.UploadPart(ctx, key, id, 1, bytes.NewReader(first), int64(len(first)))
	if err != nil {
		t.Fatal(err)
	}
	// 重建客户端适配，不重建 S3 服务；会话状态由存储端保存。
	resumed, err := stores3.New(store.Client(), "test-bucket")
	if err != nil {
		t.Fatal(err)
	}
	parts, err := resumed.ListParts(ctx, key, id)
	if err != nil || !reflect.DeepEqual(parts, []files.Part{p1, old}) {
		t.Fatalf("resumed parts=%+v err=%v", parts, err)
	}
	tail := "replacement tail"
	p2, err := resumed.UploadPart(ctx, key, id, 2, strings.NewReader(tail), int64(len(tail)))
	if err != nil {
		t.Fatal(err)
	}
	if p2.ETag == old.ETag {
		t.Fatal("retransmission did not change ETag")
	}
	if err := resumed.CompleteMultipart(ctx, key, id, []files.Part{p1, old}); err == nil {
		t.Fatal("completed using stale ETag")
	}
	if _, err := resumed.Stat(ctx, key); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("failed completion published an object: %v", err)
	}
	if err := resumed.CompleteMultipart(ctx, key, id, []files.Part{p2, p1}); err != nil {
		t.Fatal(err)
	}
	if got, want := readFakeObject(t, resumed, key), append(first, tail...); !bytes.Equal(got, want) {
		t.Fatal("assembled content differs")
	}
	info, err := resumed.Stat(ctx, key)
	if err != nil || info.Size != int64(len(first)+len(tail)) || info.ContentType != "application/octet-stream" {
		t.Fatalf("completed metadata=%+v err=%v", info, err)
	}
	if _, err := resumed.ListParts(ctx, key, id); !errors.Is(err, files.ErrUploadNotFound) {
		t.Fatalf("completed upload remained active: %v", err)
	}
}

func TestFakeS3AbortMultipart(t *testing.T) {
	t.Parallel()
	store := newFakeStore(t)
	ctx := context.Background()
	key := "uploads/abort"
	id, err := store.CreateMultipart(ctx, key, "text/plain")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.UploadPart(ctx, key, id, 1, strings.NewReader("tail"), 4); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := store.AbortMultipart(ctx, key, id); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := store.ListParts(ctx, key, id); !errors.Is(err, files.ErrUploadNotFound) {
		t.Fatalf("aborted upload remained active: %v", err)
	}
	if _, err := store.Stat(ctx, key); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("abort produced object: %v", err)
	}
}
