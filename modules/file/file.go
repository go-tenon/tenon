// Package file 是文件模块：上传、元数据与下载。
//
// 文件内容保存在 storage.Storage 中，元数据保存在 files 表。上传由应用接收后写入存储
// （大小与类型校验在服务端完成），下载也经过应用转发，因此权限由应用控制，存储桶不需要公开读。
//
// 业务表通过 file_id 引用文件；需要附件的接口在自己的 handler 中调用 Upload、Serve，
// 并按业务规则判断谁能访问。模块也提供了“上传我的文件、下载我的文件”的通用接口：
//
//	files := file.New(a.DB, a.Storage, cfg.Files)
//	rest.Post(v1, "/files", file.UploadEndpoint(files))
//	a.Router.Get("/files/{id}", files.DownloadHandler(file.OwnerOnly))
//
// # 客户端直传
//
// 使用 S3 存储时，大文件可以由客户端直接上传到对象存储，不占用应用带宽：
// 先调用 CreateUpload（CreateUploadEndpoint）得到预签名地址，客户端 PUT 文件内容，
// 再调用 CompleteUpload（CompleteUploadEndpoint）校验大小与类型。单个文件最大 5 GB。
//
// # 未被引用的文件
//
// 文件作为业务数据的附件时（先上传、再提交表单），可以设置 Config.RequireAttach：
// 上传的文件在 AttachTTL 内没有被 Attach 就会被 Cleanup 删除，用户放弃填写的表单不会留下垃圾文件。
// 业务保存时在同一事务中调用 Attach。未完成的直传也由 Cleanup 删除，产品用定时任务定期调用它。
//
// 迁移：把 file.Migrations 加入 app.Options.Publish。
package file

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"mime"
	"net/http"
	"path"
	"strings"
	"time"
	"uuid"

	"github.com/cockroachdb/errors"

	"github.com/uptrace/bun"

	"github.com/ryan053857/go-kit/database"
	"github.com/ryan053857/go-kit/httpx"
	"github.com/ryan053857/go-kit/i18n"
	"github.com/ryan053857/go-kit/storage"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

//go:embed locales/*.yaml
var locales embed.FS

func init() { i18n.MustLoad(locales) }

// Migrations 是本模块的迁移。
var Migrations = database.MigrationSource{Name: "file", FS: mustSub(migrationsFS, "migrations")}

var (
	ErrTooLarge         = httpx.DefineError(http.StatusRequestEntityTooLarge, "file.too_large")
	ErrTypeNotAllowed   = httpx.DefineError(http.StatusUnsupportedMediaType, "file.type_not_allowed")
	ErrNotFound         = httpx.DefineError(http.StatusNotFound, "file.not_found")
	ErrNoDirectUpload   = httpx.DefineError(http.StatusNotImplemented, "file.no_direct_upload") // 存储不是 S3
	ErrUploadIncomplete = httpx.DefineError(http.StatusUnprocessableEntity, "file.upload_incomplete")
)

// Config 是文件模块的配置。
type Config struct {
	// MaxSize 为单个文件的最大字节数，默认 20 MiB。
	MaxSize int64 `yaml:"max_size"`
	// AllowedTypes 为允许的 MIME 类型，以 / 结尾表示前缀，如 ["image/", "application/pdf"]；为空表示不限制。
	AllowedTypes []string `yaml:"allowed_types"`
	// URLPrefix 为下载地址的前缀，用于生成 FileDTO.URL，默认 "/files/"。与 DownloadHandler 的挂载路径一致。
	URLPrefix string `yaml:"url_prefix"`

	// MaxDirectSize 为客户端直传的最大字节数，默认 1 GiB（S3 单次 PUT 的上限为 5 GB）。
	MaxDirectSize int64 `yaml:"max_direct_size"`
	// RequireAttach 为 true 时，新上传的文件需要在 AttachTTL 内被 Attach，否则被 Cleanup 删除。
	RequireAttach bool `yaml:"require_attach"`
	// AttachTTL 默认 24h。
	AttachTTL time.Duration `yaml:"attach_ttl"`
}

