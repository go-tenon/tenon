package schedule_test

import (
	"context"
	"crypto/rand"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-tenon/tenon/database/dbtest"
	"github.com/go-tenon/tenon/schedule"
	"github.com/go-tenon/tenon/schedule/pgschedule"
)

// 多个实例共用 PostgreSQL 状态存储时，每次触发只执行一次，落后时钟不会重跑，
// 任务重叠时全集群严格只记录一次跳过。
func TestPostgresStoreRunsOnceAndSkipsOnce(t *testing.T) {
	t.Parallel()
	db := dbtest.Open(t, pgschedule.Migrations())
	ctx := context.Background()
	name := "test:" + rand.Text()
	var n atomic.Int64
	scheds := make([]*schedule.Scheduler, 3)
	for i := range scheds {
		scheds[i] = schedule.New(pgschedule.New(db), schedule.DefaultConfig())
		scheds[i].Add(name, "@everysecond", counter(&n))
	}
	for tick := range 3 {
		at := base.Add(time.Duration(tick) * time.Second)
		var wg sync.WaitGroup
		for _, s := range scheds {
			wg.Go(func() {
				if err := s.RunDue(ctx, at); err != nil {
					t.Error(err)
				}
			})
		}
		wg.Wait()
	}
	if n.Load() != 3 {
		t.Fatalf("ran %d times, want 3", n.Load())
	}
	// 落后时钟再次触发更早的 tick 不会重复执行。
	if err := scheds[0].RunDue(ctx, base); err != nil {
		t.Fatal(err)
	}
	if n.Load() != 3 {
		t.Fatalf("stale tick ran: %d", n.Load())
	}

	// 多实例并发重叠：同一 tick 全集群只记一次 overlap 跳过。
	slowName := "slow:" + rand.Text()
	var skips atomic.Int64
	started := make(chan struct{}, 2)
	release := make(chan struct{})
	var runs atomic.Int64
	for i := range scheds {
		scheds[i] = schedule.New(pgschedule.New(db), schedule.DefaultConfig(),
			schedule.WithObserver(func(_ context.Context, e schedule.Event) {
				if e.Kind == schedule.TaskSkipped && e.SkipReason == schedule.SkipOverlap {
					skips.Add(1)
				}
			}))
		scheds[i].Add(slowName, "@everysecond", blockingTask(&runs, started, release))
	}
	firstDone := make(chan error, 1)
	go func() { firstDone <- scheds[0].RunDue(ctx, base) }()
	<-started

	var wg sync.WaitGroup
	for _, s := range scheds {
		wg.Go(func() {
			if err := s.RunDue(ctx, base.Add(time.Second)); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	close(release)
	if err := <-firstDone; err != nil {
		t.Fatal(err)
	}
	if runs.Load() != 1 || skips.Load() != 1 {
		t.Fatalf("runs=%d skips=%d, want 1 and 1", runs.Load(), skips.Load())
	}

	// 状态表中每个任务只有一行。
	var rows int
	if err := db.NewRaw("SELECT count(*) FROM schedule_tasks WHERE name IN (?, ?)", name, slowName).Scan(ctx, &rows); err != nil {
		t.Fatal(err)
	}
	if rows != 2 {
		t.Fatalf("rows=%d, want 2", rows)
	}
}
