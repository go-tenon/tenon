package files

import (
	"context"
	"database/sql"
	"io"
	"path"
	"strings"
	"time"

	"github.com/cockroachdb/errors"
	"github.com/google/uuid"
	"github.com/uptrace/bun"

	"github.com/ryan053857/mygoravel/identity"
)

// Service 管理单张 files 表和显式传入的存储。DB 必须是已完成迁移的 PostgreSQL。
// 方法自行管理事务和行锁；存储操作不能随业务数据库事务一起回滚。
type Service struct {
	db    *bun.DB
	disks map[string]Backend
	cfg   Config
}

// New 创建文件服务；不会自动迁移，也不会打开或关闭传入的存储。
func New(db *bun.DB, disks map[string]Backend, cfg Config) (*Service, error) {
	if db == nil {
		return nil, errors.New("files: database is required")
	}
	if err := cfg.Validate(); err != nil {
		return nil, errors.Wrap(err, "files")
	}
	copyDisks := make(map[string]Backend, len(disks))
	for name, disk := range disks {
		if strings.TrimSpace(name) == "" || disk == nil {
			return nil, errors.New("files: disk name and backend are required")
		}
		copyDisks[name] = disk
	}
	if copyDisks[cfg.DefaultDisk] == nil {
		return nil, errors.New("files: default disk is not configured")
	}
	return &Service{db: db, disks: copyDisks, cfg: cfg}, nil
}

// Prepare 登记临时文件并生成上传对象键；客户端声明大小会在完成上传时核验。
func (s *Service) Prepare(ctx context.Context, owner identity.Subject, in PrepareInput) (Record, error) {
	if owner.ID == "" || owner.Type == "" || in.Size < 0 {
		return Record{}, ErrInvalid
	}
	if in.Size > s.cfg.MaxUploadBytes {
		return Record{}, ErrTooLarge
	}
	if in.Disk == "" {
		in.Disk = s.cfg.DefaultDisk
	}
	if s.disks[in.Disk] == nil {
		return Record{}, errors.Wrap(ErrInvalid, "unknown disk")
	}
	name := path.Base(strings.ReplaceAll(strings.TrimSpace(in.Filename), "\\", "/"))
	if name == "" || name == "." || name == ".." || name == "/" || strings.ContainsRune(name, 0) {
		return Record{}, ErrInvalid
	}
	if in.ContentType == "" {
		in.ContentType = "application/octet-stream"
	}
	if strings.ContainsAny(in.ContentType, "\r\n\x00") {
		return Record{}, ErrInvalid
	}
	id, err := uuid.NewV7()
	if err != nil {
		return Record{}, err
	}
	now := time.Now().UTC()
	expiry := now.Add(s.cfg.TempTTL)
	r := Record{ID: id, Disk: in.Disk, Key: uploadKey(id), Filename: name, ContentType: in.ContentType, Size: in.Size,
		OwnerID: owner.ID, OwnerType: string(owner.Type), Status: StatusTemporary, PartSize: s.cfg.PartSize,
		ExpiresAt: &expiry, CreatedAt: now, UpdatedAt: now}
	_, err = s.db.NewInsert().Model(&r).Returning("id").Exec(ctx)
	return r, errors.Wrap(err, "files: prepare")
}

// Get 返回当前主体拥有且未删除、未过期的文件记录。
func (s *Service) Get(ctx context.Context, owner identity.Subject, id uuid.UUID) (Record, error) {
	var r Record
	if owner.ID == "" || owner.Type == "" || id == uuid.Nil {
		return r, ErrInvalid
	}
	err := s.db.NewSelect().Model(&r).Where("f.id = ?", id).Where("owner_id = ?", owner.ID).Where("owner_type = ?", string(owner.Type)).Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && unavailable(&r)) {
		return Record{}, ErrNotFound
	}
	return r, errors.Wrap(err, "files: get")
}

// Put 通过应用服务器上传整个文件。分片会话存在或已完成上传时拒绝覆盖。
func (s *Service) Put(ctx context.Context, owner identity.Subject, id uuid.UUID, body io.Reader) (Record, error) {
	if body == nil {
		return Record{}, ErrInvalid
	}
	return s.withFile(ctx, owner, id, true, false, func(r *Record, b Backend) error {
		if err := pending(r); err != nil {
			return err
		}
		if err := claimUploadMode(r, UploadModeSingle); err != nil {
			return err
		}
		if err := b.Put(ctx, r.Key, r.ContentType, body, r.Size); err != nil {
			return err
		}
		return uploaded(ctx, r, b)
	})
}

