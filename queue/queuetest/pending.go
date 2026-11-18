package queuetest

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pglogrepl"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/queue"
)

// slotReleaseWait 是 Pending 等待刚停掉的 Worker 释放复制槽的最长时间。
const slotReleaseWait = 5 * time.Second

// sqlstateObjectInUse 是复制槽正被另一个连接使用时返回的错误码。
const sqlstateObjectInUse = "55006"

// Pending 返回 db 中 topic 还没转发的任务的 payload，按提交顺序。
// 用于只检查发布了哪些任务的测试；Worker 运行时任务会被转发，复制槽也被它占用，所以使用时停掉 Worker。
// 刚停掉的 Worker 在数据库一侧还会占用复制槽一小会儿，Pending 等它释放，最多等 slotReleaseWait。
// 只能看到已经提交的任务。读取或解码失败时使测试失败。
//
//	jobs := queuetest.Pending(t, db, article.PublishJob)
func Pending[T any](t testing.TB, db bun.IDB, topic queue.Topic[T]) []T {
	t.Helper()
	ctx := context.Background()
	var database string
	if err := db.NewRaw("SELECT current_database()").Scan(ctx, &database); err != nil {
		t.Fatalf("queuetest: current database: %v", err)
	}
	// peek 只读取，不推进复制槽的位置。
	var changes [][]byte
	deadline := time.Now().Add(slotReleaseWait)
	for {
		changes = changes[:0]
		err := db.NewRaw(`SELECT data FROM pg_logical_slot_peek_binary_changes(?, NULL, NULL,
			'proto_version', '1', 'publication_names', ?, 'messages', 'true')`,
			queue.SlotName(database), queue.Publication).Scan(ctx, &changes)
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == sqlstateObjectInUse && time.Now().Before(deadline) {
			time.Sleep(20 * time.Millisecond)
			continue
		}
		if err != nil {
			t.Fatalf("queuetest: read published jobs: %v", err)
		}
		break
	}
	var bodies [][]byte
	for _, data := range changes {
		msg, err := pglogrepl.Parse(data)
		if err != nil {
			t.Fatalf("queuetest: decode WAL data: %v", err)
		}
		if m, ok := msg.(*pglogrepl.LogicalDecodingMessage); ok && m.Prefix == queue.MessagePrefix+string(topic) {
			bodies = append(bodies, m.Content)
		}
	}

	payloads := make([]T, len(bodies))
	for i, body := range bodies {
		var env struct {
			Payload json.RawMessage `json:"payload"`
		}
		if err := json.Unmarshal(body, &env); err != nil {
			t.Fatalf("queuetest: decode %q job: %v", topic, err)
		}
		if err := json.Unmarshal(env.Payload, &payloads[i]); err != nil {
			t.Fatalf("queuetest: decode %q payload: %v", topic, err)
		}
	}
	return payloads
}
