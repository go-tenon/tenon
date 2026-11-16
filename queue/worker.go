package queue

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"runtime/debug"
	"sync"
	"sync/atomic"
	"time"

	"github.com/cockroachdb/errors"
	"github.com/google/uuid"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/uptrace/bun"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"

	"github.com/go-tenon/tenon/logging"
)

// fetchWait 是一次拉取任务的最长等待。连接中断、NATS 重启之后，最多这么久就重新拉取。
const fetchWait = 5 * time.Second

// ErrShuttingDown 是处理函数 context 的取消原因：停止等待超时，任务已放回 JetStream。
var ErrShuttingDown = errors.New("queue: shutting down")

// dbTimeout 是写入失败任务、安排重试时单次数据库或 NATS 操作的超时。这些操作不使用 Run 的 context，
// 停止过程中仍能写回处理结果。
const dbTimeout = 30 * time.Second

// Worker 转发已发布的任务，并处理已注册 topic 的任务。
//
// 转发：Topic.Publish 把任务写入 WAL，Worker 经由逻辑复制槽（SlotName）在事务提交后立即读到，发布到 JetStream 后
// 确认位置。一个槽同时只能被一个连接使用，多个 Worker 中只有一个在转发，其余的待命，它停止后由其中一个接手。
// 执行时间离现在超过 Config.MaxDefer 的任务存入 queue_scheduled，快到时间时再写回 WAL；所有 Worker 都检查它，互不重复。
//
// 处理：每个 topic 一个 JetStream consumer，同一个应用的所有 Worker 共用它，每个任务只交给其中一个；
// 每个 Worker 同时处理的任务不超过 topic 的并发数。处理函数返回 nil 时确认任务；返回错误时按退避时间重试，
// 次数用完或 IsPermanent 报告为 true 时转入失败任务表。处理期间每 Lease/3 延长一次租约。
//
// Run 的 ctx 取消后，先停止接收新任务和转发，再等待处理中的任务完成，最多等待 Config.ShutdownTimeout；
// 超时仍未完成的任务立即放回 JetStream，处理函数的 context 以 ErrShuttingDown 取消。Run 在这一切完成后返回，
// 之后可以关闭数据库。
//
//	w := queue.NewWorker(db, nc, cfg, logger)
//	SendMail.Handle(w, mailer.Send)
//	err := w.Run(ctx)
type Worker struct {
	db        *bun.DB
	nc        *nats.Conn          // 应用的连接，Worker 不关闭它
	slot      string              // 逻辑复制槽，Run 开始时确定
	js        jetstream.JetStream // Run 开始时创建
	failed    *FailedJobs
	cfg       Config
	logger    *slog.Logger
	observers []Observer
	tracer    trace.Tracer // 为 nil 时不创建 span

	relaying  atomic.Bool         // 这个 Worker 正在转发
	relayJobs metric.Int64Counter // 为 nil 时不记录

	mu       sync.Mutex
	handlers []*handler
	started  bool

	// stopping 在停止时置位，之后收到的任务直接放回。lifecycle 保护它和 inflight 的计数。
	lifecycle sync.RWMutex
	stopping  bool
	inflight  sync.WaitGroup
	running   sync.WaitGroup // 处理任务的 goroutine，包括停止后直接放回任务的
	abandon   chan struct{}  // 停止等待超时，放回处理中的任务
}

// handler 是一个 topic 的处理函数和它的设置。
type handler struct {
	topic       string
	fn          func(ctx context.Context, payload []byte) error
	concurrency int
	maxAttempts int
}

// WorkerOption 调整 NewWorker 创建的 Worker。
type WorkerOption func(*Worker)

// NewWorker 创建 Worker。db 是发布任务的数据库，必须已执行 Migrations 中的迁移；nc 是 natsx.Connect 返回的连接，
// 可以与广播等组件共用，Worker 不关闭它；cfg 是 stream 和各 topic 的默认设置；
// logger 记录任务失败，nil 时使用 slog.Default()。db 或 nc 为 nil、cfg 不合法时 panic。
func NewWorker(db *bun.DB, nc *nats.Conn, cfg Config, logger *slog.Logger, opts ...WorkerOption) *Worker {
	if db == nil {
		panic("queue: nil database")
	}
	if nc == nil {
		panic("queue: nil nats connection")
	}
	if err := cfg.Validate(); err != nil {
		panic(errors.Wrap(err, "queue"))
	}
	w := &Worker{
		db:      db,
		nc:      nc,
		failed:  NewFailedJobs(db),
		cfg:     cfg,
		logger:  logging.Or(logger).With(logging.AttrComponent, "queue"),
		abandon: make(chan struct{}),
	}
	for _, opt := range opts {
		opt(w)
	}
	return w
}

