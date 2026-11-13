package article

import (
	"context"

	"github.com/cockroachdb/errors"
	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/broadcast"
	"github.com/go-tenon/tenon/database"
	"github.com/go-tenon/tenon/errorx"
	"github.com/go-tenon/tenon/httpx"
	"github.com/go-tenon/tenon/httpx/rest"
	"github.com/go-tenon/tenon/identity"
	"github.com/go-tenon/tenon/notification"
	"github.com/go-tenon/tenon/operation"
	"github.com/go-tenon/tenon/queue"
)

// BulkPublishRequest 是批量发布的任务：哪一个操作、谁发起的、发布哪些文章。
type BulkPublishRequest struct {
	Operation uuid.UUID        `json:"operation"`
	Subject   identity.Subject `json:"subject"`
	IDs       []uuid.UUID      `json:"ids"`
}

// BulkPublishJob 是批量发布文章的任务。
var BulkPublishJob = queue.Topic[BulkPublishRequest]("articles.bulk_publish")

// BulkPublish 在后台发布多篇文章。
type BulkPublish struct {
	db      *bun.DB
	hub     *broadcast.Hub
	ops     *operation.Service
	publish *Publish
}

func NewBulkPublish(db *bun.DB, hub *broadcast.Hub, ops *operation.Service, publish *Publish) *BulkPublish {
	return &BulkPublish{db: db, hub: hub, ops: ops, publish: publish}
}

// Handle 登记操作并发布任务。
func (a *BulkPublish) Handle(ctx context.Context, subject identity.Subject, ids []uuid.UUID) (*operation.Operation, error) {
	var op *operation.Operation
	err := database.RunInTx(ctx, a.db, nil, func(ctx context.Context, tx database.Tx) error {
		var err error
		if op, err = a.ops.Start(ctx, tx, subject, "articles.bulk_publish"); err != nil {
			return err
		}
		return BulkPublishJob.Publish(ctx, tx, BulkPublishRequest{Operation: op.ID, Subject: subject, IDs: ids})
	})
	return op, err
}

type BulkPublishInput struct {
	Body struct {
		IDs []uuid.UUID `json:"ids" minItems:"1" maxItems:"1000"`
	}
}

// OperationProgress 是操作的进度，ErrorMessage 是操作失败的原因。
type OperationProgress struct {
	*operation.Operation
	ErrorMessage string `json:"error_message,omitempty"`
}

func operationProgressOf(ctx context.Context, op *operation.Operation) OperationProgress {
	return OperationProgress{Operation: op, ErrorMessage: op.ErrorMessage(ctx)}
}

type OperationOutput struct {
	Body OperationProgress
}

// HTTP 处理 POST /articles/bulk-publish，返回操作，前端用它的 ID 查看进度。
func (a *BulkPublish) HTTP(ctx context.Context, in *BulkPublishInput) (*OperationOutput, error) {
	subject, _ := identity.FromContext(ctx)
	op, err := a.Handle(ctx, subject, in.Body.IDs)
	if err != nil {
		return nil, err
	}
	return &OperationOutput{Body: operationProgressOf(ctx, op)}, nil
}

// Job 逐篇发布，每篇保存一次进度。
func (a *BulkPublish) Job(ctx context.Context, req BulkPublishRequest) error {
	return a.ops.Run(ctx, req.Operation, func(ctx context.Context, r *operation.Run) error {
		r.SetTotal(len(req.IDs))
		for _, id := range req.IDs[r.Operation().Processed:] { // 重试时跳过已经保存的
			_, err := a.publish.Handle(ctx, req.Subject, id)
			switch {
			case err == nil, errors.Is(err, ErrAlreadyPublished):
				r.Add(1, 0)
			case errorx.CodeOf(err) != "":
				r.Add(1, 1) // 文章已被删除、不是作者：只影响这一篇
			default:
				return err // 数据库不可用等，队列稍后重试
			}
			if err := r.Save(ctx, nil); err != nil {
				return err
			}
		}
		return nil
	}, a.saved)
}

// operationTopic 是一个操作的广播主题。每次保存进度后发布一条空消息，提醒正在查看的连接重新读取进度。
func operationTopic(id uuid.UUID) string {
	return "operation:" + id.String()
}

// saved 在每次保存进度的事务中调用：提交后广播进度；操作结束时在同一个事务中通知发起人。
func (a *BulkPublish) saved(ctx context.Context, tx database.Tx, op *operation.Operation) error {
	tx.AfterCommit(func(ctx context.Context) error {
		return a.hub.Publish(ctx, operationTopic(op.ID), nil)
	})
	if !op.Finished() {
		return nil
	}
	_, err := notification.Send(ctx, tx, notification.Notification{
		Event: "articles.bulk_published",
		Data: map[string]any{
			"operation_id": op.ID, "status": op.Status,
			"published": op.Processed - op.Failed, "failed": op.Failed,
		},
	}, notification.RecipientOf(op.Owner()))
	return err
}

type OperationIDInput struct {
	ID uuid.UUID `path:"id"`
}

// Progress 处理 GET /articles/bulk-publish/{id}/progress：连接后先推送当前进度，之后每次保存进度时推送一次，
// 操作结束后关闭连接。
func (a *BulkPublish) Progress(ctx context.Context, in *OperationIDInput) (*huma.StreamResponse, error) {
	subject, _ := identity.FromContext(ctx)
	if _, err := a.ops.Get(ctx, subject, in.ID); err != nil {
		return nil, err // 还没有开始推送，客户端照常收到 404
	}
	return rest.SSE(func(s *httpx.Stream) error {
		events := a.hub.Subscribe(operationTopic(in.ID)) // 先订阅，再查询，中间保存的进度不会漏掉
		defer events.Close()
		for {
			op, err := a.ops.Get(s.Context(), subject, in.ID)
			if err != nil {
				return err
			}
			if err := s.SendJSON(httpx.Event{Name: "progress"}, operationProgressOf(s.Context(), op)); err != nil {
				return nil // 客户端已经离开
			}
			if op.Finished() {
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
