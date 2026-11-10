package files

import (
	"context"
	"io"
	"io/fs"
	"slices"
	"time"

	"github.com/cockroachdb/errors"
	"github.com/google/uuid"

	"github.com/ryan053857/mygoravel/identity"
)

// CreateMultipart 创建或返回当前会话；一个文件同时只有一个有效会话。
func (s *Service) CreateMultipart(ctx context.Context, owner identity.Subject, id uuid.UUID) (Record, error) {
	var created string
	var backend Backend
	r, err := s.withFile(ctx, owner, id, true, false, func(r *Record, b Backend) error {
		if err := pending(r); err != nil {
			return err
		}
		if err := claimUploadMode(r, UploadModeMultipart); err != nil {
			return err
		}
		if r.UploadID != "" {
			return nil
		}
		if _, err := b.Stat(ctx, r.Key); err == nil {
			return ErrConflict
		} else if !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		uploadID, err := b.CreateMultipart(ctx, r.Key, r.ContentType)
		if err != nil {
			return err
		}
		if uploadID == "" {
			return errors.New("files: storage returned empty upload ID")
		}
		r.UploadID, created, backend = uploadID, uploadID, b
		return nil
	})
	if err != nil && created != "" {
		// COMMIT 的网络错误不一定表示事务回滚，确认未登记后才清理新会话。
		cleanCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()
		_, cleanErr := s.withFile(cleanCtx, owner, id, true, true, func(current *Record, _ Backend) error {
			if current.UploadID == created {
				return nil
			}
			return backend.AbortMultipart(cleanCtx, uploadKey(id), created)
		})
		err = errors.Join(err, cleanErr)
	}
	return r, err
}

// UploadPart 上传指定分片。大小按文件记录计算，重复编号原子替换旧分片。
func (s *Service) UploadPart(ctx context.Context, owner identity.Subject, id uuid.UUID, uploadID string, number int32, body io.Reader) (Part, error) {
	var part Part
	if body == nil {
		return part, ErrInvalid
	}
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
		part, err = b.UploadPart(ctx, r.Key, r.UploadID, number, body, size)
		return err
	})
	return part, err
}

// ListParts 从存储端获取已实际保存的分片，供断点续传使用。
func (s *Service) ListParts(ctx context.Context, owner identity.Subject, id uuid.UUID, uploadID string) ([]Part, error) {
	var parts []Part
	_, err := s.withFile(ctx, owner, id, false, false, func(r *Record, b Backend) error {
		if err := pending(r); err != nil {
			return err
		}
		if r.UploadMode != UploadModeMultipart || uploadID == "" || r.UploadID != uploadID {
			return ErrConflict
		}
		var err error
		parts, err = b.ListParts(ctx, r.Key, r.UploadID)
		return err
	})
	return parts, err
}

// CompleteMultipart 核对编号、ETag、实际分片大小后合并；不接受缺片或重复编号。
// parts 应使用上传响应保存的 ETag；Size 字段不作为可信输入。
func (s *Service) CompleteMultipart(ctx context.Context, owner identity.Subject, id uuid.UUID, uploadID string, parts []Part) (Record, error) {
	if len(parts) > MaxParts {
		return Record{}, ErrInvalid
	}
	parts = slices.Clone(parts)
	slices.SortFunc(parts, func(a, b Part) int { return int(a.Number) - int(b.Number) })
	return s.withFile(ctx, owner, id, true, false, func(r *Record, b Backend) error {
		if r.UploadMode != UploadModeMultipart || uploadID == "" || r.UploadID != uploadID {
			return ErrConflict
		}
		if r.UploadedAt != nil {
			return nil
		}
		if r.Status != StatusTemporary {
			return ErrConflict
		}
		if len(parts) != partCount(r) {
			return errors.Wrap(ErrInvalid, "incorrect part count")
		}
		for i, p := range parts {
			if p.Number != int32(i+1) || p.ETag == "" {
				return errors.Wrap(ErrInvalid, "invalid part list")
			}
		}
		actual, err := b.ListParts(ctx, r.Key, r.UploadID)
		if errors.Is(err, ErrUploadNotFound) {
			// 上次合并成功但事务提交或响应失败时，唯一上传键上的完整对象可用于恢复。
			if err := uploaded(ctx, r, b); err != nil {
				return err
			}
			return nil
		}
		if err != nil {
			return err
		}
		if len(actual) != len(parts) {
			return errors.Wrap(ErrInvalid, "stored part count differs")
		}
		slices.SortFunc(actual, func(a, b Part) int { return int(a.Number) - int(b.Number) })
		for i, p := range actual {
			size, err := expectedPartSize(r, p.Number)
			if err != nil {
				return err
			}
			if p.Number != parts[i].Number || p.ETag != parts[i].ETag || p.Size != size {
				return errors.Wrap(ErrInvalid, "stored part differs")
			}
		}
		if err := b.CompleteMultipart(ctx, r.Key, r.UploadID, actual); err != nil {
			return err
		}
		if err := uploaded(ctx, r, b); err != nil {
			return err
		}
		return nil
	})
}

// AbortMultipart 中止当前分片会话，保留临时文件记录以便重新创建会话。
func (s *Service) AbortMultipart(ctx context.Context, owner identity.Subject, id uuid.UUID, uploadID string) error {
	if uploadID == "" {
		return ErrInvalid
	}
	_, err := s.withFile(ctx, owner, id, true, false, func(r *Record, b Backend) error {
		if err := pending(r); err != nil {
			return err
		}
		if r.UploadMode != UploadModeMultipart {
			return ErrConflict
		}
		if r.UploadID == "" {
			return nil
		}
		if r.UploadID != uploadID {
			return ErrConflict
		}
		if err := b.AbortMultipart(ctx, r.Key, r.UploadID); err != nil {
			return err
		}
		// 清除可能在上次 Complete 后未能提交数据库状态的对象。
		if err := b.Delete(ctx, r.Key); err != nil {
			return err
		}
		r.UploadID = ""
		return nil
	})
	return err
}

func partCount(r *Record) int {
	if r.Size == 0 {
		return 1
	}
	return int((r.Size-1)/r.PartSize + 1)
}

func expectedPartSize(r *Record, number int32) (int64, error) {
	if number < 1 || int(number) > partCount(r) || number > MaxParts {
		return 0, errors.Wrap(ErrInvalid, "invalid part number")
	}
	return min(r.PartSize, r.Size-int64(number-1)*r.PartSize), nil
}
