// Package storage 保存文件（对象）：一个 Storage 接口，两种实现。
//
//	local  保存在本地目录，适合单机部署与开发环境（默认）
//	s3     S3 兼容的对象存储：AWS S3、Cloudflare R2、阿里云 OSS、腾讯云 COS、MinIO 等
//
// 由配置选择实现：
//
//	storage:
//	  driver: s3
//	  s3:
//	    endpoint: https://<account>.r2.cloudflarestorage.com  # AWS S3 留空
//	    region: auto
//	    bucket: myapp
//	    access_key_id: ${S3_ACCESS_KEY_ID}
//	    secret_access_key: ${S3_SECRET_ACCESS_KEY}
//
// 对象的访问控制由应用负责：下载经过应用转发（见 modules/file 的 Serve），桶不需要公开读。
package storage

import (
	"context"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/cockroachdb/errors"

	"github.com/ryan053857/go-kit/config"
)

// ErrNotFound 表示对象不存在。
var ErrNotFound = errors.New("storage: object not found")

// Storage 保存与读取对象。key 是以 / 分隔的相对路径，如 "avatars/0193….jpg"，
// 不能以 / 开头，不能包含 .. 或反斜杠。
type Storage interface {
	// Put 写入对象，已存在时覆盖。r 需要可 Seek（上传的文件、临时文件、bytes.Reader），用于确定长度。
	Put(ctx context.Context, key string, r io.ReadSeeker, contentType string) error
	// Get 返回对象内容，调用方负责 Close。对象不存在时返回 ErrNotFound。
	Get(ctx context.Context, key string) (io.ReadCloser, error)
	// Delete 删除对象，对象不存在时也返回 nil。
	Delete(ctx context.Context, key string) error
}

// Presigner 由支持客户端直传的存储实现（S3 驱动实现了它，本地目录没有）。
// 直传时文件内容不经过应用：应用签发一个短时有效的上传地址，客户端直接 PUT 到对象存储。
type Presigner interface {
	// PresignPut 返回上传 key 的 PUT 请求。size 与 contentType 包含在签名中，
	// 客户端必须带上 PresignedPut.Header 中的全部请求头，且内容长度必须等于 size。
	PresignPut(ctx context.Context, key, contentType string, size int64, expires time.Duration) (*PresignedPut, error)
	// Size 返回对象的字节数，对象不存在时返回 ErrNotFound。
	Size(ctx context.Context, key string) (int64, error)
}

// PresignedPut 是预签名的上传请求。
type PresignedPut struct {
	URL       string
	Header    http.Header
	ExpiresAt time.Time
}

// 驱动名。
const (
	DriverLocal = "local"
	DriverS3    = "s3"
)

// Config 是存储配置。
type Config struct {
	Driver string      `yaml:"driver"` // local（默认）| s3
	Local  LocalConfig `yaml:"local"`
	S3     S3Config    `yaml:"s3"`
}

// LocalConfig 是 local 驱动的配置。
type LocalConfig struct {
	Dir string `yaml:"dir"` // 保存目录，默认 "data/storage"
}

// S3Config 是 s3 驱动的配置。
type S3Config struct {
	Endpoint        string        `yaml:"endpoint"` // S3 兼容服务的地址；AWS S3 留空
	Region          string        `yaml:"region"`   // 默认 "auto"（R2）；AWS 填实际区域
	Bucket          string        `yaml:"bucket"`
	AccessKeyID     string        `yaml:"access_key_id"`
	SecretAccessKey config.Secret `yaml:"secret_access_key"`
	PathStyle       bool          `yaml:"path_style"` // MinIO 等需要 path-style 地址时设为 true
}

// New 按 cfg.Driver 返回 Storage。不会访问网络，配置错误在首次读写时才暴露。
func New(cfg Config) (Storage, error) {
	switch cfg.Driver {
	case "", DriverLocal:
		dir := cfg.Local.Dir
		if dir == "" {
			dir = "data/storage"
		}
		return NewLocal(dir), nil
	case DriverS3:
		return newS3(cfg)
	}
	return nil, errors.Newf("storage: unknown driver %q", cfg.Driver)
}

// ValidKey 报告 key 是否是合法的对象键。
func ValidKey(key string) bool {
	if key == "" || strings.HasPrefix(key, "/") || strings.Contains(key, `\`) {
		return false
	}
	for part := range strings.SplitSeq(key, "/") {
		if part == "" || part == "." || part == ".." {
			return false
		}
	}
	return true
}

func checkKey(key string) error {
	if !ValidKey(key) {
		return errors.Newf("storage: invalid key %q", key)
	}
	return nil
}