// handle 注册 topic 的处理函数，见 Topic.Handle。
func (w *Worker) handle(topic string, fn func(context.Context, []byte) error, opts []HandleOption) {
	if !validName(topic) {
		panic(fmt.Sprintf("queue: topic %q must be 1 to 64 letters, digits, '.', '_' or '-', without empty segments", topic))
	}
	h := &handler{topic: topic, fn: fn, concurrency: w.cfg.Concurrency, maxAttempts: w.cfg.MaxAttempts}
	for _, opt := range opts {
		opt(h)
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.started {
		panic(fmt.Sprintf("queue: Handle(%q) after Run", topic))
	}
	for _, other := range w.handlers {
		if other.topic == topic {
			panic(fmt.Sprintf("queue: topic %q handled twice", topic))
		}
		if consumerName(other.topic) == consumerName(topic) {
			panic(fmt.Sprintf("queue: topics %q and %q differ only in '.' and '_' and cannot both be handled", other.topic, topic))
		}
	}
	w.handlers = append(w.handlers, h)
}

// Run 转发任务、处理已注册 topic 的任务，直到 ctx 被取消，然后按 Worker 的说明停止，返回 nil。
// 连不上 NATS、建不了 stream 或 consumer、复制槽不存在或已失效时返回错误。没有注册处理函数时只转发。
// 第二次调用时返回错误。
func (w *Worker) Run(ctx context.Context) error {
	w.mu.Lock()
	handlers, started := w.handlers, w.started
	w.started = true
	w.mu.Unlock()
	if started {
		return errors.New("queue: Run called twice")
	}

	// 启动检查不随 ctx 取消：已经取消的 ctx 也照常启动再停止。
	if err := withTimeout(func(dbCtx context.Context) error {
		slot, err := slotOf(dbCtx, w.db)
		if err == nil {
			err = checkSlot(dbCtx, w.db, slot)
		}
		w.slot = slot
		return err
	}); err != nil {
		return err
	}

	js, err := jetstream.New(w.nc, jetstream.WithPublishAsyncTimeout(publishTimeout))
	if err != nil {
		return errors.Wrap(err, "queue: jetstream")
	}
	defer func() { _ = w.nc.FlushTimeout(time.Second) }() // 发出最后的确认；连接由应用关闭
	w.js = js
	consumers := make([]jetstream.Consumer, len(handlers))
	setupCtx, cancel := context.WithTimeout(context.Background(), setupTimeout)
	defer cancel()
	if err := ensureStream(setupCtx, js, w.cfg); err != nil {
		return err
	}
	for i, h := range handlers {
		if consumers[i], err = w.ensureConsumer(setupCtx, h); err != nil {
			return err
		}
	}

	var background, fetching sync.WaitGroup
	background.Go(func() { w.relay(ctx) })
	background.Go(func() { w.dispatchLoop(ctx) })
	for i, h := range handlers {
		fetching.Go(func() { w.consume(ctx, h, consumers[i]) })
	}
	<-ctx.Done()

	w.lifecycle.Lock()
	w.stopping = true
	w.lifecycle.Unlock()
	fetching.Wait() // 不再拉取新的任务
	w.drain()
	w.running.Wait()
	background.Wait()
	return nil
}

// consume 从 c 拉取 h 的任务交给处理函数，同时处理的不超过 h.concurrency 个，直到 ctx 结束。
// 只在有空闲名额时拉取，拉取的条数等于空闲的名额：任务不在 Worker 中排队，租约从开始处理时才起作用。
func (w *Worker) consume(ctx context.Context, h *handler, c jetstream.Consumer) {
	slots := make(chan struct{}, h.concurrency)
	for {
		select {
		case slots <- struct{}{}:
		case <-ctx.Done():
			return
		}
		n := 1
	free:
		for n < h.concurrency {
			select {
			case slots <- struct{}{}:
				n++
			default:
				break free
			}
		}
		got, err := w.fetch(ctx, h, c, n, slots)
		for range n - got {
			<-slots
		}
		if err == nil || ctx.Err() != nil {
			continue
		}
		w.logger.Error("fetch jobs failed", "topic", h.topic, "error", err)
		if errors.Is(err, jetstream.ErrConsumerNotFound) || errors.Is(err, jetstream.ErrConsumerDeleted) {
			// consumer 被人删除了：重新创建，从 stream 中还没确认的任务继续。
			setupCtx, cancel := context.WithTimeout(ctx, setupTimeout)
			if again, err := w.ensureConsumer(setupCtx, h); err == nil {
				c = again
			}
			cancel()
		}
		wait(ctx, time.Second)
	}
}

// fetch 拉取最多 n 个任务，每个任务在新的 goroutine 中处理，处理完归还 slots 中的一个名额，返回拉取到的条数。
func (w *Worker) fetch(ctx context.Context, h *handler, c jetstream.Consumer, n int, slots chan struct{}) (int, error) {
	fetchCtx, cancel := context.WithTimeout(ctx, fetchWait)
	defer cancel()
	batch, err := c.Fetch(n, jetstream.FetchContext(fetchCtx))
	if err != nil {
		return 0, err
	}
	got := 0
	for m := range batch.Messages() {
		got++
		w.running.Go(func() {
			defer func() { <-slots }()
			w.process(h, m)
		})
	}
	err = batch.Error()
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) || errors.Is(err, nats.ErrTimeout) {
		err = nil // 等待时间内没有任务
	}
	return got, err
}

