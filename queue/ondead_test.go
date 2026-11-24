package queue_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-tenon/tenon/queue"
	"github.com/go-tenon/tenon/queue/queuetest"
)

func TestOnDeadAndDedupKey(t *testing.T) {
	t.Parallel()
	db := openDB(t)
	cfg := queuetest.Config(t)
	ctx := t.Context()

	deadCh := make(chan payload, 4)
	var dedupRuns atomic.Int32
	dedupDone := make(chan struct{}, 4)

	dedupTopic := queue.Topic[payload]("jobs.dedup")
	start(t, db, cfg, func(w *queue.Worker) {
		jobs.Handle(w, func(_ context.Context, p payload) error {
			return queue.Permanent(errors.New("bad recipient"))
		}, queue.MaxAttempts(5), queue.OnDead(func(_ context.Context, p payload, err error) error {
			if err != nil {
				deadCh <- p
			}
			return nil
		}))
		dedupTopic.Handle(w, func(_ context.Context, _ payload) error {
			dedupRuns.Add(1)
			dedupDone <- struct{}{}
			return nil
		})
	})

	// Permanent 错误在第 1 次尝试就进入失败表并触发 OnDead。
	if err := jobs.Publish(ctx, db, payload{N: 42}); err != nil {
		t.Fatal(err)
	}
	select {
	case p := <-deadCh:
		if p.N != 42 {
			t.Fatalf("dead payload = %+v, want N=42", p)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for OnDead callback")
	}

	// 同一个 DedupKey 连续发布两条任务，在去重窗口内合并为一条执行。
	if err := dedupTopic.Publish(ctx, db, payload{N: 1}, queue.DedupKey("same-key")); err != nil {
		t.Fatal(err)
	}
	if err := dedupTopic.Publish(ctx, db, payload{N: 1}, queue.DedupKey("same-key")); err != nil {
		t.Fatal(err)
	}
	select {
	case <-dedupDone:
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for dedup job")
	}
	time.Sleep(300 * time.Millisecond)
	if got := dedupRuns.Load(); got != 1 {
		t.Fatalf("dedup runs = %d, want 1", got)
	}
}
