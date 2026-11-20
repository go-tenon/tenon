// Package queueapi 是队列运维的 JSON 接口：复制槽和 JetStream 的状态，各 topic 等待处理、处理中和失败的任务，
// 重试和删除失败任务，暂停和恢复 topic 的处理。它只读写 queue 已有的数据，不新增存储；吞吐、耗时这类指标看
// OpenTelemetry。面板页面由前端用这些接口实现。
//
// 接口注册在应用的 rest.API 上，路径以 /queue 开头，operationId 以 queue- 开头，生成的客户端中是 api.queue.xxx。
// protect 保护全部接口，通常要求一项运维权限：
//
//	admin := queue.NewAdmin(nc, cfg.Queue) // nc 是应用的 NATS 连接，见 natsx.Connect
//	queueapi.Register(api, rest.RequireToken(api, tokens.Validate, "queue:manage"), db, queueapi.Options{Admin: admin})
//
// 重试、删除、暂停和恢复各记一条 Info 日志，带上操作者（identity.Subject 的 Ref）、topic 和条数。
package queueapi

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/errorx"
	"github.com/go-tenon/tenon/httpx/rest"
	"github.com/go-tenon/tenon/identity"
	"github.com/go-tenon/tenon/logging"
	"github.com/go-tenon/tenon/page"
	"github.com/go-tenon/tenon/queue"
)

var (
	// ErrFailedJobNotFound 表示失败任务不存在：已经被重试、删除或清理。
	ErrFailedJobNotFound = errorx.Define(errorx.NotFound, "queue.failed_job_not_found")
	// ErrTopicNotFound 表示 JetStream 中没有这个 topic 的 consumer：没有 Worker 为它注册过处理函数。
	ErrTopicNotFound = errorx.Define(errorx.NotFound, "queue.topic_not_found")
	// ErrNoAdmin 表示应用没有设置 Options.Admin，不能暂停和恢复 topic。
	ErrNoAdmin = errorx.Define(errorx.Unavailable, "queue.no_admin")
)

const (
	// adminTimeout 是读取 JetStream 状态、暂停和恢复 topic 的超时：NATS 不可用时请求不至于一直等待。
	adminTimeout = 3 * time.Second
	// PauseFor 是 queue-topic-pause 暂停一个 topic 的时长，再次暂停时从当时起算。更长的暂停用 queue.Admin.Pause。
	PauseFor = time.Hour
	// bulkBatch 是全部重试、全部删除每批处理的条数。
	bulkBatch = 1000
	// errorLine 是列表中错误信息的最大长度（字节）。
	errorLine = 160
)

// Options 是 Register 的选项，都可以为零值。
type Options struct {
	Admin  *queue.Admin // 读取 JetStream 中的状态：设置后概览带上 stream、定时消息和各 topic 等待处理、处理中的任务，暂停和恢复 topic 才能使用。为 nil 时只有数据库一侧的状态，暂停和恢复返回 ErrNoAdmin。NATS 不可用时概览照常返回数据库一侧的状态。
	Logger *slog.Logger // 记录重试、删除、暂停和恢复，以及读不到 JetStream 的警告；nil 表示 slog.Default()。
}

// handlers 实现队列管理接口。
type handlers struct {
	db     *bun.DB
	admin  *queue.Admin
	logger *slog.Logger
}

