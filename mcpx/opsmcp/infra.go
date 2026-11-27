package opsmcp

import (
	"context"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/nats-io/nats.go/jetstream"
	"github.com/redis/go-redis/v9"

	"github.com/go-tenon/tenon/errorx"
	"github.com/go-tenon/tenon/mcpx"
	"github.com/go-tenon/tenon/queue"
)

const (
	// maxFailedListLimit 是 ops_queue_failed 单次最多返回的失败任务条数。
	maxFailedListLimit = 100
	// defaultFailedListLimit 是 ops_queue_failed 默认返回的失败任务条数。
	defaultFailedListLimit = 20
	// maxRedisScanLimit 是 ops_redis_scan 单次最多返回的键数。
	maxRedisScanLimit = 200
	// defaultRedisScanLimit 是 ops_redis_scan 默认返回的键数。
	defaultRedisScanLimit = 50
	// maxRedisScanRounds 是 ops_redis_scan 最多连续扫描的轮次，避免全库扫描拖慢 Redis。
	maxRedisScanRounds = 20
	// maxRedisCollectionItems 是 ops_redis_get 对集合类键（hash/list/set/zset）最多返回的元素个数。
	maxRedisCollectionItems = 50
)

// allowedRedisInfoSections 是 ops_redis_info 允许查询的 INFO 分段白名单。
var allowedRedisInfoSections = map[string]bool{
	"server":      true,
	"clients":     true,
	"memory":      true,
	"stats":       true,
	"replication": true,
	"cpu":         true,
	"keyspace":    true,
}

// ignoredRedisInfoFields 是 ops_redis_info 过滤掉的主机路径类字段。
var ignoredRedisInfoFields = map[string]bool{
	"executable":  true,
	"config_file": true,
}

// queueStatusArgs 是 ops_queue_status 的参数。
type queueStatusArgs struct{}

// queueOutboxSummary 是 queue_outbox 的积压摘要。
type queueOutboxSummary struct {
	Pending       int64  `json:"pending"`
	Deferred      int64  `json:"deferred"`
	OldestPending string `json:"oldest_pending,omitempty"`
}

// queueTopicSummary 是单个 topic 在 JetStream 中的状态摘要。
type queueTopicSummary struct {
	Topic         string `json:"topic"`
	Handled       bool   `json:"handled"`
	Pending       uint64 `json:"pending"`
	InFlight      int    `json:"in_flight"`
	Redelivered   int    `json:"redelivered"`
	Waiting       int    `json:"waiting"`
	Paused        bool   `json:"paused"`
	Stalled       bool   `json:"stalled"`
	LastDelivered string `json:"last_delivered,omitempty"`
	PausedUntil   string `json:"paused_until,omitempty"`
}

// queueJetStreamSummary 是 JetStream 队列状态摘要。
type queueJetStreamSummary struct {
	Stream   string              `json:"stream"`
	Exists   bool                `json:"exists"`
	Messages uint64              `json:"messages"`
	Bytes    uint64              `json:"bytes"`
	Replicas int                 `json:"replicas"`
	Leader   string              `json:"leader,omitempty"`
	Topics   []queueTopicSummary `json:"topics"`
}

// queueFailedCount 是单个 topic 的失败任务计数。
type queueFailedCount struct {
	Topic string `json:"topic"`
	Count int    `json:"count"`
}

// queueStatusResult 是 ops_queue_status 的返回值。
type queueStatusResult struct {
	Outbox           *queueOutboxSummary    `json:"outbox,omitempty"`
	FailedCounts     []queueFailedCount     `json:"failed_counts,omitempty"`
	JetStream        *queueJetStreamSummary `json:"jetstream,omitempty"`
	JetStreamWarning string                 `json:"jetstream_warning,omitempty"`
}

// queueFailedArgs 是 ops_queue_failed 的参数。
type queueFailedArgs struct {
	Topic  string `json:"topic,omitempty" doc:"可选：按 topic 筛选失败任务，留空列出全部"`
	Limit  int    `json:"limit,omitempty" doc:"返回条数，默认 20，上限 100"`
	Offset int    `json:"offset,omitempty" doc:"分页偏移，默认 0"`
}

