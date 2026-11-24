package schedule_test

import (
	"context"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/cockroachdb/errors"

	"github.com/go-tenon/tenon/logging"
	"github.com/go-tenon/tenon/logging/logtest"
	"github.com/go-tenon/tenon/schedule"
	"github.com/go-tenon/tenon/schedule/scheduletest"
)

// 每次执行有自己的请求 ID，任务中读得到；ctx 中已有请求 ID 时沿用它。
func TestEachRunHasRequestID(t *testing.T) {
	t.Parallel()
	s := newScheduler()
	var mu sync.Mutex
	var ids []string
	s.Add("ids", "@everysecond", func(ctx context.Context) error {
		mu.Lock()
		defer mu.Unlock()
		ids = append(ids, logging.RequestID(ctx))
		return nil
	})
	ctx := context.Background()
	for tick := range 2 {
		if err := s.RunDue(ctx, base.Add(time.Duration(tick)*time.Second)); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.RunDue(logging.WithRequestID(ctx, "outer"), base.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	if len(ids) != 3 || ids[0] == "" || ids[1] == "" || ids[0] == ids[1] || ids[2] != "outer" {
		t.Fatalf("request IDs %q", ids)
	}
}

// 任务失败的日志带着这次执行的任务名和请求 ID，与任务中记录的日志相同。
func TestFailureLogSharesRequestID(t *testing.T) {
	t.Parallel()
	rec := logtest.New()
	s := schedule.New(scheduletest.NewStore(), schedule.DefaultConfig(), rec.Logger())
	seen := make(chan string, 1)
	s.Add("fails", "@everysecond", func(ctx context.Context) error {
		select {
		case seen <- logging.RequestID(ctx):
		default:
		}
		return errors.New("boom")
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Run(ctx) }()
	var id string
	select {
	case id = <-seen:
	case <-time.After(5 * time.Second):
		t.Fatal("task did not run")
	}
	deadline := time.Now().Add(5 * time.Second)
	for !rec.Has(slog.LevelError, "scheduled task failed") && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	<-done
	for _, r := range rec.Records() {
		if r.Message == "scheduled task failed" && r.Attr("task") == "fails" && r.Attr(logging.AttrRequestID) == id {
			return
		}
	}
	t.Fatalf("no failure log with request ID %q: %+v", id, rec.Records())
}
