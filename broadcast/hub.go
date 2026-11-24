package broadcast

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync"

	"github.com/cockroachdb/errors"

	"github.com/go-tenon/tenon/logging"
)

// bufferSize 是每个订阅的缓冲区大小，满了就以 ErrSlow 结束订阅。
const bufferSize = 64

// Hub 是进程内的广播中心，可以并发使用。每个进程创建一个，所有长连接共用。
// Hub 按需向 Backend 订阅本实例有订阅者的主题，所有本地订阅者离开后自动退订。
type Hub struct {
	backend Backend
	session Session
	logger  *slog.Logger

	mu         sync.Mutex // 保护 topics、recvTopics、receivers 和 closed
	topics     map[string]map[*Subscription]struct{}
	recvTopics map[string]map[*Receiver]struct{}
	receivers  map[*Receiver]struct{}
	closed     bool

	subMu  sync.Mutex          // 串行化对 session.Subscribe / Unsubscribe 的调用，避免并发乱序
	active map[string]struct{} // 已在 session 中订阅的主题集合，由 subMu 保护

	cleanMu    sync.Mutex // 保护异步退订队列
	cleanQueue []string
	cleanWake  chan struct{}
	cleanStop  chan struct{}
	cleanDone  chan struct{}
}

// Option 是 New 的选项。
type Option func(*Hub)

// WithLogger 指定记录 Backend 丢失消息和退订失败的日志器，默认使用 slog.Default()。
func WithLogger(logger *slog.Logger) Option {
	return func(h *Hub) {
		if logger != nil {
			h.logger = logger.With(logging.AttrComponent, "broadcast")
		}
	}
}

// New 创建 Hub 并打开 Backend 会话，直到 Close。默认使用 slog.Default() 记录日志，可用 WithLogger 替换。
// backend 为 nil 时 panic。
func New(backend Backend, opts ...Option) *Hub {
	if backend == nil {
		panic("broadcast: nil backend")
	}
	h := &Hub{
		backend:    backend,
		logger:     logging.Or(nil).With(logging.AttrComponent, "broadcast"),
		topics:     map[string]map[*Subscription]struct{}{},
		recvTopics: map[string]map[*Receiver]struct{}{},
		receivers:  map[*Receiver]struct{}{},
		active:     map[string]struct{}{},
		cleanWake:  make(chan struct{}, 1),
		cleanStop:  make(chan struct{}),
		cleanDone:  make(chan struct{}),
	}
	for _, opt := range opts {
		if opt != nil {
			opt(h)
		}
	}
	h.session = backend.Open(h.deliver, h.lost)
	go h.cleanLoop()
	return h
}

// Publish 把 data 作为 topic 主题的消息发给订阅了它的所有实例。topic 不合法时返回错误，见 ValidateTopic。
// 它不参与数据库事务：需要在业务数据提交之后才通知时，在提交之后调用。
func (h *Hub) Publish(ctx context.Context, topic string, data []byte) error {
	if err := ValidateTopic(topic); err != nil {
		return err
	}
	return errors.Wrap(h.backend.Publish(ctx, Message{Topic: topic, Data: data}), "broadcast: publish")
}

// PublishJSON 把 v 编码为 JSON 后发布。
func (h *Hub) PublishJSON(ctx context.Context, topic string, v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return errors.Wrap(err, "broadcast: encode")
	}
	return h.Publish(ctx, topic, data)
}

