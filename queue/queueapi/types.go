package queueapi

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"

	"github.com/go-tenon/tenon/page"
)

// 框架的类型在 OpenAPI 中只用类型名（见 rest 的 schema 命名），所以这里的类型都以 Queue 开头。

// QueueStatus 是概览（queue-status）：数据库一侧的复制槽、JetStream 一侧的 stream，以及每个 topic 一行。
type QueueStatus struct {
	Slot      QueueSlot       `json:"slot" doc:"转发任务的复制槽：数据库一侧的状态"`
	JetStream *QueueJetStream `json:"jetstream,omitempty" doc:"JetStream 一侧的状态；应用没有设置 Options.Admin 时没有这个字段，暂停和恢复返回 queue.no_admin"`
	Topics    []QueueTopic    `json:"topics" nullable:"false" doc:"JetStream 中的 topic 和有失败任务的 topic，按名字排序"`
	Failed    int             `json:"failed" doc:"全部 topic 的失败任务数"`
}

// QueueSlot 是复制槽的状态，见 queue.Status。
type QueueSlot struct {
	Name      string `json:"name" doc:"复制槽的名字"`
	Exists    bool   `json:"exists" doc:"复制槽是否存在；为 false 时 Worker 拒绝启动，要运行 queue slot recreate"`
	Active    bool   `json:"active" doc:"是否有 Worker 正在转发"`
	ActivePID int    `json:"active_pid" doc:"正在转发的复制连接在 PostgreSQL 中的进程号，没有时为 0"`
	WALStatus string `json:"wal_status" doc:"pg_replication_slots.wal_status：reserved、extended 正常，unreserved 即将失效，lost 已失效；复制槽不存在时为空"`
	Lag       int64  `json:"lag" doc:"Worker 还没确认的 WAL 字节数（积压），包含整个数据库的写入"`
}

// QueueJetStream 是 stream 的状态，见 queue.StreamStatus。读不到时只有 Error。
type QueueJetStream struct {
	Error    string `json:"error,omitempty" doc:"读不到 JetStream 时的错误（例如 NATS 不可用、读取超过 3 秒），这时其余字段为零值，topics 中只有失败任务数"`
	Stream   string `json:"stream" doc:"stream 的名字"`
	Exists   bool   `json:"exists" doc:"stream 是否存在；Worker 第一次启动时创建它"`
	Messages uint64 `json:"messages" doc:"stream 中的消息数：等待处理、处理中的任务和定时消息"`
	Bytes    uint64 `json:"bytes" doc:"stream 占用的存储（字节）"`
	Replicas int    `json:"replicas" doc:"副本数"`
	Leader   string `json:"leader,omitempty" doc:"集群中负责这个 stream 的服务器，单个服务器时没有这个字段"`
	Deferred int    `json:"deferred" doc:"等待到时间投递的定时消息：延迟任务和重试"`
}

// topic 的状态，与 queue status 命令的 STATE 一致。
const (
	// StateOK 表示正常处理。
	StateOK = "ok"
	// StateNoHandler 表示没有 Worker 为这个 topic 注册处理函数，任务一直等待。
	StateNoHandler = "no_handler"
	// StatePaused 表示暂停了处理，见 PausedUntil。
	StatePaused = "paused"
	// StateStalled 表示有任务在等待，却没有 Worker 在处理。
	StateStalled = "stalled"
	// StateAbsent 表示没有这个 topic 在 JetStream 中的状态：没有设置 Admin、读不到 JetStream，或者它只有失败任务。
	StateAbsent = "absent"
)

// QueueTopic 是概览中的一个 topic。State 为 absent 时 JetStream 的各项计数为 0，只有 Failed 有意义。
type QueueTopic struct {
	Topic         string     `json:"topic"`
	State         string     `json:"state" enum:"ok,no_handler,paused,stalled,absent" doc:"ok 正常；no_handler 没有 Worker 注册处理函数；paused 暂停中；stalled 有任务在等待却没有 Worker 在处理；absent 没有 JetStream 中的状态，只有失败任务数。ok、stalled 可以暂停，paused 可以恢复"`
	Pending       uint64     `json:"pending" doc:"等待处理的任务数"`
	InFlight      int        `json:"in_flight" doc:"已经交给 Worker、还没有确认的任务数"`
	Redelivered   int        `json:"redelivered" doc:"租约到期或停止时放回、再次投递还没有确认的任务数"`
	Waiting       int        `json:"waiting" doc:"Worker 等待任务的拉取请求数"`
	Failed        int        `json:"failed" doc:"失败任务数"`
	LastDelivered *time.Time `json:"last_delivered,omitempty" doc:"最近一次把任务交给 Worker 的时间，没有交过时没有这个字段"`
	PausedUntil   *time.Time `json:"paused_until,omitempty" doc:"暂停处理的截止时间，没有暂停时没有这个字段"`
}

// QueueTopicCount 是一个 topic 中失败任务的条数。
type QueueTopicCount struct {
	Topic string `json:"topic"`
	Count int    `json:"count"`
}

// QueueFailedList 是失败任务列表（queue-failed-list）：一页失败任务，以及每个 topic 的失败任务数。
type QueueFailedList struct {
	page.Offset[QueueFailedJob]
	Topics []QueueTopicCount `json:"topics" nullable:"false" doc:"每个 topic 的失败任务数，条数多的在前，不受 topic 筛选的影响；没有失败任务的 topic 不出现"`
}

// QueueFailedJob 是列表中的一条失败任务，完整的错误、元数据和 payload 见 QueueFailedJobDetail。
type QueueFailedJob struct {
	ID       uuid.UUID `json:"id"`
	Topic    string    `json:"topic"`
	Attempts int       `json:"attempts" doc:"失败前执行的次数"`
	Error    string    `json:"error" doc:"最后一次错误的第一行，超过 160 字节时截断并以 … 结尾"`
	FailedAt time.Time `json:"failed_at"`
}

// QueueFailedJobDetail 是一条失败任务的详情（queue-failed-get）。
type QueueFailedJobDetail struct {
	ID       uuid.UUID         `json:"id"`
	Topic    string            `json:"topic"`
	Attempts int               `json:"attempts" doc:"失败前执行的次数"`
	Error    string            `json:"error" doc:"最后一次的完整错误，包括堆栈"`
	FailedAt time.Time         `json:"failed_at"`
	Metadata map[string]string `json:"metadata" nullable:"false" doc:"发布时记录的元数据，例如 request_id"`
	Payload  json.RawMessage   `json:"payload" doc:"任务的 payload，原样返回的 JSON"`
}

// QueueBulkResult 是全部重试、全部删除的结果。
type QueueBulkResult struct {
	Count int `json:"count" doc:"处理的条数"`
}

// QueuePaused 是暂停 topic 的结果。
type QueuePaused struct {
	Topic       string    `json:"topic"`
	PausedUntil time.Time `json:"paused_until" doc:"暂停处理的截止时间：请求时刻之后一小时"`
}
