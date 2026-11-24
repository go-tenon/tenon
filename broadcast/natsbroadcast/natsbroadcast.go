// Package natsbroadcast 是基于 NATS 发布订阅的 broadcast.Backend，只用 NATS 的核心功能，不需要 JetStream。
//
//	hub := broadcast.New(natsbroadcast.New(nc, "blog.broadcast"))
package natsbroadcast

import (
	"context"
	"crypto/rand"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/cockroachdb/errors"
	"github.com/nats-io/nats.go"

	"github.com/go-tenon/tenon/broadcast"
)

var _ broadcast.Backend = (*Backend)(nil)

// Backend 把主题 topic 的消息发布到 subject <prefix>.<topic>，每个实例只向 NATS 订阅本实例当前有订阅者的精确 subject。
// 主题的 '.' 是 subject 的分隔符，broadcast.ValidateTopic 保证主题可以直接作为 subject 的一部分。
//
// 同一会话下的全部 subject 共用一条有序接收通道，跨主题保持同一连接上的发布顺序。
// NATS 发布订阅只转发在线的消息：连接中断期间其他实例发布的消息会丢失；订阅跟不上时，NATS 客户端丢弃消息。
// 这些情况会话都报告丢失（连接断开时、重新连上时和检测到丢包时），Hub 随之以 broadcast.ErrLost
// 结束全部订阅并通知接收器，客户端重连后由应用补发。
type Backend struct {
	nc     *nats.Conn
	prefix string
}

// New 创建 NATS Backend。prefix 是 subject 的前缀，例如 blog.broadcast，同一个应用的实例必须相同，
// 多个应用或环境共用 NATS 时用它区分。
//
// nc 通常是 natsx.Connect 返回的连接，与队列等组件共用，Backend 不关闭它。自己创建连接时，它应当一直重连
// （nats.MaxReconnects(-1)），否则重连次数用完后广播就停止了；也不能使用 nats.NoEcho，
// 发布者所在的实例也要收到自己发布的消息。
// nc 为 nil，或者 prefix 不是不含通配符的 subject 时 panic。
func New(nc *nats.Conn, prefix string) *Backend {
	switch {
	case nc == nil:
		panic("natsbroadcast: nil connection")
	case !validPrefix(prefix):
		panic("natsbroadcast: prefix " + prefix + " must be a NATS subject without wildcards, e.g. app.broadcast")
	}
	return &Backend{nc: nc, prefix: prefix}
}

// validPrefix 报告 s 是不是由非空的段组成、不含空白和通配符的 subject。
func validPrefix(s string) bool {
	if s == "" {
		return false
	}
	for token := range strings.SplitSeq(s, ".") {
		if token == "" || strings.ContainsAny(token, " \t\r\n*>") {
			return false
		}
	}
	return true
}

const (
	// chunkHeader 标记分片发送的消息的一片："<消息 ID> <序号> <总片数>"，序号从 0 开始。
	chunkHeader = "Tenon-Chunk"
	// chunkHeadroom 是每一片给 subject 和消息头留出的字节数。
	chunkHeadroom = 4 << 10
	// flushTimeout 是 Subscribe 等待 NATS 服务器确认订阅生效的时限。
	flushTimeout = 5 * time.Second
	// recvBufferSize 是共享接收通道的容量，所有按需订阅的 subject 按到达顺序汇入同一通道。
	recvBufferSize = 4096
)

