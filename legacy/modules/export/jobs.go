package export

import (
	"context"
	"database/sql"
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"mime"
	"net/http"
	"os"
	"time"
	"uuid"

	"github.com/cockroachdb/errors"

	"github.com/uptrace/bun"

	"github.com/ryan053857/go-kit/database"
	"github.com/ryan053857/go-kit/httpx"
	"github.com/ryan053857/go-kit/i18n"
	"github.com/ryan053857/go-kit/messaging"
	"github.com/ryan053857/go-kit/storage"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

//go:embed locales/*.yaml
var locales embed.FS

func init() { i18n.MustLoad(locales) }

// Migrations 是异步导出任务表的迁移，只使用同步导出（Serve）时不需要。
var Migrations = database.MigrationSource{Name: "export", FS: mustSub(migrationsFS, "migrations")}

// TopicExport 是创建异步导出任务后发布的消息，由 NewJobs 注册的处理函数执行。
const TopicExport = "export_job"

// 任务状态。
const (
	StatusPending = "pending"
	StatusRunning = "running"
	StatusDone    = "done"
	StatusFailed  = "failed"
)

var (
	ErrNotFound      = httpx.DefineError(http.StatusNotFound, "export.not_found")
	ErrUnknownKind   = httpx.DefineError(http.StatusUnprocessableEntity, "export.unknown_kind")
	ErrUnknownFormat = httpx.DefineError(http.StatusUnprocessableEntity, "export.unknown_format")
)

// Job 是一个异步导出任务。
type Job struct {
	bun.BaseModel `bun:"table:export_jobs,alias:j"`
	database.Model

	UserID     uuid.UUID       `bun:"user_id,notnull,type:uuid"`
	Kind       string          `bun:"kind,notnull"`
	Format     Format          `bun:"format,notnull"`
	Params     json.RawMessage `bun:"params,type:jsonb,notnull"`
	Status     string          `bun:"status,notnull"`
	Filename   string          `bun:"filename,notnull"`
	Key        string          `bun:"key,notnull"`
	Size       int64           `bun:"size,notnull"`
	Error      string          `bun:"error,notnull"`
	FinishedAt time.Time       `bun:"finished_at,nullzero"`
	ExpiresAt  time.Time       `bun:"expires_at,notnull"`
}

// Decode 把任务参数解码到 v。
func (j *Job) Decode(v any) error { return json.Unmarshal(j.Params, v) }

// Exporter 生成一个异步导出任务的文件：按 job.Format 把表格写入 w。
// 查询条件来自 job.UserID 与 job.Params，必须按 job.UserID 限定数据范围。
type Exporter func(ctx context.Context, job *Job, w io.Writer) error

type exporter struct {
	name string
	fn   Exporter
}

// Jobs 运行异步导出：数据量大、同步导出会超时时，请求只创建任务，
// 消息处理函数在后台生成文件并保存到 storage，完成后用户下载。
//
//	jobs, err := export.NewJobs(a.DB, a.Storage, a.Bus, cfg.Exports)
//	jobs.Register("notes", "notes.export_name", func(ctx context.Context, job *export.Job, w io.Writer) error {
//		q := db.NewSelect().Model((*Note)(nil)).Where("user_id = ?", job.UserID).Order("id")
//		return export.Write(w, job.Format, notesTable, export.QueryRows[Note](ctx, q))
//	})
//	jobs.OnFinish(func(ctx context.Context, job *export.Job) { ... 发送站内通知 ... })
//
// NewJobs 在消息总线上注册了执行任务的处理函数。生成的文件保留 JobsConfig.TTL（默认 7 天），之后由 Cleanup 删除。
// 迁移：把 export.Migrations 加入 app.Options.Publish。
type Jobs struct {
	db        *bun.DB
	store     storage.Storage
	cfg       JobsConfig
	exporters map[string]exporter
	onFinish  []func(context.Context, *Job)
	now       func() time.Time
}

// JobsConfig 是异步导出的配置。
type JobsConfig struct {
	// TTL 是导出文件的保留时间，默认 7 天。
	TTL time.Duration `yaml:"ttl"`
	// URLPrefix 是下载地址的前缀，下载地址为 URLPrefix + 任务 ID + "/download"，默认 "/exports/"。
	// 与 DownloadHandler 的挂载路径一致。
	URLPrefix string `yaml:"url_prefix"`
}

// DefaultJobsConfig 返回带默认值的 JobsConfig。
func DefaultJobsConfig() JobsConfig {
	return JobsConfig{TTL: 7 * 24 * time.Hour, URLPrefix: "/exports/"}
}

// NewJobs 返回 Jobs，并在 bus 上注册执行任务的处理函数。
func NewJobs(db *bun.DB, store storage.Storage, bus *messaging.Bus, cfg JobsConfig) (*Jobs, error) {
	d := DefaultJobsConfig()
	if cfg.TTL <= 0 {
		cfg.TTL = d.TTL
	}
	if cfg.URLPrefix == "" {
		cfg.URLPrefix = d.URLPrefix
	}
	j := &Jobs{db: db, store: store, cfg: cfg, exporters: map[string]exporter{}, now: time.Now}
	if err := messaging.Handle(bus, TopicExport, j.handle); err != nil {
		return nil, err
	}
	return j, nil
}

// OnFinish 注册任务完成或失败后的回调（job.Status 为 done 或 failed），例如通知用户下载。
func (j *Jobs) OnFinish(fn func(ctx context.Context, job *Job)) { j.onFinish = append(j.onFinish, fn) }

// Register 注册一种导出：kind 为类型标识，name 为下载文件名前缀的消息 ID（按发起导出时的语言翻译，
// 没有对应消息时原样使用），如 "笔记" → 笔记-20261109.xlsx。
func (j *Jobs) Register(kind, name string, fn Exporter) {
	j.exporters[kind] = exporter{name: name, fn: fn}
}

// JobRequested 是 TopicExport 消息的内容。
type JobRequested struct {
	JobID uuid.UUID `json:"job_id"`
}

// Start 创建一个导出任务并发布消息，params 为 Exporter 需要的参数（会编码为 JSON）。
// kind 未注册时返回 ErrUnknownKind。
func (j *Jobs) Start(ctx context.Context, userID uuid.UUID, kind string, format Format, params any) (*Job, error) {
	e, ok := j.exporters[kind]
	if !ok {
		return nil, ErrUnknownKind
	}
	if format != CSV && format != XLSX {
		return nil, ErrUnknownFormat
	}
	raw := json.RawMessage("{}")
	if params != nil {
		var err error
		if raw, err = json.Marshal(params); err != nil {
			return nil, errors.Wrap(err, "export: encode params")
		}
	}
	job := &Job{UserID: userID, Kind: kind, Format: format, Params: raw, Status: StatusPending,
		Filename:  fmt.Sprintf("%s-%s.%s", i18n.T(ctx, e.name), j.now().Format("20060102"), format),
		ExpiresAt: j.now().Add(j.cfg.TTL)}
	err := j.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if _, err := tx.NewInsert().Model(job).Exec(ctx); err != nil {
			return err
		}
		return messaging.PublishTx(ctx, tx.Tx, TopicExport, JobRequested{JobID: job.ID})
	})
	if err != nil {
		return nil, errors.Wrap(err, "export: start")
	}
	return job, nil
}

