// Package files 提供依赖 files 表的文件上传、确认、读取和清理服务。
package files

import (
	"context"
	"io"
	"net/http"
	"time"

	"github.com/cockroachdb/errors"
)

var (
	ErrNotFound       = errors.New("files: file not found")
	ErrInvalid        = errors.New("files: invalid input")
	ErrConflict       = errors.New("files: invalid file state")
	ErrTooLarge       = errors.New("files: file too large")
	ErrUnsupported    = errors.New("files: operation unsupported by storage")
	ErrUploadNotFound = errors.New("files: multipart upload not found")
)

const (
	MinPartSize int64 = 5 << 20
	MaxPartSize int64 = 5 << 30
	MaxParts          = 10000
)

// Object 是存储中实际对象的信息；ETag 是不透明标识，不保证是内容哈希。
type Object struct {
	Size        int64
	ContentType string
	ETag        string
}

// Part 是已上传分片。Number 从 1 开始；ETag 必须原样用于合并。
type Part struct {
	Number int32  `json:"number"`
	ETag   string `json:"etag"`
	Size   int64  `json:"size"`
}

// SignedRequest 是客户端必须使用的地址、方法和请求头。
type SignedRequest struct {
	URL     string      `json:"url"`
	Method  string      `json:"method"`
	Headers http.Header `json:"headers"`
}

// Backend 是文件服务使用的存储操作面。应用通过构造函数显式传入实现。
// 对象不存在时返回可匹配 io/fs.ErrNotExist 的错误；Delete、AbortMultipart 幂等。
// Put 与 UploadPart 必须校验实际读取字节数与 size 相等，不得发布部分内容。
type Backend interface {
	Put(ctx context.Context, key, contentType string, body io.Reader, size int64) error
	Open(ctx context.Context, key string) (io.ReadCloser, error)
	Stat(ctx context.Context, key string) (Object, error)
	Delete(ctx context.Context, key string) error
	Copy(ctx context.Context, source, destination string) error
	CreateMultipart(ctx context.Context, key, contentType string) (string, error)
	UploadPart(ctx context.Context, key, uploadID string, number int32, body io.Reader, size int64) (Part, error)
	ListParts(ctx context.Context, key, uploadID string) ([]Part, error)
	CompleteMultipart(ctx context.Context, key, uploadID string, parts []Part) error
	AbortMultipart(ctx context.Context, key, uploadID string) error
}

// Signer 是 S3 等支持客户端直传的存储实现提供的附加能力。
type Signer interface {
	SignPut(ctx context.Context, key, contentType string, size int64, ttl time.Duration) (SignedRequest, error)
	SignPart(ctx context.Context, key, uploadID string, number int32, size int64, ttl time.Duration) (SignedRequest, error)
	SignGet(ctx context.Context, key string, ttl time.Duration) (SignedRequest, error)
}