// drain 等待处理中的任务写回结果，最多等待 ShutdownTimeout，超时后放回它们。
func (w *Worker) drain() {
	idle := make(chan struct{})
	go func() {
		w.inflight.Wait()
		close(idle)
	}()
	t := time.NewTimer(w.cfg.ShutdownTimeout)
	defer t.Stop()
	select {
	case <-idle:
	case <-t.C:
		close(w.abandon)
		<-idle
	}
}

// begin 登记一个开始处理的任务；停止之后返回 false，任务放回队列。
func (w *Worker) begin() bool {
	w.lifecycle.RLock()
	defer w.lifecycle.RUnlock()
	if w.stopping {
		return false
	}
	w.inflight.Add(1)
	return true
}

// process 执行一个任务并写回结果，期间按 Lease/3 延长租约。确认和放回不等待 JetStream 的回复：
// 丢失时任务在租约到期后重新投递，与至少执行一次的约定一致。
func (w *Worker) process(h *handler, m jetstream.Msg) {
	if !w.begin() {
		_ = m.Nak() // 停止时刚收到的任务直接放回
		return
	}
	defer w.inflight.Done()

	delivered := 1 // 这条消息第几次投递：租约到期、放回之后再次投递时增加
	if md, err := m.Metadata(); err == nil {
		delivered = int(md.NumDelivered)
	}
	var env envelope
	if err := json.Unmarshal(m.Data(), &env); err != nil {
		w.logger.Error("drop undecodable message", "topic", h.topic, "error", err)
		_ = withTimeout(func(ctx context.Context) error {
			return w.failed.record(ctx, failedJob{ID: uuid.Must(uuid.NewV7()), Topic: h.topic, Payload: m.Data(),
				Attempts: delivered, Error: fmt.Sprintf("%+v", errors.Wrap(err, "queue: decode message")), FailedAt: time.Now().UTC()})
		})
		_ = m.Ack()
		return
	}
	j := Job{ID: env.ID, Topic: h.topic, Payload: env.Payload, Metadata: env.Metadata, Attempt: env.Attempts + delivered}
	if delivered == 1 {
		j.AvailableAt = env.AvailableAt
	}
	ctx, cancel := context.WithCancelCause(w.jobContext(j, h.maxAttempts))
	defer cancel(nil)
	started := time.Now()
	w.observe(ctx, Event{Kind: JobStarted, Job: j, Started: started})
	result := make(chan error, 1)
	go func() { result <- call(ctx, h.fn, j.Payload) }()

	ticker := time.NewTicker(w.cfg.Lease / 3)
	defer ticker.Stop()
	for {
		select {
		case err := <-result:
			if err == nil {
				_ = m.Ack()
				w.observe(ctx, Event{Kind: JobSucceeded, Job: j, Started: started, Duration: time.Since(started)})
				return
			}
			w.nack(ctx, m, j, env, err, h.maxAttempts, started)
			return
		case <-ticker.C:
			_ = m.InProgress()
		case <-w.abandon:
			_ = m.Nak()
			w.observe(ctx, Event{Kind: JobReleased, Job: j, Started: started, Duration: time.Since(started)})
			cancel(ErrShuttingDown)
			return
		}
	}
}

