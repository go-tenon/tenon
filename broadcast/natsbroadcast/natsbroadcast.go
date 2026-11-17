// Package natsbroadcast 是基于 NATS 发布订阅的 broadcast.Backend，只用 NATS 的核心功能，不需要 JetStream。
//
//	hub := broadcast.New(natsbroadcast.New(nc, "blog.broadcast"), nil)
package natsbroadcast

import (
	"context"
	"crypto/rand"
	"fmt"
	"strings"
	"time"

	"github.com/cockroachdb/errors"
	"github.com/nats-io/nats.go"

	"github.com/go-tenon/tenon/broadcast"
)

var _ broadcast.Backend = (*Backend)(nil)

// Backend 把主题 topic 的消息发布到 subject <prefix>.<topic>，每个实例订阅 <prefix>.> 后在本地按主题分发。
// 主题的 '.' 是 subject 的分隔符，broadcast.ValidateTopic 保证主题可以直接作为 subject 的一部分。
//
// NATS 发布订阅只转发在线的消息：连接中断期间其他实例发布的消息会丢失；订阅跟不上时，NATS 客户端丢弃消息。
// 这些情况 Backend 都报告丢失（连接断开时和重新连上时各报告一次），Hub 随之以 broadcast.ErrLost
// 结束全部订阅，客户端重连后由应用补发。
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

// chunkHeader 标记分片发送的消息的一片："<消息 ID> <序号> <总片数>"，序号从 0 开始。
const chunkHeader = "Tenon-Chunk"

// chunkHeadroom 是每一片给 subject 和消息头留出的字节数。
const chunkHeadroom = 4 << 10

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

// Listen 实现 broadcast.Backend：订阅 <prefix>.>，连接断开、重新连上和订阅跟不上时报告丢失。
// 断开期间 NATS 客户端自己重连并恢复订阅；订阅本身出错（例如连接已经关闭）时报告丢失，稍后重新订阅。
func (b *Backend) Listen(ctx context.Context, deliver func(broadcast.Message), lost func(error)) {
	status := b.nc.StatusChanged(nats.RECONNECTING, nats.DISCONNECTED, nats.CONNECTED, nats.CLOSED)
	defer b.nc.RemoveStatusListener(status)
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case s, ok := <-status:
				if !ok {
					return
				}
				if s == nats.CONNECTED {
					lost(errors.New("natsbroadcast: reconnected; messages published while disconnected were lost"))
				} else {
					lost(errors.Newf("natsbroadcast: connection %s", s))
				}
			}
		}
	}()

	backoff := 100 * time.Millisecond
	for {
		subscribed, err := b.listen(ctx, deliver, lost)
		if ctx.Err() != nil {
			return
		}
		lost(err)
		if subscribed {
			backoff = 100 * time.Millisecond
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		backoff = min(backoff*2, 5*time.Second)
	}
}

// listen 订阅并接收消息，直到订阅出错或 ctx 结束。subscribed 报告是否曾订阅成功。
func (b *Backend) listen(ctx context.Context, deliver func(broadcast.Message), lost func(error)) (subscribed bool, err error) {
	sub, err := b.nc.SubscribeSync(b.prefix + ".>")
	if err != nil {
		return false, errors.Wrap(err, "natsbroadcast: subscribe")
	}
	defer func() { _ = sub.Unsubscribe() }()
	chunks := assembler{}
	for {
		msg, err := sub.NextMsgWithContext(ctx)
		switch {
		case err == nil:
			data, ok := chunks.add(msg, time.Now())
			if ok {
				deliver(broadcast.Message{Topic: msg.Subject[len(b.prefix)+1:], Data: data})
			}
		case errors.Is(err, nats.ErrSlowConsumer):
			// 客户端的缓冲区满了，丢弃过消息；订阅仍然有效。
			lost(errors.Wrap(err, "natsbroadcast"))
		default:
			return true, errors.Wrap(err, "natsbroadcast: receive")
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