// Complete 核验单次直传的实际大小。它只结束上传，业务使用文件时再调用 Confirm。
func (s *Service) Complete(ctx context.Context, owner identity.Subject, id uuid.UUID) (Record, error) {
	return s.withFile(ctx, owner, id, true, false, func(r *Record, b Backend) error {
		if r.UploadedAt != nil && r.UploadMode == UploadModeSingle {
			return nil
		}
		if r.Status != StatusTemporary || r.UploadID != "" {
			return ErrConflict
		}
		if err := claimUploadMode(r, UploadModeSingle); err != nil {
			return err
		}
		return uploaded(ctx, r, b)
	})
}

// Confirm 将已上传文件复制到正式键并标为 ready，重复确认返回原记录。
// 原上传对象留到会话过期后由 Cleanup 回收，避免仍有效的直传 URL 改写正式对象。
func (s *Service) Confirm(ctx context.Context, owner identity.Subject, id uuid.UUID) (Record, error) {
	return s.withFile(ctx, owner, id, true, false, func(r *Record, b Backend) error {
		if r.Status == StatusReady {
			return nil
		}
		if r.UploadedAt == nil {
			return ErrConflict
		}
		if err := uploaded(ctx, r, b); err != nil {
			return err
		}
		key := readyKey(r.ID)
		if err := b.Copy(ctx, r.Key, key); err != nil {
			return err
		}
		info, err := b.Stat(ctx, key)
		if err != nil {
			return err
		}
		if info.Size != r.Size {
			return errors.Wrap(ErrInvalid, "copied object size differs")
		}
		r.Key, r.Status = key, StatusReady
		return nil
	})
}

// Open 打开已确认文件。调用方负责关闭返回的流。
func (s *Service) Open(ctx context.Context, owner identity.Subject, id uuid.UUID) (io.ReadCloser, Record, error) {
	var body io.ReadCloser
	r, err := s.withFile(ctx, owner, id, false, false, func(r *Record, b Backend) error {
		if r.Status != StatusReady {
			return ErrConflict
		}
		var err error
		body, err = b.Open(ctx, r.Key)
		return err
	})
	if err != nil && body != nil {
		_ = body.Close()
		body = nil
	}
	return body, r, err
}

// Delete 删除文件和分片会话。重复删除同一主体的记录是幂等的。
// 尚未到期的会话保留回收时间，Cleanup 会再次删除可能被旧直传 URL 重建的上传对象。
func (s *Service) Delete(ctx context.Context, owner identity.Subject, id uuid.UUID) error {
	// 先提交删除意图。后续存储或数据库操作失败时，文件不会再次可见，清理任务可重试。
	_, err := s.withRecord(ctx, owner, id, true, true, func(r *Record) error {
		r.Status = StatusDeleted
		now := time.Now().UTC()
		if r.ExpiresAt == nil || r.ExpiresAt.Before(now) {
			r.ExpiresAt = &now
		}
		return nil
	})
	if err != nil {
		return err
	}
	_, err = s.withFile(ctx, owner, id, true, true, func(r *Record, b Backend) error {
		if r.Status != StatusDeleted {
			return ErrConflict
		}
		if err := removeObjects(ctx, r, b, true); err != nil {
			return err
		}
		if r.ExpiresAt != nil && !r.ExpiresAt.After(time.Now()) {
			r.ExpiresAt = nil
		}
		return nil
	})
	return err
}

// Cleanup 分批回收过期会话和上传对象。limit 是本次最多处理的记录数。
// ready 记录只清上传对象；temporary/deleted 同时清正式对象。失败保留到期时间供重试，
// 并更新尝试时间，使后续调用优先处理尚未尝试的记录。
func (s *Service) Cleanup(ctx context.Context, limit int) (int, error) {
	if limit <= 0 {
		return 0, ErrInvalid
	}
	var rows []Record
	now := time.Now().UTC()
	if err := s.db.NewSelect().Model(&rows).Where("expires_at <= ?", now).Order("updated_at", "id").Limit(int64(limit)).Scan(ctx); err != nil {
		return 0, errors.Wrap(err, "files: cleanup candidates")
	}
	done := 0
	var errs []error
	for _, row := range rows {
		cleaned := false
		attempted, err := s.withFile(ctx, identity.Subject{ID: row.OwnerID, Type: identity.SubjectType(row.OwnerType)}, row.ID, true, true, func(r *Record, b Backend) error {
			if r.ExpiresAt == nil || r.ExpiresAt.After(now) {
				return nil
			}
			if err := removeObjects(ctx, r, b, r.Status != StatusReady); err != nil {
				return err
			}
			if r.Status == StatusTemporary {
				r.Status = StatusDeleted
			}
			r.ExpiresAt = nil
			cleaned = true
			return nil
		})
		if err != nil {
			errs = append(errs, errors.Wrapf(err, "cleanup %s", row.ID))
			if attempted.ID == row.ID {
				// 原事务已回滚。只移动仍处于同一版本的过期记录，避免覆盖并发操作。
				retryCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
				_, retryErr := s.db.NewUpdate().Model((*Record)(nil)).Set("updated_at = ?", time.Now().UTC()).
					Where("id = ? AND updated_at = ? AND expires_at <= ?", row.ID, attempted.UpdatedAt, now).Exec(retryCtx)
				cancel()
				if retryErr != nil {
					errs = append(errs, errors.Wrapf(retryErr, "cleanup retry order %s", row.ID))
				}
			}
		} else if cleaned {
			done++
		}
		if ctx.Err() != nil {
			break
		}
	}
	return done, errors.Join(errs...)
}

