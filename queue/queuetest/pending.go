package queuetest

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/queue"
)

// Pending 返回 db 中 topic 还没转发到 JetStream 的任务的 payload，按可用时间和发布顺序。
// 用于只检查发布了哪些任务的测试；只能看到已经提交的任务。读取或解码失败时使测试失败。
//
//	jobs := queuetest.Pending(t, db, article.PublishJob)
func Pending[T any](t testing.TB, db bun.IDB, topic queue.Topic[T]) []T {
	t.Helper()
	var raw [][]byte
	err := db.NewRaw(`SELECT payload FROM queue_outbox WHERE topic = ? ORDER BY available_at, id`, string(topic)).
		Scan(context.Background(), &raw)
	if err != nil {
		t.Fatalf("queuetest: read published jobs: %v", err)
	}
	payloads := make([]T, len(raw))
	for i, data := range raw {
		if err := json.Unmarshal(data, &payloads[i]); err != nil {
			t.Fatalf("queuetest: decode %q payload: %v", topic, err)
		}
	}
	return payloads
}
