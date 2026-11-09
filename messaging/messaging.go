// Package messaging 基于 Watermill 与 Postgres（watermill-sql 的队列模式）收发异步消息，
// 用于发邮件、生成导出文件等不应阻塞请求、失败需要重试的任务。不需要额外部署消息中间件。
//
// 本包只做装配，不在 Watermill 之外再包一层抽象：
//
//	PublishTx    在业务事务中发布：业务数据与消息一起提交或一起回滚
//	Bus.Publish  不在事务中发布
//	Handle       注册一个 topic 的处理函数（payload 为 JSON，自动解码为 T）
//	Bus.Run      运行全部处理函数，直到 ctx 取消
//
// # 投递语义
//
// 每个 topic 一张表（watermill_<topic>），消息被处理成功后删除。
// 同一个 topic 只能注册一个处理函数；多个实例同时运行时，一条消息只会被其中一个处理（队列语义），
// 但可能被重复处理（处理中实例崩溃、超过 30 秒未确认），处理函数需要幂等。
//
// 处理函数返回错误时按 1s、2s、4s 重试 3 次；仍失败则记录 Error 日志，
// 并把消息移到 poison topic（watermill_poison 表）供人工排查，不再阻塞后续消息。
//
// # 用法
//
//	// 发布：在 bun 事务中
//	err := db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
//		if _, err := tx.NewInsert().Model(order).Exec(ctx); err != nil {
//			return err
//		}
//		return messaging.PublishTx(ctx, tx.Tx, "order_created", OrderCreated{ID: order.ID})
//	})
//
//	// 处理：在启动时注册（app.Setup 中），会立即创建该 topic 的表
//	err := messaging.Handle(a.Bus, "order_created", func(ctx context.Context, e OrderCreated) error {
//		return mailer.Send(ctx, ...)
//	})
//
// PublishTx 不会自动建表，因此发布的 topic 必须已经在某处用 Handle 注册过（或调用过 Bus.Declare）。
package messaging

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/ThreeDotsLabs/watermill"
	wsql "github.com/ThreeDotsLabs/watermill-sql/v4/pkg/sql"
	"github.com/ThreeDotsLabs/watermill/message"
	"github.com/ThreeDotsLabs/watermill/message/router/middleware"

	"github.com/ryan053857/go-kit/httpx"
	"github.com/ryan053857/go-kit/logger"
)

// PoisonTopic 存放重试后仍处理失败的消息。
const PoisonTopic = "poison"

const metaRequestID = "request_id"

var (
	schema  = wsql.PostgreSQLQueueSchema{}
	offsets = wsql.PostgreSQLQueueOffsetsAdapter{DeleteOnAck: true}
)

// Bus 管理消息的处理函数。用 New 创建。
type Bus struct {
	db     *sql.DB
	log    watermill.LoggerAdapter
	router *message.Router
	sub    *wsql.Subscriber
	pub    message.Publisher

	running chan struct{}

	mu     sync.Mutex
	topics map[string]bool
}

// New 返回使用 db 的 Bus。
func New(db *sql.DB) (*Bus, error) {
	// Watermill 的 Info 日志（建表语句、启停处理函数等）降为 Debug。
	log := watermill.NewSlogLoggerWithLevelMapping(slog.Default(), map[slog.Level]slog.Level{slog.LevelInfo: slog.LevelDebug})
	sub, err := wsql.NewSubscriber(wsql.BeginnerFromStdSQL(db), wsql.SubscriberConfig{
		SchemaAdapter:    schema,
		OffsetsAdapter:   offsets,
		InitializeSchema: true,
	}, log)
	if err != nil {
		return nil, fmt.Errorf("messaging: %w", err)
	}
	pub, err := wsql.NewPublisher(wsql.BeginnerFromStdSQL(db), wsql.PublisherConfig{SchemaAdapter: schema, AutoInitializeSchema: true}, log)
	if err != nil {
		return nil, fmt.Errorf("messaging: %w", err)
	}
	router, err := message.NewRouter(message.RouterConfig{CloseTimeout: 30 * time.Second}, log)
	if err != nil {
		return nil, fmt.Errorf("messaging: %w", err)
	}
	poison, err := middleware.PoisonQueue(pub, PoisonTopic)
	if err != nil {
		return nil, fmt.Errorf("messaging: %w", err)
	}
	router.AddMiddleware(
		poison,
		logFailures,
		middleware.Retry{
			MaxRetries:      3,
			InitialInterval: time.Second,
			MaxInterval:     10 * time.Second,
			Multiplier:      2,
		}.Middleware,
		middleware.Recoverer,
	)
	return &Bus{db: db, log: log, router: router, sub: sub, pub: pub, running: make(chan struct{}), topics: map[string]bool{}}, nil
}

