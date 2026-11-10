// Package s3 通过 AWS SDK v2 为 files 服务提供 S3 兼容存储。
package s3

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsv4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"

	"github.com/ryan053857/mygoravel/files"
)

// Config 配置桶和可选的 S3 兼容端点。未提供静态密钥时使用 AWS 默认凭证链。
type Config struct {
	Bucket       string `yaml:"bucket"`
	Region       string `yaml:"region"`
	Endpoint     string `yaml:"endpoint"`
	PathStyle    bool   `yaml:"path_style"`
	AccessKey    string `yaml:"access_key"`
	SecretKey    string `yaml:"secret_key"`
	SessionToken string `yaml:"session_token"`
}

// Validate 检查桶、端点和静态凭证的完整性。
func (c Config) Validate() error {
	if strings.TrimSpace(c.Bucket) == "" {
		return errors.New("bucket is required")
	}
	if (c.AccessKey == "") != (c.SecretKey == "") {
		return errors.New("access_key and secret_key must be supplied together")
	}
	if c.SessionToken != "" && c.AccessKey == "" {
		return errors.New("session_token requires static credentials")
	}
	if c.Endpoint != "" {
		u, err := url.Parse(c.Endpoint)
		if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
			return errors.New("endpoint must be an http or https URL without credentials, query or fragment")
		}
	}
	return nil
}

// Store 保存原生客户端及桶名。请求使用普通桶的 ETag 分片协议。
type Store struct {
	client    *awss3.Client
	presigner *awss3.PresignClient
	bucket    string
}

var _ files.Backend = (*Store)(nil)
var _ files.Signer = (*Store)(nil)

// New 使用应用创建的原生客户端；不连接网络，也不创建桶。
func New(client *awss3.Client, bucket string) (*Store, error) {
	if client == nil || strings.TrimSpace(bucket) == "" {
		return nil, fmt.Errorf("files/s3: client and bucket are required: %w", files.ErrInvalid)
	}
	return &Store{client: client, presigner: awss3.NewPresignClient(client), bucket: bucket}, nil
}

// Open 加载 AWS 配置并执行 HeadBucket。桶必须由应用或运维预先创建。
func Open(ctx context.Context, cfg Config) (*Store, error) {
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("files/s3: %w", err)
	}
	options := []func(*awsconfig.LoadOptions) error{}
	if cfg.Region != "" {
		options = append(options, awsconfig.WithRegion(cfg.Region))
	}
	if cfg.AccessKey != "" {
		options = append(options, awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(cfg.AccessKey, cfg.SecretKey, cfg.SessionToken)))
	}
	c, err := awsconfig.LoadDefaultConfig(ctx, options...)
	if err != nil {
		return nil, fmt.Errorf("files/s3: load config: %w", err)
	}
	if c.Region == "" {
		return nil, errors.New("files/s3: region is required in configuration or AWS environment")
	}
	client := awss3.NewFromConfig(c, func(o *awss3.Options) {
		if cfg.Endpoint != "" {
			o.BaseEndpoint = aws.String(cfg.Endpoint)
		}
		o.UsePathStyle = cfg.PathStyle
	})
	if _, err := client.HeadBucket(ctx, &awss3.HeadBucketInput{Bucket: aws.String(cfg.Bucket)}); err != nil {
		return nil, fmt.Errorf("files/s3: head bucket: %w", err)
	}
	return New(client, cfg.Bucket)
}

// Client 返回原生客户端，供应用使用未封装的 S3 能力。
func (s *Store) Client() *awss3.Client { return s.client }

// etagProtocol 禁止 SDK 自动选择额外的分片校验和协议，保持 Part 的 ETag 契约。
// 调用方仍可通过原生 Client 使用额外校验和、加密或其他 S3 专有能力。
func etagProtocol(o *awss3.Options) {
	o.RequestChecksumCalculation = aws.RequestChecksumCalculationWhenRequired
}

