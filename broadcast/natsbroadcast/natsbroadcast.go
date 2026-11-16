// Package natsbroadcast 是基于 NATS 发布订阅的 broadcast.Backend，只用 NATS 的核心功能，不需要 JetStream。
//
//	hub := broadcast.New(natsbroadcast.New(nc, "blog.broadcast"), nil)
package natsbroadcast

import (
	"context"
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
// nc 由应用创建，可以与其他组件共用。它应当一直重连（nats.MaxReconnects(-1)），否则重连次数用完后
// 广播就停止了；也不能使用 nats.NoEcho，发布者所在的实例也要收到自己发布的消息。
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

// Publish 实现 broadcast.Backend。消息由连接在后台发送；连接中断期间先缓存在客户端，重新连上后发出。
// 消息的大小受服务器的 max_payload 限制，默认 1MB。
func (b *Backend) Publish(_ context.Context, msg broadcast.Message) error {
	return errors.Wrap(b.nc.Publish(b.prefix+"."+msg.Topic, msg.Data), "natsbroadcast: publish")
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
	for {
		msg, err := sub.NextMsgWithContext(ctx)
		switch {
		case err == nil:
			deliver(broadcast.Message{Topic: msg.Subject[len(b.prefix)+1:], Data: msg.Data})
		case errors.Is(err, nats.ErrSlowConsumer):
			// 客户端的缓冲区满了，丢弃过消息；订阅仍然有效。
			lost(errors.Wrap(err, "natsbroadcast"))
		default:
			return true, errors.Wrap(err, "natsbroadcast: receive")
		}
	}
}
