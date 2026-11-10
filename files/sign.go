package files

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/ryan053857/mygoravel/identity"
)

// SignPut 为单次直传签名；本地存储使用 Put 接收应用 HTTP 请求体。
func (s *Service) SignPut(ctx context.Context, owner identity.Subject, id uuid.UUID) (SignedRequest, error) {
	var signed SignedRequest
	_, err := s.withFile(ctx, owner, id, true, false, func(r *Record, b Backend) error {
		if err := pending(r); err != nil {
			return err
		}
		if err := claimUploadMode(r, UploadModeSingle); err != nil {
			return err
		}
		signer, ok := b.(Signer)
		if !ok {
			return ErrUnsupported
		}
		ttl, err := s.uploadTTL(r)
		if err != nil {
			return err
		}
		signed, err = signer.SignPut(ctx, r.Key, r.ContentType, r.Size, ttl)
		return err
	})
	return signed, err
}

// SignPart 为当前会话的指定分片签名；客户端必须保存响应 ETag 用于完成请求。
func (s *Service) SignPart(ctx context.Context, owner identity.Subject, id uuid.UUID, uploadID string, number int32) (SignedRequest, error) {
	var signed SignedRequest
	_, err := s.withFile(ctx, owner, id, false, false, func(r *Record, b Backend) error {
		if err := pending(r); err != nil {
			return err
		}
		if r.UploadMode != UploadModeMultipart || uploadID == "" || r.UploadID != uploadID {
			return ErrConflict
		}
		size, err := expectedPartSize(r, number)
		if err != nil {
			return err
		}
		signer, ok := b.(Signer)
		if !ok {
			return ErrUnsupported
		}
		ttl, err := s.uploadTTL(r)
		if err != nil {
			return err
		}
		signed, err = signer.SignPart(ctx, r.Key, r.UploadID, number, size, ttl)
		return err
	})
	return signed, err
}

// SignGet 返回已确认文件的临时下载地址；本地存储通过 Open 提供读取。
func (s *Service) SignGet(ctx context.Context, owner identity.Subject, id uuid.UUID) (SignedRequest, error) {
	var signed SignedRequest
	_, err := s.withFile(ctx, owner, id, false, false, func(r *Record, b Backend) error {
		if r.Status != StatusReady {
			return ErrConflict
		}
		signer, ok := b.(Signer)
		if !ok {
			return ErrUnsupported
		}
		var err error
		signed, err = signer.SignGet(ctx, r.Key, s.cfg.SignTTL)
		return err
	})
	return signed, err
}

func (s *Service) uploadTTL(r *Record) (time.Duration, error) {
	if r.ExpiresAt == nil {
		return 0, ErrConflict
	}
	ttl := min(s.cfg.SignTTL, time.Until(*r.ExpiresAt)).Truncate(time.Second)
	if ttl < time.Second {
		return 0, ErrNotFound
	}
	return ttl, nil
}
