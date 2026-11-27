package filesystem

import (
	"context"
	"database/sql"
	"io"
	"io/fs"
	"path"
	"slices"
	"strings"
	"time"

	"github.com/cockroachdb/errors"
	"github.com/google/uuid"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/database"
	"github.com/go-tenon/tenon/identity"
)

const (
	maxObjectSize = 5 * TiB          // S3 单对象上限
	retryDelay    = 5 * time.Minute  // 对象清理失败后的重试间隔
	signTTL       = 15 * time.Minute // 直传签名有效期，签完立即使用
)

// Service 管理 files 表和应用装配的磁盘。存储读写不放在数据库事务里，
// 状态变更都是带条件的单条 UPDATE，多进程并发安全。
type Service struct {
	db    bun.IDB
	disks map[string]Disk
	cfg   Config
}

// New 创建文件服务。db 必须已执行 files 迁移；disks 的键是写入 files.disk 的磁盘名，
// 通常是 local 和 s3，由 cfg.Disks 中对应的配置打开。
func New(db *bun.DB, cfg Config, disks map[string]Disk) (*Service, error) {
	if db == nil {
		return nil, errors.New("filesystem: database is required")
	}
	if err := cfg.validateLimits(); err != nil {
		return nil, errors.Wrap(err, "filesystem")
	}
	copied := make(map[string]Disk, len(disks))
	for name, disk := range disks {
		if strings.TrimSpace(name) == "" || disk == nil {
			return nil, errors.New("filesystem: disk name and disk are required")
		}
		copied[name] = disk
	}
	if copied[cfg.DefaultDisk] == nil {
		return nil, errors.Newf("filesystem: default disk %q is not configured", cfg.DefaultDisk)
	}
	if cfg.PrivateDisk != "" && copied[cfg.PrivateDisk] == nil {
		return nil, errors.Newf("filesystem: private disk %q is not configured", cfg.PrivateDisk)
	}
	return &Service{db: db, disks: copied, cfg: cfg}, nil
}

// DefaultDisk 返回配置中的默认磁盘名。
func (s *Service) DefaultDisk() string { return s.cfg.DefaultDisk }

// PrivateDisk 返回配置中的私有磁盘名，未配置时返回空字符串。
func (s *Service) PrivateDisk() string { return s.cfg.PrivateDisk }

// SignGet 为支持预签名读取的磁盘签发限时访问请求；磁盘不支持时返回 ErrUnsupported。
func (s *Service) SignGet(ctx context.Context, id uuid.UUID, ttl time.Duration) (SignedRequest, error) {
	r, err := s.Get(ctx, id)
	if err != nil {
		return SignedRequest{}, err
	}
	getter, ok := s.disk(r).(SignGetter)
	if !ok {
		return SignedRequest{}, ErrUnsupported
	}
	return getter.SignGet(ctx, r.Key, ttl)
}

// WithDB 返回使用 db（通常是业务事务）的副本，用于让 Confirm、Delete 与业务数据一起提交。
func (s *Service) WithDB(db bun.IDB) *Service {
	cp := *s
	cp.db = db
	return &cp
}

// Prepare 登记一个 temporary 文件并分配对象键，供客户端直传。超过 TempTTL 未确认会被回收。
func (s *Service) Prepare(ctx context.Context, owner identity.Subject, in FileInput) (File, error) {
	if owner.ID == "" || owner.Type == "" || in.Size < 0 {
		return File{}, ErrInvalid
	}
	if in.Size > int64(s.cfg.MaxUploadSize) {
		return File{}, ErrTooLarge
	}
	if in.Disk == "" {
		in.Disk = s.cfg.DefaultDisk
	}
	if s.disks[in.Disk] == nil {
		return File{}, errors.Wrapf(ErrInvalid, "unknown disk %q", in.Disk)
	}
	name := path.Base(strings.ReplaceAll(strings.TrimSpace(in.Filename), "\\", "/"))
	if name == "." || name == "/" || name == ".." || strings.ContainsAny(name, "\x00\r\n") {
		return File{}, errors.Wrap(ErrInvalid, "invalid filename")
	}
	contentType := strings.TrimSpace(in.ContentType)
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	if strings.ContainsAny(contentType, "\x00\r\n") {
		return File{}, errors.Wrap(ErrInvalid, "invalid content type")
	}
	id, err := uuid.NewV7()
	if err != nil {
		return File{}, err
	}
	now := time.Now().UTC()
	expires := now.Add(s.cfg.TempTTL)
	r := File{
		ID: id, Disk: in.Disk, Key: objectKey(id, now, name), Filename: name, ContentType: contentType, Size: in.Size,
		OwnerType: owner.Type, OwnerID: owner.ID, Status: StatusTemporary, ExpiresAt: &expires,
		Timestamps: database.Timestamps{CreatedAt: now},
	}
	if _, err := s.db.NewInsert().Model(&r).Exec(ctx); err != nil {
		return File{}, errors.Wrap(err, "filesystem: prepare")
	}
	return s.withURL(r), nil
}