// queueFailedItem 是脱敏并截断后的单条失败任务摘要。
type queueFailedItem struct {
	ID       string            `json:"id"`
	Topic    string            `json:"topic"`
	Attempts int               `json:"attempts"`
	FailedAt string            `json:"failed_at"`
	Error    string            `json:"error"`
	Payload  string            `json:"payload"`
	Metadata map[string]string `json:"metadata,omitempty"`
}

// queueFailedResult 是 ops_queue_failed 的返回值。
type queueFailedResult struct {
	Jobs []queueFailedItem `json:"jobs"`
}

// registerQueue 登记队列只读观测工具 ops_queue_status 与 ops_queue_failed。
func (k *kit) registerQueue(srv *mcpx.Server) {
	mcpx.Register(srv, k.tool(
		"ops_queue_status",
		"查看队列状态",
		"查看任务队列的数据库发件箱（outbox）积压、各 topic 失败任务计数以及 JetStream 的处理与暂停状态。",
	), k.handleQueueStatus)

	if k.queueDB != nil {
		mcpx.Register(srv, k.tool(
			"ops_queue_failed",
			"查看队列失败任务",
			"按失败时间倒序列出 queue_failed_jobs 中的失败任务、失败次数、错误堆栈与截断后的载荷（纯只读）。",
		), k.handleQueueFailed)
	}
}

// handleQueueStatus 汇总 outbox、失败计数和 JetStream 状态。
func (k *kit) handleQueueStatus(ctx context.Context, _ queueStatusArgs) (queueStatusResult, error) {
	k.audit(ctx, "ops_queue_status")
	var res queueStatusResult
	if k.queueDB != nil {
		st, err := queue.ReadStatus(ctx, k.queueDB)
		if err != nil {
			return queueStatusResult{}, ErrQueryFailed.Wrap(err, errorx.Public("reason", err.Error()))
		}
		ob := &queueOutboxSummary{Pending: st.Pending, Deferred: st.Deferred}
		if !st.OldestPending.IsZero() {
			ob.OldestPending = st.OldestPending.UTC().Format(time.RFC3339)
		}
		res.Outbox = ob

		counts, err := queue.NewFailedJobs(k.queueDB).Counts(ctx)
		if err != nil {
			return queueStatusResult{}, ErrQueryFailed.Wrap(err, errorx.Public("reason", err.Error()))
		}
		res.FailedCounts = make([]queueFailedCount, 0, len(counts))
		for _, c := range counts {
			res.FailedCounts = append(res.FailedCounts, queueFailedCount{Topic: c.Topic, Count: c.Count})
		}
	}
	if k.queueAdmin != nil {
		jsCtx, cancel := context.WithTimeout(ctx, defaultInfraTimeout)
		defer cancel()
		js, err := k.queueAdmin.Status(jsCtx)
		if err != nil {
			res.JetStreamWarning = err.Error()
		} else {
			summary := &queueJetStreamSummary{
				Stream:   js.Stream,
				Exists:   js.Exists,
				Messages: js.Messages,
				Bytes:    js.Bytes,
				Replicas: js.Replicas,
				Leader:   js.Leader,
				Topics:   make([]queueTopicSummary, 0, len(js.Topics)),
			}
			for _, t := range js.Topics {
				item := queueTopicSummary{
					Topic:       t.Topic,
					Handled:     t.Handled,
					Pending:     t.Pending,
					InFlight:    t.InFlight,
					Redelivered: t.Redelivered,
					Waiting:     t.Waiting,
					Paused:      t.Paused(),
					Stalled:     t.Stalled(),
				}
				if !t.LastDelivered.IsZero() {
					item.LastDelivered = t.LastDelivered.UTC().Format(time.RFC3339)
				}
				if !t.PausedUntil.IsZero() {
					item.PausedUntil = t.PausedUntil.UTC().Format(time.RFC3339)
				}
				summary.Topics = append(summary.Topics, item)
			}
			res.JetStream = summary
		}
	}
	return res, nil
}

