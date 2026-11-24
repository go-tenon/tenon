package realtime

import (
	"context"
	"encoding/json"
	"log/slog"
	"math/rand/v2"
	"sync"
	"time"

	"github.com/centrifugal/centrifuge"

	"github.com/go-tenon/tenon/identity"
	"github.com/go-tenon/tenon/logging"
)

// recheckConcurrency 是单节点并发复查连接凭据的上限，防止租户级撤销或大批重连同时冲击认证后端。
const recheckConcurrency = 64

// connection 是一个连接的身份与复查状态。
type connection struct {
	token  string // 由 Options.Authenticate 认证时的 token：SDK 传入的、Authorization 请求头中的，或浏览器的会话 cookie
	client *centrifuge.Client

	mu     sync.Mutex
	sub    identity.Subject // ID 为空表示匿名
	timer  *time.Timer      // 下一次定期复查的定时器
	closed bool
}

// subject 返回连接当前的主体。
func (c *connection) subject() identity.Subject {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.sub
}

// setSubject 换上复查凭据时重新加载的主体。
func (c *connection) setSubject(sub identity.Subject) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.sub = sub
}

// isClosed 报告连接是否已经断开。
func (c *connection) isClosed() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.closed
}

// schedule 在 d 之后触发 fn；连接已关闭时忽略。
func (c *connection) schedule(d time.Duration, fn func()) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return
	}
	if c.timer != nil {
		c.timer.Stop()
	}
	c.timer = time.AfterFunc(d, fn)
}

// close 标记连接已关闭并停止定期复查定时器。
func (c *connection) close() {
	c.mu.Lock()
	c.closed = true
	if c.timer != nil {
		c.timer.Stop()
		c.timer = nil
	}
	c.mu.Unlock()
}

// connectionKey 是连接信息在客户端 context 中的键。
type connectionKey struct{}

// rechecker 是节点级的有界并发凭据复查调度器：每个活跃连接不占用常驻 goroutine，
// 由定时器和 Credentials 回调按需入队，同一个连接的多次复查请求合并执行。
type rechecker struct {
	rt *Realtime

	mu      sync.Mutex
	queue   []*connection
	queued  map[*connection]struct{} // 已在等待队列中
	running map[*connection]struct{} // 正在复查中
	again   map[*connection]struct{} // 复查期间又收到新信号，结束后立即补查一次
	workers int
	closed  bool
	wg      sync.WaitGroup
}

// newRechecker 创建节点级凭据复查调度器。
func newRechecker(rt *Realtime) *rechecker {
	return &rechecker{
		rt:      rt,
		queued:  map[*connection]struct{}{},
		running: map[*connection]struct{}{},
		again:   map[*connection]struct{}{},
	}
}

// enqueue 把 conn 加入复查队列，合并重复请求，并按需启动不超过 recheckConcurrency 个 worker。
func (r *rechecker) enqueue(conn *connection) {
	if conn.isClosed() {
		return
	}
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return
	}
	if _, busy := r.running[conn]; busy {
		r.again[conn] = struct{}{}
		r.mu.Unlock()
		return
	}
	if _, pending := r.queued[conn]; pending {
		r.mu.Unlock()
		return
	}
	r.queued[conn] = struct{}{}
	r.queue = append(r.queue, conn)
	if r.workers < recheckConcurrency {
		r.workers++
		r.wg.Add(1)
		go r.work()
	}
	r.mu.Unlock()
}

// work 持续从队列取出连接复查，队列空时退出，不占用空闲 goroutine。
func (r *rechecker) work() {
	defer r.wg.Done()
	for {
		r.mu.Lock()
		if r.closed || len(r.queue) == 0 {
			r.workers--
			r.mu.Unlock()
			return
		}
		conn := r.queue[0]
		r.queue[0] = nil
		r.queue = r.queue[1:]
		delete(r.queued, conn)
		r.running[conn] = struct{}{}
		r.mu.Unlock()

		disconnected := false
		if !conn.isClosed() {
			if d := r.rt.check(conn); d != nil {
				conn.close()
				conn.client.Disconnect(*d)
				disconnected = true
			}
		}

		r.mu.Lock()
		delete(r.running, conn)
		_, needAgain := r.again[conn]
		delete(r.again, conn)
		closed := r.closed
		if !closed && !disconnected && !conn.isClosed() {
			if needAgain {
				r.queued[conn] = struct{}{}
				r.queue = append(r.queue, conn)
			} else {
				conn.schedule(r.rt.nextRecheck(), func() { r.enqueue(conn) })
			}
		}
		r.mu.Unlock()
	}
}

