package article

import (
	"context"

	"github.com/cockroachdb/errors"
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
	"github.com/go-tenon/tenon/sheet/exports"
)

// ExportRequest 是后台导出的任务：哪一次导出、谁在导出、导出哪些列。
type ExportRequest struct {
	ID      uuid.UUID        `json:"id"`
	Subject identity.Subject `json:"subject"`
	Columns []string         `json:"columns"`
}

// ExportJob 是后台导出文章的任务。
var ExportJob = queue.Topic[ExportRequest]("articles.export")

// QueueExport 在后台导出文章。
type QueueExport struct {
	db       *bun.DB
	hub      *broadcast.Hub
	exporter *exports.Service
	export   *Export
}

func NewQueueExport(db *bun.DB, hub *broadcast.Hub, exporter *exports.Service, export *Export) *QueueExport {
	return &QueueExport{db: db, hub: hub, exporter: exporter, export: export}
}

// Handle 检查要导出的列，登记导出并发布任务。
func (a *QueueExport) Handle(ctx context.Context, subject identity.Subject, format sheet.Format, columns []string) (*exports.Export, error) {
	if _, err := exportColumns.Select(columns); err != nil {
		return nil, err
	}
	var exp *exports.Export
	err := database.RunInTx(ctx, a.db, nil, func(ctx context.Context, tx database.Tx) error {
		var err error
		if exp, err = a.exporter.Start(ctx, tx, subject, "articles.export", format); err != nil {
			return err
		}
		return ExportJob.Publish(ctx, tx, ExportRequest{ID: exp.ID, Subject: subject, Columns: columns})
	})
	return exp, err
}

// ExportProgress 是导出的进度，ErrorMessage 是导出失败的原因。
type ExportProgress struct {
	*exports.Export
	ErrorMessage string `json:"error_message,omitempty"`
}

func exportProgressOf(ctx context.Context, exp *exports.Export) ExportProgress {
	return ExportProgress{Export: exp, ErrorMessage: exp.ErrorMessage(ctx)}
}

type ExportProgressOutput struct {
	Body ExportProgress
}

// HTTP 处理 POST /articles/exports，返回导出的 ID，前端用它查看进度。
func (a *QueueExport) HTTP(ctx context.Context, in *ExportInput) (*ExportProgressOutput, error) {
	subject, _ := identity.FromContext(ctx)
	exp, err := a.Handle(ctx, subject, in.Format, in.Columns)
	if err != nil {
		return nil, err
	}
	return &ExportProgressOutput{Body: exportProgressOf(ctx, exp)}, nil
}

// Job 统计行数，再把文章写入记录进度的 Writer。
func (a *QueueExport) Job(ctx context.Context, req ExportRequest) error {
	return a.exporter.Process(ctx, req.ID, func(ctx context.Context, exp *exports.Export, w *exports.Writer) error {
		cols, err := exportColumns.Select(req.Columns)
		if err != nil {
			return err
		}
		total, err := a.export.query(req.Subject).Count(ctx)
		if err != nil {
			return errors.Wrap(err, "count articles")
		}
		if err := w.SetTotal(int(total)); err != nil {
			return err
		}
		return a.export.Handle(ctx, req.Subject, w, cols)
	}, a.saved)
}

// saved 在每次保存进度的事务中调用：提交后广播进度；导出结束时在同一个事务中通知导出的人。
func (a *QueueExport) saved(ctx context.Context, tx database.Tx, exp *exports.Export) error {
	tx.AfterCommit(func(ctx context.Context) error {
		return a.hub.Publish(ctx, operationTopic(exp.ID), nil)
	})
	if !exp.Finished() {
		return nil
	}
	_, err := notification.Send(ctx, tx, notification.Notification{
		Event: "articles.exported",
		Data: map[string]any{
			"export_id": exp.ID, "status": exp.Status,
			"filename": exp.Filename, "url": exp.URL, "expires_at": exp.ExpiresAt,
		},
	}, notification.RecipientOf(exp.Owner()))
	return err
}

type ExportIDInput struct {
	ID uuid.UUID `path:"id"`
}

// Progress 处理 GET /articles/exports/{id}/progress：连接后先推送当前进度，之后每次保存进度时推送一次，
// 导出结束后关闭连接。
func (a *QueueExport) Progress(ctx context.Context, in *ExportIDInput) (*huma.StreamResponse, error) {
	subject, _ := identity.FromContext(ctx)
	if _, err := a.exporter.Get(ctx, subject, in.ID); err != nil {
		return nil, err // 还没有开始推送，客户端照常收到 404
	}
	return rest.SSE(func(s *httpx.Stream) error {
		events := a.hub.Subscribe(operationTopic(in.ID)) // 先订阅，再查询
		defer events.Close()
		for {
			exp, err := a.exporter.Get(s.Context(), subject, in.ID)
			if err != nil {
				return err
			}
			if err := s.SendJSON(httpx.Event{Name: "progress"}, exportProgressOf(s.Context(), exp)); err != nil {
				return nil // 客户端已经离开
			}
			if exp.Finished() {
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
