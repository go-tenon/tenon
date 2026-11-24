package realtime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/centrifugal/centrifuge"

	"github.com/go-tenon/tenon/broadcast"
)

const (
	// controlAllTopic 是广播给所有 realtime 节点的控制消息主题。
	controlAllTopic = "rt.ctl.all"
	// controlNodePrefix 是发往指定 realtime 节点的控制消息主题前缀，后接 nodeID。
	controlNodePrefix = "rt.ctl."
	// channelEventPrefix 是实时频道内部事件（客户端发布、加入、离开）的主题前缀，后接频道名哈希。
	channelEventPrefix = "rt.e."
	// publishTimeout 是 bridge 向 Hub 发布一条消息的时限。
	publishTimeout = 5 * time.Second
)

const (
	kindPublish = "pub"
	kindJoin    = "join"
	kindLeave   = "leave"
)

// channelEvent 是 channelEventPrefix 主题上的频道内部事件：客户端发布和成员加入、离开。
type channelEvent struct {
	Kind    string                 `json:"k"`
	Channel string                 `json:"c"`
	Data    []byte                 `json:"d,omitempty"`
	Info    *centrifuge.ClientInfo `json:"i,omitempty"`
}

// channelEventTopic 返回频道 ch 对应的内部事件主题。固定 37 字节，不超出 MaxTopicLength。
func channelEventTopic(ch string) string {
	h := sha256.Sum256([]byte(ch))
	return channelEventPrefix + hex.EncodeToString(h[:16])
}

// isReservedTopic 报告模式或主题是否占用了 realtime 的内部主题前缀。
func isReservedTopic(s string) bool {
	return strings.HasPrefix(s, controlNodePrefix) || strings.HasPrefix(s, channelEventPrefix)
}

// bridge 用 broadcast.Hub 的按需接收器实现 centrifuge.Broker 和 centrifuge.Controller：
//
//   - 业务主题 ch 与内部事件主题 rt.e.<hash(ch)> 仅在有客户端订阅 ch 时向 Hub 订阅，最后一个订阅者离开时退订；
//   - 控制消息按 rt.ctl.all（全节点）和 rt.ctl.<nodeID>（目标节点）精确路由，直接传输原始字节；
//   - 业务代码通过 Hub.PublishJSON 发布到 ch 的消息直接投递给订阅了同名频道的客户端。
type bridge struct {
	hub       *broadcast.Hub
	node      *centrifuge.Node
	nodeTopic string
	recv      *broadcast.Receiver
	pubs      centrifuge.BrokerEventHandler
	ctl       centrifuge.ControlEventHandler
	logger    *slog.Logger // 记录转发失败的消息

	members *membership

	mu    sync.Mutex    // 保护 tasks
	tasks []func()      // 交给 run 依次执行的任务：控制消息和丢失消息的处理可能再向 Hub 发布或修改订阅，deliver 回调中禁止阻塞，所以排队处理并保持先后顺序
	wake  chan struct{} // 有新任务时唤醒 run
	done  chan struct{} // 关闭时 run 退出
	once  sync.Once     // 保证 done 只关闭一次
}

// start 登记节点控制主题并启动后台任务循环。在 node.Run 之后调用，确保 handler 已就绪。
func (b *bridge) start() error {
	b.recv = b.hub.Receiver(b.deliver, b.lost)
	if err := b.recv.Subscribe(controlAllTopic, b.nodeTopic); err != nil {
		b.recv.Close()
		return err
	}
	go b.run()
	return nil
}

// later 把 task 排到 run 中执行，不阻塞。
func (b *bridge) later(task func()) {
	b.mu.Lock()
	b.tasks = append(b.tasks, task)
	b.mu.Unlock()
	select {
	case b.wake <- struct{}{}:
	default:
	}
}

// run 依次执行 later 排入的任务，直到 stop。
func (b *bridge) run() {
	for {
		select {
		case <-b.done:
			return
		case <-b.wake:
		}
		for {
			b.mu.Lock()
			tasks := b.tasks
			b.tasks = nil
			b.mu.Unlock()
			if len(tasks) == 0 {
				break
			}
			for _, task := range tasks {
				task()
			}
		}
	}
}

// stop 结束接收器和后台循环，可以重复调用。
func (b *bridge) stop() {
	b.once.Do(func() {
		if b.recv != nil {
			b.recv.Close()
		}
		close(b.done)
	})
}

// RegisterBrokerEventHandler 记下 Centrifuge 处理发布、加入、离开的处理器。
func (b *bridge) RegisterBrokerEventHandler(h centrifuge.BrokerEventHandler) error {
	b.pubs = h
	return nil
}

// RegisterControlEventHandler 记下 Centrifuge 处理控制消息的处理器。
func (b *bridge) RegisterControlEventHandler(h centrifuge.ControlEventHandler) error {
	b.ctl = h
	return nil
}

// Subscribe 按需订阅 channels 对应的业务主题和内部事件主题。
func (b *bridge) Subscribe(channels ...string) error {
	if len(channels) == 0 {
		return nil
	}
	topics := make([]string, 0, len(channels)*2)
	for _, ch := range channels {
		topics = append(topics, ch, channelEventTopic(ch))
	}
	return b.recv.Subscribe(topics...)
}

