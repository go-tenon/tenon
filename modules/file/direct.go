package file

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"path"
	"strings"
	"time"
	"uuid"

	"github.com/uptrace/bun"

	"github.com/ryan053857/go-kit/storage"
)

// attachDeadline 返回新上传文件的 ExpiresAt：开启 RequireAttach 时为 AttachTTL 之后，否则为零值（永久）。
func (s *Service) attachDeadline() time.Time {
	if !s.cfg.RequireAttach {
		return time.Time{}
	}
	return time.Now().Add(s.cfg.AttachTTL)
}

// Upload 是客户端直传的上传请求。
type Upload struct {
	URL       string
	Method    string // 固定为 PUT
	Header    map[string]string
	ExpiresAt time.Time
}

// CreateUpload 为客户端直传创建一个待上传的文件，返回预签名的上传请求（15 分钟内有效）。
// contentType 为客户端声明的类型，上传完成后会按内容重新识别。
// 存储不支持直传时返回 ErrNoDirectUpload；超过 MaxDirectSize 时返回 ErrTooLarge；
// 声明的类型不在 AllowedTypes 中时返回 ErrTypeNotAllowed。
func (s *Service) CreateUpload(ctx context.Context, userID uuid.UUID, filename, contentType string, size int64) (*File, *Upload, error) {
	p, ok := s.store.(storage.Presigner)
	if !ok {
		return nil, nil, ErrNoDirectUpload
	}
	if size <= 0 || size > s.cfg.MaxDirectSize {
		return nil, nil, ErrTooLarge
	}
	filename = cleanFilename(filename)
	ext := strings.ToLower(path.Ext(filename))
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	if !s.allowed(contentType) {
		return nil, nil, ErrTypeNotAllowed
	}
	f := &File{UserID: userID, Filename: filename, ContentType: contentType, Size: size, Status: StatusPending,
		ExpiresAt: time.Now().Add(pendingTTL)}
	f.ID = uuid.NewV7()
	f.Key = "files/" + f.ID.String() + ext
	put, err := p.PresignPut(ctx, f.Key, contentType, size, presignTTL)
	if err != nil {
		return nil, nil, err
	}
	if _, err := s.db.NewInsert().Model(f).Exec(ctx); err != nil {
		return nil, nil, fmt.Errorf("file: create upload: %w", err)
	}
	up := &Upload{URL: put.URL, Method: "PUT", Header: map[string]string{}, ExpiresAt: put.ExpiresAt}
	for k := range put.Header {
		up.Header[k] = put.Header.Get(k)
	}
	return f, up, nil
}

// CompleteUpload 在客户端直传完成后调用：确认对象已上传且大小与声明一致，按内容重新识别类型，
// 把文件标记为可用。userID 必须是创建者。文件不存在时返回 ErrNotFound；
// 对象未上传或大小不符时返回 ErrUploadIncomplete；识别出的类型不允许时删除文件并返回 ErrTypeNotAllowed。
// 已完成的文件再次调用时直接返回。
func (s *Service) CompleteUpload(ctx context.Context, userID, id uuid.UUID) (*File, error) {
	f := new(File)
	err := s.db.NewSelect().Model(f).Where("id = ? AND user_id = ?", id, userID).Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("file: complete upload: %w", err)
	}
	if f.Status == StatusReady {
		return f, nil
	}
	p, ok := s.store.(storage.Presigner)
	if !ok {
		return nil, ErrNoDirectUpload
	}
	size, err := p.Size(ctx, f.Key)
	if errors.Is(err, storage.ErrNotFound) || (err == nil && size != f.Size) {
		return nil, ErrUploadIncomplete
	}
	if err != nil {
		return nil, err
	}
	rc, err := s.store.Get(ctx, f.Key)
	if err != nil {
		return nil, err
	}
	head := make([]byte, 512)
	n, _ := io.ReadFull(rc, head)
	_ = rc.Close()
	contentType := detectType(head[:n], strings.ToLower(path.Ext(f.Filename)))
	if !s.allowed(contentType) {
		if err := s.Delete(ctx, f.ID); err != nil {
			return nil, err
		}
		return nil, ErrTypeNotAllowed
	}

	f.ContentType, f.Status, f.ExpiresAt = contentType, StatusReady, s.attachDeadline()
	_, err = s.db.NewUpdate().Model(f).Column("content_type", "status", "expires_at", "updated_at").
		Where("id = ? AND status = ?", f.ID, StatusPending).Exec(ctx)
	if err != nil {
		return nil, fmt.Errorf("file: complete upload: %w", err)
	}
	return f, nil
}

// Attach 把文件标记为已被业务数据引用，不再被 Cleanup 删除（开启 RequireAttach 时使用）。
// 应在保存业务数据的同一事务中调用；db 可以是 bun.Tx。任一文件不存在、未上传完成或已过期时返回 ErrNotFound。
func (s *Service) Attach(ctx context.Context, db bun.IDB, ids ...uuid.UUID) error {
	if len(ids) == 0 {
		return nil
	}
	res, err := db.NewUpdate().Model((*File)(nil)).Set("expires_at = NULL").
		Where("id IN (?) AND status = ? AND (expires_at IS NULL OR expires_at > ?)", bun.List(ids), StatusReady, time.Now()).
		Exec(ctx)
	if err != nil {
		return fmt.Errorf("file: attach: %w", err)
	}
	if n, _ := res.RowsAffected(); int(n) != len(ids) {
		return ErrNotFound
	}
	return nil
}

// Cleanup 删除已过期的文件（未完成的直传、未被 Attach 的上传），返回删除的数量。
// 由产品的定时任务定期调用，例如每小时一次。
func (s *Service) Cleanup(ctx context.Context) (int, error) {
	total := 0
	for {
		var files []File
		err := s.db.NewSelect().Model(&files).Column("id", "key").
			Where("expires_at < ?", time.Now()).Limit(500).Scan(ctx)
		if err != nil {
			return total, fmt.Errorf("file: cleanup: %w", err)
		}
		deleted := 0
		for _, f := range files {
			if err := s.store.Delete(ctx, f.Key); err != nil {
				slog.WarnContext(ctx, "cleanup file content", "file_id", f.ID, "key", f.Key, "err", err)
				continue // 留到下次重试
			}
			if _, err := s.db.NewDelete().Model((*File)(nil)).Where("id = ?", f.ID).Exec(ctx); err != nil {
				return total, fmt.Errorf("file: cleanup: %w", err)
			}
			deleted++
		}
		total += deleted
		if len(files) < 500 || deleted == 0 {
			return total, nil
		}
	}
}
