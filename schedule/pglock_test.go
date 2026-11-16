package schedule_test

import (
	"context"
	"crypto/rand"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-tenon/tenon/database/dbtest"
	"github.com/go-tenon/tenon/lock"
	"github.com/go-tenon/tenon/lock/pglock"
	"github.com/go-tenon/tenon/schedule"
)

// 多个实例共用 PostgreSQL 锁时，每次触发只执行一次。
func TestPostgresLockRunsOncePerTick(t *testing.T) {
	t.Parallel()
	db := dbtest.Open(t, pglock.Migrations())
	name := "test:" + rand.Text()
	var n atomic.Int64
	scheds := make([]*schedule.Scheduler, 3)
	for i := range scheds {
		scheds[i] = schedule.New(lock.New(pglock.New(db)), schedule.DefaultConfig(), nil)
		scheds[i].Add(name, "@everysecond", counter(&n))
	}
	for tick := range 3 {
		at := base.Add(time.Duration(tick) * time.Second)
		var wg sync.WaitGroup
		for _, s := range scheds {
			wg.Go(func() {
				if err := s.RunDue(context.Background(), at); err != nil {
					t.Error(err)
				}
			})
		}
		wg.Wait()
	}
	if n.Load() != 3 {
		t.Fatalf("ran %d times, want 3", n.Load())
	}
}
