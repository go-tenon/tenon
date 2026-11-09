package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
)

// S3 把对象保存在 S3 兼容的存储桶中。
type S3 struct {
	client *s3.Client
	bucket string
}

func newS3(cfg Config) (*S3, error) {
	c := cfg.S3
	if c.Bucket == "" || c.AccessKeyID == "" || c.SecretAccessKey == "" {
		return nil, errors.New("storage: s3 requires bucket, access_key_id and secret_access_key")
	}
	region := c.Region
	if region == "" {
		region = "auto"
	}
	opts := s3.Options{
		Region:       region,
		Credentials:  credentials.NewStaticCredentialsProvider(c.AccessKeyID, string(c.SecretAccessKey), ""),
		UsePathStyle: c.PathStyle,
		// 只在必须时计算与校验校验和：部分 S3 兼容服务不支持 SDK 默认附加的 CRC 校验。
		RequestChecksumCalculation: aws.RequestChecksumCalculationWhenRequired,
		ResponseChecksumValidation: aws.ResponseChecksumValidationWhenRequired,
	}
	if c.Endpoint != "" {
		opts.BaseEndpoint = aws.String(c.Endpoint)
	}
	return &S3{client: s3.New(opts), bucket: c.Bucket}, nil
}

func (s *S3) Put(ctx context.Context, key string, r io.ReadSeeker, contentType string) error {
	if err := checkKey(key); err != nil {
		return err
	}
	size, err := r.Seek(0, io.SeekEnd)
	if err != nil {
		return fmt.Errorf("storage: %w", err)
	}
	if _, err := r.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("storage: %w", err)
	}
	in := &s3.PutObjectInput{
		Bucket:        aws.String(s.bucket),
		Key:           aws.String(key),
		Body:          r,
		ContentLength: aws.Int64(size),
	}
	if contentType != "" {
		in.ContentType = aws.String(contentType)
	}
	if _, err := s.client.PutObject(ctx, in); err != nil {
		return fmt.Errorf("storage: put %s: %w", key, err)
	}
	return nil
}

func (s *S3) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	if err := checkKey(key); err != nil {
		return nil, err
	}
	out, err := s.client.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(s.bucket), Key: aws.String(key)})
	if err != nil {
		if isNotFound(err) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("storage: get %s: %w", key, err)
	}
	return out.Body, nil
}

func (s *S3) Delete(ctx context.Context, key string) error {
	if err := checkKey(key); err != nil {
		return err
	}
	_, err := s.client.DeleteObject(ctx, &s3.DeleteObjectInput{Bucket: aws.String(s.bucket), Key: aws.String(key)})
	if err != nil && !isNotFound(err) {
		return fmt.Errorf("storage: delete %s: %w", key, err)
	}
	return nil
}

// PresignPut 实现 Presigner。
func (s *S3) PresignPut(ctx context.Context, key, contentType string, size int64, expires time.Duration) (*PresignedPut, error) {
	if err := checkKey(key); err != nil {
		return nil, err
	}
	in := &s3.PutObjectInput{Bucket: aws.String(s.bucket), Key: aws.String(key), ContentLength: aws.Int64(size)}
	if contentType != "" {
		in.ContentType = aws.String(contentType)
	}
	req, err := s3.NewPresignClient(s.client).PresignPutObject(ctx, in, s3.WithPresignExpires(expires))
	if err != nil {
		return nil, fmt.Errorf("storage: presign %s: %w", key, err)
	}
	h := http.Header{}
	for k, v := range req.SignedHeader {
		if k != "Host" {
			h[k] = v
		}
	}
	return &PresignedPut{URL: req.URL, Header: h, ExpiresAt: time.Now().Add(expires)}, nil
}

// Size 实现 Presigner。
func (s *S3) Size(ctx context.Context, key string) (int64, error) {
	if err := checkKey(key); err != nil {
		return 0, err
	}
	out, err := s.client.HeadObject(ctx, &s3.HeadObjectInput{Bucket: aws.String(s.bucket), Key: aws.String(key)})
	if err != nil {
		if isNotFound(err) {
			return 0, ErrNotFound
		}
		return 0, fmt.Errorf("storage: head %s: %w", key, err)
	}
	return aws.ToInt64(out.ContentLength), nil
}

func isNotFound(err error) bool {
	var nsk *types.NoSuchKey
	if errors.As(err, &nsk) {
		return true
	}
	var apiErr smithy.APIError
	if errors.As(err, &apiErr) {
		switch apiErr.ErrorCode() {
		case "NoSuchKey", "NotFound":
			return true
		}
	}
	return false
}