// close 停止接收新任务并等待进行中的复查结束。
func (r *rechecker) close() {
	r.mu.Lock()
	r.closed = true
	r.queue = nil
	clear(r.queued)
	clear(r.again)
	r.mu.Unlock()
	r.wg.Wait()
}

// nextRecheck 返回下一次定期复查的延迟：间隔至少 1 秒时加上 ±10% 随机抖动以分散并发。
func (rt *Realtime) nextRecheck() time.Duration {
	d := rt.recheck
	if d < time.Second {
		return d
	}
	jitter := d / 10
	return d - jitter + rand.N(2*jitter)
}

// connecting 识别连接的主体：SDK 传入的 token 优先，其次是连接请求带来的 token（Authorization: Bearer 请求头，
// 没有时是浏览器的会话 cookie），都由 Options.Authenticate 校验；都没有时是匿名连接。
func (rt *Realtime) connecting(ctx context.Context, e centrifuge.ConnectEvent) (centrifuge.ConnectReply, error) {
	conn := &connection{}
	token := e.Token
	if token == "" {
		token, _ = ctx.Value(requestTokenKey{}).(string)
	}
	if token != "" {
		sub, err := identity.Establish(ctx, rt.authenticator(token), rt.authPolicy())
		switch {
		case identity.Failed(err):
			rt.logger.ErrorContext(ctx, "authentication backend failed", "error", err)
			return centrifuge.ConnectReply{}, centrifuge.ErrorInternal
		case err != nil:
			return centrifuge.ConnectReply{}, centrifuge.DisconnectInvalidToken
		}
		conn.sub, conn.token = sub, token
		ctx = identity.WithSubject(ctx, sub)
		if sub.Impersonated() {
			ctx = logging.WithAttrs(ctx, slog.String(logging.AttrImpersonator, sub.Impersonator.String()))
		}
	}
	return centrifuge.ConnectReply{
		Context:     context.WithValue(ctx, connectionKey{}, conn),
		Credentials: &centrifuge.Credentials{UserID: conn.sub.Ref().String()},
	}, nil
}

// authPolicy 返回实时连接使用的身份与代为登录校验策略。
func (rt *Realtime) authPolicy() identity.Policy {
	return identity.Policy{Accounts: rt.opts.Accounts, Impersonation: rt.opts.Impersonation}
}

// authenticator 用 Options.Authenticate 校验 token；没有设置时带 token 的连接一律无效。
func (rt *Realtime) authenticator(token string) identity.Authenticator {
	return func(ctx context.Context) (identity.Subject, error) {
		if rt.opts.Authenticate == nil {
			return identity.Subject{}, nil
		}
		return rt.opts.Authenticate(ctx, token)
	}
}

// check 重新校验连接的凭据。确定已经失效时返回 DisconnectInvalidToken，SDK 收到后停止重连；
// 校验通过时换上重新加载的主体（权限可能已经变化），按它重新授权已经订阅的频道，见 reauthorize；暂时无法判断时连接保持原样。
func (rt *Realtime) check(conn *connection) *centrifuge.Disconnect {
	ctx := conn.client.Context()
	if ctx.Err() != nil {
		return nil
	}
	cctx, cancel := context.WithTimeout(ctx, credentialTimeout)
	sub, err := identity.Reestablish(cctx, conn.subject(), rt.authenticator(conn.token), rt.authPolicy())
	cancel()
	switch {
	case identity.Failed(err):
		return nil
	case err != nil:
		return &centrifuge.DisconnectInvalidToken
	}
	conn.setSubject(sub)
	rt.reauthorize(ctx, conn)
	return nil
}

// reauthorize 按连接当前的主体重新授权当前连接已订阅的每个频道。不再允许的频道退订当前连接，
// 同主体的其他连接保持不变；presence 频道立即移除本连接的在线记录并在无其他连接时发出离开事件。
// 授权暂时失败的频道保持订阅。
func (rt *Realtime) reauthorize(ctx context.Context, conn *connection) {
	client := conn.client
	ref := conn.subject().Ref()
	for _, channel := range client.Channels() {
		rule, _, _ := rt.match(channel)
		_, err := rt.authorize(ctx, conn, channel)
		if err == nil || err == centrifuge.ErrorInternal {
			continue
		}
		client.Unsubscribe(channel)
		if rule.presence != nil {
			if err := rt.members.expelClient(channel, client.ID(), ref); err != nil {
				rt.logger.ErrorContext(ctx, "remove presence after losing access", "channel", channel, "error", err)
			}
		}
	}
}

