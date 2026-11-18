package queue_test

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/database/dbtest"
	"github.com/go-tenon/tenon/errorx"
	"github.com/go-tenon/tenon/logging"
	"github.com/go-tenon/tenon/queue"
	"github.com/go-tenon/tenon/queue/queuetest"
)

type payload struct {
	N int `json:"n"`
}

// jobs 是大多数测试使用的 topic。每个测试的 Worker 有自己的随机 stream，同名的 topic 相互独立。
var jobs = queue.Topic[payload]("jobs")

func openDB(t *testing.T) *bun.DB {
	t.Helper()
	return dbtest.Open(t, queue.Migrations())
}

// start 运行一个 Worker，register 在运行前注册处理函数；返回的函数停止 Worker 并等待它返回。
func start(t *testing.T, db *bun.DB, cfg queue.Config, register func(w *queue.Worker), opts ...queue.WorkerOption) (stop func()) {
	t.Helper()
	w := queue.NewWorker(db, queuetest.Conn(t), cfg, slog.New(slog.DiscardHandler), opts...)
	register(w)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- w.Run(ctx) }()
	var once sync.Once
	stop = func() {
		once.Do(func() {
			cancel()
			select {
			case err := <-done:
				if err != nil {
					t.Errorf("worker: %v", err)
				}
			case <-time.After(30 * time.Second):
				t.Error("worker did not stop")
			}
		})
	}
	t.Cleanup(stop)
	return stop
}

func publish(t *testing.T, db bun.IDB, n int, opts ...queue.PublishOption) {
	t.Helper()
	if err := jobs.Publish(context.Background(), db, payload{N: n}, opts...); err != nil {
		t.Fatal(err)
	}
}

func eventually(t *testing.T, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition not met in time")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

type failedRow struct {
	bun.BaseModel `bun:"table:queue_failed_jobs"`

	ID       uuid.UUID `bun:"id"`
	Topic    string    `bun:"topic"`
	Attempts int       `bun:"attempts"`
	Error    string    `bun:"error"`
}

func failedRows(t *testing.T, db bun.IDB) []failedRow {
	t.Helper()
	var rows []failedRow
	if err := db.NewSelect().Model(&rows).Order("failed_at").Scan(context.Background()); err != nil {
		t.Fatal(err)
	}
	return rows
}

func attr(ctx context.Context, key string) string {
	for _, a := range logging.ContextAttrs(ctx) {
		if a.Key == key {
			return a.Value.String()
		}
	}
	return ""
}

func TestConfig(t *testing.T) {
	t.Parallel()
	if err := queue.DefaultConfig().Validate(); err != nil {
		t.Fatal(err)
	}
	for name, change := range map[string]func(*queue.Config){
		"no stream":       func(c *queue.Config) { c.Stream = "" },
		"dotted stream":   func(c *queue.Config) { c.Stream = "blog.jobs" },
		"zero replicas":   func(c *queue.Config) { c.Replicas = 0 },
		"zero lease":      func(c *queue.Config) { c.Lease = 0 },
		"zero attempts":   func(c *queue.Config) { c.MaxAttempts = 0 },
		"zero concurrent": func(c *queue.Config) { c.Concurrency = 0 },
	} {
		cfg := queue.DefaultConfig()
		change(&cfg)
		if cfg.Validate() == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

// 事务中发布的任务随事务提交，回滚时不留下；处理函数的 context 带着发布方的 request_id 和执行次数。
func TestPublishInTransaction(t *testing.T) {
	t.Parallel()
	db := openDB(t)
	type seen struct {
		n                         int
		requestID, msgID, attempt string
	}
	got := make(chan seen, 2)
	stop := start(t, db, queuetest.Config(t), func(w *queue.Worker) {
		jobs.Handle(w, func(ctx context.Context, p payload) error {
			got <- seen{p.N, logging.RequestID(ctx), attr(ctx, logging.AttrMessageID), attr(ctx, logging.AttrAttempt)}
			return nil
		})
	})
	ctx := logging.WithRequestID(context.Background(), "req-1")
	rollback := errors.New("rollback")
	err := db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if err := jobs.Publish(ctx, tx, payload{N: 1}); err != nil {
			return err
		}
		return rollback
	})
	if !errors.Is(err, rollback) {
		t.Fatal(err)
	}
	err = db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		return jobs.Publish(ctx, tx, payload{N: 2})
	})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case v := <-got:
		if v.n != 2 || v.requestID != "req-1" || v.attempt != "1" {
			t.Fatalf("seen %+v", v)
		}
		if _, err := uuid.Parse(v.msgID); err != nil {
			t.Fatalf("message id %q", v.msgID)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("job not handled")
	}
	select {
	case v := <-got:
		t.Fatalf("rolled back job handled: %+v", v)
	case <-time.After(300 * time.Millisecond):
	}
	// 转发之后确认了复制槽的位置：停止 Worker 后，槽中没有待转发的任务。
	stop()
	if p := queuetest.Pending(t, db, jobs); len(p) != 0 {
		t.Fatalf("pending after relay: %v", p)
	}
}