// Put 先把正文流式暂存到磁盘，验证长度后再发送，支持不可 seek 的请求体和 SDK 重试。
func (s *Store) Put(ctx context.Context, key, contentType string, body io.Reader, size int64) error {
	if err := validKey(key); err != nil {
		return err
	}
	if size < 0 || size > files.MaxPartSize {
		return fmt.Errorf("files/s3: single PUT size: %w", files.ErrInvalid)
	}
	f, err := spool(ctx, body, size)
	if err != nil {
		return err
	}
	defer removeSpool(f)
	_, err = s.client.PutObject(ctx, &awss3.PutObjectInput{Bucket: aws.String(s.bucket), Key: aws.String(key), Body: f, ContentLength: aws.Int64(size), ContentType: optional(contentType)}, etagProtocol)
	return wrap("put object", err)
}

// Open 返回对象正文，调用方必须关闭返回的流。
func (s *Store) Open(ctx context.Context, key string) (io.ReadCloser, error) {
	if err := validKey(key); err != nil {
		return nil, err
	}
	out, err := s.client.GetObject(ctx, &awss3.GetObjectInput{Bucket: aws.String(s.bucket), Key: aws.String(key)})
	if err != nil {
		return nil, wrap("get object", objectError(err))
	}
	return out.Body, nil
}

// Stat 查询对象大小和类型。
func (s *Store) Stat(ctx context.Context, key string) (files.Object, error) {
	if err := validKey(key); err != nil {
		return files.Object{}, err
	}
	out, err := s.client.HeadObject(ctx, &awss3.HeadObjectInput{Bucket: aws.String(s.bucket), Key: aws.String(key)})
	if err != nil {
		// HEAD 响应没有 XML 正文，桶不存在和对象不存在都可能表现为普通 404。
		// 先确认桶仍可访问，避免上层把桶配置故障误判成可以重新上传的缺失对象。
		if code := apiCode(err); code == "NotFound" || code == "404" {
			if _, bucketErr := s.client.HeadBucket(ctx, &awss3.HeadBucketInput{Bucket: aws.String(s.bucket)}); bucketErr != nil {
				return files.Object{}, wrap("head bucket after object 404", bucketErr)
			}
			err = errors.Join(fs.ErrNotExist, err)
		}
		return files.Object{}, wrap("head object", objectError(err))
	}
	return files.Object{Size: aws.ToInt64(out.ContentLength), ContentType: aws.ToString(out.ContentType), ETag: aws.ToString(out.ETag)}, nil
}

// Delete 删除对象；S3 对不存在的对象也返回成功。
func (s *Store) Delete(ctx context.Context, key string) error {
	if err := validKey(key); err != nil {
		return err
	}
	_, err := s.client.DeleteObject(ctx, &awss3.DeleteObjectInput{Bucket: aws.String(s.bucket), Key: aws.String(key)})
	if apiCode(err) == "NoSuchKey" {
		return nil
	}
	return wrap("delete object", err)
}

