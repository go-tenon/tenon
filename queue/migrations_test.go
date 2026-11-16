package queue_test

import (
	"context"
	"fmt"
	"io/fs"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/go-tenon/tenon/queue"
	"github.com/go-tenon/tenon/queue/queuetest"
)

// 早先建过 queue_scheduled 的数据库中，迁移把还在等待的任务写回 WAL，交给 Worker 转发，然后删除这张表。
func TestMigrateQueueScheduled(t *testing.T) {
	t.Parallel()
	db := openDB(t)
	ctx := context.Background()
	_, err := db.ExecContext(ctx, `CREATE TABLE queue_scheduled (id uuid PRIMARY KEY, topic text NOT NULL,
		body bytea NOT NULL, available_at timestamptz NOT NULL, created_at timestamptz NOT NULL)`)
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range []int{72, 48} {
		at := time.Now().UTC().Add(time.Duration(n) * time.Hour)
		body := fmt.Sprintf(`{"id":%q,"payload":{"n":%d},"available_at":%q}`, uuid.New(), n, at.Format(time.RFC3339Nano))
		_, err := db.ExecContext(ctx, `INSERT INTO queue_scheduled VALUES (?, 'jobs', ?, ?, now())`, uuid.New(), []byte(body), at)
		if err != nil {
			t.Fatal(err)
		}
	}

	src, err := fs.ReadFile(queue.Migrations(), "0003_drop_queue_scheduled.sql")
	if err != nil {
		t.Fatal(err)
	}
	_, up, _ := strings.Cut(string(src), "-- +goose StatementBegin")
	up, _, _ = strings.Cut(up, "-- +goose StatementEnd")
	if _, err := db.ExecContext(ctx, up); err != nil {
		t.Fatal(err)
	}

	var got []int
	for _, p := range queuetest.Pending(t, db, jobs) {
		got = append(got, p.N)
	}
	if !slices.Equal(got, []int{48, 72}) {
		t.Fatalf("pending %v", got)
	}
	var exists bool
	if err := db.NewRaw("SELECT to_regclass('queue_scheduled') IS NOT NULL").Scan(ctx, &exists); err != nil || exists {
		t.Fatalf("queue_scheduled still exists: %v", err)
	}
}