// handle 执行导出任务。导出函数出错时任务标记为失败（不重试），
// 用户可以重新发起；保存文件、更新状态出错时返回错误，由消息总线重试。
func (j *Jobs) handle(ctx context.Context, m JobRequested) error {
	job := new(Job)
	err := j.db.NewSelect().Model(job).Where("id = ?", m.JobID).Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil // 已被清理
	}
	if err != nil {
		return err
	}
	if job.Status == StatusDone || job.Status == StatusFailed {
		return nil // 重复投递
	}
	job.Status = StatusRunning
	if _, err := j.db.NewUpdate().Model(job).Column("status", "updated_at").WherePK().Exec(ctx); err != nil {
		return err
	}

	runErr := j.run(ctx, job)
	if runErr != nil && ctx.Err() != nil {
		return runErr // 停机中断，保持 running，消息重新投递后再执行
	}
	job.FinishedAt = j.now()
	if runErr != nil {
		slog.ErrorContext(ctx, "export job failed", "job_id", job.ID, "kind", job.Kind, "err", runErr)
		job.Status, job.Error = StatusFailed, "export.failed"
	} else {
		job.Status = StatusDone
	}
	_, err = j.db.NewUpdate().Model(job).Column("status", "key", "size", "error", "finished_at", "updated_at").WherePK().Exec(ctx)
	if err != nil {
		return err
	}
	for _, fn := range j.onFinish {
		fn(ctx, job)
	}
	return nil
}

