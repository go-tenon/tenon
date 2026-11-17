package queue

import (
	"context"
	"slices"
	"strings"
	"time"

	"github.com/cockroachdb/errors"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

// ErrTopicNotFound 表示 JetStream 中没有这个 topic 的 consumer：没有 Worker 为它注册过处理函数。
var ErrTopicNotFound = errors.New("queue: topic not found")

// Admin 查看和调整 JetStream 中的队列：各 topic 等待处理和处理中的任务、定时消息，暂停和恢复 topic 的处理。
// 队列的运维接口（queueapi）和 queue 命令（queuecmd）用它；数据库一侧的状态见 ReadStatus。
//
// 它使用应用的 NATS 连接，不关闭它。可以被多个 goroutine 同时使用。
type Admin struct {
	cfg Config
	js  jetstream.JetStream
}

// NewAdmin 创建 Admin。nc 是 natsx.Connect 返回的连接，cfg 与 Worker 的相同。nc 为 nil 或 cfg 不合法时 panic。
func NewAdmin(nc *nats.Conn, cfg Config) *Admin {
	if nc == nil {
		panic("queue: nil nats connection")
	}
	if err := cfg.Validate(); err != nil {
		panic(errors.Wrap(err, "queue"))
	}
	js, err := jetstream.New(nc)
	if err != nil {
		panic(errors.Wrap(err, "queue: jetstream")) // 只在选项不合法时出错
	}
	return &Admin{cfg: cfg, js: js}
}

func (a *Admin) jetStream() (jetstream.JetStream, error) { return a.js, nil }

// StreamStatus 是队列在 JetStream 一侧的状态。
type StreamStatus struct {
	Stream string // stream 的名字，见 Config.Stream
	// Exists 报告 stream 是否存在。Worker 第一次启动时创建它，之前为 false，其余字段为零值。
	Exists bool
	// Messages 和 Bytes 是 stream 中的消息数和占用的存储：等待处理、处理中的任务和定时消息，确认后删除。
	Messages uint64
	Bytes    uint64
	// Replicas 是副本数；Leader 是集群中负责这个 stream 的服务器，单个服务器时为空。
	Replicas int
	Leader   string
	// Deferred 是等待 JetStream 到时间投递的定时消息：延迟任务和重试。
	Deferred int
	// Topics 是有 consumer 或者有任务的 topic，按名字排序。
	Topics []TopicStatus
}

// TopicStatus 是一个 topic 在 JetStream 中的状态。
type TopicStatus struct {
	Topic string
	// Handled 报告是否有 consumer：有 Worker 为这个 topic 注册过处理函数。为 false 时任务一直等待，
	// 直到有 Worker 注册它，通常是忘了注册，或者 topic 名写错了。
	Handled bool
	// Pending 是等待处理的任务数。
	Pending uint64
	// InFlight 是已经交给 Worker、还没有确认的任务数。
	InFlight int
	// Redelivered 是租约到期或停止时放回、再次投递还没有确认的任务数。
	Redelivered int
	// Waiting 是 Worker 等待任务的拉取请求数：Worker 有空闲的并发名额时一直有拉取请求。
	Waiting int
	// LastDelivered 是最近一次把任务交给 Worker 的时间，没有交过时为零值。
	LastDelivered time.Time
	// PausedUntil 是暂停处理的截止时间，没有暂停时为零值，见 Admin.Pause。
	PausedUntil time.Time
}

// Paused 报告 topic 是否暂停了处理。
func (s TopicStatus) Paused() bool { return !s.PausedUntil.IsZero() }

// Stalled 报告是否有任务在等待，却没有 Worker 在处理：有 consumer、没有暂停、没有处理中的任务，
// 也没有 Worker 在拉取。通常是所有 Worker 都停止了。Worker 刚处理完一批、还没有再次拉取的瞬间也可能报告 true。
func (s TopicStatus) Stalled() bool {
	return s.Handled && !s.Paused() && s.Pending > 0 && s.InFlight == 0 && s.Waiting == 0
}

// Status 读取 stream 和各 topic 的状态。
func (a *Admin) Status(ctx context.Context) (StreamStatus, error) {
	js, err := a.jetStream()
	if err != nil {
		return StreamStatus{}, err
	}
	st := StreamStatus{Stream: a.cfg.Stream}
	stream, err := js.Stream(ctx, a.cfg.Stream)
	if errors.Is(err, jetstream.ErrStreamNotFound) {
		return st, nil
	}
	if err != nil {
		return StreamStatus{}, errors.Wrapf(err, "queue: stream %s", a.cfg.Stream)
	}
	jobs := a.cfg.Stream + ".job."
	info, err := stream.Info(ctx, jetstream.WithSubjectFilter(jobs+">"))
	if err != nil {
		return StreamStatus{}, errors.Wrapf(err, "queue: stream %s", a.cfg.Stream)
	}
	st.Exists = true
	st.Messages, st.Bytes, st.Replicas = info.State.Msgs, info.State.Bytes, info.Config.Replicas
	if info.Cluster != nil && len(info.Cluster.Replicas) > 0 {
		st.Leader = info.Cluster.Leader
	}
	// 每个定时消息一个 subject（见 publishAsync），其余的 subject 是各个 topic。
	st.Deferred = max(int(info.State.NumSubjects)-len(info.State.Subjects), 0)

	topics := map[string]*TopicStatus{}
	for subject := range info.State.Subjects {
		topic := strings.TrimPrefix(subject, jobs)
		topics[topic] = &TopicStatus{Topic: topic}
	}
	consumers := stream.ListConsumers(ctx)
	for c := range consumers.Info() {
		topic, ok := strings.CutPrefix(c.Config.FilterSubject, jobs)
		if !ok {
			continue // 不是队列建的 consumer
		}
		t := &TopicStatus{Topic: topic, Handled: true, Pending: c.NumPending, InFlight: c.NumAckPending,
			Redelivered: c.NumRedelivered, Waiting: c.NumWaiting}
		if c.Delivered.Last != nil {
			t.LastDelivered = *c.Delivered.Last
		}
		if c.Paused && c.Config.PauseUntil != nil {
			t.PausedUntil = *c.Config.PauseUntil
		}
		topics[topic] = t
	}
	if err := consumers.Err(); err != nil {
		return StreamStatus{}, errors.Wrapf(err, "queue: consumers of stream %s", a.cfg.Stream)
	}
	for topic, t := range topics {
		if !t.Handled {
			t.Pending = info.State.Subjects[jobs+topic]
		}
		st.Topics = append(st.Topics, *t)
	}
	slices.SortFunc(st.Topics, func(x, y TopicStatus) int { return strings.Compare(x.Topic, y.Topic) })
	return st, nil
}

// Pause 暂停 topic 的处理，直到 until：任务照常发布、保存，Worker 不再领取新的任务，处理中的任务不受影响。
// 下游服务故障时用它，免得任务大量失败。Worker 重启后仍然暂停。没有这个 topic 的 consumer 时返回 ErrTopicNotFound。
func (a *Admin) Pause(ctx context.Context, topic string, until time.Time) error {
	if !validName(topic) {
		return ErrTopicNotFound
	}
	js, err := a.jetStream()
	if err != nil {
		return err
	}
	_, err = js.PauseConsumer(ctx, a.cfg.Stream, consumerName(topic), until)
	return topicError(err, "pause", topic)
}

// Resume 恢复暂停的 topic，没有暂停时什么也不做。没有这个 topic 的 consumer 时返回 ErrTopicNotFound。
func (a *Admin) Resume(ctx context.Context, topic string) error {
	if !validName(topic) {
		return ErrTopicNotFound
	}
	js, err := a.jetStream()
	if err != nil {
		return err
	}
	_, err = js.ResumeConsumer(ctx, a.cfg.Stream, consumerName(topic))
	return topicError(err, "resume", topic)
}

func topicError(err error, op, topic string) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, jetstream.ErrConsumerNotFound), errors.Is(err, jetstream.ErrStreamNotFound):
		return ErrTopicNotFound
	}
	return errors.Wrapf(err, "queue: %s %q", op, topic)
}