// Handle 注册 topic 的处理函数，并立即创建该 topic 的表。payload 按 JSON 解码为 T。
// 处理函数的 ctx 带有 message_id、topic，以及发布方请求的 request_id 日志字段。
// 必须在 Run 之前调用；同一个 topic 只能注册一次。
func Handle[T any](b *Bus, topic string, fn func(ctx context.Context, payload T) error) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.topics[topic] {
		return fmt.Errorf("messaging: topic %q already has a handler", topic)
	}
	if err := b.sub.SubscribeInitialize(topic); err != nil {
		return fmt.Errorf("messaging: declare %q: %w", topic, err)
	}
	b.topics[topic] = true

	b.router.AddConsumerHandler(topic, topic, b.sub, func(msg *message.Message) error {
		attrs := []slog.Attr{slog.String("topic", topic), slog.String("message_id", msg.UUID)}
		if id := msg.Metadata.Get(metaRequestID); id != "" {
			attrs = append(attrs, slog.String("request_id", id))
		}
		ctx := logger.WithAttrs(msg.Context(), attrs...)

		var payload T
		if err := json.Unmarshal(msg.Payload, &payload); err != nil {
			return fmt.Errorf("messaging: decode %s payload: %w", topic, err)
		}
		return fn(ctx, payload)
	})
	return nil
}

// Declare 创建 topic 的表，用于只发布、不在本进程处理的 topic。
func (b *Bus) Declare(topics ...string) error {
	for _, t := range topics {
		if err := b.sub.SubscribeInitialize(t); err != nil {
			return fmt.Errorf("messaging: declare %q: %w", t, err)
		}
	}
	return nil
}

// Publish 不在事务中发布一条消息，payload 编码为 JSON。topic 的表不存在时自动创建。
func (b *Bus) Publish(ctx context.Context, topic string, payload any) error {
	msg, err := newMessage(ctx, payload)
	if err != nil {
		return err
	}
	if err := b.pub.Publish(topic, msg); err != nil {
		return fmt.Errorf("messaging: publish %s: %w", topic, err)
	}
	return nil
}

// PublishTx 在事务 tx 中发布一条消息，payload 编码为 JSON。消息在事务提交后才可见，回滚则丢弃。
// 使用 bun 时传 bun.Tx 的 Tx 字段。topic 的表必须已存在（见包文档）。
func PublishTx(ctx context.Context, tx *sql.Tx, topic string, payload any) error {
	msg, err := newMessage(ctx, payload)
	if err != nil {
		return err
	}
	pub, err := wsql.NewPublisher(wsql.TxFromStdSQL(tx), wsql.PublisherConfig{SchemaAdapter: schema}, watermill.NopLogger{})
	if err != nil {
		return fmt.Errorf("messaging: %w", err)
	}
	if err := pub.Publish(topic, msg); err != nil {
		return fmt.Errorf("messaging: publish %s: %w", topic, err)
	}
	return nil
}

// Run 运行全部处理函数，直到 ctx 取消（返回 nil）或出错。没有注册处理函数时等待 ctx 取消。
// 停止时最多等待 30 秒让进行中的处理完成。
func (b *Bus) Run(ctx context.Context) error {
	b.mu.Lock()
	n := len(b.topics)
	b.mu.Unlock()
	if n == 0 { // 没有处理函数时 Router 不会进入运行状态
		close(b.running)
		<-ctx.Done()
		return nil
	}
	go func() {
		select {
		case <-b.router.Running():
			close(b.running)
		case <-ctx.Done():
		}
	}()
	// ctx 取消时用 Close 优雅停止：等待进行中的处理与确认完成。
	// 直接把 ctx 传给 Router 会中断正在确认的消息，导致它在下次启动时被重复处理。
	go func() {
		<-ctx.Done()
		_ = b.router.Close()
	}()
	if err := b.router.Run(context.WithoutCancel(ctx)); err != nil {
		return fmt.Errorf("messaging: %w", err)
	}
	return nil
}

// Running 在 Run 开始处理消息后关闭。
func (b *Bus) Running() <-chan struct{} {
	return b.running
}

func newMessage(ctx context.Context, payload any) (*message.Message, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("messaging: encode payload: %w", err)
	}
	msg := message.NewMessage(watermill.NewUUID(), data)
	if id := httpx.RequestIDFrom(ctx); id != "" {
		msg.Metadata.Set(metaRequestID, id)
	}
	return msg, nil
}

// logFailures 在重试耗尽、消息即将移入 poison topic 时记录 Error 日志。
func logFailures(h message.HandlerFunc) message.HandlerFunc {
	return func(msg *message.Message) ([]*message.Message, error) {
		out, err := h(msg)
		if err != nil {
			slog.ErrorContext(msg.Context(), "message handling failed, moved to poison queue",
				"topic", message.SubscribeTopicFromCtx(msg.Context()),
				"message_id", msg.UUID,
				"request_id", msg.Metadata.Get(metaRequestID),
				"err", fmt.Sprintf("%+v", err))
		}
		return out, err
	}
}