// handleQueueFailed 读取最近的失败任务记录。
func (k *kit) handleQueueFailed(ctx context.Context, in queueFailedArgs) (queueFailedResult, error) {
	limit := in.Limit
	if limit <= 0 {
		limit = defaultFailedListLimit
	}
	if limit > maxFailedListLimit {
		limit = maxFailedListLimit
	}
	k.audit(ctx, "ops_queue_failed", "topic", in.Topic, "limit", limit, "offset", in.Offset)
	jobs, err := queue.NewFailedJobs(k.queueDB).List(ctx, queue.FailedQuery{
		Topic:  strings.TrimSpace(in.Topic),
		Limit:  limit,
		Offset: max(in.Offset, 0),
	})
	if err != nil {
		return queueFailedResult{}, ErrQueryFailed.Wrap(err, errorx.Public("reason", err.Error()))
	}
	out := make([]queueFailedItem, 0, len(jobs))
	for _, j := range jobs {
		payload := string(j.Payload)
		if !utf8.Valid(j.Payload) {
			payload = "<binary payload>"
		}
		out = append(out, queueFailedItem{
			ID:       j.ID.String(),
			Topic:    j.Topic,
			Attempts: j.Attempts,
			FailedAt: j.FailedAt.UTC().Format(time.RFC3339),
			Error:    truncateString(j.Error, k.maxCellLen*2),
			Payload:  truncateString(payload, k.maxCellLen),
			Metadata: j.Metadata,
		})
	}
	return queueFailedResult{Jobs: out}, nil
}

// scheduleStatusArgs 是 ops_schedule_status 的参数。
type scheduleStatusArgs struct{}

// scheduleTaskItem 是单个定时任务的持久状态。
type scheduleTaskItem struct {
	Name     string `json:"name"`
	LastTick string `json:"last_tick,omitempty"`
	Running  bool   `json:"running"`
}

// scheduleStatusResult 是 ops_schedule_status 的返回值。
type scheduleStatusResult struct {
	Tasks []scheduleTaskItem `json:"tasks"`
}

// registerSchedule 登记定时任务只读状态查询工具 ops_schedule_status。
func (k *kit) registerSchedule(srv *mcpx.Server) {
	mcpx.Register(srv, k.tool(
		"ops_schedule_status",
		"查看定时任务状态",
		"列出全部已记录的定时任务最近触发时间（last_tick）以及当前是否持有运行租约。",
	), func(ctx context.Context, _ scheduleStatusArgs) (scheduleStatusResult, error) {
		k.audit(ctx, "ops_schedule_status")
		states, err := k.schedule.States(ctx)
		if err != nil {
			return scheduleStatusResult{}, ErrQueryFailed.Wrap(err, errorx.Public("reason", err.Error()))
		}
		names := make([]string, 0, len(states))
		for name := range states {
			names = append(names, name)
		}
		slices.Sort(names)
		tasks := make([]scheduleTaskItem, 0, len(names))
		for _, name := range names {
			st := states[name]
			item := scheduleTaskItem{Name: name, Running: st.Running}
			if !st.LastTick.IsZero() {
				item.LastTick = st.LastTick.UTC().Format(time.RFC3339)
			}
			tasks = append(tasks, item)
		}
		return scheduleStatusResult{Tasks: tasks}, nil
	})
}

// redisInfoArgs 是 ops_redis_info 的参数。
type redisInfoArgs struct {
	Section string `json:"section,omitempty" doc:"可选：INFO 分段（server/clients/memory/stats/replication/cpu/keyspace），留空返回常用核心分段"`
}

// redisInfoResult 是 ops_redis_info 的返回值。
type redisInfoResult struct {
	Sections map[string]map[string]string `json:"sections"`
}

// redisScanArgs 是 ops_redis_scan 的参数。
type redisScanArgs struct {
	Pattern string `json:"pattern,omitempty" doc:"SCAN 匹配模式，例如 app:cache:*，默认 *"`
	Limit   int    `json:"limit,omitempty" doc:"最多返回的键数，默认 50，上限 200"`
}

