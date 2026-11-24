package queue

import (
	"encoding/json"
	"math"
	"math/rand/v2"
	"time"

	"github.com/google/uuid"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/database"
)

// Job 是一个任务，处理函数之外通过 Event 和失败任务看到它。
type Job struct {
	ID          uuid.UUID
	Topic       string
	Payload     []byte
	Metadata    map[string]string
	AvailableAt time.Time // 任务首次可以开始执行的时间；重试、租约到期或停止时放回后由 JetStream 重新投递的任务为零值。
	Attempt     int       // 本次是第几次执行，从 1 开始。
}

// envelope 是发给 JetStream 的消息体。
type envelope struct {
	ID          uuid.UUID         `json:"id"`
	Payload     json.RawMessage   `json:"payload"`
	Metadata    map[string]string `json:"metadata,omitempty"`
	AvailableAt time.Time         `json:"available_at"`
}

// dedupMetaKey 是 queue_outbox.metadata 中存放自定义去重键的内部键名。
const dedupMetaKey = "tenon.dedup_key"

// outboxJob 是 queue_outbox 表中的一行：待转发到 JetStream 的任务或尚未到期的延迟任务。
type outboxJob struct {
	bun.BaseModel `bun:"table:queue_outbox,alias:o"`

	ID                  uuid.UUID         `bun:"id,pk,type:uuid"`
	database.Timestamps                   // CreatedAt 是任务发布时间
	Topic               string            `bun:"topic,notnull"`
	Payload             []byte            `bun:"payload,notnull"`
	Metadata            map[string]string `bun:"metadata,type:jsonb,notnull"`
	AvailableAt         time.Time         `bun:"available_at,notnull"`
}

// failedJob 是 queue_failed_jobs 表中的一行：一个失败的任务。
type failedJob struct {
	bun.BaseModel `bun:"table:queue_failed_jobs,alias:f"`

	ID                  uuid.UUID         `bun:"id,pk,type:uuid"`
	database.Timestamps                   // CreatedAt 是失败的时间
	Topic               string            `bun:"topic,notnull"`
	Payload             []byte            `bun:"payload,notnull"`
	Metadata            map[string]string `bun:"metadata,type:jsonb,notnull"`
	Attempts            int               `bun:"attempts,notnull"`
	Error               string            `bun:"error,notnull"`
}

// maxBackoff 是两次重试之间的最长等待时间。
const maxBackoff = 24 * time.Hour

// backoff 返回第 attempts 次处理失败后到下一次处理的等待时间：约 attempts⁴ 秒，上下浮动 10%，最长 24 小时。
func backoff(attempts int) time.Duration {
	// 先按秒封顶再转换，否则次数很大时 float64 转 Duration 会溢出。
	seconds := min(math.Pow(float64(max(attempts, 1)), 4)*(0.9+0.2*rand.Float64()), maxBackoff.Seconds())
	return time.Duration(seconds * float64(time.Second))
}