// Copy 在桶内复制对象。超过单次复制上限时使用 S3 服务端分片复制，不下载正文。
func (s *Store) Copy(ctx context.Context, source, destination string) error {
	if err := validKey(source); err != nil {
		return err
	}
	if err := validKey(destination); err != nil {
		return err
	}
	info, err := s.Stat(ctx, source)
	if err != nil {
		return err
	}
	if source == destination {
		return nil
	}
	// PathEscape 保留 literal '+'，部分 S3 兼容服务会把它解释为空格。
	// QueryEscape 编码 '+' 后，再将其对空格生成的 '+' 改为 '%20'。
	encodedKey := strings.ReplaceAll(url.QueryEscape(source), "+", "%20")
	copySource := url.PathEscape(s.bucket) + "/" + strings.ReplaceAll(encodedKey, "%2F", "/")
	if info.Size <= files.MaxPartSize {
		_, err := s.client.CopyObject(ctx, &awss3.CopyObjectInput{Bucket: aws.String(s.bucket), Key: aws.String(destination), CopySource: aws.String(copySource), CopySourceIfMatch: optional(info.ETag)})
		return wrap("copy object", objectError(err))
	}
	partSize := max(int64(64<<20), (info.Size+files.MaxParts-1)/files.MaxParts)
	if partSize > files.MaxPartSize {
		return files.ErrTooLarge
	}
	id, err := s.CreateMultipart(ctx, destination, info.ContentType)
	if err != nil {
		return err
	}
	var parts []files.Part
	for offset := int64(0); offset < info.Size; offset += partSize {
		size := min(partSize, info.Size-offset)
		number := int32(len(parts) + 1)
		out, copyErr := s.client.UploadPartCopy(ctx, &awss3.UploadPartCopyInput{
			Bucket: aws.String(s.bucket), Key: aws.String(destination), UploadId: aws.String(id), PartNumber: aws.Int32(number),
			CopySource: aws.String(copySource), CopySourceIfMatch: optional(info.ETag), CopySourceRange: aws.String(fmt.Sprintf("bytes=%d-%d", offset, offset+size-1)),
		})
		if copyErr != nil {
			err = wrap("copy part", objectError(copyErr))
			break
		}
		if out.CopyPartResult == nil || aws.ToString(out.CopyPartResult.ETag) == "" {
			err = errors.New("files/s3: copy part returned no ETag")
			break
		}
		parts = append(parts, files.Part{Number: number, ETag: aws.ToString(out.CopyPartResult.ETag), Size: size})
	}
	if err == nil {
		err = s.CompleteMultipart(ctx, destination, id, parts)
	}
	if err != nil {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
		return errors.Join(err, s.AbortMultipart(cleanupCtx, destination, id))
	}
	return nil
}

// CreateMultipart 创建服务端分片会话。
func (s *Store) CreateMultipart(ctx context.Context, key, contentType string) (string, error) {
	if err := validKey(key); err != nil {
		return "", err
	}
	out, err := s.client.CreateMultipartUpload(ctx, &awss3.CreateMultipartUploadInput{Bucket: aws.String(s.bucket), Key: aws.String(key), ContentType: optional(contentType)}, etagProtocol)
	if err != nil {
		return "", wrap("create multipart", err)
	}
	if aws.ToString(out.UploadId) == "" {
		return "", errors.New("files/s3: create multipart returned no upload ID")
	}
	return *out.UploadId, nil
}

// UploadPart 校验长度后上传一片；同一编号的重传替换旧片。
func (s *Store) UploadPart(ctx context.Context, key, uploadID string, number int32, body io.Reader, size int64) (files.Part, error) {
	if err := validUpload(key, uploadID); err != nil {
		return files.Part{}, err
	}
	if err := validPart(number, size); err != nil {
		return files.Part{}, err
	}
	f, err := spool(ctx, body, size)
	if err != nil {
		return files.Part{}, err
	}
	defer removeSpool(f)
	out, err := s.client.UploadPart(ctx, &awss3.UploadPartInput{Bucket: aws.String(s.bucket), Key: aws.String(key), UploadId: aws.String(uploadID), PartNumber: aws.Int32(number), Body: f, ContentLength: aws.Int64(size)}, etagProtocol)
	if err != nil {
		return files.Part{}, wrap("upload part", uploadError(err))
	}
	if aws.ToString(out.ETag) == "" {
		return files.Part{}, errors.New("files/s3: upload part returned no ETag")
	}
	return files.Part{Number: number, ETag: *out.ETag, Size: size}, nil
}

// ListParts 分页读取全部已上传分片，供客户端恢复进度。
func (s *Store) ListParts(ctx context.Context, key, uploadID string) ([]files.Part, error) {
	if err := validUpload(key, uploadID); err != nil {
		return nil, err
	}
	var parts []files.Part
	var marker *string
	for {
		out, err := s.client.ListParts(ctx, &awss3.ListPartsInput{Bucket: aws.String(s.bucket), Key: aws.String(key), UploadId: aws.String(uploadID), PartNumberMarker: marker})
		if err != nil {
			return nil, wrap("list parts", uploadError(err))
		}
		for _, p := range out.Parts {
			parts = append(parts, files.Part{Number: aws.ToInt32(p.PartNumber), ETag: aws.ToString(p.ETag), Size: aws.ToInt64(p.Size)})
		}
		if len(parts) > files.MaxParts {
			return nil, errors.New("files/s3: too many parts in response")
		}
		if !aws.ToBool(out.IsTruncated) {
			return parts, nil
		}
		next, parseErr := strconv.ParseInt(aws.ToString(out.NextPartNumberMarker), 10, 32)
		previous, _ := strconv.ParseInt(aws.ToString(marker), 10, 32)
		if parseErr != nil || next <= previous || next > files.MaxParts {
			return nil, errors.New("files/s3: invalid list parts pagination")
		}
		marker = out.NextPartNumberMarker
	}
}

