package queue_test

import (
	"context"
	"errors"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/queue"
	"github.com/go-tenon/tenon/queue/queuetest"
)

// 只有提交的任务被发布：事务回滚、保存点回滚中的任务随之撤销；在事务之外发布时立即生效。
func TestPendingCommittedOnly(t *testing.T) {
	t.Parallel()
	db := openDB(t)
	ctx := context.Background()
	publish(t, db, 1)
	rollback := errors.New("rollback")
	err := db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if err := jobs.Publish(ctx, tx, payload{N: 2}); err != nil {
			return err
		}
		// 保存点中的任务随保存点回滚，外层事务照常提交。
		err := tx.RunInTx(ctx, nil, func(ctx context.Context, sp bun.Tx) error {
			if err := jobs.Publish(ctx, sp, payload{N: 3}); err != nil {
				return err
			}
			return rollback
		})
		if !errors.Is(err, rollback) {
			return err
		}
		return jobs.Publish(ctx, tx, payload{N: 4}, queue.Delay(48*time.Hour))
	})
	if err != nil {
		t.Fatal(err)
	}
	err = db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if err := jobs.Publish(ctx, tx, payload{N: 5}); err != nil {
			return err
		}
		return rollback
	})
	if !errors.Is(err, rollback) {
		t.Fatal(err)
	}
	var got []int
	for _, p := range queuetest.Pending(t, db, jobs) {
		got = append(got, p.N)
	}
	if !slices.Equal(got, []int{1, 2, 4}) {
		t.Fatalf("pending %v", got)
	}
	if other := queuetest.Pending(t, db, queue.Topic[payload]("other")); len(other) != 0 {
		t.Fatalf("other topic %v", other)
	}
	st, err := queue.ReadStatus(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	if st.Pending != 2 || st.Deferred != 1 || st.OldestPending.IsZero() {
		t.Fatalf("outbox status %+v", st)
	}
}

// 多个 Worker 通过 FOR UPDATE SKIP LOCKED 并行转发，任一 Worker 停止后其余照常转发，任务不重复也不丢失。
func TestParallelRelayAndFailover(t *testing.T) {
	t.Parallel()
	db := openDB(t)
	cfg := queuetest.Config(t)
	var mu sync.Mutex
	seen := map[int]int{}
	handle := func(w *queue.Worker) {
		jobs.Handle(w, func(_ context.Context, p payload) error {
			mu.Lock()
			seen[p.N]++
			mu.Unlock()
			return nil
		})
	}
	count := func() int {
		mu.Lock()
		defer mu.Unlock()
		return len(seen)
	}
	first := start(t, db, cfg, handle)
	start(t, db, cfg, handle)
	publish(t, db, 1)
	eventually(t, 5*time.Second, func() bool { return count() == 1 })

	first()
	for i := 2; i <= 50; i++ {
		publish(t, db, i)
	}
	eventually(t, 10*time.Second, func() bool { return count() == 50 })
	mu.Lock()
	defer mu.Unlock()
	for n, c := range seen {
		if c != 1 {
			t.Fatalf("job %d handled %d times", n, c)
		}
	}
}

// 超过 NATS max_payload 的毒丸消息转入 queue_failed_jobs 并从 outbox 删除，不阻塞同批和后续正常任务。
func TestPoisonJobIsolated(t *testing.T) {
	t.Parallel()
	db := openDB(t)
	cfg := queuetest.Config(t)
	big := queue.Topic[string]("big")
	got := make(chan int, 2)
	start(t, db, cfg, func(w *queue.Worker) {
		jobs.Handle(w, func(_ context.Context, p payload) error {
			got <- p.N
			return nil
		})
		big.Handle(w, func(context.Context, string) error { return nil })
	})

	ctx := context.Background()
	nc := queuetest.Conn(t)
	oversize := strings.Repeat("x", int(nc.MaxPayload())+1024)
	if err := big.Publish(ctx, db, oversize); err != nil {
		t.Fatal(err)
	}
	publish(t, db, 42)

	select {
	case n := <-got:
		if n != 42 {
			t.Fatalf("handled %d", n)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("normal job blocked by poison job")
	}
	eventually(t, 5*time.Second, func() bool { return len(failedRows(t, db)) == 1 })
	rows := failedRows(t, db)
	if rows[0].Topic != "big" || rows[0].Attempts != 0 {
		t.Fatalf("poison failed row %+v", rows[0])
	}
	if pending := queuetest.Pending(t, db, big); len(pending) != 0 {
		t.Fatalf("poison job left in outbox: %d", len(pending))
	}
}

// 队列迁移未执行时 Run 返回错误，问题在启动时就暴露出来。
func TestRunWithoutTables(t *testing.T) {
	t.Parallel()
	db := openDB(t)
	ctx := context.Background()
	if _, err := db.ExecContext(ctx, "DROP TABLE queue_outbox"); err != nil {
		t.Fatal(err)
	}
	w := queue.NewWorker(db, queuetest.Conn(t), queuetest.Config(t), slog.New(slog.DiscardHandler))
	err := w.Run(ctx)
	if err == nil || !strings.Contains(err.Error(), "run the queue migrations") {
		t.Fatalf("err %v", err)
	}
}