// Publish 实现 broadcast.Backend。消息由连接在后台发送；连接中断期间先缓存在客户端，重新连上后发出。
//
// 超过服务器 max_payload（默认 1MB）的消息分成多片依次发送，订阅方收齐后拼回原来的消息再交给 Hub，例如带附件的
// AI 对话转交给 worker。同一个连接发布的消息按顺序到达，所以各片不会乱序；缺了一片时整条消息丢弃。
func (b *Backend) Publish(_ context.Context, msg broadcast.Message) error {
	subject := b.prefix + "." + msg.Topic
	size := int(b.nc.MaxPayload()) - chunkHeadroom
	if len(msg.Data) <= size || size <= 0 {
		return errors.Wrap(b.nc.Publish(subject, msg.Data), "natsbroadcast: publish")
	}
	id := rand.Text()
	total := (len(msg.Data) + size - 1) / size
	for i := range total {
		part := nats.NewMsg(subject)
		part.Header.Set(chunkHeader, fmt.Sprintf("%s %d %d", id, i, total))
		part.Data = msg.Data[i*size : min((i+1)*size, len(msg.Data))]
		if err := b.nc.PublishMsg(part); err != nil {
			return errors.Wrap(err, "natsbroadcast: publish")
		}
	}
	return nil
}

// Open 实现 broadcast.Backend：创建按需订阅的 NATS 接收会话。
func (b *Backend) Open(deliver func(broadcast.Message), lost func(error)) broadcast.Session {
	s := &session{
		nc:      b.nc,
		prefix:  b.prefix,
		deliver: deliver,
		lost:    lost,
		ch:      make(chan *nats.Msg, recvBufferSize),
		done:    make(chan struct{}),
		subs:    map[string]*nats.Subscription{},
		dropped: map[*nats.Subscription]int{},
	}
	status := b.nc.StatusChanged(nats.RECONNECTING, nats.DISCONNECTED, nats.CONNECTED, nats.CLOSED)
	s.wg.Add(2)
	go s.readLoop()
	go s.statusLoop(status)
	return s
}

// session 是 NATS Backend 的按需接收会话。
type session struct {
	nc      *nats.Conn
	prefix  string
	deliver func(broadcast.Message)
	lost    func(error)

	ch   chan *nats.Msg
	done chan struct{}
	wg   sync.WaitGroup

	mu      sync.Mutex
	subs    map[string]*nats.Subscription
	dropped map[*nats.Subscription]int // 上次记录的丢包计数，用于检测慢消费者
	closed  bool
}

// Subscribe 为本会话登记 topics 对应的精确 subject 订阅，并在连接正常时等待 NATS 服务器确认。
func (s *session) Subscribe(topics ...string) error {
	var added []string
	var addedSubs []*nats.Subscription

	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return broadcast.ErrClosed
	}
	for _, topic := range topics {
		if _, ok := s.subs[topic]; ok {
			continue
		}
		sub, err := s.nc.ChanSubscribe(s.prefix+"."+topic, s.ch)
		if err != nil {
			for i, t := range added {
				_ = addedSubs[i].Unsubscribe()
				delete(s.subs, t)
				delete(s.dropped, addedSubs[i])
			}
			s.mu.Unlock()
			return errors.Wrap(err, "natsbroadcast: subscribe")
		}
		s.subs[topic] = sub
		s.dropped[sub] = 0
		added = append(added, topic)
		addedSubs = append(addedSubs, sub)
	}
	s.mu.Unlock()

	if len(added) > 0 && s.nc.Status() == nats.CONNECTED {
		if err := s.nc.FlushTimeout(flushTimeout); err != nil {
			s.mu.Lock()
			for i, t := range added {
				if s.subs[t] == addedSubs[i] {
					_ = addedSubs[i].Unsubscribe()
					delete(s.subs, t)
					delete(s.dropped, addedSubs[i])
				}
			}
			s.mu.Unlock()
			return errors.Wrap(err, "natsbroadcast: flush subscribe")
		}
	}
	return nil
}

// Unsubscribe 取消本会话对 topics 对应 subject 的订阅。
func (s *session) Unsubscribe(topics ...string) error {
	var removed []*nats.Subscription
	s.mu.Lock()
	for _, topic := range topics {
		if sub, ok := s.subs[topic]; ok {
			delete(s.subs, topic)
			delete(s.dropped, sub)
			removed = append(removed, sub)
		}
	}
	s.mu.Unlock()

	var firstErr error
	for _, sub := range removed {
		if err := sub.Unsubscribe(); err != nil && !errors.Is(err, nats.ErrConnectionClosed) && firstErr == nil {
			firstErr = errors.Wrap(err, "natsbroadcast: unsubscribe")
		}
	}
	return firstErr
}