// Upload 由应用服务器接收正文并写入磁盘，返回 temporary 记录；业务使用时再 Confirm。
// in.Size 为非负数时严格核对正文长度，不符时返回 ErrInvalid；in.Size 小于 0（如 UnknownSize）时
// 流式写入未知长度的正文并限制不超过 max_upload_size，写完后把真实字节数回填到记录中。
// 指定 in.Accept 时在写入后立即校验内容类型、尺寸和体积，不合格时立刻删除对象和记录并返回 ErrNotAccepted。
func (s *Service) Upload(ctx context.Context, owner identity.Subject, in FileInput, body io.Reader) (File, error) {
	if body == nil {
		return File{}, ErrInvalid
	}
	if in.Accept != nil && in.Accept.MaxSize > 0 && in.Size >= 0 && in.Size > int64(in.Accept.MaxSize) {
		return File{}, errors.Wrapf(ErrNotAccepted, "size %d exceeds %s", in.Size, in.Accept.MaxSize)
	}
	var r File
	if in.Size >= 0 {
		var err error
		r, err = s.Prepare(ctx, owner, in)
		if err != nil {
			return File{}, err
		}
		if err := s.disks[r.Disk].Put(ctx, r.Key, r.ContentType, &exactReader{r: body, left: r.Size}, r.Size); err != nil {
			// 磁盘保证失败时不发布对象，记录直接删除；删除失败也会在过期后被回收。
			_, _ = s.db.NewDelete().Model((*File)(nil)).Where("id = ?", r.ID).Exec(context.WithoutCancel(ctx))
			return File{}, errors.Wrap(err, "filesystem: upload")
		}
	} else {
		prep := in
		prep.Size = 0
		var err error
		r, err = s.Prepare(ctx, owner, prep)
		if err != nil {
			return File{}, err
		}
		maxBytes := int64(s.cfg.MaxUploadSize)
		limitByAccept := in.Accept != nil && in.Accept.MaxSize > 0 && int64(in.Accept.MaxSize) < maxBytes
		if limitByAccept {
			maxBytes = int64(in.Accept.MaxSize)
		}
		lr := &limitCounter{r: body, max: maxBytes}
		if err := s.disks[r.Disk].Put(ctx, r.Key, r.ContentType, lr, UnknownSize); err != nil {
			_, _ = s.db.NewDelete().Model((*File)(nil)).Where("id = ?", r.ID).Exec(context.WithoutCancel(ctx))
			if errors.Is(err, ErrTooLarge) {
				if limitByAccept {
					return File{}, errors.Wrapf(ErrNotAccepted, "size exceeds %s", in.Accept.MaxSize)
				}
				return File{}, ErrTooLarge
			}
			return File{}, errors.Wrap(err, "filesystem: upload")
		}
		if lr.n > 0 {
			if _, err := s.db.NewUpdate().Model((*File)(nil)).
				Set("size = ?", lr.n).
				Where("id = ?", r.ID).Exec(ctx); err != nil {
				_ = s.disks[r.Disk].Delete(context.WithoutCancel(ctx), r.Key)
				_, _ = s.db.NewDelete().Model((*File)(nil)).Where("id = ?", r.ID).Exec(context.WithoutCancel(ctx))
				return File{}, errors.Wrap(err, "filesystem: update size")
			}
			r.Size = lr.n
		}
	}
	if in.Accept == nil {
		return r, nil
	}
	detected, meta, err := s.validateAndExtractMeta(ctx, r, *in.Accept)
	if err != nil {
		bg := context.WithoutCancel(ctx)
		_ = s.disks[r.Disk].Delete(bg, r.Key)
		_, _ = s.db.NewDelete().Model((*File)(nil)).Where("id = ?", r.ID).Exec(bg)
		return File{}, err
	}
	q := s.db.NewUpdate().Model((*File)(nil)).Where("id = ?", r.ID)
	changed := false
	if detected != "" && mediaType(r.ContentType) == "application/octet-stream" && detected != "application/octet-stream" {
		q = q.Set("content_type = ?", detected)
		r.ContentType = detected
		changed = true
	}
	if meta != (Meta{}) {
		q = q.Set("meta = ?", meta)
		r.Meta = meta
		changed = true
	}
	if changed {
		if _, err := q.Exec(ctx); err != nil {
			bg := context.WithoutCancel(ctx)
			_ = s.disks[r.Disk].Delete(bg, r.Key)
			_, _ = s.db.NewDelete().Model((*File)(nil)).Where("id = ?", r.ID).Exec(bg)
			return File{}, errors.Wrap(err, "filesystem: save detected metadata")
		}
	}
	return r, nil
}