func TestPublishAll(t *testing.T) {
	t.Parallel()
	db := openDB(t)
	var mu sync.Mutex
	var seen []int
	start(t, db, queuetest.Config(t), func(w *queue.Worker) {
		jobs.Handle(w, func(_ context.Context, p payload) error {
			mu.Lock()
			seen = append(seen, p.N)
			mu.Unlock()
			return nil
		})
	})
	payloads := make([]payload, 1200) // 超过一条 INSERT 的行数和一次转发的条数
	for i := range payloads {
		payloads[i] = payload{N: i}
	}
	if err := jobs.PublishAll(context.Background(), db, payloads); err != nil {
		t.Fatal(err)
	}
	eventually(t, 10*time.Second, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(seen) == len(payloads)
	})
	mu.Lock()
	defer mu.Unlock()
	slices.Sort(seen)
	for i, n := range seen {
		if n != i {
			t.Fatalf("seen %d at %d", n, i)
		}
	}
	if err := jobs.PublishAll(context.Background(), db, nil); err != nil {
		t.Fatal(err)
	}
	if err := queue.Topic[int]("bad topic").Publish(context.Background(), db, 1); err == nil {
		t.Fatal("invalid topic accepted")
	}
}

// 延迟由 JetStream 的定时消息完成，到时间才投递。
func TestDelay(t *testing.T) {
	t.Parallel()
	db := openDB(t)
	cfg := queuetest.Config(t)
	got := make(chan int, 2)
	start(t, db, cfg, func(w *queue.Worker) {
		jobs.Handle(w, func(_ context.Context, p payload) error {
			got <- p.N
			return nil
		})
	})
	begin := time.Now()
	publish(t, db, 1, queue.Delay(500*time.Millisecond))
	publish(t, db, 2, queue.Delay(2*time.Second))
	// 定时消息精确到秒，向上取整。
	at := map[int]time.Duration{}
	for range 2 {
		select {
		case n := <-got:
			at[n] = time.Since(begin)
		case <-time.After(10 * time.Second):
			t.Fatalf("handled %v", at)
		}
	}
	if at[1] < 500*time.Millisecond || at[1] > 3*time.Second || at[2] < 2*time.Second || at[2] > 5*time.Second {
		t.Fatalf("handled after %v", at)
	}
}

