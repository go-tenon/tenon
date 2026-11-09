package export

import (
	"context"
	"database/sql"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"mime"
	"net/http"
	"os"
	"time"
	"uuid"

	"github.com/uptrace/bun"

	"github.com/ryan053857/go-kit/database"
	"github.com/ryan053857/go-kit/messaging"
	"github.com/ryan053857/go-kit/storage"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// Migrations 是异步导出任务表的迁移，只使用同步导出（Serve）时不需要。
var Migrations = database.MigrationSource{Name: "export", FS: mustSub(migrationsFS, "migrations")}

// TopicExport 是创建异步导出任务后发布的消息，产品必须注册 Jobs.Handle：
//
//	messaging.Handle(a.Bus, export.TopicExport, jobs.Handle)
const TopicExport = "export_job"

// 任务状态。
const (
	StatusPending = "pending"
	StatusRunning = "running"
	StatusDone    = "done"
	StatusFailed  = "failed"
)

var (
	ErrNotFound    = errors.New("export: job not found")
	ErrUnknownKind = errors.New("export: unknown kind")
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
//	jobs := export.NewJobs(a.DB, a.Storage)
//	jobs.Register("notes", "笔记", func(ctx context.Context, job *export.Job, w io.Writer) error {
//		q := db.NewSelect().Model((*Note)(nil)).Where("user_id = ?", job.UserID).Order("id")
//		return export.Write(w, job.Format, notesTable, export.QueryRows[Note](ctx, q))
//	})
//	jobs.OnFinish = func(ctx context.Context, job *export.Job) { ... 发送站内通知 ... }
//	err := messaging.Handle(a.Bus, export.TopicExport, jobs.Handle)
//
// 生成的文件保留 TTL（默认 7 天），之后由 Cleanup 删除。迁移：把 export.Migrations 加入 app.Options.Publish。
type Jobs struct {
	db        *bun.DB
	store     storage.Storage
	exporters map[string]exporter

	// TTL 是导出文件的保留时间，默认 7 天。
	TTL time.Duration
	// OnFinish 可选，任务完成或失败后调用（job.Status 为 done 或 failed），例如通知用户下载。
	OnFinish func(ctx context.Context, job *Job)
	// URLPrefix 是下载地址的前缀，下载地址为 URLPrefix + 任务 ID + "/download"，默认 "/exports/"。
	// 与 DownloadHandler 的挂载路径一致。
	URLPrefix string
}

// NewJobs 返回 Jobs。
func NewJobs(db *bun.DB, store storage.Storage) *Jobs {
	return &Jobs{db: db, store: store, exporters: map[string]exporter{}, TTL: 7 * 24 * time.Hour, URLPrefix: "/exports/"}
}

// Register 注册一种导出：kind 为类型标识，name 用作下载文件名的前缀（如 "笔记" → 笔记-20261109.xlsx）。
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
		return nil, fmt.Errorf("export: unknown format %q", format)
	}
	raw := json.RawMessage("{}")
	if params != nil {
		var err error
		if raw, err = json.Marshal(params); err != nil {
			return nil, fmt.Errorf("export: encode params: %w", err)
		}
	}
	job := &Job{UserID: userID, Kind: kind, Format: format, Params: raw, Status: StatusPending,
		Filename:  fmt.Sprintf("%s-%s.%s", e.name, time.Now().Format("20060102"), format),
		ExpiresAt: time.Now().Add(j.TTL)}
	err := j.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if _, err := tx.NewInsert().Model(job).Exec(ctx); err != nil {
			return err
		}
		return messaging.PublishTx(ctx, tx.Tx, TopicExport, JobRequested{JobID: job.ID})
	})
	if err != nil {
		return nil, fmt.Errorf("export: start: %w", err)
	}
	return job, nil
}

// Handle 执行导出任务，供 messaging.Handle 注册。导出函数出错时任务标记为失败（不重试），
// 用户可以重新发起；保存文件、更新状态出错时返回错误，由消息总线重试。
func (j *Jobs) Handle(ctx context.Context, m JobRequested) error {
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
	job.FinishedAt = time.Now()
	if runErr != nil {
		slog.ErrorContext(ctx, "export job failed", "job_id", job.ID, "kind", job.Kind, "err", runErr)
		job.Status, job.Error = StatusFailed, "导出失败"
	} else {
		job.Status = StatusDone
	}
	_, err = j.db.NewUpdate().Model(job).Column("status", "key", "size", "error", "finished_at", "updated_at").WherePK().Exec(ctx)
	if err != nil {
		return err
	}
	if j.OnFinish != nil {
		j.OnFinish(ctx, job)
	}
	return nil
}

// run 把导出写入临时文件，再保存到 storage。
func (j *Jobs) run(ctx context.Context, job *Job) error {
	e, ok := j.exporters[job.Kind]
	if !ok {
		return fmt.Errorf("%w: %s", ErrUnknownKind, job.Kind)
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
		return nil, fmt.Errorf("export: get: %w", err)
	}
	return job, nil
}

// List 返回用户最近的导出任务（最多 limit 个），最新的在前。
func (j *Jobs) List(ctx context.Context, userID uuid.UUID, limit int) ([]Job, error) {
	list := []Job{}
	err := j.db.NewSelect().Model(&list).Where("user_id = ?", userID).OrderExpr("id DESC").Limit(int64(limit)).Scan(ctx)
	if err != nil {
		return nil, fmt.Errorf("export: list: %w", err)
	}
	return list, nil
}

// URL 返回已完成任务的下载地址，未完成时返回空串。
func (j *Jobs) URL(job *Job) string {
	if job.Status != StatusDone {
		return ""
	}
	return j.URLPrefix + job.ID.String() + "/download"
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
	if err := j.db.NewSelect().Model(&jobs).Column("id", "key").Where("expires_at < ?", time.Now()).Scan(ctx); err != nil {
		return 0, fmt.Errorf("export: cleanup: %w", err)
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
			return n, fmt.Errorf("export: cleanup: %w", err)
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