// redisKeySummary 是扫描到的单个 Redis 键摘要。
type redisKeySummary struct {
	Key        string `json:"key"`
	Type       string `json:"type"`
	TTLSeconds int64  `json:"ttl_seconds"`
}

// redisScanResult 是 ops_redis_scan 的返回值。
type redisScanResult struct {
	Pattern   string            `json:"pattern"`
	Keys      []redisKeySummary `json:"keys"`
	Truncated bool              `json:"truncated"`
}

// redisGetArgs 是 ops_redis_get 的参数。
type redisGetArgs struct {
	Key string `json:"key" doc:"要查看的 Redis 键名"`
}

// redisGetResult 是 ops_redis_get 的返回值。
type redisGetResult struct {
	Key        string `json:"key"`
	Exists     bool   `json:"exists"`
	Type       string `json:"type,omitempty"`
	TTLSeconds int64  `json:"ttl_seconds,omitempty"`
	Redacted   bool   `json:"redacted,omitempty"`
	Value      any    `json:"value,omitempty"`
	Truncated  bool   `json:"truncated,omitempty"`
}

// registerRedis 登记 Redis 只读观测工具（INFO、限次 SCAN 与单键只读查看）。
func (k *kit) registerRedis(srv *mcpx.Server) {
	mcpx.Register(srv, k.tool(
		"ops_redis_info",
		"查看 Redis 运行信息",
		"返回 Redis 的内存、连接数、命中率与 keyspace 等核心只读指标（INFO）。",
	), k.handleRedisInfo)

	mcpx.Register(srv, k.tool(
		"ops_redis_scan",
		"按前缀扫描 Redis 键",
		"使用限次 SCAN（绝不使用 KEYS *）按 pattern 扫描键列表，并返回每个键的类型与剩余 TTL（秒）。",
	), k.handleRedisScan)

	mcpx.Register(srv, k.tool(
		"ops_redis_get",
		"查看单个 Redis 键",
		"查看指定 Redis 键的类型、TTL 与只读内容（支持 string/hash/list/set/zset，自动截断超长值并对敏感键名脱敏）。",
	), k.handleRedisGet)
}

// handleRedisInfo 读取并解析 Redis INFO 白名单分段。
func (k *kit) handleRedisInfo(ctx context.Context, in redisInfoArgs) (redisInfoResult, error) {
	sec := strings.ToLower(strings.TrimSpace(in.Section))
	var sections []string
	if sec == "" {
		sections = []string{"server", "clients", "memory", "stats", "keyspace"}
	} else {
		if !allowedRedisInfoSections[sec] {
			return redisInfoResult{}, ErrQueryRejected.New(errorx.Public("reason",
				"不支持的 Redis INFO 分段，仅允许 server/clients/memory/stats/replication/cpu/keyspace"))
		}
		sections = []string{sec}
	}
	k.audit(ctx, "ops_redis_info", "section", sec)

	rctx, cancel := context.WithTimeout(ctx, defaultInfraTimeout)
	defer cancel()
	raw, err := k.rdb.Info(rctx, sections...).Result()
	if err != nil {
		return redisInfoResult{}, ErrQueryFailed.Wrap(err, errorx.Public("reason", err.Error()))
	}
	parsed := parseRedisInfo(raw)
	return redisInfoResult{Sections: parsed}, nil
}

// parseRedisInfo 将 Redis INFO 文本解析为按分段组织的键值表，跳过路径类字段。
func parseRedisInfo(raw string) map[string]map[string]string {
	out := map[string]map[string]string{}
	cur := "default"
	for line := range strings.Lines(raw) {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "#") {
			cur = strings.ToLower(strings.TrimSpace(strings.TrimPrefix(line, "#")))
			continue
		}
		key, val, ok := strings.Cut(line, ":")
		if !ok || ignoredRedisInfoFields[key] {
			continue
		}
		if out[cur] == nil {
			out[cur] = map[string]string{}
		}
		out[cur][key] = val
	}
	return out
}