// Close 取消全部订阅并停止接收，返回后不再调用 deliver 和 lost。
func (s *session) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	subs := make([]*nats.Subscription, 0, len(s.subs))
	for _, sub := range s.subs {
		subs = append(subs, sub)
	}
	clear(s.subs)
	clear(s.dropped)
	s.mu.Unlock()

	close(s.done)
	for _, sub := range subs {
		_ = sub.Unsubscribe()
	}
	s.wg.Wait()
	return nil
}

// readLoop 按到达顺序消费共享通道中的消息，拼合分片后交给 deliver。
func (s *session) readLoop() {
	defer s.wg.Done()
	chunks := assembler{}
	prefixLen := len(s.prefix) + 1
	for {
		select {
		case <-s.done:
			return
		case msg := <-s.ch:
			if s.checkDropped(msg.Sub) {
				s.lost(errors.Wrap(nats.ErrSlowConsumer, "natsbroadcast"))
			}
			if len(msg.Subject) <= prefixLen {
				continue
			}
			if data, ok := chunks.add(msg, time.Now()); ok {
				s.deliver(broadcast.Message{Topic: msg.Subject[prefixLen:], Data: data})
			}
		}
	}
}

// checkDropped 报告 sub 的丢包数是否自上次检查以来增加了。
func (s *session) checkDropped(sub *nats.Subscription) bool {
	if sub == nil {
		return false
	}
	n, err := sub.Dropped()
	if err != nil || n == 0 {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	prev, ok := s.dropped[sub]
	if !ok || n <= prev {
		return false
	}
	s.dropped[sub] = n
	return true
}

// statusLoop 监听 NATS 连接状态变化，断开或重连时报告可能丢消息。
func (s *session) statusLoop(status chan nats.Status) {
	defer s.wg.Done()
	defer s.nc.RemoveStatusListener(status)
	for {
		select {
		case <-s.done:
			return
		case st, ok := <-status:
			if !ok {
				return
			}
			if st == nats.CONNECTED {
				s.lost(errors.New("natsbroadcast: reconnected; messages published while disconnected were lost"))
			} else {
				s.lost(errors.Newf("natsbroadcast: connection %s", st))
			}
		}
	}
}

// 正在拼接的消息最多保留 chunkTTL，最多 maxPending 条：发布方中途停下、订阅晚于第一片开始时，残片不会一直占着内存。
const (
	chunkTTL   = 30 * time.Second
	maxPending = 16
)

// assembler 把分片发送的消息拼回原样。只在接收消息的协程中使用，不需要加锁。
type assembler map[string]*pending

// pending 是正在拼合的一条分片消息。
type pending struct {
	data    []byte
	next    int // 下一片的序号
	total   int
	started time.Time
}

// add 处理收到的一条消息：没有分片的直接返回；分片的收齐后返回整条消息，否则 ok 为 false。
func (a assembler) add(msg *nats.Msg, now time.Time) (data []byte, ok bool) {
	header := msg.Header.Get(chunkHeader)
	if header == "" {
		return msg.Data, true
	}
	var id string
	var i, total int
	if _, err := fmt.Sscanf(header, "%s %d %d", &id, &i, &total); err != nil || total <= 0 || i < 0 || i >= total {
		return nil, false
	}
	for k, p := range a {
		if now.Sub(p.started) > chunkTTL {
			delete(a, k)
		}
	}
	p := a[id]
	switch {
	case i == 0 && len(a) < maxPending:
		p = &pending{total: total, started: now}
		a[id] = p
	case p == nil || i != p.next || total != p.total:
		delete(a, id) // 没有收到第一片（订阅晚于发布开始）或者中间缺了一片：整条消息丢弃
		return nil, false
	}
	p.data = append(p.data, msg.Data...)
	p.next++
	if p.next < p.total {
		return nil, false
	}
	delete(a, id)
	return p.data, true
}