// Register 在 api 上注册队列运维的接口，protect 保护全部接口，通常是 rest.RequireToken(api, validate, 权限)。
//
//	GET    /queue/status                 概览（queue-status）
//	GET    /queue/failed                 失败任务列表，按 topic 筛选、分页（queue-failed-list）
//	GET    /queue/failed/{id}            失败任务详情（queue-failed-get）
//	POST   /queue/failed/{id}/retry      重新发布一条失败任务（queue-failed-retry）
//	DELETE /queue/failed/{id}            删除一条失败任务（queue-failed-delete）
//	POST   /queue/failed/retry?topic=    重新发布一个 topic（为空时全部）的失败任务（queue-failed-retry-all）
//	DELETE /queue/failed?topic=          删除一个 topic（为空时全部）的失败任务（queue-failed-delete-all）
//	POST   /queue/topics/{topic}/pause   暂停 topic 一小时（queue-topic-pause）
//	POST   /queue/topics/{topic}/resume  恢复 topic（queue-topic-resume）
//
// 暂停和恢复总是注册，生成的客户端中总有这两个方法：生成文档和客户端时（DocRouter）通常没有 NATS 连接，
// 也就没有 Admin，接口却要和运行时一致。没有设置 Admin 时它们返回 ErrNoAdmin，概览中没有 jetstream 字段，
// 面板据此不显示暂停和恢复。
//
// db、opts.Admin 只在处理请求时使用，openapi、client 命令离线构造路由时可以为 nil。api 或 protect 为 nil 时 panic。
func Register(api *rest.API, protect func(*huma.Operation), db *bun.DB, opts Options) {
	if api == nil || protect == nil {
		panic("queueapi: nil API or protect")
	}
	h := &handlers{
		db: db, admin: opts.Admin,
		logger: logging.Or(opts.Logger).With(logging.AttrComponent, "queueapi"),
	}

	op := func(id, method, path, summary string) huma.Operation {
		o := huma.Operation{OperationID: id, Method: method, Path: path, Summary: summary, Tags: []string{"queue"}}
		protect(&o)
		return o
	}
	rest.Register(api, op("queue-status", http.MethodGet, "/queue/status", "队列概览"), h.status)
	rest.Register(api, op("queue-failed-list", http.MethodGet, "/queue/failed", "失败任务列表"), h.failedList)
	rest.Register(api, op("queue-failed-get", http.MethodGet, "/queue/failed/{id}", "失败任务详情"), h.failedGet)
	rest.Register(api, op("queue-failed-retry", http.MethodPost, "/queue/failed/{id}/retry", "重试失败任务"), h.retry)
	rest.Register(api, op("queue-failed-delete", http.MethodDelete, "/queue/failed/{id}", "删除失败任务"), h.delete)
	rest.Register(api, op("queue-failed-retry-all", http.MethodPost, "/queue/failed/retry", "全部重试失败任务"), h.retryAll)
	rest.Register(api, op("queue-failed-delete-all", http.MethodDelete, "/queue/failed", "全部删除失败任务"), h.deleteAll)
	rest.Register(api, op("queue-topic-pause", http.MethodPost, "/queue/topics/{topic}/pause", "暂停 topic 一小时"), h.pause)
	rest.Register(api, op("queue-topic-resume", http.MethodPost, "/queue/topics/{topic}/resume", "恢复 topic"), h.resume)
}

// failed 返回失败任务表。
func (h *handlers) failed() *queue.FailedJobs { return queue.NewFailedJobs(h.db) }

// statusOutput 是队列概览的响应。
type statusOutput struct{ Body QueueStatus }

// status 读取概览。JetStream 的状态读不到时写在 JetStream.Error 中，数据库一侧照常返回。
func (h *handlers) status(ctx context.Context, _ *struct{}) (*statusOutput, error) {
	st, err := queue.ReadStatus(ctx, h.db)
	if err != nil {
		return nil, err
	}
	counts, err := h.failed().Counts(ctx)
	if err != nil {
		return nil, err
	}
	out := QueueStatus{
		Slot: QueueSlot{Name: st.Slot, Exists: st.Exists, Active: st.Active, ActivePID: st.ActivePID, WALStatus: st.WALStatus, Lag: st.Lag},
	}
	var stream queue.StreamStatus
	if h.admin != nil {
		actx, cancel := context.WithTimeout(ctx, adminTimeout)
		stream, err = h.admin.Status(actx)
		cancel()
		js := &QueueJetStream{Stream: stream.Stream, Exists: stream.Exists, Messages: stream.Messages, Bytes: stream.Bytes,
			Replicas: stream.Replicas, Leader: stream.Leader, Deferred: stream.Deferred}
		if err != nil {
			h.logger.WarnContext(ctx, "read jetstream status failed", "error", err)
			js = &QueueJetStream{Error: err.Error()}
			stream = queue.StreamStatus{}
		}
		out.JetStream = js
	}
	out.Topics = topics(stream.Topics, counts)
	for _, c := range counts {
		out.Failed += c.Count
	}
	return &statusOutput{Body: out}, nil
}

// topics 合并 JetStream 中的 topic 和有失败任务的 topic，按名字排序。
func topics(inStream []queue.TopicStatus, counts []queue.TopicCount) []QueueTopic {
	byName := map[string]*QueueTopic{}
	for _, t := range inStream {
		row := &QueueTopic{Topic: t.Topic, State: state(t), Pending: t.Pending, InFlight: t.InFlight,
			Redelivered: t.Redelivered, Waiting: t.Waiting, LastDelivered: timePtr(t.LastDelivered), PausedUntil: timePtr(t.PausedUntil)}
		byName[t.Topic] = row
	}
	for _, c := range counts {
		row, ok := byName[c.Topic]
		if !ok {
			row = &QueueTopic{Topic: c.Topic, State: StateAbsent}
			byName[c.Topic] = row
		}
		row.Failed = c.Count
	}
	rows := make([]QueueTopic, 0, len(byName))
	for _, row := range byName {
		rows = append(rows, *row)
	}
	slices.SortFunc(rows, func(a, b QueueTopic) int { return strings.Compare(a.Topic, b.Topic) })
	return rows
}