// Subscribe 订阅 topics 中的全部主题。主题不合法时 panic。
// Hub 已关闭时返回一个以 ErrClosed 结束的订阅；向后端登记订阅失败时返回以 ErrLost 结束的订阅。
func (h *Hub) Subscribe(topics ...string) *Subscription {
	if len(topics) == 0 {
		panic("broadcast: Subscribe needs at least one topic")
	}
	for _, topic := range topics {
		if err := ValidateTopic(topic); err != nil {
			panic(err)
		}
	}
	s := &Subscription{hub: h, topics: topics, ch: make(chan Message, bufferSize)}
	h.mu.Lock()
	if h.closed {
		s.end(ErrClosed)
		h.mu.Unlock()
		return s
	}
	for _, topic := range topics {
		subs := h.topics[topic]
		if subs == nil {
			subs = map[*Subscription]struct{}{}
			h.topics[topic] = subs
		}
		subs[s] = struct{}{}
	}
	h.mu.Unlock()

	if err := h.syncTopics(topics...); err != nil {
		h.logger.Warn("broadcast subscribe failed", "topics", topics, "error", err)
		h.mu.Lock()
		if s.err == nil {
			h.removeSub(s)
			s.end(errors.WithSecondaryError(ErrLost, err))
		}
		h.mu.Unlock()
		h.enqueueCleanup(topics...)
	}
	return s
}

// Receiver 创建一个按需管理主题的回调接收器，供需要自行按主题分发的组件（例如 realtime、Credentials）使用；
// 业务代码用 Subscribe。
//
// deliver 和 lost 按到达顺序调用，必须立即返回：慢的回调会拖住本实例的全部订阅。
// 回调中要发布或修改订阅时交给另一个 goroutine。lost 可以为 nil。
func (h *Hub) Receiver(deliver func(Message), lost func(error)) *Receiver {
	if deliver == nil {
		panic("broadcast: nil deliver")
	}
	r := &Receiver{
		hub:     h,
		deliver: deliver,
		lost:    lost,
		topics:  map[string]struct{}{},
	}
	h.mu.Lock()
	if !h.closed {
		h.receivers[r] = struct{}{}
	} else {
		r.closed = true
	}
	h.mu.Unlock()
	return r
}

// Close 停止接收消息，关闭后端会话，并以 ErrClosed 结束全部订阅。之后的 Publish 仍会交给 Backend。
func (h *Hub) Close() error {
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return nil
	}
	h.closed = true
	h.endAll(ErrClosed)
	for r := range h.receivers {
		r.closed = true
		clear(r.topics)
	}
	clear(h.receivers)
	clear(h.recvTopics)
	h.mu.Unlock()

	close(h.cleanStop)
	<-h.cleanDone
	return errors.Wrap(h.session.Close(), "broadcast: close")
}

// wantTopic 报告 topic 当前是否有任何本地订阅者。调用方持有 h.mu。
func (h *Hub) wantTopic(topic string) bool {
	return len(h.topics[topic]) > 0 || len(h.recvTopics[topic]) > 0
}

// syncTopics 把 topics 的内存订阅状态水平同步到后端会话：有订阅者且未订阅时向后端订阅，
// 无订阅者且已订阅时向后端退订。
func (h *Hub) syncTopics(topics ...string) error {
	if len(topics) == 0 {
		return nil
	}
	h.subMu.Lock()
	defer h.subMu.Unlock()

	var toSub, toUnsub []string
	seen := make(map[string]struct{}, len(topics))
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return nil
	}
	for _, topic := range topics {
		if _, dup := seen[topic]; dup {
			continue
		}
		seen[topic] = struct{}{}
		want := h.wantTopic(topic)
		_, active := h.active[topic]
		switch {
		case want && !active:
			toSub = append(toSub, topic)
		case !want && active:
			toUnsub = append(toUnsub, topic)
		}
	}
	h.mu.Unlock()

	if len(toSub) > 0 {
		if err := h.session.Subscribe(toSub...); err != nil {
			return errors.Wrap(err, "broadcast: subscribe")
		}
		for _, topic := range toSub {
			h.active[topic] = struct{}{}
		}
	}
	if len(toUnsub) > 0 {
		if err := h.session.Unsubscribe(toUnsub...); err != nil {
			return errors.Wrap(err, "broadcast: unsubscribe")
		}
		for _, topic := range toUnsub {
			delete(h.active, topic)
		}
	}
	return nil
}