// 失败的任务按 RetryAfter 重试，由 JetStream 定时投递，执行次数接着计算。
func TestRetry(t *testing.T) {
	t.Parallel()
	db := openDB(t)
	cfg := queuetest.Config(t)
	type attempt struct {
		n  int
		at time.Time
	}
	got := make(chan attempt, 5)
	var events []queue.Event
	var mu sync.Mutex
	start(t, db, cfg, func(w *queue.Worker) {
		jobs.Handle(w, func(ctx context.Context, p payload) error {
			n, _ := queue.AttemptFromContext(ctx)
			got <- attempt{n: n, at: time.Now()}
			switch n {
			case 1:
				return queue.RetryAfter(errors.New("soon"), 200*time.Millisecond)
			case 2:
				return queue.RetryAfter(errors.New("later"), 2*time.Second)
			}
			return nil
		})
	}, queue.WithObserver(func(_ context.Context, e queue.Event) {
		mu.Lock()
		events = append(events, e)
		mu.Unlock()
	}))
	publish(t, db, 1)
	var attempts []attempt
	for range 3 {
		select {
		case a := <-got:
			attempts = append(attempts, a)
		case <-time.After(15 * time.Second):
			t.Fatalf("only %d attempts", len(attempts))
		}
	}
	for i, a := range attempts {
		if a.n != i+1 {
			t.Fatalf("attempt %d reported as %d", i+1, a.n)
		}
	}
	// 定时消息精确到秒，向上取整，见 TestDelay。
	if d := attempts[1].at.Sub(attempts[0].at); d < 200*time.Millisecond || d > 3*time.Second {
		t.Fatalf("second attempt after %v", d)
	}
	if d := attempts[2].at.Sub(attempts[1].at); d < 2*time.Second || d > 5*time.Second {
		t.Fatalf("third attempt after %v", d)
	}
	eventually(t, 2*time.Second, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(events) == 6
	})
	mu.Lock()
	defer mu.Unlock()
	var kinds []string
	for _, e := range events {
		kinds = append(kinds, e.Kind.String())
	}
	want := []string{"started", "retrying", "started", "retrying", "started", "succeeded"}
	if !slices.Equal(kinds, want) {
		t.Fatalf("events %v", kinds)
	}
	// 首次执行和重试都知道任务可以执行的时间，重试的是安排重试时算出的时间。
	if events[0].Job.AvailableAt.IsZero() || !events[2].Job.AvailableAt.Equal(events[1].RetryAt) || events[4].Job.AvailableAt.IsZero() {
		t.Fatalf("available at %v %v %v", events[0].Job.AvailableAt, events[2].Job.AvailableAt, events[4].Job.AvailableAt)
	}
	if events[0].Job.ID != events[4].Job.ID {
		t.Fatal("job id changed across retries")
	}
}

// 次数用完、Permanent 的错误或业务错误转入失败任务表；Retry 重新发布，执行次数从头计算。
func TestFailedJobs(t *testing.T) {
	t.Parallel()
	db := openDB(t)
	cfg := queuetest.Config(t)
	cfg.MaxAttempts = 2
	var calls atomic.Int64
	var heal atomic.Bool
	permanent := queue.Topic[payload]("permanent")
	// 业务错误默认不重试，第一次失败就转入失败任务表。
	business := queue.Topic[payload]("business")
	errNotFound := errorx.Define(errorx.NotFound, "article.not_found")
	var businessCalls atomic.Int64
	start(t, db, cfg, func(w *queue.Worker) {
		jobs.Handle(w, func(ctx context.Context, p payload) error {
			calls.Add(1)
			if heal.Load() {
				return nil
			}
			return queue.RetryAfter(errors.New("boom"), 0)
		})
		permanent.Handle(w, func(context.Context, payload) error {
			return queue.Permanent(errors.New("bad input"))
		})
		business.Handle(w, func(context.Context, payload) error {
			businessCalls.Add(1)
			return fmt.Errorf("load article: %w", errNotFound.New())
		})
	})
	publish(t, db, 1)
	if err := permanent.Publish(context.Background(), db, payload{N: 2}); err != nil {
		t.Fatal(err)
	}
	if err := business.Publish(context.Background(), db, payload{N: 3}); err != nil {
		t.Fatal(err)
	}
	eventually(t, 5*time.Second, func() bool { return len(failedRows(t, db)) == 3 })
	rows := failedRows(t, db)
	byTopic := map[string]failedRow{}
	for _, r := range rows {
		byTopic[r.Topic] = r
	}
	if r := byTopic["jobs"]; r.Attempts != 2 || calls.Load() != 2 {
		t.Fatalf("jobs failed %+v after %d calls", r, calls.Load())
	}
	if r := byTopic["permanent"]; r.Attempts != 1 {
		t.Fatalf("permanent failed %+v", r)
	}
	if r := byTopic["business"]; r.Attempts != 1 || businessCalls.Load() != 1 || !strings.Contains(r.Error, "article.not_found") {
		t.Fatalf("business error failed %+v after %d calls", r, businessCalls.Load())
	}

	heal.Store(true)
	failed := queue.NewFailedJobs(db)
	if err := failed.Retry(context.Background(), byTopic["jobs"].ID); err != nil {
		t.Fatal(err)
	}
	eventually(t, 5*time.Second, func() bool { return calls.Load() == 3 })
	if err := failed.Retry(context.Background(), byTopic["jobs"].ID); !errors.Is(err, queue.ErrFailedJobNotFound) {
		t.Fatalf("retry twice: %v", err)
	}
	if rows := failedRows(t, db); len(rows) != 2 {
		t.Fatalf("failed rows %+v", rows)
	}
}

