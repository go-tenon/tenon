package s3_test

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/ryan053857/mygoravel/files"
	stores3 "github.com/ryan053857/mygoravel/files/s3"
)

func newStore(t *testing.T, handler http.HandlerFunc) *stores3.Store {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	client := awss3.New(awss3.Options{
		BaseEndpoint: aws.String(server.URL), Region: "us-east-1", UsePathStyle: true,
		Credentials:      credentials.NewStaticCredentialsProvider("test-key", "test-secret", "session-token"),
		RetryMaxAttempts: 1,
	})
	store, err := stores3.New(client, "test-bucket")
	if err != nil {
		t.Fatal(err)
	}
	if store.Client() != client {
		t.Fatal("native client is not preserved")
	}
	return store
}

func xmlResponse(w http.ResponseWriter, body string) {
	w.Header().Set("Content-Type", "application/xml")
	fmt.Fprint(w, body)
}

func TestMultipartProtocol(t *testing.T) {
	ctx := context.Background()
	var sawCreate, sawUpload, sawComplete, sawAbort bool
	store := newStore(t, func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if r.URL.Path != "/test-bucket/uploads/file" {
			t.Errorf("path=%s", r.URL.Path)
		}
		if r.Header.Get("Authorization") == "" {
			t.Error("request must be signed")
		}
		switch {
		case r.Method == "POST" && q.Has("uploads"):
			sawCreate = true
			if r.Header.Get("Content-Type") != "application/octet-stream" {
				t.Errorf("create type=%s", r.Header.Get("Content-Type"))
			}
			if r.Header.Get("X-Amz-Checksum-Algorithm") != "" {
				t.Error("unexpected extra checksum protocol")
			}
			xmlResponse(w, `<InitiateMultipartUploadResult><UploadId>upload-id</UploadId></InitiateMultipartUploadResult>`)
		case r.Method == "PUT":
			sawUpload = true
			body, _ := io.ReadAll(r.Body)
			if string(body) != "end" || r.ContentLength != 3 || q.Get("partNumber") != "2" || q.Get("uploadId") != "upload-id" {
				t.Errorf("upload: body=%q length=%d query=%v", body, r.ContentLength, q)
			}
			w.Header().Set("ETag", `"part2"`)
		case r.Method == "GET":
			if q.Get("part-number-marker") == "" {
				xmlResponse(w, `<ListPartsResult><IsTruncated>true</IsTruncated><NextPartNumberMarker>9</NextPartNumberMarker><Part><PartNumber>1</PartNumber><ETag>"part1"</ETag><Size>5242880</Size></Part></ListPartsResult>`)
			} else if q.Get("part-number-marker") == "9" {
				// 数字 10 大于 9；不能使用字符串字典序比较分页标记。
				xmlResponse(w, `<ListPartsResult><IsTruncated>true</IsTruncated><NextPartNumberMarker>10</NextPartNumberMarker></ListPartsResult>`)
			} else if q.Get("part-number-marker") == "10" {
				xmlResponse(w, `<ListPartsResult><IsTruncated>false</IsTruncated><Part><PartNumber>2</PartNumber><ETag>"part2"</ETag><Size>3</Size></Part></ListPartsResult>`)
			} else {
				t.Errorf("unexpected marker %v", q)
				w.WriteHeader(500)
			}
		case r.Method == "POST":
			sawComplete = true
			var data struct {
				Parts []struct {
					Number int    `xml:"PartNumber"`
					ETag   string `xml:"ETag"`
				} `xml:"Part"`
			}
			if err := xml.NewDecoder(r.Body).Decode(&data); err != nil {
				t.Error(err)
			}
			if len(data.Parts) != 2 || data.Parts[0].Number != 1 || data.Parts[1].Number != 2 || data.Parts[0].ETag != `"part1"` {
				t.Errorf("completion=%+v", data)
			}
			xmlResponse(w, `<CompleteMultipartUploadResult><ETag>"complete"</ETag></CompleteMultipartUploadResult>`)
		case r.Method == "DELETE":
			sawAbort = true
			w.WriteHeader(204)
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL)
			w.WriteHeader(500)
		}
	})
	id, err := store.CreateMultipart(ctx, "uploads/file", "application/octet-stream")
	if err != nil || id != "upload-id" {
		t.Fatalf("id=%q err=%v", id, err)
	}
	// 通过只实现 Reader 的包装验证 SDK 可处理不可 seek 的输入。
	part, err := store.UploadPart(ctx, "uploads/file", id, 2, struct{ io.Reader }{strings.NewReader("end")}, 3)
	if err != nil || part.ETag != `"part2"` || part.Size != 3 {
		t.Fatalf("part=%+v err=%v", part, err)
	}
	parts, err := store.ListParts(ctx, "uploads/file", id)
	if err != nil || len(parts) != 2 || parts[0].Size != files.MinPartSize {
		t.Fatalf("parts=%+v err=%v", parts, err)
	}
	parts[0], parts[1] = parts[1], parts[0]
	if err := store.CompleteMultipart(ctx, "uploads/file", id, parts); err != nil {
		t.Fatal(err)
	}
	if parts[0].Number != 2 {
		t.Fatal("completion mutated caller's list")
	}
	if err := store.AbortMultipart(ctx, "uploads/file", id); err != nil {
		t.Fatal(err)
	}
	if !sawCreate || !sawUpload || !sawComplete || !sawAbort {
		t.Fatal("multipart flow incomplete")
	}
}