// DefaultConfig 返回带默认值的 Config。
func DefaultConfig() Config {
	return Config{MaxSize: 20 << 20, URLPrefix: "/files/", MaxDirectSize: 1 << 30, AttachTTL: 24 * time.Hour}
}

// 文件状态。
const (
	StatusPending = "pending" // 客户端直传尚未完成
	StatusReady   = "ready"
)

// 直传的预签名地址有效期；未完成的直传在 pendingTTL 后被 Cleanup 删除。
const (
	presignTTL = 15 * time.Minute
	pendingTTL = time.Hour
)

// File 是一个已上传的文件。
type File struct {
	bun.BaseModel `bun:"table:files,alias:f"`
	database.Model

	UserID      uuid.UUID `bun:"user_id,nullzero,type:uuid"`
	Filename    string    `bun:"filename,notnull"`
	ContentType string    `bun:"content_type,notnull"`
	Size        int64     `bun:"size,notnull"`
	Key         string    `bun:"key,notnull"`
	Status      string    `bun:"status,notnull"`
	ExpiresAt   time.Time `bun:"expires_at,nullzero"` // 非零时到期后被 Cleanup 删除
}

// Service 管理文件。
type Service struct {
	db    *bun.DB
	store storage.Storage
	cfg   Config
}

// New 返回使用 db 与 store 的 Service。
func New(db *bun.DB, store storage.Storage, cfg Config) *Service {
	if cfg.MaxSize <= 0 {
		cfg.MaxSize = DefaultConfig().MaxSize
	}
	if cfg.URLPrefix == "" {
		cfg.URLPrefix = DefaultConfig().URLPrefix
	}
	if cfg.MaxDirectSize <= 0 {
		cfg.MaxDirectSize = DefaultConfig().MaxDirectSize
	}
	if cfg.AttachTTL <= 0 {
		cfg.AttachTTL = DefaultConfig().AttachTTL
	}
	return &Service{db: db, store: store, cfg: cfg}
}

// Config 返回生效的配置。
func (s *Service) Config() Config { return s.cfg }

// URL 返回文件的下载地址。
func (s *Service) URL(f *File) string { return s.cfg.URLPrefix + f.ID.String() }

// Upload 保存文件并返回其元数据。userID 为上传者，系统生成的文件传 uuid.Nil()。
//
// 文件类型按内容识别（不信任客户端声明的类型）；内容无法识别时才参考扩展名。
// 超过 MaxSize 时返回 ErrTooLarge，类型不在 AllowedTypes 中时返回 ErrTypeNotAllowed。
func (s *Service) Upload(ctx context.Context, userID uuid.UUID, filename string, r io.ReadSeeker) (*File, error) {
	size, err := r.Seek(0, io.SeekEnd)
	if err != nil {
		return nil, errors.Wrap(err, "file")
	}
	if size > s.cfg.MaxSize {
		return nil, s.tooLarge(s.cfg.MaxSize)
	}
	if _, err := r.Seek(0, io.SeekStart); err != nil {
		return nil, errors.Wrap(err, "file")
	}
	head := make([]byte, 512)
	n, _ := io.ReadFull(r, head)
	if _, err := r.Seek(0, io.SeekStart); err != nil {
		return nil, errors.Wrap(err, "file")
	}

	filename = cleanFilename(filename)
	ext := strings.ToLower(path.Ext(filename))
	contentType := detectType(head[:n], ext)
	if !s.allowed(contentType) {
		return nil, ErrTypeNotAllowed
	}

	f := &File{UserID: userID, Filename: filename, ContentType: contentType, Size: size, Status: StatusReady,
		ExpiresAt: s.attachDeadline()}
	f.ID = uuid.NewV7()
	f.Key = "files/" + f.ID.String() + ext
	if err := s.store.Put(ctx, f.Key, r, contentType); err != nil {
		return nil, err
	}
	if _, err := s.db.NewInsert().Model(f).Exec(ctx); err != nil {
		_ = s.store.Delete(context.WithoutCancel(ctx), f.Key)
		return nil, errors.Wrap(err, "file: save")
	}
	return f, nil
}

