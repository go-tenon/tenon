package realtime

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync"
	"time"

	"github.com/centrifugal/centrifuge"

	"github.com/go-tenon/tenon/broadcast"
)

// internalTopic 是 realtime 在 Hub 上使用的主题：节点之间的控制消息、客户端发布的消息和 join/leave。
const internalTopic = "realtime.internal"

// publishTimeout 是 bridge 向 Hub 发布一条消息的时限。
const publishTimeout = 5 * time.Second

const (
	kindControl = "ctl"
	kindPublish = "pub"
	kindJoin    = "join"
	kindLeave   = "leave"
)

// envelope 是 internalTopic 上的消息。
type envelope struct {
	Kind    string                 `json:"k"`
	Node    string                 `json:"n,omitempty"` // 控制消息的目标节点，空表示所有节点
	Channel string                 `json:"c,omitempty"`
	Data    []byte                 `json:"d,omitempty"`
	Info    *centrifuge.ClientInfo `json:"i,omitempty"`
}

// bridge 用 broadcast.Hub 实现 centrifuge.Broker 和 centrifuge.Controller：
// 频道就是 Hub 的主题，应用通过 Hub 发布的消息直接交给订阅了同名频道的客户端。
// 每个实例本来就收到全部主题，所以按频道订阅和退订直接返回。只投递实时消息。
type bridge struct {
	hub    *broadcast.Hub
	node   *centrifuge.Node
	pubs   centrifuge.BrokerEventHandler
	ctl    centrifuge.ControlEventHandler
	logger *slog.Logger // 记录转发失败的消息

	members *membership

	mu    sync.Mutex    // 保护 tasks
	tasks []func()      // 交给 run 依次执行的任务：控制消息和丢失消息的处理可能再向 Hub 发布（例如断开连接时的离开事件），deliver 在 Hub 的投递中调用时禁止发布，所以排队处理，保持先后顺序
	wake  chan struct{} // 有新任务时唤醒 run
	done  chan struct{} // 关闭时 run 退出
	once  sync.Once     // 保证 done 只关闭一次
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

// stop 结束 run，可以重复调用。
func (b *bridge) stop() { b.once.Do(func() { close(b.done) }) }

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

// Subscribe 什么也不做：每个实例都收到全部主题。
func (b *bridge) Subscribe(...string) error { return nil }

// Unsubscribe 什么也不做：每个实例都收到全部主题。
func (b *bridge) Unsubscribe(...string) error { return nil }

// Publish 通过 Hub 发布频道 ch 的消息；带客户端信息的发布经由内部主题转发。
func (b *bridge) Publish(ch string, data []byte, opts centrifuge.PublishOptions) (centrifuge.PublishResult, error) {
	ctx, cancel := context.WithTimeout(context.Background(), publishTimeout)
	defer cancel()
	if opts.ClientInfo == nil {
		return centrifuge.PublishResult{}, b.hub.Publish(ctx, ch, data)
	}
	// 客户端发布的消息带有发布者的信息，只发给 WebSocket 客户端。
	return centrifuge.PublishResult{}, b.send(ctx, envelope{Kind: kindPublish, Channel: ch, Data: data, Info: opts.ClientInfo})
}

// PublishJoin 和 PublishLeave 交给 membership 决定是否发出、何时发出。
func (b *bridge) PublishJoin(ch string, info *centrifuge.ClientInfo) error {
	return b.members.join(ch, info)
}

// PublishLeave 交给 membership 决定是否发出离开、何时发出。
func (b *bridge) PublishLeave(ch string, info *centrifuge.ClientInfo) error {
	return b.members.leave(ch, info)
}

// PublishControl 通过 Hub 发送控制消息，nodeID 为空时发给所有节点。
func (b *bridge) PublishControl(data []byte, nodeID, _ string) error {
	return b.sendNow(envelope{Kind: kindControl, Node: nodeID, Data: data})
}

// History 返回 ErrorNotAvailable：只投递实时消息，没有历史。
func (b *bridge) History(string, centrifuge.HistoryOptions) ([]*centrifuge.Publication, centrifuge.StreamPosition, error) {
	return nil, centrifuge.StreamPosition{}, centrifuge.ErrorNotAvailable
}

// RemoveHistory 什么也不做：没有历史。
func (b *bridge) RemoveHistory(string) error { return nil }

// sendNow 发送内部消息，最多等待 publishTimeout。
func (b *bridge) sendNow(e envelope) error {
	ctx, cancel := context.WithTimeout(context.Background(), publishTimeout)
	defer cancel()
	return b.send(ctx, e)
}

// send 在内部主题上发送 e。
func (b *bridge) send(ctx context.Context, e envelope) error {
	return b.hub.PublishJSON(ctx, internalTopic, e)
}

// deliver 处理本实例从 Hub 收到的每一条消息。它在 Hub 的接收循环中调用，必须立即返回。
func (b *bridge) deliver(msg broadcast.Message) {
	if msg.Topic != internalTopic {
		// 没有客户端订阅的主题直接跳过。JSON 协议的客户端只能接收 JSON，其他内容（例如空消息）不转发。
		if b.node.Hub().NumSubscribers(msg.Topic) == 0 || !json.Valid(msg.Data) {
			return
		}
		if err := b.pubs.HandlePublication(msg.Topic, &centrifuge.Publication{Data: msg.Data}, centrifuge.StreamPosition{}, false, nil); err != nil {
			b.logger.Error("deliver publication", "channel", msg.Topic, "error", err)
		}
		return
	}
	var e envelope
	if err := json.Unmarshal(msg.Data, &e); err != nil {
		b.logger.Error("decode internal message", "error", err)
		return
	}
	var err error
	switch e.Kind {
	case kindControl:
		if e.Node == "" || e.Node == b.node.ID() {
			b.later(func() {
				if err := b.ctl.HandleControl(e.Data); err != nil {
					b.logger.Error("handle control message", "error", err)
				}
			})
		}
	case kindPublish:
		err = b.pubs.HandlePublication(e.Channel, &centrifuge.Publication{Data: e.Data, Info: e.Info}, centrifuge.StreamPosition{}, false, nil)
	case kindJoin:
		err = b.pubs.HandleJoin(e.Channel, e.Info)
	case kindLeave:
		err = b.pubs.HandleLeave(e.Channel, e.Info)
	}
	if err != nil {
		b.logger.Error("deliver internal message", "kind", e.Kind, "channel", e.Channel, "error", err)
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