func TestObjectProtocolAndExactLength(t *testing.T) {
	ctx := context.Background()
	var requests atomic.Int32
	store := newStore(t, func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		switch r.Method {
		case "PUT":
			if copySource := r.Header.Get("X-Amz-Copy-Source"); copySource != "" {
				decoded, _ := url.PathUnescape(copySource)
				if decoded != "test-bucket/a ?#.txt" || r.Header.Get("X-Amz-Copy-Source-If-Match") != `"object-etag"` {
					t.Errorf("copy source=%q headers=%v", decoded, r.Header)
				}
				xmlResponse(w, `<CopyObjectResult><ETag>"copied"</ETag></CopyObjectResult>`)
				return
			}
			body, err := io.ReadAll(r.Body)
			if err != nil || string(body) != "hello" || r.ContentLength != 5 || r.Header.Get("Content-Type") != "text/plain" {
				t.Errorf("put: body=%q size=%d err=%v", body, r.ContentLength, err)
			}
			w.Header().Set("ETag", `"object-etag"`)
		case "HEAD":
			w.Header().Set("Content-Length", "5")
			w.Header().Set("Content-Type", "text/plain")
			w.Header().Set("ETag", `"object-etag"`)
		case "GET":
			fmt.Fprint(w, "hello")
		case "DELETE":
			w.WriteHeader(204)
		default:
			t.Errorf("unexpected %s", r.Method)
			w.WriteHeader(500)
		}
	})
	for _, body := range []string{"hell", "helloo"} {
		if err := store.Put(ctx, "file", "text/plain", strings.NewReader(body), 5); !errors.Is(err, files.ErrInvalid) {
			t.Fatalf("size mismatch err=%v", err)
		}
	}
	if requests.Load() != 0 {
		t.Fatal("invalid body was sent to S3")
	}
	if err := store.Put(ctx, "a ?#.txt", "text/plain", struct{ io.Reader }{strings.NewReader("hello")}, 5); err != nil {
		t.Fatal(err)
	}
	info, err := store.Stat(ctx, "a ?#.txt")
	if err != nil || info.Size != 5 || info.ContentType != "text/plain" {
		t.Fatalf("info=%+v err=%v", info, err)
	}
	body, err := store.Open(ctx, "a ?#.txt")
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(body)
	if err != nil {
		t.Fatal(err)
	}
	body.Close()
	if string(data) != "hello" {
		t.Fatalf("body=%q", data)
	}
	if err := store.Copy(ctx, "a ?#.txt", "destination"); err != nil {
		t.Fatal(err)
	}
	if err := store.Delete(ctx, "destination"); err != nil {
		t.Fatal(err)
	}
	if requests.Load() != 6 {
		t.Fatalf("request count=%d", requests.Load())
	}
}