// Get 返回已上传完成的文件的元数据，不存在（或直传尚未完成）时返回 ErrNotFound。
func (s *Service) Get(ctx context.Context, id uuid.UUID) (*File, error) {
	f := new(File)
	err := s.db.NewSelect().Model(f).Where("id = ? AND status = ?", id, StatusReady).Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, errors.Wrap(err, "file: get")
	}
	return f, nil
}

// Delete 删除文件记录与内容。内容删除失败只记录日志（记录已删除，不会再被访问）。
func (s *Service) Delete(ctx context.Context, id uuid.UUID) error {
	f := new(File)
	res, err := s.db.NewDelete().Model(f).Where("id = ?", id).Returning("key").Exec(ctx)
	if err != nil {
		return errors.Wrap(err, "file: delete")
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	if err := s.store.Delete(ctx, f.Key); err != nil {
		slog.WarnContext(ctx, "delete file content", "file_id", id, "key", f.Key, "err", err)
	}
	return nil
}

// Serve 把文件内容写入响应。图片、PDF、音视频在浏览器中直接打开，其他类型作为附件下载。
// 文件内容不会改变，因此设置了一年的私有缓存与 ETag。
func (s *Service) Serve(w http.ResponseWriter, r *http.Request, f *File) error {
	etag := `"` + f.ID.String() + `"`
	h := w.Header()
	h.Set("ETag", etag)
	h.Set("Cache-Control", "private, max-age=31536000, immutable")
	if r.Header.Get("If-None-Match") == etag {
		w.WriteHeader(http.StatusNotModified)
		return nil
	}
	rc, err := s.store.Get(r.Context(), f.Key)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			return ErrNotFound
		}
		return err
	}
	defer rc.Close()

	disposition := "attachment"
	if inline(f.ContentType) {
		disposition = "inline"
	}
	h.Set("Content-Type", f.ContentType)
	h.Set("Content-Length", fmt.Sprint(f.Size))
	h.Set("Content-Disposition", mime.FormatMediaType(disposition, map[string]string{"filename": f.Filename}))
	h.Set("X-Content-Type-Options", "nosniff")
	if r.Method == http.MethodHead {
		return nil
	}
	_, err = io.Copy(w, rc)
	return err
}

func (s *Service) allowed(contentType string) bool {
	if len(s.cfg.AllowedTypes) == 0 {
		return true
	}
	base, _, _ := strings.Cut(contentType, ";")
	for _, t := range s.cfg.AllowedTypes {
		if base == t || (strings.HasSuffix(t, "/") && strings.HasPrefix(base, t)) {
			return true
		}
	}
	return false
}

// detectType 按内容识别类型；内容只能识别为通用类型（任意二进制、zip、纯文本）时参考扩展名，
// 例如 docx/xlsx 的内容是 zip，csv 的内容是纯文本。
func detectType(head []byte, ext string) string {
	sniffed := http.DetectContentType(head)
	base, _, _ := strings.Cut(sniffed, ";")
	switch base {
	case "application/octet-stream", "application/zip", "text/plain":
		if byExt := mime.TypeByExtension(ext); byExt != "" && !strings.HasPrefix(byExt, "text/html") {
			return byExt
		}
	}
	return sniffed
}

func inline(contentType string) bool {
	base, _, _ := strings.Cut(contentType, ";")
	switch {
	case base == "image/svg+xml": // SVG 可以包含脚本，不在浏览器中直接打开
		return false
	case strings.HasPrefix(base, "image/"), strings.HasPrefix(base, "video/"), strings.HasPrefix(base, "audio/"),
		base == "application/pdf":
		return true
	}
	return false
}

func cleanFilename(name string) string {
	name = path.Base(strings.ReplaceAll(strings.TrimSpace(name), `\`, "/"))
	if name == "." || name == "/" || name == "" {
		name = "file"
	}
	if r := []rune(name); len(r) > 200 {
		ext := path.Ext(name)
		name = string(r[:200-len([]rune(ext))]) + ext
	}
	return name
}

func mustSub(fsys fs.FS, dir string) fs.FS {
	sub, err := fs.Sub(fsys, dir)
	if err != nil {
		panic(err)
	}
	return sub
}

func (s *Service) tooLarge(max int64) error {
	return httpx.WithData(ErrTooLarge, map[string]any{"MaxMB": max >> 20})
}
