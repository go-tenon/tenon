// Package export 在后台导出一个主体的个人数据：调用 privacy.Source 的 Export，把结果打包成 zip 放到文件存储，交给本人下载。
//
// 一次导出是一个 operation：状态和进度记在 operations 表中，与导入、导出表格出现在同一个任务列表里；
// privacy_exports 表只记录生成的压缩包。各项的数据来自 privacy.Service 登记的 Sources，与注销使用同一份清单。
//
// Start 在事务中登记导出，同一个事务发布队列任务；任务调用 Process，Open 读取生成的压缩包：
//
//	exp, err := personal.Start(ctx, tx, subject)
//	err = ExportPersonalData.Publish(ctx, tx, exp.ID)
//
//	// 队列任务
//	err := personal.Process(ctx, id, nil)
//
// 压缩包以 temporary 状态上传，超过 filesystem 的 temp_ttl 后被回收。它包含本人的个人数据，
// 不返回公开地址，由应用在校验身份之后用 Open 读取并返回给本人。
package export

import (
	"archive/zip"
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"os"
	"time"

	"github.com/cockroachdb/errors"
	"github.com/google/uuid"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/database"
	"github.com/go-tenon/tenon/errorx"
	"github.com/go-tenon/tenon/filesystem"
	"github.com/go-tenon/tenon/identity"
	"github.com/go-tenon/tenon/operation"
	"github.com/go-tenon/tenon/privacy"
)

// Kind 是导出个人数据的操作类型，出现在 operation.Operation.Kind 中。
const Kind = "privacy.export"

var (
	// ErrInProgress 表示这个主体已经有一次导出还没有完成。
	ErrInProgress = errorx.Define(errorx.Conflict, "privacy.export_in_progress")
	// ErrNotReady 表示导出还没有完成，或者失败了，没有可以下载的压缩包。
	ErrNotReady = errorx.Define(errorx.Conflict, "privacy.export_not_ready")
	// ErrExpired 表示压缩包已经过期回收，需要重新导出。
	ErrExpired = errorx.Define(errorx.NotFound, "privacy.export_expired")
)

// Export 是一次导出：嵌入的 Operation 是它的状态和进度，一项 Source 是一个单位；其余字段是 privacy_exports 表的 Bun 模型。
type Export struct {
	bun.BaseModel       `bun:"table:privacy_exports,alias:pex"`
	operation.Operation `bun:"-"`

	OperationID uuid.UUID  `bun:"operation_id,pk,type:uuid" json:"-"`
	FileID      uuid.UUID  `bun:"file_id,type:uuid,nullzero" json:"-"`                    // 压缩包，completed 时写入
	Filename    string     `bun:"filename,notnull"           json:"filename,omitempty"`   // 压缩包的文件名，completed 时写入
	ExpiresAt   *time.Time `bun:"expires_at"                 json:"expires_at,omitempty"` // 压缩包被回收的时间，completed 时写入
}

// Service 登记、处理和读取导出，可以并发使用。
type Service struct {
	db      *bun.DB
	ops     *operation.Service
	files   *filesystem.Service
	typ     identity.SubjectType
	sources []privacy.Source
}

// New 返回导出服务。ops 记录导出的状态和进度，files 保存压缩包，deletions 提供账号体系和登记的 Sources，
// 其中有 Export 的各项依次导出。db 必须已执行 operation、filesystem 和本包的迁移。参数为 nil 时 panic。
func New(db *bun.DB, ops *operation.Service, files *filesystem.Service, deletions *privacy.Service) *Service {
	if db == nil || ops == nil || files == nil || deletions == nil {
		panic("privacy/export: nil dependency")
	}
	var sources []privacy.Source
	for _, src := range deletions.Sources() {
		if src.Export != nil {
			sources = append(sources, src)
		}
	}
	return &Service{db: db, ops: ops, files: files, typ: deletions.Type(), sources: sources}
}