// run 把导出写入临时文件，再保存到 storage。
func (j *Jobs) run(ctx context.Context, job *Job) error {
	e, ok := j.exporters[job.Kind]
	if !ok {
		return errors.Newf("%w: %s", ErrUnknownKind, job.Kind)
	}
	tmp, err := os.CreateTemp("", "export-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	defer tmp.Close()
	if err := e.fn(ctx, job, tmp); err != nil {
		return err
	}
	size, err := tmp.Seek(0, io.SeekCurrent)
	if err != nil {
		return err
	}
	if _, err := tmp.Seek(0, io.SeekStart); err != nil {
		return err
	}
	key := "exports/" + job.ID.String() + "." + string(job.Format)
	if err := j.store.Put(ctx, key, tmp, job.Format.ContentType()); err != nil {
		return err
	}
	job.Key, job.Size = key, size
	return nil
}

// Get 返回用户的导出任务，不存在或不属于该用户时返回 ErrNotFound。
func (j *Jobs) Get(ctx context.Context, userID, id uuid.UUID) (*Job, error) {
	job := new(Job)
	err := j.db.NewSelect().Model(job).Where("id = ? AND user_id = ?", id, userID).Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, errors.Wrap(err, "export: get")
	}
	return job, nil
}

// List 返回用户最近的导出任务（最多 limit 个），最新的在前。
func (j *Jobs) List(ctx context.Context, userID uuid.UUID, limit int) ([]Job, error) {
	list := []Job{}
	err := j.db.NewSelect().Model(&list).Where("user_id = ?", userID).OrderExpr("id DESC").Limit(int64(limit)).Scan(ctx)
	if err != nil {
		return nil, errors.Wrap(err, "export: list")
	}
	return list, nil
}

// URL 返回已完成任务的下载地址，未完成时返回空串。
func (j *Jobs) URL(job *Job) string {
	if job.Status != StatusDone {
		return ""
	}
	return j.cfg.URLPrefix + job.ID.String() + "/download"
}

// Serve 以附件形式把已完成任务的文件写入响应；任务未完成时返回 ErrNotFound。
func (j *Jobs) Serve(w http.ResponseWriter, r *http.Request, job *Job) error {
	if job.Status != StatusDone {
		return ErrNotFound
	}
	rc, err := j.store.Get(r.Context(), job.Key)
	if errors.Is(err, storage.ErrNotFound) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	defer rc.Close()
	h := w.Header()
	h.Set("Content-Type", job.Format.ContentType())
	h.Set("Content-Length", fmt.Sprint(job.Size))
	h.Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": job.Filename}))
	h.Set("Cache-Control", "private, no-store")
	_, err = io.Copy(w, rc)
	return err
}

// Cleanup 删除过期的任务及其文件，返回删除的任务数。由产品的定时任务定期调用。
func (j *Jobs) Cleanup(ctx context.Context) (int, error) {
	var jobs []Job
	if err := j.db.NewSelect().Model(&jobs).Column("id", "key").Where("expires_at < ?", j.now()).Scan(ctx); err != nil {
		return 0, errors.Wrap(err, "export: cleanup")
	}
	n := 0
	for _, job := range jobs {
		if job.Key != "" {
			if err := j.store.Delete(ctx, job.Key); err != nil {
				slog.WarnContext(ctx, "cleanup export file", "job_id", job.ID, "err", err)
				continue
			}
		}
		if _, err := j.db.NewDelete().Model((*Job)(nil)).Where("id = ?", job.ID).Exec(ctx); err != nil {
			return n, errors.Wrap(err, "export: cleanup")
		}
		n++
	}
	return n, nil
}

func mustSub(fsys fs.FS, dir string) fs.FS {
	sub, err := fs.Sub(fsys, dir)
	if err != nil {
		panic(err)
	}
	return sub
}