// CompleteMultipart 排序并验证清单后合并；ETag 和分片实际存在与否由 S3 再校验。
func (s *Store) CompleteMultipart(ctx context.Context, key, uploadID string, parts []files.Part) error {
	if err := validUpload(key, uploadID); err != nil {
		return err
	}
	if len(parts) == 0 || len(parts) > files.MaxParts {
		return files.ErrInvalid
	}
	parts = slices.Clone(parts)
	slices.SortFunc(parts, func(a, b files.Part) int { return int(a.Number) - int(b.Number) })
	completed := make([]types.CompletedPart, len(parts))
	for i, p := range parts {
		if err := validPart(p.Number, p.Size); err != nil {
			return err
		}
		if strings.TrimSpace(p.ETag) == "" || (i > 0 && p.Number == parts[i-1].Number) || (i < len(parts)-1 && p.Size < files.MinPartSize) {
			return files.ErrInvalid
		}
		completed[i] = types.CompletedPart{PartNumber: aws.Int32(p.Number), ETag: aws.String(p.ETag)}
	}
	_, err := s.client.CompleteMultipartUpload(ctx, &awss3.CompleteMultipartUploadInput{Bucket: aws.String(s.bucket), Key: aws.String(key), UploadId: aws.String(uploadID), MultipartUpload: &types.CompletedMultipartUpload{Parts: completed}}, etagProtocol)
	return wrap("complete multipart", uploadError(err))
}

// AbortMultipart 中止会话；NoSuchUpload 表示清理已完成。
func (s *Store) AbortMultipart(ctx context.Context, key, uploadID string) error {
	if err := validUpload(key, uploadID); err != nil {
		return err
	}
	_, err := s.client.AbortMultipartUpload(ctx, &awss3.AbortMultipartUploadInput{Bucket: aws.String(s.bucket), Key: aws.String(key), UploadId: aws.String(uploadID)})
	if apiCode(err) == "NoSuchUpload" {
		return nil
	}
	return wrap("abort multipart", err)
}

// SignPut 签发单次上传地址，绑定声明的长度和类型。
func (s *Store) SignPut(ctx context.Context, key, contentType string, size int64, ttl time.Duration) (files.SignedRequest, error) {
	if err := validKey(key); err != nil {
		return files.SignedRequest{}, err
	}
	if err := validTTL(ttl); err != nil {
		return files.SignedRequest{}, err
	}
	if size < 0 || size > files.MaxPartSize {
		return files.SignedRequest{}, files.ErrInvalid
	}
	out, err := s.presigner.PresignPutObject(ctx, &awss3.PutObjectInput{Bucket: aws.String(s.bucket), Key: aws.String(key), ContentType: optional(contentType), ContentLength: aws.Int64(size)}, signOptions(ttl))
	return signed(out, err)
}

// SignPart 签发单片上传地址，绑定会话、编号和声明长度。
func (s *Store) SignPart(ctx context.Context, key, uploadID string, number int32, size int64, ttl time.Duration) (files.SignedRequest, error) {
	if err := validUpload(key, uploadID); err != nil {
		return files.SignedRequest{}, err
	}
	if err := validPart(number, size); err != nil {
		return files.SignedRequest{}, err
	}
	if err := validTTL(ttl); err != nil {
		return files.SignedRequest{}, err
	}
	out, err := s.presigner.PresignUploadPart(ctx, &awss3.UploadPartInput{Bucket: aws.String(s.bucket), Key: aws.String(key), UploadId: aws.String(uploadID), PartNumber: aws.Int32(number), ContentLength: aws.Int64(size)}, signOptions(ttl))
	return signed(out, err)
}