// enqueueCleanup 把需要检查退订的主题放进后台队列，避免在 deliver 或 lost 回调中阻塞网络操作。
func (h *Hub) enqueueCleanup(topics ...string) {
	if len(topics) == 0 {
		return
	}
	h.cleanMu.Lock()
	h.cleanQueue = append(h.cleanQueue, topics...)
	h.cleanMu.Unlock()
	select {
	case h.cleanWake <- struct{}{}:
	default:
	}
}

// cleanLoop 在后台处理异步退订请求，直到 Close。
func (h *Hub) cleanLoop() {
	defer close(h.cleanDone)
	for {
		select {
		case <-h.cleanStop:
			return
		case <-h.cleanWake:
		}
		for {
			h.cleanMu.Lock()
			topics := h.cleanQueue
			h.cleanQueue = nil
			h.cleanMu.Unlock()
			if len(topics) == 0 {
				break
			}
			if err := h.syncTopics(topics...); err != nil {
				h.logger.Warn("broadcast unsubscribe cleanup failed", "error", err)
			}
		}
	}
}

// deliver 把消息交给订阅了它的主题的每个 Subscription 和 Receiver。
func (h *Hub) deliver(msg Message) {
	h.mu.Lock()
	var dropped []string
	for s := range h.topics[msg.Topic] {
		select {
		case s.ch <- msg:
		default:
			dropped = append(dropped, h.removeSub(s)...)
			s.end(ErrSlow)
		}
	}
	var recvs []*Receiver
	if set := h.recvTopics[msg.Topic]; len(set) > 0 {
		recvs = make([]*Receiver, 0, len(set))
		for r := range set {
			recvs = append(recvs, r)
		}
	}
	h.mu.Unlock()

	if len(dropped) > 0 {
		h.enqueueCleanup(dropped...)
	}
	for _, r := range recvs {
		r.deliver(msg)
	}
}

// lost 在后端可能丢了消息时调用：记录 Warn 日志，结束全部 Subscription，并通知全部 Receiver。
func (h *Hub) lost(err error) {
	h.logger.Warn("broadcast messages may have been lost; ending subscriptions", "error", err)
	h.mu.Lock()
	orphaned := h.endAll(errors.WithSecondaryError(ErrLost, err))
	recvs := make([]*Receiver, 0, len(h.receivers))
	for r := range h.receivers {
		if r.lost != nil {
			recvs = append(recvs, r)
		}
	}
	h.mu.Unlock()

	if len(orphaned) > 0 {
		h.enqueueCleanup(orphaned...)
	}
	for _, r := range recvs {
		r.lost(err)
	}
}

// endAll 以 err 结束全部 Subscription，返回不再有任何订阅者的主题列表。调用方持有 h.mu。
func (h *Hub) endAll(err error) []string {
	var orphaned []string
	for topic, subs := range h.topics {
		for s := range subs {
			s.end(err)
		}
		delete(h.topics, topic)
		if !h.wantTopic(topic) {
			orphaned = append(orphaned, topic)
		}
	}
	return orphaned
}

// removeSub 把 s 从全部主题中移除，返回已无订阅者的主题列表。调用方持有 h.mu。
func (h *Hub) removeSub(s *Subscription) []string {
	var orphaned []string
	for _, topic := range s.topics {
		delete(h.topics[topic], s)
		if len(h.topics[topic]) == 0 {
			delete(h.topics, topic)
			if !h.wantTopic(topic) {
				orphaned = append(orphaned, topic)
			}
		}
	}
	return orphaned
}

// Subscription 是一个订阅。消息从 C 读取；订阅结束时 C 被关闭，Err 返回原因。
type Subscription struct {
	hub    *Hub
	topics []string
	ch     chan Message
	err    error // 由 hub.mu 保护
}

// C 返回接收消息的通道。订阅结束时它被关闭，缓冲区中尚未读取的消息仍可读出。
func (s *Subscription) C() <-chan Message { return s.ch }

// Err 返回订阅结束的原因：ErrClosed、ErrSlow 或 ErrLost；订阅仍在进行时为 nil。
func (s *Subscription) Err() error {
	s.hub.mu.Lock()
	defer s.hub.mu.Unlock()
	return s.err
}