func TestPresignRetainsRequiredHeaders(t *testing.T) {
	store := newStore(t, func(w http.ResponseWriter, r *http.Request) { t.Error("presigning must not use network") })
	ctx := context.Background()
	put, err := store.SignPut(ctx, "a ?#.txt", "text/plain", 5, 10*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(put.URL)
	if err != nil {
		t.Fatal(err)
	}
	if put.Method != "PUT" || u.Path != "/test-bucket/a ?#.txt" || u.Query().Get("X-Amz-Expires") != "600" || put.Headers.Get("Content-Length") != "5" || put.Headers.Get("Content-Type") != "text/plain" {
		t.Fatalf("signed request=%+v query=%v", put, u.Query())
	}
	for _, h := range strings.Split(u.Query().Get("X-Amz-SignedHeaders"), ";") {
		if put.Headers.Get(h) == "" {
			t.Errorf("missing signed header %s in %v", h, put.Headers)
		}
	}
	part, err := store.SignPart(ctx, "file", "upload+id", 7, 42, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	u, _ = url.Parse(part.URL)
	if u.Query().Get("uploadId") != "upload+id" || u.Query().Get("partNumber") != "7" || part.Headers.Get("Content-Length") != "42" {
		t.Fatalf("part=%+v", part)
	}
	get, err := store.SignGet(ctx, "file", time.Minute)
	if err != nil || get.Method != "GET" {
		t.Fatalf("get=%+v err=%v", get, err)
	}
	for _, ttl := range []time.Duration{0, time.Millisecond, 7*24*time.Hour + time.Second} {
		if _, err := store.SignGet(ctx, "file", ttl); !errors.Is(err, files.ErrInvalid) {
			t.Fatalf("ttl=%s err=%v", ttl, err)
		}
	}
}

func TestS3ErrorsRemainDistinct(t *testing.T) {
	for _, tc := range []struct {
		code                         string
		status                       int
		objectMissing, uploadMissing bool
	}{
		{"NoSuchKey", 404, true, false}, {"NoSuchUpload", 404, false, true},
		{"NoSuchBucket", 404, false, false}, {"AccessDenied", 403, false, false},
	} {
		t.Run(tc.code, func(t *testing.T) {
			store := newStore(t, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/xml")
				w.WriteHeader(tc.status)
				fmt.Fprintf(w, "<Error><Code>%s</Code><Message>test</Message></Error>", tc.code)
			})
			_, err := store.Open(context.Background(), "file")
			if err == nil || errors.Is(err, fs.ErrNotExist) != tc.objectMissing {
				t.Fatalf("open err=%v", err)
			}
			_, err = store.ListParts(context.Background(), "file", "upload")
			if err == nil || errors.Is(err, files.ErrUploadNotFound) != tc.uploadMissing {
				t.Fatalf("list err=%v", err)
			}
			err = store.AbortMultipart(context.Background(), "file", "upload")
			if (err == nil) != tc.uploadMissing {
				t.Fatalf("abort err=%v", err)
			}
		})
	}
}

func TestHead404DistinguishesMissingBucket(t *testing.T) {
	for _, bucketStatus := range []int{200, 403, 404} {
		t.Run(fmt.Sprint(bucketStatus), func(t *testing.T) {
			store := newStore(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "HEAD" {
					t.Errorf("unexpected method %s", r.Method)
				}
				if r.URL.Path == "/test-bucket" {
					w.WriteHeader(bucketStatus)
				} else {
					w.WriteHeader(404)
				}
			})
			_, err := store.Stat(context.Background(), "missing")
			if err == nil || errors.Is(err, fs.ErrNotExist) != (bucketStatus == 200) {
				t.Fatalf("bucket=%d err=%v", bucketStatus, err)
			}
		})
	}
}

func TestValidationAndCancellationAvoidWrites(t *testing.T) {
	store := newStore(t, func(w http.ResponseWriter, r *http.Request) { t.Errorf("unexpected network request %s", r.URL) })
	ctx := context.Background()
	for _, parts := range [][]files.Part{
		nil,
		{{Number: 0, ETag: "tag", Size: 3}},
		{{Number: 10001, ETag: "tag", Size: 3}},
		{{Number: 1, ETag: "tag", Size: files.MinPartSize}, {Number: 1, ETag: "tag", Size: 3}},
		{{Number: 1, ETag: "tag", Size: 3}, {Number: 2, ETag: "tag", Size: 3}},
		{{Number: 1, ETag: "", Size: 3}},
	} {
		if err := store.CompleteMultipart(ctx, "file", "id", parts); !errors.Is(err, files.ErrInvalid) {
			t.Fatalf("parts=%v err=%v", parts, err)
		}
	}
	if _, err := store.UploadPart(ctx, "file", "id", 10001, strings.NewReader("x"), 1); !errors.Is(err, files.ErrInvalid) {
		t.Fatal(err)
	}
	if _, err := store.UploadPart(ctx, "file", "id", 1, strings.NewReader("too long"), 1); !errors.Is(err, files.ErrInvalid) {
		t.Fatal(err)
	}
	cancelCtx, cancel := context.WithCancel(ctx)
	cancel()
	if err := store.Put(cancelCtx, "file", "", strings.NewReader("x"), 1); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if err := store.Put(ctx, "file", "", nil, 0); !errors.Is(err, files.ErrInvalid) {
		t.Fatal(err)
	}
}