// SignGet 为私有对象签发临时读取地址。
func (s *Store) SignGet(ctx context.Context, key string, ttl time.Duration) (files.SignedRequest, error) {
	if err := validKey(key); err != nil {
		return files.SignedRequest{}, err
	}
	if err := validTTL(ttl); err != nil {
		return files.SignedRequest{}, err
	}
	out, err := s.presigner.PresignGetObject(ctx, &awss3.GetObjectInput{Bucket: aws.String(s.bucket), Key: aws.String(key)}, signOptions(ttl))
	return signed(out, err)
}

func signOptions(ttl time.Duration) func(*awss3.PresignOptions) {
	return func(o *awss3.PresignOptions) {
		o.Expires = ttl
		o.ClientOptions = append(o.ClientOptions, etagProtocol)
	}
}

func signed(out *awsv4.PresignedHTTPRequest, err error) (files.SignedRequest, error) {
	if err != nil {
		return files.SignedRequest{}, wrap("presign", err)
	}
	return files.SignedRequest{URL: out.URL, Method: out.Method, Headers: out.SignedHeader.Clone()}, nil
}

func validTTL(ttl time.Duration) error {
	if ttl < time.Second || ttl > 7*24*time.Hour {
		return fmt.Errorf("files/s3: signature TTL must be between one second and seven days: %w", files.ErrInvalid)
	}
	return nil
}

func validKey(key string) error {
	if key == "" || len(key) > 1024 || strings.ContainsRune(key, 0) {
		return fmt.Errorf("files/s3: invalid key: %w", files.ErrInvalid)
	}
	return nil
}

func validUpload(key, id string) error {
	if err := validKey(key); err != nil {
		return err
	}
	if strings.TrimSpace(id) == "" {
		return files.ErrInvalid
	}
	return nil
}

func validPart(number int32, size int64) error {
	if number < 1 || number > files.MaxParts || size < 0 || size > files.MaxPartSize {
		return files.ErrInvalid
	}
	return nil
}

func optional(value string) *string {
	if value == "" {
		return nil
	}
	return aws.String(value)
}

func wrap(op string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("files/s3: %s: %w", op, err)
}

func apiCode(err error) string {
	var e smithy.APIError
	if errors.As(err, &e) {
		return e.ErrorCode()
	}
	return ""
}

func objectError(err error) error {
	switch apiCode(err) {
	case "NoSuchKey":
		return errors.Join(fs.ErrNotExist, err)
	default:
		return err
	}
}

func uploadError(err error) error {
	if apiCode(err) == "NoSuchUpload" {
		return errors.Join(files.ErrUploadNotFound, err)
	}
	return err
}

// spool 用磁盘保存恰好 size 字节，并多读一字节检查溢出；不把任意请求体整体读入内存。
func spool(ctx context.Context, body io.Reader, size int64) (*os.File, error) {
	if body == nil || size < 0 {
		return nil, files.ErrInvalid
	}
	f, err := os.CreateTemp("", "mygoravel-s3-*")
	if err != nil {
		return nil, wrap("create upload spool", err)
	}
	n, err := io.Copy(f, io.LimitReader(contextReader{ctx: ctx, r: body}, size+1))
	if err == nil {
		err = ctx.Err()
	}
	if err == nil && n != size {
		err = fmt.Errorf("files/s3: expected %d bytes, received %d: %w", size, n, files.ErrInvalid)
	}
	if err == nil {
		_, err = f.Seek(0, io.SeekStart)
	}
	if err != nil {
		removeSpool(f)
		return nil, wrap("read upload", err)
	}
	return f, nil
}

func removeSpool(f *os.File) { name := f.Name(); _ = f.Close(); _ = os.Remove(name) }

type contextReader struct {
	ctx context.Context
	r   io.Reader
}

func (r contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.r.Read(p)
}