// Close 结束订阅，并在没有其他订阅者时向后端退订。可以重复调用。
func (s *Subscription) Close() {
	s.hub.mu.Lock()
	var orphaned []string
	if s.err == nil {
		orphaned = s.hub.removeSub(s)
		s.end(ErrClosed)
	}
	s.hub.mu.Unlock()
	if len(orphaned) > 0 {
		s.hub.enqueueCleanup(orphaned...)
	}
}

// end 记录原因并关闭通道。调用方持有 hub.mu。
func (s *Subscription) end(err error) {
	if s.err != nil {
		return
	}
	s.err = err
	close(s.ch)
}

// Receiver 是按需管理主题集合的回调接收器。可以并发使用。
type Receiver struct {
	hub     *Hub
	deliver func(Message)
	lost    func(error)

	// 以下字段由 hub.mu 保护。
	topics map[string]struct{}
	closed bool
}

// Subscribe 为接收器增加 topics 主题，并同步向后端登记。
// 主题不合法时返回错误；接收器或 Hub 已关闭时返回 ErrClosed。
func (r *Receiver) Subscribe(topics ...string) error {
	if len(topics) == 0 {
		return nil
	}
	for _, topic := range topics {
		if err := ValidateTopic(topic); err != nil {
			return err
		}
	}
	var added []string
	r.hub.mu.Lock()
	if r.closed || r.hub.closed {
		r.hub.mu.Unlock()
		return ErrClosed
	}
	for _, topic := range topics {
		if _, ok := r.topics[topic]; ok {
			continue
		}
		r.topics[topic] = struct{}{}
		set := r.hub.recvTopics[topic]
		if set == nil {
			set = map[*Receiver]struct{}{}
			r.hub.recvTopics[topic] = set
		}
		set[r] = struct{}{}
		added = append(added, topic)
	}
	r.hub.mu.Unlock()
	if len(added) == 0 {
		return nil
	}
	if err := r.hub.syncTopics(added...); err != nil {
		r.hub.mu.Lock()
		for _, topic := range added {
			delete(r.topics, topic)
			delete(r.hub.recvTopics[topic], r)
			if len(r.hub.recvTopics[topic]) == 0 {
				delete(r.hub.recvTopics, topic)
			}
		}
		r.hub.mu.Unlock()
		r.hub.enqueueCleanup(added...)
		return err
	}
	return nil
}

// Unsubscribe 从接收器移除 topics 主题，并在没有其他订阅者时向后端退订。
func (r *Receiver) Unsubscribe(topics ...string) error {
	if len(topics) == 0 {
		return nil
	}
	var removed []string
	r.hub.mu.Lock()
	for _, topic := range topics {
		if _, ok := r.topics[topic]; !ok {
			continue
		}
		delete(r.topics, topic)
		delete(r.hub.recvTopics[topic], r)
		if len(r.hub.recvTopics[topic]) == 0 {
			delete(r.hub.recvTopics, topic)
		}
		removed = append(removed, topic)
	}
	r.hub.mu.Unlock()
	if len(removed) == 0 {
		return nil
	}
	return r.hub.syncTopics(removed...)
}

// Close 关闭接收器并退订它的全部主题。可以重复调用。
func (r *Receiver) Close() {
	r.hub.mu.Lock()
	if r.closed {
		r.hub.mu.Unlock()
		return
	}
	r.closed = true
	delete(r.hub.receivers, r)
	removed := make([]string, 0, len(r.topics))
	for topic := range r.topics {
		delete(r.hub.recvTopics[topic], r)
		if len(r.hub.recvTopics[topic]) == 0 {
			delete(r.hub.recvTopics, topic)
		}
		removed = append(removed, topic)
	}
	clear(r.topics)
	r.hub.mu.Unlock()
	if len(removed) > 0 {
		_ = r.hub.syncTopics(removed...)
	}
}