// Start 登记 owner 的一次导出，返回 pending 状态的导出。owner 不属于 privacy.Service 的账号体系时返回错误；
// owner 已经有一次 pending 或 running 的导出时返回 ErrInProgress。
// tx 通常是发布导出任务的事务，在同一个事务中发布任务，任务中调用 Process。
func (s *Service) Start(ctx context.Context, tx bun.IDB, owner identity.Subject) (*Export, error) {
	if err := s.check(owner.Ref()); err != nil {
		return nil, err
	}
	running, err := tx.NewSelect().Model((*operation.Operation)(nil)).
		Where("owner_type = ? AND owner_id = ?", owner.Type, owner.ID).Where("kind = ?", Kind).
		Where("status IN (?)", bun.List([]operation.Status{operation.StatusPending, operation.StatusRunning})).Exists(ctx)
	if err != nil {
		return nil, errors.Wrap(err, "privacy/export: start")
	}
	if running {
		return nil, ErrInProgress.New()
	}
	op, err := s.ops.Start(ctx, tx, owner, Kind)
	if err != nil {
		return nil, err
	}
	exp := &Export{Operation: *op, OperationID: op.ID}
	_, err = tx.NewInsert().Model(exp).Exec(ctx)
	return exp, errors.Wrap(err, "privacy/export: start")
}

// Get 返回 owner 发起的导出；不存在或者不是 owner 发起的时返回 operation.ErrNotFound。
func (s *Service) Get(ctx context.Context, owner identity.Subject, id uuid.UUID) (*Export, error) {
	op, err := s.ops.Get(ctx, owner, id)
	if err != nil {
		return nil, err
	}
	exp, err := s.load(ctx, id)
	if err != nil {
		return nil, err
	}
	if exp == nil {
		return nil, operation.ErrNotFound.New()
	}
	exp.Operation = *op
	return exp, nil
}

// Open 打开 owner 的导出生成的压缩包，供下载接口返回给本人。调用方负责关闭。
// 不存在或者不是 owner 发起的时返回 operation.ErrNotFound；还没有完成或者失败时返回 ErrNotReady；
// 压缩包已经过期时返回 ErrExpired。
func (s *Service) Open(ctx context.Context, owner identity.Subject, id uuid.UUID) (io.ReadCloser, *Export, error) {
	exp, err := s.Get(ctx, owner, id)
	if err != nil {
		return nil, nil, err
	}
	if exp.Status != operation.StatusCompleted || exp.FileID == uuid.Nil {
		return nil, nil, ErrNotReady.New()
	}
	if exp.ExpiresAt != nil && !time.Now().Before(*exp.ExpiresAt) {
		return nil, nil, ErrExpired.New()
	}
	body, _, err := s.files.Open(ctx, exp.FileID)
	if errors.Is(err, filesystem.ErrNotFound) {
		return nil, nil, ErrExpired.New()
	}
	if err != nil {
		return nil, nil, err
	}
	return body, exp, nil
}

// load 读取导出的压缩包，不存在时返回 nil。
func (s *Service) load(ctx context.Context, id uuid.UUID) (*Export, error) {
	exp := new(Export)
	err := s.db.NewSelect().Model(exp).Where("operation_id = ?", id).Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, errors.Wrap(err, "privacy/export: load")
	}
	return exp, nil
}

// Process 处理导出 id，在队列任务中调用。它用 operation.Service.Run 执行导出，一项 Source 是一个单位。
//
// 各项的 Export 在同一个只读的 REPEATABLE READ 事务中依次执行，读到的是同一时刻的数据；
// 每一项的结果编码为 JSON，写成压缩包中的 <Name>.json，返回 nil 的项不写文件。压缩包另有一个 export.json，
// 记录主体、导出时间和包含的各项。压缩包以 temporary 状态上传到文件存储，导出变为 completed。
//
// 每次保存进度（变为 running、每导出一项、结束）都在同一个事务中调用 saved（可以为 nil），exp 是保存后的导出，
// 用法见 operation.Service.Run，例如在导出完成时发送通知。
//
// 某一项返回带错误码的业务错误时导出变为 failed，Error 是错误码；其他错误照常返回给队列，重试时从头重新导出。
// 导出不存在（例如已被清理）或者已经结束时，Process 直接返回 nil。
func (s *Service) Process(ctx context.Context, id uuid.UUID,
	saved func(ctx context.Context, tx database.Tx, exp *Export) error,
) error {
	exp, err := s.load(ctx, id)
	if err != nil || exp == nil {
		return err
	}
	return s.ops.Run(ctx, id, func(ctx context.Context, r *operation.Run) error {
		exp.Operation = *r.Operation()
		r.Restart() // 重试时从头开始
		return s.export(ctx, r, exp)
	}, func(ctx context.Context, tx database.Tx, op *operation.Operation) error {
		exp.Operation = *op
		if saved == nil {
			return nil
		}
		return saved(ctx, tx, exp)
	})
}

