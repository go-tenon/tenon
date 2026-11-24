package broadcast

import (
	"context"
	"strings"

	"github.com/cockroachdb/errors"
)

// Message 是一条广播消息。Data 通常是一个小的 JSON 领域事件，由每个订阅者自己渲染。
type Message struct {
	Topic string
	Data  []byte
}

// Backend 在实例之间传递消息，按主题只把消息送给订阅了该主题的实例。实现必须可以并发使用。
type Backend interface {
	// Publish 把消息发给订阅了 msg.Topic 的所有实例，包括发布者所在的实例。
	Publish(ctx context.Context, msg Message) error
	// Open 为一个 Hub 打开接收会话：把本会话已订阅主题的消息按发布顺序交给 deliver；
	// 可能丢失了消息时（例如与服务器的连接中断或客户端缓冲区溢出）调用 lost，然后继续接收。
	// deliver 和 lost 必须立即返回，可以在任意 goroutine 中调用。
	Open(deliver func(Message), lost func(error)) Session
}

// Session 是一个 Hub 在 Backend 上的接收会话，按需管理本实例在后端的主题订阅。
// 实现必须可以并发使用。
type Session interface {
	// Subscribe 让本会话开始接收 topics 中主题的消息，返回时订阅已在后端生效。
	Subscribe(topics ...string) error
	// Unsubscribe 让本会话停止接收 topics 中主题的消息。
	Unsubscribe(topics ...string) error
	// Close 结束会话并停止接收，返回后不再调用 deliver 和 lost。
	Close() error
}

var (
	// ErrClosed 表示订阅被 Close 或 Hub.Close 结束。
	ErrClosed = errors.New("broadcast: subscription closed")
	// ErrSlow 表示订阅的缓冲区已满：订阅者处理消息太慢，继续投递就要丢消息。
	ErrSlow = errors.New("broadcast: subscriber too slow")
	// ErrLost 表示 Backend 可能丢失了消息，例如与 NATS 的连接中断过。
	ErrLost = errors.New("broadcast: messages may have been lost")
)

// MaxTopicLength 是主题的最大字节数。
const MaxTopicLength = 200

// ValidateTopic 检查主题：非空，最长 MaxTopicLength 字节，只含字母、数字和 _ - : .，
// 且 . 的两侧都是其他字符。主题同时是实时频道的名称，这些规则让它可以直接出现在 URL 和日志中。
func ValidateTopic(topic string) error {
	if topic == "" || len(topic) > MaxTopicLength {
		return errors.Newf("broadcast: topic must be 1 to %d bytes", MaxTopicLength)
	}
	for _, c := range topic {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '_', c == '-', c == ':', c == '.':
		default:
			return errors.Newf("broadcast: topic %q contains %q", topic, c)
		}
	}
	if strings.HasPrefix(topic, ".") || strings.HasSuffix(topic, ".") || strings.Contains(topic, "..") {
		return errors.Newf("broadcast: topic %q has an empty segment", topic)
	}
	return nil
}