// Confirm 核对对象已完整上传并符合 accept，然后把 temporary 文件转为 ready。重复确认返回原记录，
// 同样要符合 accept。只有上传者本人可以确认，每个临时文件只被它的上传者引用。
// 不符合 accept 时返回 ErrNotAccepted，文件保持 temporary，到期后被 Cleanup 回收。
func (s *Service) Confirm(ctx context.Context, owner identity.Subject, id uuid.UUID, accept Accept) (File, error) {
	r, err := s.owned(ctx, owner, id)
	if err != nil {
		return File{}, err
	}
	if r.Status != StatusReady {
		if err := s.checkUploaded(ctx, r); err != nil {
			return File{}, err
		}
	}
	detected, meta, err := s.validateAndExtractMeta(ctx, r, accept)
	if err != nil {
		return File{}, err
	}
	if r.Status == StatusReady {
		return r, nil
	}
	q := s.db.NewUpdate().Model((*File)(nil)).
		Set("status = ?", StatusReady).Set("expires_at = NULL")
	// 客户端上传时只给了兜底类型（application/octet-stream）且探测出了规范类型时，写回规范类型供业务使用。
	if detected != "" && mediaType(r.ContentType) == "application/octet-stream" && detected != "application/octet-stream" {
		q = q.Set("content_type = ?", detected)
		r.ContentType = detected
	}
	if meta != (Meta{}) {
		q = q.Set("meta = ?", meta)
		r.Meta = meta
	}
	err = q.Where("id = ?", id).Where("status = ?", StatusTemporary).Where("expires_at > ?", time.Now().UTC()).
		Returning("updated_at").Scan(ctx, &r.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		// 并发确认已成功时返回最新记录；被删除或刚好过期则视为不存在。
		r, err = s.owned(ctx, owner, id)
		if err == nil && r.Status != StatusReady {
			err = ErrNotFound
		}
		return r, err
	}
	if err != nil {
		return File{}, errors.Wrap(err, "filesystem: confirm")
	}
	r.Status, r.ExpiresAt = StatusReady, nil
	return r, nil
}

