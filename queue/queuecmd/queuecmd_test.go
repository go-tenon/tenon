package queuecmd_test

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/database/dbtest"
	"github.com/go-tenon/tenon/queue"
	"github.com/go-tenon/tenon/queue/queuecmd"
	"github.com/go-tenon/tenon/queue/queuetest"
)

// run 执行命令，返回每行按空白分隔的字段。
func run(t *testing.T, db bun.IDB, args ...string) [][]string {
	t.Helper()
	out, err := execute(db, args...)
	if err != nil {
		t.Fatal(err)
	}
	var rows [][]string
	for line := range strings.Lines(out) {
		rows = append(rows, strings.Fields(line))
	}
	return rows
}

func execute(db bun.IDB, args ...string) (string, error) {
	return executeWith(db, nil, args...)
}

func executeWith(db bun.IDB, opts []queuecmd.Option, args ...string) (string, error) {
	cmd := queuecmd.New(db, opts...)
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs(args)
	err := cmd.ExecuteContext(context.Background())
	return out.String(), err
}

func insertFailed(t *testing.T, db *bun.DB, topic, msg string, at time.Time) uuid.UUID {
	t.Helper()
	id := uuid.New()
	_, err := db.ExecContext(context.Background(), `INSERT INTO queue_failed_jobs (id, created_at, updated_at, topic, payload, attempts, error)
		VALUES (?, ?, ?, ?, ?, 3, ?)`, id, at.UTC(), at.UTC(), topic, []byte("{}"), msg)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestFailed(t *testing.T) {
	db := dbtest.Open(t, queue.Migrations())
	now := time.Now()
	old := insertFailed(t, db, "mail", "smtp down\nstack trace", now.Add(-time.Hour))
	recent := insertFailed(t, db, "mail", strings.Repeat("长", 100), now)
	insertFailed(t, db, "report", "boom", now.Add(-time.Minute))
	rows := run(t, db, "failed", "--topic", "mail")
	if len(rows) != 3 || rows[0][0] != "ID" || rows[1][0] != recent.String() || rows[2][0] != old.String() {
		t.Fatalf("rows %q", rows)
	}
	if e := rows[1][len(rows[1])-1]; len([]rune(e)) != 80 || !strings.HasSuffix(e, "…") {
		t.Fatalf("long error %q", e)
	}
	if e := strings.Join(rows[2][5:], " "); e != "smtp down" {
		t.Fatalf("first line %q", e)
	}
	if rows := run(t, db, "failed", "--limit", "1"); len(rows) != 2 || rows[1][0] != recent.String() {
		t.Fatalf("limited rows %q", rows)
	}
}

// status 返回 queue status 输出中 key 一行的值。
func status(t *testing.T, db bun.IDB, key string) string {
	t.Helper()
	out, err := execute(db, "status")
	if err != nil {
		t.Fatal(err)
	}
	for line := range strings.Lines(out) {
		if v, ok := strings.CutPrefix(line, key+"  "); ok {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// status 显示 outbox 中的待转发任务数和延迟任务数。
func TestStatusOutbox(t *testing.T) {
	db := dbtest.Open(t, queue.Migrations())
	ctx := context.Background()
	if s := status(t, db, "outbox pending"); s != "0" {
		t.Fatalf("pending %q", s)
	}
	if s := status(t, db, "outbox deferred"); s != "0" {
		t.Fatalf("deferred %q", s)
	}
	if err := queue.Topic[int]("mail").Publish(ctx, db, 1); err != nil {
		t.Fatal(err)
	}
	if err := queue.Topic[int]("mail").Publish(ctx, db, 2, queue.Delay(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if s := status(t, db, "outbox pending"); s != "1" {
		t.Fatalf("pending %q", s)
	}
	if s := status(t, db, "outbox deferred"); s != "1" {
		t.Fatalf("deferred %q", s)
	}
	if s := status(t, db, "oldest pending"); s == "" {
		t.Fatal("oldest pending not shown")
	}
}

// WithJetStream 时 status 还输出 stream 和各 topic 的状态。
func TestStatusJetStream(t *testing.T) {
	db := dbtest.Open(t, queue.Migrations())
	cfg := queuetest.Config(t)
	opts := []queuecmd.Option{queuecmd.WithJetStream(queuetest.Conn(t), cfg)}
	out, err := executeWith(db, opts, "status")
	if err != nil || !strings.Contains(out, "not created yet") {
		t.Fatalf("before the worker ran: %v\n%s", err, out)
	}

	release := make(chan struct{})
	w := queue.NewWorker(db, queuetest.Conn(t), cfg, slog.New(slog.DiscardHandler))
	queue.Topic[int]("mail").Handle(w, func(context.Context, int) error {
		<-release
		return nil
	})
	queuetest.Run(t, w)
	t.Cleanup(func() { close(release) }) // 在 Worker 停止之前放行处理中的任务
	ctx := context.Background()
	if err := queue.Topic[int]("mail").Publish(ctx, db, 1); err != nil {
		t.Fatal(err)
	}
	if err := queue.Topic[int]("orphan").Publish(ctx, db, 1); err != nil {
		t.Fatal(err)
	}
	topics := map[string][]string{}
	deadline := time.Now().Add(5 * time.Second)
	for len(topics) < 2 || topics["mail"][2] != "1" {
		if time.Now().After(deadline) {
			t.Fatalf("topics %q\n%s", topics, out)
		}
		time.Sleep(50 * time.Millisecond)
		if out, err = executeWith(db, opts, "status"); err != nil {
			t.Fatal(err)
		}
		topics = map[string][]string{}
		_, table, _ := strings.Cut(out, "TOPIC")
		for line := range strings.Lines(table) {
			if f := strings.Fields(line); len(f) >= 6 && f[0] != "PENDING" {
				topics[f[0]] = f
			}
		}
	}
	// 最后一列是状态；LAST DELIVERED 是日期和时间两个字段，没有时是 "-"。
	if !strings.Contains(out, cfg.Stream) || topics["mail"][6] != "ok" || topics["orphan"][1] != "1" ||
		!strings.HasPrefix(strings.Join(topics["orphan"][5:], " "), "no handler") {
		t.Fatalf("status\n%s", out)
	}
}
