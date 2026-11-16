package queue_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/go-tenon/tenon/queue"
	"github.com/go-tenon/tenon/queue/queuetest"
)

// Admin 报告各 topic 的任务、定时消息和没有处理函数的 topic；暂停的 topic 在 Worker 重启后仍然暂停，恢复后照常处理。
func TestAdmin(t *testing.T) {
	t.Parallel()
	db := openDB(t)
	cfg := queuetest.Config(t)
	admin := queue.NewAdmin(queuetest.Conn(t), cfg)
	ctx := context.Background()

	st, err := admin.Status(ctx)
	if err != nil || st.Exists || st.Stream != cfg.Stream {
		t.Fatalf("before the worker ran: %+v %v", st, err)
	}

	release := make(chan struct{})
	handled := make(chan int, 10)
	register := func(w *queue.Worker) {
		jobs.Handle(w, func(_ context.Context, p payload) error {
			if p.N == 1 {
				<-release
			}
			handled <- p.N
			return nil
		})
	}
	stop := start(t, db, cfg, register)
	publish(t, db, 1)                                                                  // 处理中
	publish(t, db, 2, queue.Delay(time.Minute))                                        // JetStream 中的定时消息
	if err := queue.Topic[payload]("orphan").Publish(ctx, db, payload{}); err != nil { // 没有处理函数
		t.Fatal(err)
	}
	var jobsStatus, orphan queue.TopicStatus
	eventually(t, 5*time.Second, func() bool {
		st, err = admin.Status(ctx)
		if err != nil {
			t.Fatal(err)
		}
		jobsStatus, orphan = queue.TopicStatus{}, queue.TopicStatus{}
		for _, s := range st.Topics {
			switch s.Topic {
			case "jobs":
				jobsStatus = s
			case "orphan":
				orphan = s
			}
		}
		return jobsStatus.InFlight == 1 && orphan.Pending == 1 && st.Deferred == 1
	})
	if !st.Exists || st.Replicas != 1 || st.Messages != 3 || st.Bytes == 0 {
		t.Fatalf("stream %+v", st)
	}
	if !jobsStatus.Handled || jobsStatus.LastDelivered.IsZero() || jobsStatus.Stalled() || orphan.Handled {
		t.Fatalf("topics %+v %+v", jobsStatus, orphan)
	}
	close(release)
	<-handled

	until := time.Now().Add(time.Hour)
	if err := admin.Pause(ctx, "jobs", until); err != nil {
		t.Fatal(err)
	}
	if err := admin.Pause(ctx, "missing", until); !errors.Is(err, queue.ErrTopicNotFound) {
		t.Fatalf("pause missing topic: %v", err)
	}
	stop()
	start(t, db, cfg, register)
	publish(t, db, 3)
	select {
	case n := <-handled:
		t.Fatalf("paused topic handled %d", n)
	case <-time.After(time.Second):
	}
	st, err = admin.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range st.Topics {
		if s.Topic == "jobs" && (!s.Paused() || s.PausedUntil.Sub(until).Abs() > time.Second || s.Pending != 1 || s.Stalled()) {
			t.Fatalf("paused topic %+v", s)
		}
	}
	if err := admin.Resume(ctx, "jobs"); err != nil {
		t.Fatal(err)
	}
	select {
	case n := <-handled:
		if n != 3 {
			t.Fatalf("handled %d", n)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("resumed topic not handled")
	}
}

// 连接不可用时 Status 返回错误，之后可以再试。
func TestAdminUnreachable(t *testing.T) {
	t.Parallel()
	nc := queuetest.Conn(t)
	nc.Close()
	admin := queue.NewAdmin(nc, queue.DefaultConfig())
	for range 2 {
		if _, err := admin.Status(context.Background()); err == nil {
			t.Fatal("status without NATS succeeded")
		}
	}
}