// 同一个应用的多个 Worker 共同消费，每个任务只处理一次。
func TestCompetingWorkers(t *testing.T) {
	t.Parallel()
	db := openDB(t)
	cfg := queuetest.Config(t)
	var mu sync.Mutex
	seen := map[int]int{}
	for range 2 {
		start(t, db, cfg, func(w *queue.Worker) {
			jobs.Handle(w, func(_ context.Context, p payload) error {
				mu.Lock()
				seen[p.N]++
				mu.Unlock()
				return nil
			})
		})
	}
	payloads := make([]payload, 200)
	for i := range payloads {
		payloads[i] = payload{N: i}
	}
	if err := jobs.PublishAll(context.Background(), db, payloads); err != nil {
		t.Fatal(err)
	}
	eventually(t, 10*time.Second, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(seen) == len(payloads)
	})
	time.Sleep(300 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	for n, c := range seen {
		if c != 1 {
			t.Fatalf("job %d handled %d times", n, c)
		}
	}
}

// 处理时间超过租约时自动延长，任务只投递一次。
func TestLeaseIsExtended(t *testing.T) {
	t.Parallel()
	db := openDB(t)
	cfg := queuetest.Config(t)
	cfg.Lease = time.Second
	var calls atomic.Int64
	start(t, db, cfg, func(w *queue.Worker) {
		jobs.Handle(w, func(context.Context, payload) error {
			calls.Add(1)
			time.Sleep(3 * time.Second)
			return nil
		})
	})
	publish(t, db, 1)
	eventually(t, 5*time.Second, func() bool { return calls.Load() == 1 })
	time.Sleep(4 * time.Second)
	if n := calls.Load(); n != 1 {
		t.Fatalf("handled %d times", n)
	}
}

