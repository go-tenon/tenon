package queue

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/cockroachdb/errors"
	"github.com/google/uuid"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

const (
	// publishTimeout 是等待 JetStream 确认一条发布的最长时间。
	publishTimeout = 10 * time.Second
	// setupTimeout 是启动时创建 stream 和 consumer 的超时。
	setupTimeout = 30 * time.Second
	// duplicateWindow 是 stream 按消息 ID 去重的时间窗口：转发成功但删除 outbox 提交之前中断时，
	// 重新转发同一条任务在窗口内被 JetStream 丢弃。
	duplicateWindow = 10 * time.Minute
)

// streamConfig 返回任务的 stream 的设置：工作队列（任务确认后删除）、存在磁盘上。
func (c Config) streamConfig() jetstream.StreamConfig {
	return jetstream.StreamConfig{
		Name:        c.Stream,
		Description: "tenon queue jobs",
		Subjects:    []string{c.Stream + ".>"},
		Retention:   jetstream.WorkQueuePolicy,
		Storage:     jetstream.FileStorage,
		Replicas:    c.Replicas,
		Duplicates:  duplicateWindow,
	}
}

// ensureStream 创建任务的 stream。已经存在时检查保留策略、subject、存储方式和容量限制（见 checkLimits），
// 副本数和去重窗口与配置不同时更新它；运维调整过的其他设置（例如容量上限）保持不变。
func ensureStream(ctx context.Context, js jetstream.JetStream, cfg Config) error {
	want := cfg.streamConfig()
	_, err := js.CreateStream(ctx, want)
	if !errors.Is(err, jetstream.ErrStreamNameAlreadyInUse) {
		return errors.Wrapf(err, "queue: create stream %s", cfg.Stream)
	}
	s, err := js.Stream(ctx, cfg.Stream)
	if err != nil {
		return errors.Wrapf(err, "queue: stream %s", cfg.Stream)
	}
	have := s.CachedInfo().Config
	if have.Retention != want.Retention || have.Storage != want.Storage || !slices.Equal(have.Subjects, want.Subjects) {
		return errors.Newf("queue: stream %s exists with retention %s, storage %s and subjects %v; "+
			"want %s, %s and %v: use another stream name", cfg.Stream, have.Retention, have.Storage, have.Subjects,
			want.Retention, want.Storage, want.Subjects)
	}
	if err := checkLimits(cfg.Stream, have); err != nil {
		return err
	}
	if have.Replicas == want.Replicas && have.Duplicates >= want.Duplicates {
		return nil
	}
	have.Replicas, have.Duplicates = want.Replicas, max(have.Duplicates, want.Duplicates)
	_, err = js.UpdateStream(ctx, have)
	return errors.Wrapf(err, "queue: update stream %s", cfg.Stream)
}

// checkLimits 拒绝会删掉还没处理的任务的 stream 设置：max_age 会在任务处理前删除它，
// 容量满时丢弃最早的消息（discard old）删除的也是任务。
// 容量上限要配合 discard new：满了之后发布失败，任务留在 queue_outbox 中，有空间后再转发。
func checkLimits(stream string, c jetstream.StreamConfig) error {
	if c.MaxAge > 0 {
		return errors.Newf("queue: stream %s has max_age %s, which deletes pending jobs before they run: remove the limit", stream, c.MaxAge)
	}
	if c.Discard == jetstream.DiscardOld && (c.MaxMsgs > 0 || c.MaxBytes > 0 || c.MaxMsgsPerSubject > 0) {
		return errors.Newf("queue: stream %s discards old messages when full, which deletes jobs: set discard to new", stream)
	}
	return nil
}

// ensureConsumer 创建或更新 topic 的 consumer。同一个应用的所有 Worker 共用它，每个任务只交给其中一个。
func (w *Worker) ensureConsumer(ctx context.Context, h *handler) (jetstream.Consumer, error) {
	name, subject := consumerName(h.topic), w.jobSubject(h.topic)
	if c, err := w.js.Consumer(ctx, w.cfg.Stream, name); err == nil {
		if have := c.CachedInfo().Config.FilterSubject; have != subject {
			return nil, errors.Newf("queue: consumer %s of stream %s consumes %s, not %s: topic names differing only in '.' and '_' conflict",
				name, w.cfg.Stream, have, subject)
		}
	} else if !errors.Is(err, jetstream.ErrConsumerNotFound) {
		return nil, errors.Wrapf(err, "queue: consumer for %q", h.topic)
	}
	c, err := w.js.CreateOrUpdateConsumer(ctx, w.cfg.Stream, jetstream.ConsumerConfig{
		Durable:       name,
		Description:   "tenon queue topic " + h.topic,
		FilterSubject: subject,
		AckPolicy:     jetstream.AckExplicitPolicy,
		AckWait:       w.cfg.Lease,
		MaxDeliver:    -1, // 次数由 Worker 判断，超过后转入失败任务表
		MaxAckPending: -1, // 同时处理的任务数由各个 Worker 的并发数限制
	})
	return c, errors.Wrapf(err, "queue: consumer for %q", h.topic)
}

// consumerName 返回 topic 的 consumer 名：consumer 名不能包含 '.'，换成 '_'。
func consumerName(topic string) string { return strings.ReplaceAll(topic, ".", "_") }

// jobSubject 返回 topic 的任务发布到的 subject。
func (w *Worker) jobSubject(topic string) string { return w.cfg.Stream + ".job." + topic }

// msgID 是一次转发的消息 ID：由任务 ID 和该次入库时间组成。
// 同一条 outbox 行在删除提交前中断重发时 ID 相同，被 JetStream 去重；
// 从失败任务表重新发布时写入新的 outbox 行，CreatedAt 不同，不会被去重窗口误丢弃。
func msgID(id uuid.UUID, createdAt time.Time) string {
	return fmt.Sprintf("%s-%d", id, createdAt.UnixNano())
}

// publishAsync 把任务 body 异步发布到 topic 对应的 subject。
func (w *Worker) publishAsync(topic string, body []byte, id string) (jetstream.PubAckFuture, error) {
	msg := &nats.Msg{Subject: w.jobSubject(topic), Data: body}
	return w.js.PublishMsgAsync(msg, jetstream.WithMsgID(id))
}