// Unsubscribe 退订 channels 对应的业务主题和内部事件主题。
func (b *bridge) Unsubscribe(channels ...string) error {
	if len(channels) == 0 {
		return nil
	}
	topics := make([]string, 0, len(channels)*2)
	for _, ch := range channels {
		topics = append(topics, ch, channelEventTopic(ch))
	}
	return b.recv.Unsubscribe(topics...)
}

// Publish 通过 Hub 发布频道 ch 的消息；带客户端信息的发布经由该频道的内部事件主题转发。
func (b *bridge) Publish(ch string, data []byte, opts centrifuge.PublishOptions) (centrifuge.PublishResult, error) {
	ctx, cancel := context.WithTimeout(context.Background(), publishTimeout)
	defer cancel()
	if opts.ClientInfo == nil {
		return centrifuge.PublishResult{}, b.hub.Publish(ctx, ch, data)
	}
	// 客户端发布的消息带有发布者的信息，只发给订阅该频道的 WebSocket 客户端。
	return centrifuge.PublishResult{}, b.sendEvent(ctx, channelEvent{Kind: kindPublish, Channel: ch, Data: data, Info: opts.ClientInfo})
}

// PublishJoin 交给 membership 决定是否发出加入事件。
func (b *bridge) PublishJoin(ch string, info *centrifuge.ClientInfo) error {
	return b.members.join(ch, info)
}

// PublishLeave 交给 membership 决定是否发出离开事件及何时发出。
func (b *bridge) PublishLeave(ch string, info *centrifuge.ClientInfo) error {
	return b.members.leave(ch, info)
}

// PublishControl 通过 Hub 发送控制消息：nodeID 为空时发给所有 realtime 节点，非空时只发给目标节点。
func (b *bridge) PublishControl(data []byte, nodeID, _ string) error {
	topic := controlAllTopic
	if nodeID != "" {
		topic = controlNodePrefix + nodeID
	}
	ctx, cancel := context.WithTimeout(context.Background(), publishTimeout)
	defer cancel()
	return b.hub.Publish(ctx, topic, data)
}

// History 返回 ErrorNotAvailable：只投递实时消息，没有历史。
func (b *bridge) History(string, centrifuge.HistoryOptions) ([]*centrifuge.Publication, centrifuge.StreamPosition, error) {
	return nil, centrifuge.StreamPosition{}, centrifuge.ErrorNotAvailable
}

// RemoveHistory 什么也不做：没有历史。
func (b *bridge) RemoveHistory(string) error { return nil }

// sendEventNow 发送频道内部事件，最多等待 publishTimeout。
func (b *bridge) sendEventNow(ev channelEvent) error {
	ctx, cancel := context.WithTimeout(context.Background(), publishTimeout)
	defer cancel()
	return b.sendEvent(ctx, ev)
}

// sendEvent 把 ev 发布到对应频道的内部事件主题。
func (b *bridge) sendEvent(ctx context.Context, ev channelEvent) error {
	return b.hub.PublishJSON(ctx, channelEventTopic(ev.Channel), ev)
}

// deliver 处理本实例从 Hub 收到的每一条已订阅消息。它在 Hub 的接收循环中调用，必须立即返回。
func (b *bridge) deliver(msg broadcast.Message) {
	if msg.Topic == controlAllTopic || msg.Topic == b.nodeTopic {
		data := append([]byte(nil), msg.Data...)
		b.later(func() {
			if err := b.ctl.HandleControl(data); err != nil {
				b.logger.Error("handle control message", "error", err)
			}
		})
		return
	}
	if strings.HasPrefix(msg.Topic, channelEventPrefix) {
		var ev channelEvent
		if err := json.Unmarshal(msg.Data, &ev); err != nil {
			b.logger.Error("decode channel event", "error", err)
			return
		}
		var err error
		switch ev.Kind {
		case kindPublish:
			err = b.pubs.HandlePublication(ev.Channel, &centrifuge.Publication{Data: ev.Data, Info: ev.Info}, centrifuge.StreamPosition{}, false, nil)
		case kindJoin:
			err = b.pubs.HandleJoin(ev.Channel, ev.Info)
		case kindLeave:
			err = b.pubs.HandleLeave(ev.Channel, ev.Info)
		}
		if err != nil {
			b.logger.Error("deliver channel event", "kind", ev.Kind, "channel", ev.Channel, "error", err)
		}
		return
	}
	// JSON 协议的客户端只能接收 JSON，其他内容（例如空消息）不转发。
	if b.node.Hub().NumSubscribers(msg.Topic) == 0 || !json.Valid(msg.Data) {
		return
	}
	if err := b.pubs.HandlePublication(msg.Topic, &centrifuge.Publication{Data: msg.Data}, centrifuge.StreamPosition{}, false, nil); err != nil {
		b.logger.Error("deliver publication", "channel", msg.Topic, "error", err)
	}
}

// lost 在 Hub 的后端可能丢了消息时调用：断开本实例的全部客户端，SDK 会自动重连，
// 应用在重新订阅成功后取回期间的数据。
func (b *bridge) lost(error) {
	b.later(func() {
		for _, c := range b.node.Hub().Connections() {
			c.Disconnect(centrifuge.DisconnectInsufficientState)
		}
	})
}