// 停止时等待处理中的任务完成；超过 ShutdownTimeout 的任务放回，处理函数的 context 以 ErrShuttingDown 取消，
// 下一个 Worker 重新处理它。
func TestShutdown(t *testing.T) {
	t.Parallel()
	db := openDB(t)
	cfg := queuetest.Config(t)
	cfg.ShutdownTimeout = 300 * time.Millisecond
	slow := queue.Topic[payload]("slow")
	started := make(chan struct{}, 2)
	causes := make(chan error, 1)
	var finished atomic.Int64
	var slowReturned atomic.Bool
	stop := start(t, db, cfg, func(w *queue.Worker) {
		jobs.Handle(w, func(context.Context, payload) error {
			started <- struct{}{}
			time.Sleep(100 * time.Millisecond)
			finished.Add(1)
			return nil
		})
		slow.Handle(w, func(ctx context.Context, p payload) error {
			started <- struct{}{}
			<-ctx.Done()
			causes <- context.Cause(ctx)
			time.Sleep(100 * time.Millisecond) // 取消之后还要一会儿才返回，例如回滚事务
			slowReturned.Store(true)
			return ctx.Err()
		})
	})
	publish(t, db, 1)
	if err := slow.Publish(context.Background(), db, payload{N: 2}); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		select {
		case <-started:
		case <-time.After(5 * time.Second):
			t.Fatal("jobs not started")
		}
	}
	stop()
	if finished.Load() != 1 {
		t.Fatal("in-flight job did not finish before the worker stopped")
	}
	if cause := <-causes; !errors.Is(cause, queue.ErrShuttingDown) {
		t.Fatalf("cause %v", cause)
	}
	if !slowReturned.Load() {
		t.Fatal("worker stopped before the released job's handler returned")
	}

	again := make(chan int, 1)
	start(t, db, cfg, func(w *queue.Worker) {
		slow.Handle(w, func(ctx context.Context, p payload) error {
			n, _ := queue.AttemptFromContext(ctx)
			again <- n
			return nil
		})
	})
	select {
	case n := <-again:
		if n != 2 {
			t.Fatalf("released job attempt %d", n)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("released job not handled again")
	}
}

// Handle 的选项覆盖 Config：MaxAttempts 决定转入失败表之前的次数，也决定 AttemptFromContext 的 final；
// Concurrency 限制同时处理的任务数。
func TestHandleOptions(t *testing.T) {
	t.Parallel()
	db := openDB(t)
	var finals atomic.Int64
	var running, peak, done atomic.Int64
	slow := queue.Topic[payload]("slow")
	start(t, db, queuetest.Config(t), func(w *queue.Worker) {
		jobs.Handle(w, func(ctx context.Context, p payload) error {
			if _, final := queue.AttemptFromContext(ctx); final {
				finals.Add(1)
			}
			return queue.RetryAfter(errors.New("boom"), 0)
		}, queue.MaxAttempts(2))
		slow.Handle(w, func(context.Context, payload) error {
			n := running.Add(1)
			for p := peak.Load(); n > p && !peak.CompareAndSwap(p, n); p = peak.Load() {
			}
			time.Sleep(50 * time.Millisecond)
			running.Add(-1)
			done.Add(1)
			return nil
		}, queue.Concurrency(2))
	})
	publish(t, db, 1)
	payloads := make([]payload, 6)
	if err := slow.PublishAll(context.Background(), db, payloads); err != nil {
		t.Fatal(err)
	}
	eventually(t, 5*time.Second, func() bool { return len(failedRows(t, db)) == 1 && done.Load() == 6 })
	if rows := failedRows(t, db); rows[0].Attempts != 2 || finals.Load() != 1 {
		t.Fatalf("failed %+v, final attempts seen %d", rows, finals.Load())
	}
	if p := peak.Load(); p != 2 {
		t.Fatalf("peak concurrency %d, want 2", p)
	}
}