// handleRedisScan 通过有界 SCAN 返回键名、类型和 TTL。
func (k *kit) handleRedisScan(ctx context.Context, in redisScanArgs) (redisScanResult, error) {
	pattern := strings.TrimSpace(in.Pattern)
	if pattern == "" {
		pattern = "*"
	}
	limit := in.Limit
	if limit <= 0 {
		limit = defaultRedisScanLimit
	}
	if limit > maxRedisScanLimit {
		limit = maxRedisScanLimit
	}
	k.audit(ctx, "ops_redis_scan", "pattern", pattern, "limit", limit)

	rctx, cancel := context.WithTimeout(ctx, defaultInfraTimeout)
	defer cancel()

	var found []string
	seen := make(map[string]bool, limit)
	var cursor uint64
	truncated := false
	for round := 0; round < maxRedisScanRounds; round++ {
		keys, next, err := k.rdb.Scan(rctx, cursor, pattern, 100).Result()
		if err != nil {
			return redisScanResult{}, ErrQueryFailed.Wrap(err, errorx.Public("reason", err.Error()))
		}
		for _, key := range keys {
			if seen[key] {
				continue
			}
			seen[key] = true
			found = append(found, key)
			if len(found) >= limit {
				truncated = next != 0 || len(keys) > len(found)
				break
			}
		}
		cursor = next
		if len(found) >= limit {
			break
		}
		if cursor == 0 {
			break
		}
		if round == maxRedisScanRounds-1 {
			truncated = true
		}
	}
	slices.Sort(found)

	items := make([]redisKeySummary, len(found))
	if len(found) > 0 {
		pipe := k.rdb.Pipeline()
		typeCmds := make([]*redis.StatusCmd, len(found))
		ttlCmds := make([]*redis.DurationCmd, len(found))
		for i, key := range found {
			typeCmds[i] = pipe.Type(rctx, key)
			ttlCmds[i] = pipe.TTL(rctx, key)
		}
		if _, err := pipe.Exec(rctx); err != nil && err != redis.Nil {
			return redisScanResult{}, ErrQueryFailed.Wrap(err, errorx.Public("reason", err.Error()))
		}
		for i, key := range found {
			items[i] = redisKeySummary{
				Key:        key,
				Type:       typeCmds[i].Val(),
				TTLSeconds: ttlToSeconds(ttlCmds[i].Val()),
			}
		}
	}
	return redisScanResult{Pattern: pattern, Keys: items, Truncated: truncated}, nil
}