// validateAndExtractMeta 校验文件是否符合 accept，并提取图片的宽高元数据。
func (s *Service) validateAndExtractMeta(ctx context.Context, r File, accept Accept) (string, Meta, error) {
	detected, err := s.checkAccept(ctx, r, accept)
	if err != nil {
		return "", Meta{}, err
	}
	effective := detected
	if effective == "" {
		effective = mediaType(r.ContentType)
	}
	var meta Meta
	if strings.HasPrefix(effective, "image/") {
		w, h, ok := r.Meta.Width, r.Meta.Height, r.Meta.Width > 0 && r.Meta.Height > 0
		if !ok {
			w, h, ok, err = s.readImageDimensions(ctx, r, effective)
			if err != nil {
				return "", Meta{}, err
			}
		}
		if accept.MaxDimension > 0 {
			if !ok || w <= 0 || h <= 0 || w > accept.MaxDimension || h > accept.MaxDimension {
				return "", Meta{}, errors.Wrapf(ErrNotAccepted, "image dimensions %dx%d exceed %d", w, h, accept.MaxDimension)
			}
		}
		if ok && w > 0 && h > 0 {
			meta = Meta{Width: w, Height: h}
		}
	}
	return detected, meta, nil
}

// Get 返回未删除的文件记录。业务授权由应用在调用前检查。
func (s *Service) Get(ctx context.Context, id uuid.UUID) (File, error) {
	var r File
	err := s.db.NewSelect().Model(&r).Where("f.id = ?", id).Where("f.status <> ?", StatusDeleted).Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return File{}, ErrNotFound
	}
	if err != nil {
		return File{}, errors.Wrap(err, "filesystem: get")
	}
	return s.withURL(r), nil
}

// GetMany 批量返回未删除的文件记录，按文件 ID 索引；不存在或已删除的 ID 不在结果中。
// 每条记录按其所在磁盘填好公开地址 URL。业务授权由应用在调用前检查。
func (s *Service) GetMany(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]File, error) {
	out := make(map[uuid.UUID]File, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	var rows []File
	err := s.db.NewSelect().Model(&rows).
		Where("f.id IN (?)", bun.List(ids)).Where("f.status <> ?", StatusDeleted).Scan(ctx)
	if err != nil {
		return nil, errors.Wrap(err, "filesystem: get many")
	}
	for _, r := range rows {
		out[r.ID] = s.withURL(r)
	}
	return out, nil
}

// Open 打开文件正文，供服务端二次处理；对外访问请使用 File.URL。调用方负责关闭。
func (s *Service) Open(ctx context.Context, id uuid.UUID) (io.ReadCloser, File, error) {
	r, err := s.Get(ctx, id)
	if err != nil {
		return nil, File{}, err
	}
	body, err := s.disk(r).Open(ctx, r.Key)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, File{}, ErrNotFound
	}
	return body, r, err
}

// Delete 把文件标为 deleted，之后 Get、Open、Confirm 都视为不存在；文件不存在也返回成功。
// 对象和记录由 Cleanup 删除，因此 Delete 只是一条 UPDATE，可以放进业务事务（WithDB(tx)），
// 事务回滚时文件原样保留。
func (s *Service) Delete(ctx context.Context, id uuid.UUID) error {
	now := time.Now().UTC()
	_, err := s.db.NewUpdate().Model((*File)(nil)).
		Set("status = ?", StatusDeleted).Set("expires_at = ?", now).
		Where("id = ?", id).Where("status <> ?", StatusDeleted).Exec(ctx)
	return errors.Wrap(err, "filesystem: delete")
}

// Cleanup 回收最多 limit 条到期记录：过期未确认的 temporary 文件和已删除的文件，
// 删除它们的对象、未完成的分片会话和记录。返回成功清理的数量；失败的记录推迟重试并合并返回错误。
//
// 它是文件清理唯一的执行入口，设计为由异步队列的定时任务反复调用：每次处理一批，可重入，
// 多个实例同时执行也是安全的。
func (s *Service) Cleanup(ctx context.Context, limit int) (int, error) {
	if limit <= 0 {
		return 0, ErrInvalid
	}
	now := time.Now().UTC()
	leaseUntil := now.Add(retryDelay)
	var rows []File
	// 原子认领一批到期记录并推进租约时间，多实例并发执行时互不重复删除对象存储。
	err := s.db.NewRaw(`
UPDATE files SET status = ?, expires_at = ?, updated_at = ?
WHERE id IN (
    SELECT id FROM files WHERE expires_at <= ? ORDER BY expires_at LIMIT ? FOR UPDATE SKIP LOCKED
)
RETURNING id, created_at, updated_at, disk, key, filename, content_type, size, meta, owner_type, owner_id, status, expires_at`,
		StatusDeleted, leaseUntil, now, now, limit).Scan(ctx, &rows)
	if err != nil {
		return 0, errors.Wrap(err, "filesystem: cleanup")
	}
	done := 0
	var errs []error
	for _, r := range rows {
		if ctx.Err() != nil {
			errs = append(errs, ctx.Err())
			break
		}
		if err := s.purge(ctx, r); err != nil {
			errs = append(errs, errors.Wrapf(err, "cleanup %s", r.ID))
			continue
		}
		done++
	}
	return done, errors.Join(errs...)
}