func TestRejectsBrokenPagination(t *testing.T) {
	store := newStore(t, func(w http.ResponseWriter, r *http.Request) {
		xmlResponse(w, `<ListPartsResult><IsTruncated>true</IsTruncated><NextPartNumberMarker>1</NextPartNumberMarker></ListPartsResult>`)
	})
	if _, err := store.ListParts(context.Background(), "file", "id"); err == nil || !strings.Contains(err.Error(), "pagination") {
		t.Fatal(err)
	}
}

func TestLargeObjectCopyUsesRanges(t *testing.T) {
	var ranges []string
	var completedParts int
	const size = files.MaxPartSize + 1
	store := newStore(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case "HEAD":
			w.Header().Set("Content-Length", fmt.Sprint(size))
			w.Header().Set("ETag", `"source"`)
			w.Header().Set("Content-Type", "video/mp4")
		case "POST":
			if r.URL.Query().Has("uploads") {
				if r.Header.Get("Content-Type") != "video/mp4" {
					t.Errorf("content type=%s", r.Header.Get("Content-Type"))
				}
				xmlResponse(w, `<InitiateMultipartUploadResult><UploadId>copy-id</UploadId></InitiateMultipartUploadResult>`)
			} else {
				var body struct {
					Parts []struct{} `xml:"Part"`
				}
				xml.NewDecoder(r.Body).Decode(&body)
				completedParts = len(body.Parts)
				xmlResponse(w, `<CompleteMultipartUploadResult><ETag>"copied"</ETag></CompleteMultipartUploadResult>`)
			}
		case "PUT":
			if r.Header.Get("X-Amz-Copy-Source-If-Match") != `"source"` {
				t.Errorf("source must be pinned to ETag")
			}
			ranges = append(ranges, r.Header.Get("X-Amz-Copy-Source-Range"))
			xmlResponse(w, `<CopyPartResult><ETag>"part"</ETag></CopyPartResult>`)
		default:
			t.Errorf("unexpected request %s", r.Method)
			w.WriteHeader(500)
		}
	})
	if err := store.Copy(context.Background(), "source", "destination"); err != nil {
		t.Fatal(err)
	}
	if completedParts != 81 || len(ranges) != 81 || ranges[0] != "bytes=0-67108863" || ranges[80] != "bytes=5368709120-5368709120" {
		t.Fatalf("parts=%d ranges=%v", completedParts, ranges)
	}
}

func TestConfigAndOpen(t *testing.T) {
	for _, cfg := range []stores3.Config{{}, {Bucket: "b", AccessKey: "key"}, {Bucket: "b", SessionToken: "token"}, {Bucket: "b", Endpoint: "ftp://example.com"}, {Bucket: "b", Endpoint: "https://user:password@example.com"}} {
		if err := cfg.Validate(); err == nil {
			t.Fatalf("expected invalid config: %+v", cfg)
		}
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "HEAD" || r.URL.Path != "/bucket" {
			t.Errorf("request=%s %s", r.Method, r.URL)
		}
	}))
	t.Cleanup(server.Close)
	store, err := stores3.Open(context.Background(), stores3.Config{Bucket: "bucket", Region: "us-east-1", Endpoint: server.URL, PathStyle: true, AccessKey: "key", SecretKey: "secret"})
	if err != nil || store == nil {
		t.Fatalf("store=%v err=%v", store, err)
	}
	if _, err := stores3.New(nil, "bucket"); err == nil {
		t.Fatal("nil client accepted")
	}
	if reflect.TypeOf(store.Client()) != reflect.TypeOf((*awss3.Client)(nil)) {
		t.Fatal("expected native client")
	}
}