// handleRedisGet 只读查看单个 Redis 键的元数据与内容，敏感键自动脱敏。
func (k *kit) handleRedisGet(ctx context.Context, in redisGetArgs) (redisGetResult, error) {
	key := strings.TrimSpace(in.Key)
	if key == "" {
		return redisGetResult{}, ErrQueryRejected.New(errorx.Public("reason", "key 不能为空"))
	}
	k.audit(ctx, "ops_redis_get", "key", key)

	rctx, cancel := context.WithTimeout(ctx, defaultInfraTimeout)
	defer cancel()

	keyType, err := k.rdb.Type(rctx, key).Result()
	if err != nil {
		return redisGetResult{}, ErrQueryFailed.Wrap(err, errorx.Public("reason", err.Error()))
	}
	if keyType == "none" {
		return redisGetResult{Key: key, Exists: false}, nil
	}
	ttl, err := k.rdb.TTL(rctx, key).Result()
	if err != nil {
		return redisGetResult{}, ErrQueryFailed.Wrap(err, errorx.Public("reason", err.Error()))
	}
	res := redisGetResult{
		Key:        key,
		Exists:     true,
		Type:       keyType,
		TTLSeconds: ttlToSeconds(ttl),
	}
	if matchesAny(key, k.redactRedisKeys) {
		res.Redacted = true
		res.Value = redactedValue
		return res, nil
	}

	switch keyType {
	case "string":
		val, err := k.rdb.Get(rctx, key).Result()
		if err != nil && err != redis.Nil {
			return redisGetResult{}, ErrQueryFailed.Wrap(err, errorx.Public("reason", err.Error()))
		}
		truncated := utf8.RuneCountInString(val) > k.maxCellLen
		res.Value = truncateString(val, k.maxCellLen)
		res.Truncated = truncated
	case "hash":
		vals, cursor, err := k.rdb.HScan(rctx, key, 0, "*", maxRedisCollectionItems).Result()
		if err != nil {
			return redisGetResult{}, ErrQueryFailed.Wrap(err, errorx.Public("reason", err.Error()))
		}
		m := make(map[string]string, len(vals)/2)
		for i := 0; i+1 < len(vals); i += 2 {
			f, v := vals[i], vals[i+1]
			if matchesAny(f, k.redactColumns) {
				m[f] = redactedValue
			} else {
				m[f] = truncateString(v, k.maxCellLen)
			}
		}
		res.Value = m
		res.Truncated = cursor != 0
	case "list":
		items, err := k.rdb.LRange(rctx, key, 0, maxRedisCollectionItems).Result()
		if err != nil {
			return redisGetResult{}, ErrQueryFailed.Wrap(err, errorx.Public("reason", err.Error()))
		}
		if len(items) > maxRedisCollectionItems {
			items = items[:maxRedisCollectionItems]
			res.Truncated = true
		}
		for i := range items {
			items[i] = truncateString(items[i], k.maxCellLen)
		}
		res.Value = items
	case "set":
		items, cursor, err := k.rdb.SScan(rctx, key, 0, "*", maxRedisCollectionItems).Result()
		if err != nil {
			return redisGetResult{}, ErrQueryFailed.Wrap(err, errorx.Public("reason", err.Error()))
		}
		for i := range items {
			items[i] = truncateString(items[i], k.maxCellLen)
		}
		res.Value = items
		res.Truncated = cursor != 0
	case "zset":
		items, err := k.rdb.ZRangeWithScores(rctx, key, 0, maxRedisCollectionItems).Result()
		if err != nil {
			return redisGetResult{}, ErrQueryFailed.Wrap(err, errorx.Public("reason", err.Error()))
		}
		if len(items) > maxRedisCollectionItems {
			items = items[:maxRedisCollectionItems]
			res.Truncated = true
		}
		type zItem struct {
			Member string  `json:"member"`
			Score  float64 `json:"score"`
		}
		out := make([]zItem, 0, len(items))
		for _, it := range items {
			out = append(out, zItem{Member: truncateString(toString(it.Member), k.maxCellLen), Score: it.Score})
		}
		res.Value = out
	default:
		res.Value = "<unsupported redis type: " + keyType + ">"
	}
	return res, nil
}

// ttlToSeconds 把 go-redis 的 TTL 转换为秒（-1 表示永不过期，-2 表示不存在）。
func ttlToSeconds(d time.Duration) int64 {
	if d < 0 {
		return int64(d)
	}
	return int64(d / time.Second)
}

// toString 将 Redis 成员转为字符串。
func toString(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	if b, ok := v.([]byte); ok {
		return string(b)
	}
	return ""
}

// natsStatusArgs 是 ops_nats_status 的参数。
type natsStatusArgs struct {
	Stream string `json:"stream,omitempty" doc:"可选：指定 JetStream 的 stream 名时同时列出其全部 consumer 详情"`
}

// natsStreamSummary 是单个 JetStream stream 的只读概览。
type natsStreamSummary struct {
	Name          string   `json:"name"`
	Subjects      []string `json:"subjects"`
	Messages      uint64   `json:"messages"`
	Bytes         uint64   `json:"bytes"`
	ConsumerCount int      `json:"consumer_count"`
	Replicas      int      `json:"replicas"`
	Leader        string   `json:"leader,omitempty"`
}

