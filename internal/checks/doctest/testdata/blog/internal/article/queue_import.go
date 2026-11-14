package article

import (
	"context"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/broadcast"
	"github.com/go-tenon/tenon/database"
	"github.com/go-tenon/tenon/httpx"
	"github.com/go-tenon/tenon/httpx/rest"
	"github.com/go-tenon/tenon/identity"
	"github.com/go-tenon/tenon/notification"
	"github.com/go-tenon/tenon/queue"
	"github.com/go-tenon/tenon/sheet"
	"github.com/go-tenon/tenon/sheet/imports"
)

// ImportJob 是后台导入文章的任务，payload 是导入的 ID。
var ImportJob = queue.Topic[uuid.UUID]("articles.import")

// QueueImport 在后台导入文章。
type QueueImport struct {
	db       *bun.DB
	hub      *broadcast.Hub
	importer *imports.Service
	rows     *Import
}

func NewQueueImport(db *bun.DB, hub *broadcast.Hub, importer *imports.Service, rows *Import) *QueueImport {
	return &QueueImport{db: db, hub: hub, importer: importer, rows: rows}
}

// Handle 登记 subject 上传的文件 fileID 的导入，并发布处理它的任务。
func (a *QueueImport) Handle(ctx context.Context, subject identity.Subject, fileID uuid.UUID) (*imports.Import, error) {
	var imp *imports.Import
	err := database.RunInTx(ctx, a.db, nil, func(ctx context.Context, tx database.Tx) error {
		var err error
		if imp, err = a.importer.Start(ctx, tx, subject, "articles.import", fileID); err != nil {
			return err
		}
		return ImportJob.Publish(ctx, tx, imp.ID)
	})
	return imp, err
}

type QueueImportInput struct {
	Body struct {
		FileID uuid.UUID `json:"file_id"`
	}
}

// ImportProgress 是导入的进度，ErrorMessage 是导入失败的原因。
type ImportProgress struct {
	*imports.Import
	ErrorMessage string `json:"error_message,omitempty"`
}

func progressOf(ctx context.Context, imp *imports.Import) ImportProgress {
	return ImportProgress{Import: imp, ErrorMessage: imp.ErrorMessage(ctx)}
}

type ImportProgressOutput struct {
	Body ImportProgress
}

// HTTP 处理 POST /articles/imports。
func (a *QueueImport) HTTP(ctx context.Context, in *QueueImportInput) (*ImportProgressOutput, error) {
	subject, _ := identity.FromContext(ctx)
	imp, err := a.Handle(ctx, subject, in.Body.FileID)
	if err != nil {
		return nil, err
	}
	return &ImportProgressOutput{Body: progressOf(ctx, imp)}, nil
}

// Job 处理后台导入的任务。
func (a *QueueImport) Job(ctx context.Context, id uuid.UUID) error {
	return imports.Process(ctx, a.importer, id, func(ctx context.Context, imp *imports.Import, rec *sheet.Record[Changes]) error {
		return a.rows.row(ctx, imp.Owner(), rec)
	}, a.saved)
}

// saved 在每次保存进度的事务中调用：提交后广播进度；导入结束时在同一个事务中通知发起导入的人。
func (a *QueueImport) saved(ctx context.Context, tx database.Tx, imp *imports.Import) error {
	tx.AfterCommit(func(ctx context.Context) error {
		return a.hub.Publish(ctx, operationTopic(imp.ID), nil)
	})
	if !imp.Finished() {
		return nil
	}
	_, err := notification.Send(ctx, tx, notification.Notification{
		Event: "articles.imported",
		Data: map[string]any{
			"import_id": imp.ID, "status": imp.Status,
			"created": imp.Processed - imp.Failed, "failed": imp.Failed,
		},
	}, notification.RecipientOf(imp.Owner()))
	return err
}

type ImportIDInput struct {
	ID uuid.UUID `path:"id"`
}

// Progress 处理 GET /articles/imports/{id}/progress：连接后先推送当前进度，之后每次保存进度时推送一次，
// 导入结束后关闭连接。
func (a *QueueImport) Progress(ctx context.Context, in *ImportIDInput) (*huma.StreamResponse, error) {
	subject, _ := identity.FromContext(ctx)
	if _, err := a.importer.Get(ctx, subject, in.ID); err != nil {
		return nil, err // 还没有开始推送，客户端照常收到 404
	}
	return rest.SSE(func(s *httpx.Stream) error {
		events := a.hub.Subscribe(operationTopic(in.ID)) // 先订阅，再查询，中间保存的进度不会漏掉
		defer events.Close()
		for {
			imp, err := a.importer.Get(s.Context(), subject, in.ID)
			if err != nil {
				return err
			}
			if err := s.SendJSON(httpx.Event{Name: "progress"}, progressOf(s.Context(), imp)); err != nil {
				return nil // 客户端已经离开
			}
			if imp.Finished() {
				return nil
			}
			select {
			case <-s.Done():
				return nil
			case _, ok := <-events.C():
				if !ok {
					return nil // 订阅已结束，客户端重连后先收到当前进度
				}
			}
		}
	}), nil
}

type ImportFailuresInput struct {
	ID     uuid.UUID    `path:"id"`
	Format sheet.Format `query:"format" enum:"csv,xlsx" default:"xlsx"`
}

// Failures 处理 GET /articles/imports/{id}/failures，下载没有导入的行。
func (a *QueueImport) Failures(ctx context.Context, in *ImportFailuresInput) (*huma.StreamResponse, error) {
	subject, _ := identity.FromContext(ctx)
	imp, err := a.importer.Get(ctx, subject, in.ID)
	if err != nil {
		return nil, err
	}
	return download("articles-failures", in.Format, func(w *sheet.Writer) error {
		return a.importer.WriteFailures(ctx, imp, w)
	}), nil
}