// connected 为已经建立的连接安装订阅、发布和断开处理器，并登记凭据复查。
func (rt *Realtime) connected(client *centrifuge.Client) {
	conn, _ := client.Context().Value(connectionKey{}).(*connection)
	if conn == nil {
		client.Disconnect(centrifuge.DisconnectServerError)
		return
	}
	conn.client = client
	client.OnDisconnect(func(centrifuge.DisconnectEvent) {
		conn.close()
	})
	client.OnSubscribe(func(e centrifuge.SubscribeEvent, cb centrifuge.SubscribeCallback) {
		// 同步授权：Centrifuge 先写出订阅的回复、再把频道标记为已订阅，
		// 异步回调时客户端收到回复后立即发布或读取成员，会赶在标记之前而被拒绝。
		opts, err := rt.authorize(client.Context(), conn, e.Channel)
		cb(centrifuge.SubscribeReply{Options: opts}, err)
	})
	client.OnPublish(func(e centrifuge.PublishEvent, cb centrifuge.PublishCallback) {
		rule, _, ok := rt.match(e.Channel)
		if !ok || !rule.publish || !client.IsSubscribed(e.Channel) {
			cb(centrifuge.PublishReply{}, centrifuge.ErrorPermissionDenied)
			return
		}
		cb(centrifuge.PublishReply{Options: centrifuge.PublishOptions{ClientInfo: e.ClientInfo}}, nil)
	})
	// 只有 presence 频道的订阅者可以读取成员列表；结果留空时由 Centrifuge 从 PresenceStore 读取。
	member := func(channel string) error {
		if rule, _, ok := rt.match(channel); !ok || rule.presence == nil || !client.IsSubscribed(channel) {
			return centrifuge.ErrorPermissionDenied
		}
		return nil
	}
	client.OnPresence(func(e centrifuge.PresenceEvent, cb centrifuge.PresenceCallback) {
		cb(centrifuge.PresenceReply{}, member(e.Channel))
	})
	client.OnPresenceStats(func(e centrifuge.PresenceStatsEvent, cb centrifuge.PresenceStatsCallback) {
		cb(centrifuge.PresenceStatsReply{}, member(e.Channel))
	})
	if sub := conn.subject(); sub.ID != "" {
		conn.schedule(rt.nextRecheck(), func() { rt.rechecker.enqueue(conn) })
		if rt.opts.Credentials != nil {
			rt.opts.Credentials.Watch(client.Context(), sub, func() { rt.rechecker.enqueue(conn) })
		}
	}
}

// authorize 按频道规则决定连接能否订阅 channel，返回订阅的选项；错误为 nil 表示允许。
func (rt *Realtime) authorize(ctx context.Context, conn *connection, channel string) (centrifuge.SubscribeOptions, error) {
	var opts centrifuge.SubscribeOptions
	rule, params, ok := rt.match(channel)
	if !ok {
		return opts, centrifuge.ErrorUnknownChannel
	}
	if rule.authorize == nil && rule.presence == nil {
		return opts, nil
	}
	sub := conn.subject()
	if sub.ID == "" {
		return opts, centrifuge.ErrorPermissionDenied
	}
	ctx, cancel := context.WithTimeout(ctx, credentialTimeout)
	defer cancel()
	var (
		info    any
		allowed bool
		err     error
	)
	if rule.presence != nil {
		info, allowed, err = rule.presence(ctx, sub, params)
	} else {
		allowed, err = rule.authorize(ctx, sub, params)
	}
	switch {
	case identity.Failed(err):
		if ctx.Err() == nil || context.Cause(ctx) == context.DeadlineExceeded {
			rt.logger.ErrorContext(ctx, "channel authorization failed", "channel", channel, "error", err)
		}
		return opts, centrifuge.ErrorInternal
	case err != nil || !allowed:
		return opts, centrifuge.ErrorPermissionDenied
	}
	if rule.presence != nil {
		opts.EmitPresence, opts.EmitJoinLeave, opts.PushJoinLeave = true, true, true
		if info != nil {
			if opts.ChannelInfo, err = json.Marshal(info); err != nil {
				rt.logger.ErrorContext(ctx, "encode presence info", "channel", channel, "error", err)
				return opts, centrifuge.ErrorInternal
			}
		}
	}
	return opts, nil
}