func TestWorkerRegistration(t *testing.T) {
	t.Parallel()
	db := openDB(t)
	newWorker := func() *queue.Worker { return queue.NewWorker(db, queuetest.Conn(t), queuetest.Config(t), nil) }
	noop := func(context.Context, payload) error { return nil }
	mustPanic := func(name string, fn func()) {
		t.Helper()
		defer func() {
			if recover() == nil {
				t.Errorf("%s: no panic", name)
			}
		}()
		fn()
	}
	mustPanic("nil database", func() { queue.NewWorker(nil, queuetest.Conn(t), queue.DefaultConfig(), nil) })
	mustPanic("nil connection", func() { queue.NewWorker(db, nil, queue.DefaultConfig(), nil) })
	mustPanic("invalid topic", func() { queue.Topic[payload]("a b").Handle(newWorker(), noop) })
	mustPanic("empty segment", func() { queue.Topic[payload]("a..b").Handle(newWorker(), noop) })
	mustPanic("same consumer", func() {
		w := newWorker()
		queue.Topic[payload]("mail.send").Handle(w, noop)
		queue.Topic[payload]("mail_send").Handle(w, noop)
	})
	mustPanic("handled twice", func() {
		w := newWorker()
		jobs.Handle(w, noop)
		jobs.Handle(w, noop)
	})
	mustPanic("zero attempts", func() { queue.MaxAttempts(0) })
	mustPanic("zero concurrency", func() { queue.Concurrency(0) })
	mustPanic("nil observer", func() { queue.WithObserver(nil) })

	w := newWorker()
	jobs.Handle(w, noop)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := w.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if err := w.Run(ctx); err == nil {
		t.Error("second Run succeeded")
	}
	mustPanic("Handle after Run", func() { queue.Topic[payload]("other").Handle(w, noop) })

	closed := queuetest.Conn(t)
	closed.Close()
	if err := queue.NewWorker(db, closed, queuetest.Config(t), nil).Run(context.Background()); err == nil {
		t.Error("Run on a closed NATS connection succeeded")
	}
}

