package realtime

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/centrifugal/centrifuge"

	"github.com/go-tenon/tenon/identity"
)

// connection 是一个连接的身份。主体在复查凭据时更新，订阅的处理器在另一个 goroutine 中读取它。
type connection struct {
	token string // 由 Options.Authenticate 认证时的 token：SDK 传入的、Authorization 请求头中的，或浏览器的会话 cookie

	mu  sync.Mutex
	sub identity.Subject // ID 为空表示匿名
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

// connectionKey 是连接信息在客户端 context 中的键。
type connectionKey struct{}

// connecting 识别连接的主体：SDK 传入的 token 优先，其次是连接请求带来的 token（Authorization: Bearer 请求头，
// 没有时是浏览器的会话 cookie），都由 Options.Authenticate 校验；都没有时是匿名连接。
func (rt *Realtime) connecting(ctx context.Context, e centrifuge.ConnectEvent) (centrifuge.ConnectReply, error) {
	conn := &connection{}
	token := e.Token
	if token == "" {
		token, _ = ctx.Value(requestTokenKey{}).(string)
	}
	if token != "" {
		sub, err := identity.Establish(ctx, rt.authenticator(token), rt.opts.Accounts.Resolve)
		switch {
		case identity.Failed(err):
			rt.logger.ErrorContext(ctx, "authentication backend failed", "error", err)
			return centrifuge.ConnectReply{}, centrifuge.ErrorInternal
		case err != nil:
			return centrifuge.ConnectReply{}, centrifuge.DisconnectInvalidToken
		}
		conn.sub, conn.token = sub, token
		ctx = identity.WithSubject(ctx, sub)
	}
	return centrifuge.ConnectReply{
		Context:     context.WithValue(ctx, connectionKey{}, conn),
		Credentials: &centrifuge.Credentials{UserID: conn.sub.Ref().String()},
	}, nil
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
func (rt *Realtime) check(ctx context.Context, client *centrifuge.Client, conn *connection) *centrifuge.Disconnect {
	cctx, cancel := context.WithTimeout(ctx, credentialTimeout)
	sub, err := identity.Reestablish(cctx, conn.subject(), rt.authenticator(conn.token), rt.opts.Accounts.Resolve)
	cancel()
	switch {
	case identity.Failed(err):
		return nil
	case err != nil:
		return &centrifuge.DisconnectInvalidToken
	}
	conn.setSubject(sub)
	rt.reauthorize(ctx, client, conn)
	return nil
}

// reauthorize 按连接当前的主体重新授权它订阅的每个频道。不再允许的频道用 Unsubscribe 把主体移出，
// 与应用自己调用时相同：主体在各个实例上的连接都退订，presence 频道立即发出离开事件。授权暂时失败的频道保持订阅。
func (rt *Realtime) reauthorize(ctx context.Context, client *centrifuge.Client, conn *connection) {
	ref := conn.subject().Ref()
	for _, channel := range client.Channels() {
		_, err := rt.authorize(ctx, conn, channel)
		if err == nil || err == centrifuge.ErrorInternal {
			continue
		}
		if err := rt.Unsubscribe(ref, channel); err != nil {
			rt.logger.ErrorContext(ctx, "unsubscribe after losing access", "channel", channel, "error", err)
		}
	}
}

// connected 为已经建立的连接安装订阅和发布的处理器，并在连接期间复查凭据。
func (rt *Realtime) connected(client *centrifuge.Client) {
	conn, _ := client.Context().Value(connectionKey{}).(*connection)
	if conn == nil {
		client.Disconnect(centrifuge.DisconnectServerError)
		return
	}
	client.OnSubscribe(func(e centrifuge.SubscribeEvent, cb centrifuge.SubscribeCallback) {
		// 在连接的读取循环中同步授权：Centrifuge 先写出订阅的回复、再把频道标记为已订阅，
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
	if conn.subject().ID != "" {
		go rt.watch(client, conn)
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

// watch 在连接期间定期复查凭据和频道授权，收到 Options.Credentials 的信号时立即复查；凭据失效后断开连接，见 check。
func (rt *Realtime) watch(client *centrifuge.Client, conn *connection) {
	ctx := client.Context()
	var changed <-chan struct{}
	if rt.opts.Credentials != nil {
		changed = rt.opts.Credentials.Watch(ctx, conn.subject())
	}
	t := time.NewTicker(rt.recheck)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case _, ok := <-changed:
			if !ok {
				changed = nil
				continue
			}
		}
		if d := rt.check(ctx, client, conn); d != nil {
			client.Disconnect(*d)
			return
		}
	}
}