// state 返回 JetStream 中一个 topic 的状态，与 queue status 命令的 STATE 一致。
func state(t queue.TopicStatus) string {
	switch {
	case !t.Handled:
		return StateNoHandler
	case t.Paused():
		return StatePaused
	case t.Stalled():
		return StateStalled
	default:
		return StateOK
	}
}

// timePtr 返回 t 的指针，零值时返回 nil。
func timePtr(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	u := t.UTC()
	return &u
}

// failedListInput 是失败任务列表的查询参数。
type failedListInput struct {
	Topic string `query:"topic" doc:"只列出这个 topic 的失败任务，为空时列出全部"`
	page.OffsetParams
}

// failedListOutput 是失败任务列表的响应。
type failedListOutput struct{ Body QueueFailedList }

// failedList 列出失败的任务，附带各 topic 的数量。
func (h *handlers) failedList(ctx context.Context, in *failedListInput) (*failedListOutput, error) {
	failed := h.failed()
	counts, err := failed.Counts(ctx)
	if err != nil {
		return nil, err
	}
	var total int64
	for _, c := range counts {
		if in.Topic == "" || c.Topic == in.Topic {
			total += int64(c.Count)
		}
	}
	jobs, err := failed.List(ctx, queue.FailedQuery{Topic: in.Topic, Limit: int(in.Limit()), Offset: int(in.Offset())})
	if err != nil {
		return nil, err
	}
	items := make([]QueueFailedJob, len(jobs))
	for i, j := range jobs {
		items[i] = QueueFailedJob{ID: j.ID, Topic: j.Topic, Attempts: j.Attempts, Error: firstLine(j.Error), FailedAt: j.FailedAt.UTC()}
	}
	return &failedListOutput{Body: QueueFailedList{Offset: page.NewOffset(items, in.OffsetParams, total)}}, nil
}

// idInput 指定一条失败任务。
type idInput struct {
	ID uuid.UUID `path:"id" doc:"失败任务的 ID"`
}

// failedGetOutput 是一个失败任务的详情。
type failedGetOutput struct{ Body QueueFailedJobDetail }

// failedGet 返回一个失败任务的详情。
func (h *handlers) failedGet(ctx context.Context, in *idInput) (*failedGetOutput, error) {
	j, err := h.failed().Get(ctx, in.ID)
	if err != nil {
		return nil, notFound(err)
	}
	d := QueueFailedJobDetail{ID: j.ID, Topic: j.Topic, Attempts: j.Attempts, Error: j.Error, FailedAt: j.FailedAt.UTC(),
		Metadata: j.Metadata, Payload: payload(j.Payload)}
	if d.Metadata == nil {
		d.Metadata = map[string]string{}
	}
	return &failedGetOutput{Body: d}, nil
}

// payload 原样返回 JSON 格式的 payload；不是合法的 JSON 时返回包含它的 JSON 字符串。
func payload(raw []byte) json.RawMessage {
	if json.Valid(raw) {
		return raw
	}
	s, _ := json.Marshal(string(raw))
	return s
}

// retry 重新发布一个失败的任务。
func (h *handlers) retry(ctx context.Context, in *idInput) (*struct{}, error) {
	return h.single(ctx, in.ID, "retried", h.failed().Retry)
}

// delete 删除一个失败的任务。
func (h *handlers) delete(ctx context.Context, in *idInput) (*struct{}, error) {
	return h.single(ctx, in.ID, "deleted", h.failed().Delete)
}

// single 对一条失败任务执行 do，记一条日志。
func (h *handlers) single(ctx context.Context, id uuid.UUID, action string, do func(ctx context.Context, id uuid.UUID) error) (*struct{}, error) {
	job, err := h.failed().Get(ctx, id)
	if err != nil {
		return nil, notFound(err)
	}
	if err := do(ctx, id); err != nil {
		return nil, notFound(err)
	}
	h.logger.InfoContext(ctx, "failed job "+action, "subject", operator(ctx), "topic", job.Topic, "id", id, "count", 1)
	return &struct{}{}, nil
}