// nack 安排重试，或者把任务转入失败任务表。重试是一条新的消息，执行次数记在 envelope 中：
// 不超过 MaxDefer 的交给 JetStream 定时投递，更晚的存入 queue_scheduled。安排好之后才确认这一条。
func (w *Worker) nack(ctx context.Context, m jetstream.Msg, j Job, env envelope, cause error, maxAttempts int, started time.Time) {
	if !IsPermanent(cause) && j.Attempt < maxAttempts {
		delay := retryDelay(cause, j.Attempt)
		later := env
		later.AvailableAt, later.Attempts = time.Now().Add(delay).UTC(), j.Attempt
		if err := withTimeout(func(ctx context.Context) error { return w.retryLater(ctx, j.Topic, later, delay) }); err != nil {
			// 安排不了时让 JetStream 稍后重新投递这一条，执行次数照样增加。
			w.logger.ErrorContext(ctx, "schedule retry failed", "topic", j.Topic, "error", err)
			nakAfter(m, min(delay, w.cfg.MaxDefer))
			return
		}
		_ = m.Ack()
		w.logger.WarnContext(ctx, "job failed, will retry", "topic", j.Topic, "retry_in", delay, "error", cause)
		w.observe(ctx, Event{Kind: JobRetrying, Job: j, Started: started, Duration: time.Since(started), Err: cause, RetryAt: later.AvailableAt})
		return
	}
	record := failedJob{ID: j.ID, Topic: j.Topic, Payload: j.Payload, Metadata: j.Metadata,
		Attempts: j.Attempt, Error: fmt.Sprintf("%+v", cause), FailedAt: time.Now().UTC()}
	if err := withTimeout(func(ctx context.Context) error { return w.failed.record(ctx, record) }); err != nil {
		// 失败任务写不进去时不确认：稍后重新投递，再次失败时再写。
		w.logger.ErrorContext(ctx, "move job to failed jobs failed", "topic", j.Topic, "error", err)
		nakAfter(m, min(w.cfg.MaxDefer, time.Minute))
		return
	}
	_ = m.Ack()
	w.logger.ErrorContext(ctx, "job failed", "topic", j.Topic, "error", cause)
	w.observe(ctx, Event{Kind: JobFailed, Job: j, Started: started, Duration: time.Since(started), Err: cause})
}

// retryLater 安排 env 在 env.AvailableAt 再次执行：delay 不超过 MaxDefer 时交给 JetStream，否则存入 queue_scheduled。
func (w *Worker) retryLater(ctx context.Context, topic string, env envelope, delay time.Duration) error {
	if delay > w.cfg.MaxDefer {
		return w.postpone(ctx, topic, env)
	}
	body, err := json.Marshal(env)
	if err != nil {
		return err
	}
	return w.publish(ctx, topic, body, msgID(env.ID, env.Attempts, env.AvailableAt), env.AvailableAt)
}

// nakAfter 让 JetStream 在 d 之后重新投递 m。
func nakAfter(m jetstream.Msg, d time.Duration) {
	if d > 0 {
		_ = m.NakWithDelay(d)
	} else {
		_ = m.Nak()
	}
}

// postpone 把 env 存入 queue_scheduled，快到 env.AvailableAt 时再写回 WAL。
func (w *Worker) postpone(ctx context.Context, topic string, env envelope) error {
	body, err := json.Marshal(env)
	if err != nil {
		return err
	}
	return schedule(ctx, w.db, []scheduledRow{{ID: env.ID, Topic: topic, Body: body, AvailableAt: env.AvailableAt, CreatedAt: time.Now().UTC()}})
}

// jobContext 返回处理函数的 context：还原发布方的链路和 request_id，附上任务 ID、执行次数。
func (w *Worker) jobContext(j Job, maxAttempts int) context.Context {
	attrs := []slog.Attr{slog.String(logging.AttrMessageID, j.ID.String()), slog.Int(logging.AttrAttempt, j.Attempt)}
	for _, key := range propagated {
		if v := j.Metadata[key]; v != "" {
			attrs = append(attrs, slog.String(key, v))
		}
	}
	ctx := logging.WithAttrs(w.startSpan(context.Background(), j), attrs...)
	return context.WithValue(ctx, attemptKey{}, attemptInfo{j.Attempt, maxAttempts})
}

// call 调用处理函数，把 panic 转为错误。
func call(ctx context.Context, fn func(context.Context, []byte) error, payload []byte) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = errors.Newf("queue: panic: %v\n%s", r, debug.Stack())
		}
	}()
	return fn(ctx, payload)
}

// withTimeout 以 dbTimeout 调用 fn，独立于 Run 和处理函数的 context。
func withTimeout(fn func(ctx context.Context) error) error {
	ctx, cancel := context.WithTimeout(context.Background(), dbTimeout)
	defer cancel()
	return fn(ctx)
}

// wait 等待 d 或 ctx 结束。
func wait(ctx context.Context, d time.Duration) {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
	case <-ctx.Done():
	}
}