// natsConsumerSummary 是单个 JetStream consumer 的只读状态。
type natsConsumerSummary struct {
	Name          string `json:"name"`
	FilterSubject string `json:"filter_subject,omitempty"`
	NumPending    uint64 `json:"num_pending"`
	NumAckPending int    `json:"num_ack_pending"`
	NumRedeliver  int    `json:"num_redelivered"`
	NumWaiting    int    `json:"num_waiting"`
	Paused        bool   `json:"paused"`
}

// natsStatusResult 是 ops_nats_status 的返回值。
type natsStatusResult struct {
	Connected     bool                  `json:"connected"`
	ServerName    string                `json:"server_name,omitempty"`
	ServerVersion string                `json:"server_version,omitempty"`
	JetStream     bool                  `json:"jetstream"`
	Streams       []natsStreamSummary   `json:"streams,omitempty"`
	Consumers     []natsConsumerSummary `json:"consumers,omitempty"`
	Warning       string                `json:"warning,omitempty"`
}

// registerNATS 登记 NATS 与 JetStream 只读状态工具 ops_nats_status。
func (k *kit) registerNATS(srv *mcpx.Server) {
	mcpx.Register(srv, k.tool(
		"ops_nats_status",
		"查看 NATS 与 JetStream 状态",
		"查看 NATS 连接状态以及 JetStream 全部 stream 的消息数、体积和副本信息；传入 stream 时同时列出其全部 consumer 的积压与待确认数。",
	), k.handleNATSStatus)
}

// handleNATSStatus 读取 NATS 与 JetStream 的只读元数据。
func (k *kit) handleNATSStatus(ctx context.Context, in natsStatusArgs) (natsStatusResult, error) {
	streamFilter := strings.TrimSpace(in.Stream)
	k.audit(ctx, "ops_nats_status", "stream", streamFilter)

	res := natsStatusResult{
		Connected:     k.nc.IsConnected(),
		ServerName:    k.nc.ConnectedServerName(),
		ServerVersion: k.nc.ConnectedServerVersion(),
	}
	js, err := jetstream.New(k.nc)
	if err != nil {
		res.Warning = err.Error()
		return res, nil
	}
	nctx, cancel := context.WithTimeout(ctx, defaultInfraTimeout)
	defer cancel()

	if _, err := js.AccountInfo(nctx); err != nil {
		res.Warning = err.Error()
		return res, nil
	}
	res.JetStream = true

	lister := js.ListStreams(nctx)
	for info := range lister.Info() {
		item := natsStreamSummary{
			Name:          info.Config.Name,
			Subjects:      info.Config.Subjects,
			Messages:      info.State.Msgs,
			Bytes:         info.State.Bytes,
			ConsumerCount: info.State.Consumers,
			Replicas:      info.Config.Replicas,
		}
		if info.Cluster != nil {
			item.Leader = info.Cluster.Leader
		}
		res.Streams = append(res.Streams, item)
	}
	if err := lister.Err(); err != nil {
		return natsStatusResult{}, ErrQueryFailed.Wrap(err, errorx.Public("reason", err.Error()))
	}
	slices.SortFunc(res.Streams, func(a, b natsStreamSummary) int { return strings.Compare(a.Name, b.Name) })

	if streamFilter != "" {
		st, err := js.Stream(nctx, streamFilter)
		if err != nil {
			return natsStatusResult{}, ErrQueryFailed.Wrap(err, errorx.Public("reason", err.Error()))
		}
		cl := st.ListConsumers(nctx)
		for ci := range cl.Info() {
			res.Consumers = append(res.Consumers, natsConsumerSummary{
				Name:          ci.Name,
				FilterSubject: ci.Config.FilterSubject,
				NumPending:    ci.NumPending,
				NumAckPending: ci.NumAckPending,
				NumRedeliver:  ci.NumRedelivered,
				NumWaiting:    ci.NumWaiting,
				Paused:        ci.Paused,
			})
		}
		if err := cl.Err(); err != nil {
			return natsStatusResult{}, ErrQueryFailed.Wrap(err, errorx.Public("reason", err.Error()))
		}
		slices.SortFunc(res.Consumers, func(a, b natsConsumerSummary) int { return strings.Compare(a.Name, b.Name) })
	}
	return res, nil
}