// purge 删除 deleted 记录的对象和未完成分片会话，再删除记录。
func (s *Service) purge(ctx context.Context, r File) error {
	disk := s.disks[r.Disk]
	if disk == nil {
		return errors.Newf("filesystem: disk %q is not configured", r.Disk)
	}
	if direct, ok := disk.(DirectUploader); ok {
		if err := direct.AbortAllMultipart(ctx, r.Key); err != nil {
			return err
		}
	}
	if err := disk.Delete(ctx, r.Key); err != nil {
		return err
	}
	_, err := s.db.NewDelete().Model((*File)(nil)).Where("id = ?", r.ID).Where("status = ?", StatusDeleted).Exec(ctx)
	return err
}

// owned 返回 owner 上传的可用文件：ready，或尚未过期的 temporary。
func (s *Service) owned(ctx context.Context, owner identity.Subject, id uuid.UUID) (File, error) {
	if owner.ID == "" || owner.Type == "" {
		return File{}, ErrInvalid
	}
	r, err := s.Get(ctx, id)
	if err != nil {
		return File{}, err
	}
	if r.OwnerID != owner.ID || r.OwnerType != owner.Type ||
		(r.Status == StatusTemporary && (r.ExpiresAt == nil || !r.ExpiresAt.After(time.Now()))) {
		return File{}, ErrNotFound
	}
	return r, nil
}

// checkUploaded 核对对象存在且实际大小等于声明大小。
func (s *Service) checkUploaded(ctx context.Context, r File) error {
	info, err := s.disk(r).Stat(ctx, r.Key)
	if errors.Is(err, fs.ErrNotExist) {
		return ErrNotUploaded
	}
	if err != nil {
		return errors.Wrap(err, "filesystem: stat")
	}
	if info.Size != r.Size {
		return ErrNotUploaded
	}
	return nil
}

// checkAccept 检查大小，并在限定类型时按文件内容识别真实类型，返回探测出的规范 MIME 类型。
func (s *Service) checkAccept(ctx context.Context, r File, accept Accept) (string, error) {
	if accept.MaxSize > 0 && r.Size > int64(accept.MaxSize) {
		return "", errors.Wrapf(ErrNotAccepted, "size %d exceeds %s", r.Size, accept.MaxSize)
	}
	if len(accept.Types) == 0 {
		return "", nil
	}
	actual, err := s.detectContentType(ctx, r)
	if err != nil {
		return "", err
	}
	if !slices.ContainsFunc(accept.Types, func(t string) bool { return typeMatches(t, actual) }) {
		return "", errors.Wrapf(ErrNotAccepted, "content type %s", actual)
	}
	if declared := mediaType(r.ContentType); !declaredCompatible(declared, actual) {
		return "", errors.Wrapf(ErrNotAccepted, "declared %s but content is %s", declared, actual)
	}
	if ext := path.Ext(r.Key); ext != "" && !extCompatible(ext, actual) {
		return "", errors.Wrapf(ErrNotAccepted, "extension %s does not match %s", ext, actual)
	}
	return actual, nil
}

// mediaType 返回去掉参数、转为小写的 MIME 类型，例如 "text/html; charset=utf-8" 返回 "text/html"。
func mediaType(contentType string) string {
	t, _, _ := strings.Cut(contentType, ";")
	return strings.ToLower(strings.TrimSpace(t))
}