// manifest 是压缩包中的 export.json。
type manifest struct {
	Subject    subject   `json:"subject"`
	ExportedAt time.Time `json:"exported_at"`
	Files      []string  `json:"files"`
}

// subject 是导出文件中的主体。
type subject struct {
	Type identity.SubjectType `json:"type"`
	ID   string               `json:"id"`
}

// export 收集主体的全部数据，打包上传，写入导出记录。
func (s *Service) export(ctx context.Context, r *operation.Run, exp *Export) error {
	owner := exp.Owner()
	if err := s.check(owner.Ref()); err != nil {
		return err
	}
	r.SetTotal(len(s.sources))
	if err := r.Save(ctx, nil); err != nil {
		return err
	}
	tmp, err := os.CreateTemp("", "privacy-export-*.zip")
	if err != nil {
		return errors.Wrap(err, "privacy/export: create temporary file")
	}
	defer os.Remove(tmp.Name())
	defer tmp.Close()

	zw := zip.NewWriter(tmp)
	m := manifest{Subject: subject{Type: owner.Type, ID: owner.ID}, ExportedAt: time.Now().UTC(), Files: []string{}}
	read := &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true}
	err = database.RunInTx(ctx, s.db, read, func(ctx context.Context, tx database.Tx) error {
		for _, src := range s.sources {
			v, err := src.Export(ctx, tx, owner.Ref())
			if err != nil {
				return errors.Wrapf(err, "privacy/export: export %s for %s", src.Name, owner.Ref())
			}
			if v != nil {
				name := src.Name + ".json"
				if err := writeJSON(zw, name, m.ExportedAt, v); err != nil {
					return errors.Wrapf(err, "privacy/export: write %s", name)
				}
				m.Files = append(m.Files, name)
			}
			r.Add(1, 0)
		}
		return nil
	})
	if err != nil {
		return err
	}
	if err := r.Save(ctx, nil); err != nil {
		return err
	}
	if err := writeJSON(zw, "export.json", m.ExportedAt, m); err != nil {
		return errors.Wrap(err, "privacy/export: write export.json")
	}
	if err := zw.Close(); err != nil {
		return errors.Wrap(err, "privacy/export: close zip")
	}
	size, err := tmp.Seek(0, io.SeekCurrent)
	if err == nil {
		_, err = tmp.Seek(0, io.SeekStart)
	}
	if err != nil {
		return errors.Wrap(err, "privacy/export: rewind temporary file")
	}
	file, err := s.files.Upload(ctx, owner, filesystem.FileInput{
		Filename:    "personal-data-" + m.ExportedAt.Format("20060102") + ".zip",
		ContentType: "application/zip",
		Size:        size,
	}, tmp)
	if err != nil {
		return err
	}
	exp.FileID, exp.Filename, exp.ExpiresAt = file.ID, file.Filename, file.ExpiresAt
	return r.Complete(ctx, func(ctx context.Context, tx database.Tx) error {
		_, err := tx.NewUpdate().Model(exp).Column("file_id", "filename", "expires_at").WherePK().Exec(ctx)
		return errors.Wrap(err, "privacy/export: save file")
	})
}

// writeJSON 把 v 编码为缩进的 JSON，写成压缩包中的文件 name。
func writeJSON(zw *zip.Writer, name string, modified time.Time, v any) error {
	w, err := zw.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Deflate, Modified: modified})
	if err != nil {
		return err
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// check 确认 ref 有效，并且属于 privacy.Service 的账号体系。
func (s *Service) check(ref identity.Ref) error {
	if err := ref.Validate(); err != nil {
		return errors.Wrap(err, "privacy/export")
	}
	if ref.Type != s.typ {
		return errors.Newf("privacy/export: subject type %q does not belong to service %q", ref.Type, s.typ)
	}
	return nil
}