func TestPermanent(t *testing.T) {
	t.Parallel()
	base := errors.New("bad input")
	err := queue.Permanent(base)
	if !queue.IsPermanent(err) || !errors.Is(err, base) || queue.Permanent(nil) != nil {
		t.Fatalf("permanent %v", err)
	}
	if queue.Retry(nil) != nil || queue.RetryAfter(nil, time.Second) != nil {
		t.Fatal("nil error marked for retry")
	}

	notFound := errorx.Define(errorx.NotFound, "article.not_found")
	limited := errorx.Define(errorx.ResourceExhausted, "sms.limited")
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"plain error", base, false},
		{"wrapped plain error", fmt.Errorf("send: %w", base), false},
		{"unavailable", errorx.Define(errorx.Unavailable, "mail.unavailable").New(), false},
		{"internal", errorx.Define(errorx.Internal, "demo.internal").New(), false},
		{"resource exhausted", limited.New(), false},
		{"not found", fmt.Errorf("load article: %w", notFound.New()), true},
		{"invalid argument", errorx.Define(errorx.InvalidArgument, "document.corrupt").New(), true},
		{"conflict", errorx.Define(errorx.Conflict, "article.already_published").New(), true},
		{"business error with errorx.RetryAfter", errorx.Define(errorx.Conflict, "demo.busy").New(errorx.RetryAfter(time.Second)), false},
		{"business error marked Retry", queue.Retry(notFound.New()), false},
		{"business error marked RetryAfter", queue.RetryAfter(notFound.New(), time.Second), false},
		{"Permanent wins over Retry", queue.Permanent(queue.Retry(base)), true},
		{"Permanent inside RetryAfter", queue.RetryAfter(queue.Permanent(base), time.Second), true},
		{"Permanent system failure", queue.Permanent(errorx.Define(errorx.Unavailable, "demo.down").New()), true},
	}
	for _, c := range cases {
		if got := queue.IsPermanent(c.err); got != c.want {
			t.Errorf("%s: IsPermanent = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestAttemptFromContextOutsideJob(t *testing.T) {
	t.Parallel()
	if n, final := queue.AttemptFromContext(context.Background()); n != 0 || final {
		t.Fatalf("attempt %d final %v", n, final)
	}
}

// consumer 被人删除后，Worker 重新创建它，继续处理任务。
func TestConsumerRecreated(t *testing.T) {
	t.Parallel()
	db := openDB(t)
	cfg := queuetest.Config(t)
	got := make(chan int, 2)
	start(t, db, cfg, func(w *queue.Worker) {
		jobs.Handle(w, func(_ context.Context, p payload) error {
			got <- p.N
			return nil
		})
	})
	publish(t, db, 1)
	if n := <-got; n != 1 {
		t.Fatalf("handled %d", n)
	}
	js, err := jetstream.New(queuetest.Conn(t))
	if err != nil {
		t.Fatal(err)
	}
	if err := js.DeleteConsumer(context.Background(), cfg.Stream, "jobs"); err != nil {
		t.Fatal(err)
	}
	publish(t, db, 2)
	select {
	case n := <-got:
		if n != 2 {
			t.Fatalf("handled %d", n)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("job not handled after the consumer was deleted")
	}
}

// consumer 在两次拉取之间被删除时（没有等待中的拉取，之后的拉取得到 no responders），Worker 同样重建它。
func TestConsumerDeletedBetweenFetches(t *testing.T) {
	t.Parallel()
	db := openDB(t)
	cfg := queuetest.Config(t)
	js, err := jetstream.New(queuetest.Conn(t))
	if err != nil {
		t.Fatal(err)
	}
	got := make(chan int, 2)
	var armed atomic.Bool
	deleted := make(chan error, 1)
	start(t, db, cfg, func(w *queue.Worker) {
		jobs.Handle(w, func(_ context.Context, p payload) error {
			got <- p.N
			return nil
		})
		queue.SetBeforeFetch(w, func() {
			if armed.CompareAndSwap(true, false) {
				deleted <- js.DeleteConsumer(context.Background(), cfg.Stream, "jobs")
			}
		})
	})
	publish(t, db, 1)
	if n := <-got; n != 1 {
		t.Fatalf("handled %d", n)
	}
	armed.Store(true) // 正在等待的拉取结束后、下一次拉取之前删除
	select {
	case err := <-deleted:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("consumer not deleted")
	}
	publish(t, db, 2)
	select {
	case n := <-got:
		if n != 2 {
			t.Fatalf("handled %d", n)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("job not handled after the consumer was deleted between fetches")
	}
}

// 任务连续到达时，relay 按攒批的最长等待发出，不等攒满一批：第一个任务在发布还没结束时就已处理。
func TestRelayDoesNotWaitForFullBatch(t *testing.T) {
	t.Parallel()
	db := openDB(t)
	var mu sync.Mutex
	var warm bool
	var first time.Time // 第一个 N >= 1 的任务被处理的时间
	start(t, db, queuetest.Config(t), func(w *queue.Worker) {
		jobs.Handle(w, func(_ context.Context, p payload) error {
			mu.Lock()
			defer mu.Unlock()
			if p.N == 0 {
				warm = true
			} else if first.IsZero() {
				first = time.Now()
			}
			return nil
		})
	})
	publish(t, db, 0) // 等 Worker 开始转发
	eventually(t, 5*time.Second, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return warm
	})
	// 8 个发布方各自每 4ms 提交一个任务：合计约每 0.5ms 一个，远小于攒批的等待；
	// 200ms 内约 400 个，不到一批的上限，旧的实现要等发布结束、出现空闲才发出。
	begin := time.Now()
	var wg sync.WaitGroup
	for g := range 8 {
		wg.Go(func() {
			next := begin.Add(time.Duration(g) * time.Millisecond / 2)
			for n := 1 + g; next.Before(begin.Add(200 * time.Millisecond)); n += 8 {
				time.Sleep(time.Until(next))
				if err := jobs.Publish(context.Background(), db, payload{N: n}); err != nil {
					t.Error(err)
					return
				}
				next = next.Add(4 * time.Millisecond)
			}
		})
	}
	wg.Wait()
	finished := time.Now()
	eventually(t, 5*time.Second, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return !first.IsZero()
	})
	mu.Lock()
	defer mu.Unlock()
	if !first.Before(finished) {
		t.Fatalf("first job handled %v after publishing ended", first.Sub(finished))
	}
}