// bulkInput 是全部重试、全部删除的参数。
type bulkInput struct {
	Topic string `query:"topic" doc:"只处理这个 topic 的失败任务，为空时处理全部 topic"`
}

// bulkOutput 是批量操作的结果。
type bulkOutput struct{ Body QueueBulkResult }

// retryAll 重新发布 topic 中全部失败的任务，topic 为空时是全部。
func (h *handlers) retryAll(ctx context.Context, in *bulkInput) (*bulkOutput, error) {
	return h.bulk(ctx, in.Topic, "retried", h.failed().RetryAll)
}

// deleteAll 删除 topic 中全部失败的任务，topic 为空时是全部。
func (h *handlers) deleteAll(ctx context.Context, in *bulkInput) (*bulkOutput, error) {
	return h.bulk(ctx, in.Topic, "deleted", h.failed().DeleteAll)
}

// bulk 分批处理 topic（为空时是全部 topic）中请求之前失败的任务，直到处理完，返回条数。
// 只处理请求之前失败的任务：重试之后马上又失败的任务在这一轮中只放回一次。
func (h *handlers) bulk(ctx context.Context, topic, action string, do func(ctx context.Context, topic string, before time.Time, limit int) (int, error)) (*bulkOutput, error) {
	before := time.Now()
	total := 0
	for {
		n, err := do(ctx, topic, before, bulkBatch)
		total += n
		if err != nil {
			if total > 0 {
				h.logger.InfoContext(ctx, "failed jobs "+action, "subject", operator(ctx), "topic", topic, "count", total)
			}
			return nil, err
		}
		if n < bulkBatch {
			break
		}
	}
	h.logger.InfoContext(ctx, "failed jobs "+action, "subject", operator(ctx), "topic", topic, "count", total)
	return &bulkOutput{Body: QueueBulkResult{Count: total}}, nil
}

// topicInput 指定一个 topic。
type topicInput struct {
	Topic string `path:"topic" doc:"topic 的名字，例如 mail.send"`
}

// pauseOutput 是暂停的结果：恢复的时间。
type pauseOutput struct{ Body QueuePaused }

// pause 暂停 topic 的处理 PauseFor 这么久。
func (h *handlers) pause(ctx context.Context, in *topicInput) (*pauseOutput, error) {
	until := time.Now().Add(PauseFor)
	err := h.topicAction(ctx, in.Topic, "paused", func(ctx context.Context, topic string) error {
		return h.admin.Pause(ctx, topic, until)
	})
	if err != nil {
		return nil, err
	}
	return &pauseOutput{Body: QueuePaused{Topic: in.Topic, PausedUntil: until.UTC()}}, nil
}

// resume 恢复 topic 的处理。
func (h *handlers) resume(ctx context.Context, in *topicInput) (*struct{}, error) {
	if err := h.topicAction(ctx, in.Topic, "resumed", func(ctx context.Context, topic string) error {
		return h.admin.Resume(ctx, topic)
	}); err != nil {
		return nil, err
	}
	return &struct{}{}, nil
}

// topicAction 对 topic 执行 do，记一条日志。
func (h *handlers) topicAction(ctx context.Context, topic, action string, do func(ctx context.Context, topic string) error) error {
	if h.admin == nil {
		return ErrNoAdmin.New()
	}
	actx, cancel := context.WithTimeout(ctx, adminTimeout)
	err := do(actx, topic)
	cancel()
	if errors.Is(err, queue.ErrTopicNotFound) {
		return ErrTopicNotFound.Wrap(err)
	}
	if err != nil {
		return err
	}
	h.logger.InfoContext(ctx, "topic "+action, "subject", operator(ctx), "topic", topic)
	return nil
}

// operator 返回操作者的引用，写入日志。
func operator(ctx context.Context) string {
	sub, _ := identity.FromContext(ctx)
	return sub.Ref().String()
}

// notFound 把失败任务不存在转换为 ErrFailedJobNotFound。
func notFound(err error) error {
	if errors.Is(err, queue.ErrFailedJobNotFound) {
		return ErrFailedJobNotFound.Wrap(err)
	}
	return err
}

// firstLine 返回错误的第一行，最长 errorLine 字节（按 UTF-8 字符截断）。
func firstLine(s string) string {
	line, _, _ := strings.Cut(s, "\n")
	if len(line) <= errorLine {
		return line
	}
	cut := errorLine
	for cut > 0 && !utf8.RuneStart(line[cut]) {
		cut--
	}
	return line[:cut] + "…"
}
