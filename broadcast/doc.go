// Package broadcast 在应用的多个实例之间按主题广播消息，供 SSE、WebSocket 等长连接推送使用。
//
// Hub 是每个进程一个的广播中心：Publish 把消息经由 Backend 发给订阅了该主题的实例，
// Subscribe 订阅一个或多个主题，Receiver 供自行管理大量动态主题的组件（如 realtime）按需订阅。
// 每个实例只向 Backend 订阅本实例当前有订阅者的主题，再分发给本实例的全部订阅。
//
// 消息只投递给当前在线的订阅，最多一次。订阅跟不上（缓冲区满），或 Backend 可能丢了消息时，
// Hub 结束受影响的订阅：长连接随之结束，客户端重连后由应用按
// Last-Event-ID 从自己的数据（例如消息表）补发。
//
// Backend：子包 natsbroadcast 使用 NATS 按主题精确发布订阅；broadcasttest 提供测试用的内存 Backend。
//
//	hub := broadcast.New(natsbroadcast.New(nc, "blog.broadcast"), nil)
//	defer hub.Close()
//	err := hub.PublishJSON(ctx, "room:42", event)
//
//	sub := hub.Subscribe("room:42")
//	defer sub.Close()
//	for msg := range sub.C() { ... }
//
// Credentials 基于 Hub 提醒所有实例上的长连接立即重新校验凭据，用于撤销登录后立即断开。
package broadcast
