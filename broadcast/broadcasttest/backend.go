package broadcasttest

import (
	"context"
	"sync"

	"github.com/go-tenon/tenon/broadcast"
)

// Backend 是测试用的内存 broadcast.Backend：同一个 Backend 上的多个 Hub 各自维护独立的主题订阅，
// 互相只投递已订阅主题的消息，相当于按主题精确路由的多个实例。
//
// Publish 在返回之前把消息同步交给订阅了该主题的所有会话。Backend 只用于测试；
// 生产环境中，不停机升级和多实例部署时同时运行的进程需要共用 NATS 后端（broadcast/natsbroadcast）。
type Backend struct {
	mu       sync.Mutex
	sessions map[*memSession]struct{}
}

var _ broadcast.Backend = (*Backend)(nil)

// NewBackend 创建内存 Backend。
func NewBackend() *Backend {
	return &Backend{sessions: map[*memSession]struct{}{}}
}

// Publish 实现 broadcast.Backend：把 msg 投递给订阅了 msg.Topic 的每个会话。
func (b *Backend) Publish(_ context.Context, msg broadcast.Message) error {
	b.mu.Lock()
	var targets []*memSession
	for s := range b.sessions {
		if _, ok := s.topics[msg.Topic]; ok {
			targets = append(targets, s)
		}
	}
	b.mu.Unlock()
	for _, s := range targets {
		s.deliver(msg)
	}
	return nil
}

// Open 实现 broadcast.Backend：创建一个独立的内存接收会话。
func (b *Backend) Open(deliver func(broadcast.Message), _ func(error)) broadcast.Session {
	s := &memSession{
		backend: b,
		deliver: deliver,
		topics:  map[string]struct{}{},
	}
	b.mu.Lock()
	b.sessions[s] = struct{}{}
	b.mu.Unlock()
	return s
}

// memSession 是内存 Backend 的一个接收会话。
type memSession struct {
	backend *Backend
	deliver func(broadcast.Message)
	topics  map[string]struct{} // 由 backend.mu 保护
}

// Subscribe 实现 broadcast.Session。
func (s *memSession) Subscribe(topics ...string) error {
	s.backend.mu.Lock()
	for _, topic := range topics {
		s.topics[topic] = struct{}{}
	}
	s.backend.mu.Unlock()
	return nil
}

// Unsubscribe 实现 broadcast.Session。
func (s *memSession) Unsubscribe(topics ...string) error {
	s.backend.mu.Lock()
	for _, topic := range topics {
		delete(s.topics, topic)
	}
	s.backend.mu.Unlock()
	return nil
}

// Close 实现 broadcast.Session。
func (s *memSession) Close() error {
	s.backend.mu.Lock()
	delete(s.backend.sessions, s)
	clear(s.topics)
	s.backend.mu.Unlock()
	return nil
}