// typeMatches 报告 actual 是否匹配 pattern：完全相同，或 pattern 为 "image/*" 这样的一类。
func typeMatches(pattern, actual string) bool {
	pattern = mediaType(pattern)
	if prefix, ok := strings.CutSuffix(pattern, "/*"); ok {
		return strings.HasPrefix(actual, prefix+"/")
	}
	return pattern == actual
}

// disk 返回文件 r 所在的磁盘，没有配置时返回一个总是报错的磁盘。
func (s *Service) disk(r File) Disk {
	if d := s.disks[r.Disk]; d != nil {
		return d
	}
	return missingDisk(r.Disk)
}

// withURL 为文件 r 填上公开地址，磁盘没有配置时保持原样。
func (s *Service) withURL(r File) File {
	if d := s.disks[r.Disk]; d != nil {
		r.URL = d.URL(r.Key)
	}
	return r
}

// objectKey 按上传日期分目录，文件名用 ID 加小写扩展名，公开地址不暴露原始文件名。
func objectKey(id uuid.UUID, now time.Time, filename string) string {
	return now.Format("2006/01/02/") + id.String() + extension(filename)
}

// extension 取小写扩展名，只保留 [a-z0-9] 且不超过 16 个字符，否则返回空串。
func extension(filename string) string {
	ext := strings.ToLower(path.Ext(filename))
	if len(ext) < 2 || len(ext) > 17 {
		return ""
	}
	for _, c := range ext[1:] {
		if (c < 'a' || c > 'z') && (c < '0' || c > '9') {
			return ""
		}
	}
	return ext
}

// exactReader 只放行 left 字节；正文过短或过长时返回错误，使磁盘放弃发布。
type exactReader struct {
	r    io.Reader
	left int64
}

// Read 从 r 读取，最多 left 字节；正文过短或过长时返回错误。
func (e *exactReader) Read(p []byte) (int, error) {
	if e.left == 0 {
		var one [1]byte
		n, err := e.r.Read(one[:])
		if n > 0 {
			return 0, errors.Wrap(ErrInvalid, "body longer than declared size")
		}
		if err == nil {
			return 0, nil
		}
		return 0, err
	}
	if int64(len(p)) > e.left {
		p = p[:e.left]
	}
	n, err := e.r.Read(p)
	e.left -= int64(n)
	if errors.Is(err, io.EOF) && e.left > 0 {
		return n, errors.Wrap(ErrInvalid, "body shorter than declared size")
	}
	if errors.Is(err, io.EOF) {
		err = nil
	}
	return n, err
}

// limitCounter 统计流式上传读取到的真实字节数，超过 max 字节时返回 ErrTooLarge 使磁盘中止写入。
type limitCounter struct {
	r   io.Reader
	max int64
	n   int64
}

// Read 从 r 读取并累加已读字节数；超出上限 max 时返回 ErrTooLarge。
func (l *limitCounter) Read(p []byte) (int, error) {
	if l.n >= l.max {
		var one [1]byte
		n, err := l.r.Read(one[:])
		if n > 0 {
			return 0, ErrTooLarge
		}
		if err != nil {
			return 0, err
		}
		return 0, nil
	}
	remain := l.max - l.n
	if int64(len(p)) > remain {
		p = p[:remain]
	}
	n, err := l.r.Read(p)
	l.n += int64(n)
	return n, err
}

// missingDisk 是没有配置的磁盘，所有操作都返回错误。
type missingDisk string

// err 返回磁盘没有配置的错误。
func (m missingDisk) err() error {
	return errors.Newf("filesystem: disk %q is not configured", string(m))
}

// Put 返回磁盘没有配置的错误。
func (m missingDisk) Put(context.Context, string, string, io.Reader, int64) error {
	return m.err()
}

// Open 返回磁盘没有配置的错误。
func (m missingDisk) Open(context.Context, string) (io.ReadCloser, error) { return nil, m.err() }

// Stat 返回磁盘没有配置的错误。
func (m missingDisk) Stat(context.Context, string) (Object, error) { return Object{}, m.err() }

// Delete 返回磁盘没有配置的错误。
func (m missingDisk) Delete(context.Context, string) error { return m.err() }

// URL 返回空字符串：磁盘没有配置，文件没有地址。
func (m missingDisk) URL(string) string { return "" }