// withFile 使用行锁协调不同进程对同一文件的操作；分片写入使用共享锁，允许并发。
func (s *Service) withFile(ctx context.Context, owner identity.Subject, id uuid.UUID, write, inactive bool, fn func(*Record, Backend) error) (Record, error) {
	return s.withRecord(ctx, owner, id, write, inactive, func(r *Record) error {
		b := s.disks[r.Disk]
		if b == nil {
			return errors.Newf("files: stored disk %q is not configured", r.Disk)
		}
		return fn(r, b)
	})
}

// withRecord 在数据库事务中锁定记录，允许不依赖存储的状态转换先提交。
func (s *Service) withRecord(ctx context.Context, owner identity.Subject, id uuid.UUID, write, inactive bool, fn func(*Record) error) (Record, error) {
	var r Record
	var loaded Record
	if owner.ID == "" || owner.Type == "" || id == uuid.Nil {
		return r, ErrInvalid
	}
	err := s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		lock := "SHARE"
		if write {
			lock = "UPDATE"
		}
		err := tx.NewSelect().Model(&r).Where("f.id = ?", id).Where("owner_id = ?", owner.ID).Where("owner_type = ?", string(owner.Type)).For(lock).Scan(ctx)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		loaded = r
		if !inactive && unavailable(&r) {
			return ErrNotFound
		}
		if err := fn(&r); err != nil {
			return err
		}
		if write && r != loaded {
			r.UpdatedAt = time.Now().UTC()
			_, err = tx.NewUpdate().Model(&r).Column("key", "status", "upload_mode", "upload_id", "uploaded_at", "expires_at", "updated_at").WherePK().Exec(ctx)
		}
		return err
	})
	if err != nil {
		// 失败时返回读取到的版本，供清理任务使用 CAS 更新尝试时间。
		r = loaded
	}
	return r, errors.Wrap(err, "files: operation")
}

func unavailable(r *Record) bool {
	return r.Status == StatusDeleted || (r.Status == StatusTemporary && (r.ExpiresAt == nil || !r.ExpiresAt.After(time.Now())))
}

func claimUploadMode(r *Record, mode string) error {
	if (r.UploadMode != "" && r.UploadMode != mode) || (mode == UploadModeSingle && r.UploadID != "") {
		return ErrConflict
	}
	r.UploadMode = mode
	return nil
}

func pending(r *Record) error {
	if r.Status != StatusTemporary || r.UploadedAt != nil {
		return ErrConflict
	}
	return nil
}

func uploaded(ctx context.Context, r *Record, b Backend) error {
	info, err := b.Stat(ctx, r.Key)
	if err != nil {
		return err
	}
	if info.Size != r.Size {
		return errors.Wrap(ErrInvalid, "uploaded object size differs")
	}
	now := time.Now().UTC()
	r.UploadedAt = &now
	return nil
}

func uploadKey(id uuid.UUID) string { return "uploads/" + id.String() }
func readyKey(id uuid.UUID) string  { return "objects/" + id.String() }

func removeObjects(ctx context.Context, r *Record, b Backend, all bool) error {
	if r.UploadID != "" {
		if err := b.AbortMultipart(ctx, uploadKey(r.ID), r.UploadID); err != nil {
			return err
		}
	}
	if err := b.Delete(ctx, uploadKey(r.ID)); err != nil {
		return err
	}
	if all {
		if err := b.Delete(ctx, readyKey(r.ID)); err != nil {
			return err
		}
	}
	r.UploadID = ""
	return nil
}
