package broadcast

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"sync"

	"github.com/cockroachdb/errors"

	"github.com/go-tenon/tenon/identity"
)

// Credentials 通知所有实例上的长连接立即重新校验凭据，实现 identity.CredentialWatcher。
// 把它设置为 rest.Options.Credentials 和 realtime.Options.Credentials，
// 撤销 token、停用用户或修改权限之后调用对应的 Recheck 方法，受影响的长连接随即复查，
// 凭据已经失效的连接结束。
//
// 所有长连接共享进程内的单个 Hub.Receiver，按需向后端订阅当前活跃连接关注的主题，
// 活跃期间不创建额外 goroutine。
//
// 它只提醒复查，不说明凭据怎样变化：是否结束连接由认证中间件的复查决定，多发的提醒只是多查一次。
// 提醒丢失（Backend 断线、调用之前进程退出）时，连接仍由 30 秒一次的定期复查兜底。
//
// 提醒要在数据提交之后发出，否则复查读到的还是旧数据：撤销在事务中进行时，用 tx.AfterCommit 调用。
type Credentials struct {
	hub  *Hub
	recv *Receiver

	mu       sync.Mutex
	watchers map[string]map[*credWatch]struct{} // 提醒主题 -> 关注该主题的回调集合
	all      map[*credWatch]struct{}            // 所有活跃的关注者，用于在后端丢消息时统一提醒复查

	subMu  sync.Mutex          // 串行化对 recv.Subscribe / Unsubscribe 的调用
	active map[string]struct{} // 已向 recv 订阅的提醒主题集合，由 subMu 保护
}

// credWatch 是单个长连接的复查回调。
type credWatch struct {
	topics   []string
	onChange func()
}

// NewCredentials 创建基于 hub 的 Credentials。hub 为 nil 时 panic。
func NewCredentials(hub *Hub) *Credentials {
	if hub == nil {
		panic("broadcast: nil hub")
	}
	c := &Credentials{
		hub:      hub,
		watchers: map[string]map[*credWatch]struct{}{},
		all:      map[*credWatch]struct{}{},
		active:   map[string]struct{}{},
	}
	c.recv = hub.Receiver(c.deliver, c.lost)
	return c
}

// 三种提醒的主题前缀。
const (
	credentialTopic = "cred.c."
	subjectTopic    = "cred.s."
	tenantTopic     = "cred.t."
)

// credTopic 返回提醒的主题。ID 中可能有主题不允许的字符，因此使用哈希。
func credTopic(prefix, id string) string {
	h := sha256.Sum256([]byte(id))
	return prefix + hex.EncodeToString(h[:16])
}

// RecheckCredential 让使用凭据 id 的长连接立即复查，id 是主体的 Credential：
// API 令牌的 ID。用于退出某一台设备，例如 apitoken.Store.Revoke 之后。
func (c *Credentials) RecheckCredential(ctx context.Context, id string) error {
	if id == "" {
		return errors.New("broadcast: empty credential id")
	}
	return c.hub.Publish(ctx, credTopic(credentialTopic, id), nil)
}

// RecheckSubject 让主体 ref 的全部长连接立即复查。用于退出其他设备、修改密码、停用账号、
// 移出租户或修改这个主体的角色之后。
func (c *Credentials) RecheckSubject(ctx context.Context, ref identity.Ref) error {
	if err := ref.Validate(); err != nil {
		return errors.Wrap(err, "broadcast")
	}
	return c.hub.Publish(ctx, credTopic(subjectTopic, ref.String()), nil)
}

// RecheckTenant 让请求所在租户为 tenant 的全部长连接立即复查，"" 是平台本身。
// 用于修改角色的权限等影响整个租户的变化之后。
func (c *Credentials) RecheckTenant(ctx context.Context, tenant string) error {
	return c.hub.Publish(ctx, credTopic(tenantTopic, tenant), nil)
}

// Watch 实现 identity.CredentialWatcher：关注 sub.Credential、sub.Ref()、sub.Impersonator 和 identity.TenantFromContext(ctx)，
// 收到对应的 Recheck 时调用 onChange；后端可能丢了消息时同样调用 onChange 提醒复查。
// ctx 结束后停止关注并在没有其他关注者时退订对应主题。
func (c *Credentials) Watch(ctx context.Context, sub identity.Subject, onChange func()) {
	if onChange == nil || ctx.Err() != nil {
		return
	}
	topics := []string{credTopic(tenantTopic, identity.TenantFromContext(ctx))}
	if sub.ID != "" {
		topics = append(topics, credTopic(subjectTopic, sub.Ref().String()))
	}
	if !sub.Impersonator.IsZero() {
		topics = append(topics, credTopic(subjectTopic, sub.Impersonator.String()))
	}
	if sub.Credential != "" {
		topics = append(topics, credTopic(credentialTopic, sub.Credential))
	}

	w := &credWatch{topics: topics, onChange: onChange}
	c.mu.Lock()
	c.all[w] = struct{}{}
	for _, topic := range topics {
		set := c.watchers[topic]
		if set == nil {
			set = map[*credWatch]struct{}{}
			c.watchers[topic] = set
		}
		set[w] = struct{}{}
	}
	c.mu.Unlock()

	c.syncTopics(topics...)
	context.AfterFunc(ctx, func() { c.unwatch(w) })
}

// unwatch 取消 w 的关注，并在主题失去最后一个关注者时退订。
func (c *Credentials) unwatch(w *credWatch) {
	c.mu.Lock()
	if _, ok := c.all[w]; !ok {
		c.mu.Unlock()
		return
	}
	delete(c.all, w)
	for _, topic := range w.topics {
		set := c.watchers[topic]
		delete(set, w)
		if len(set) == 0 {
			delete(c.watchers, topic)
		}
	}
	c.mu.Unlock()

	c.syncTopics(w.topics...)
}

// syncTopics 把 topics 的关注状态水平同步到 recv。
func (c *Credentials) syncTopics(topics ...string) {
	c.subMu.Lock()
	defer c.subMu.Unlock()

	var toSub, toUnsub []string
	c.mu.Lock()
	for _, topic := range topics {
		want := len(c.watchers[topic]) > 0
		_, active := c.active[topic]
		switch {
		case want && !active:
			toSub = append(toSub, topic)
		case !want && active:
			toUnsub = append(toUnsub, topic)
		}
	}
	c.mu.Unlock()

	if len(toSub) > 0 {
		if err := c.recv.Subscribe(toSub...); err != nil {
			if !errors.Is(err, ErrClosed) {
				c.hub.logger.Warn("credentials watch subscribe failed", "error", err)
			}
		} else {
			for _, topic := range toSub {
				c.active[topic] = struct{}{}
			}
		}
	}
	if len(toUnsub) > 0 {
		if err := c.recv.Unsubscribe(toUnsub...); err != nil {
			c.hub.logger.Warn("credentials watch unsubscribe failed", "error", err)
		} else {
			for _, topic := range toUnsub {
				delete(c.active, topic)
			}
		}
	}
}

// deliver 通知关注 msg.Topic 的全部长连接立即复查。
func (c *Credentials) deliver(msg Message) {
	c.mu.Lock()
	set := c.watchers[msg.Topic]
	if len(set) == 0 {
		c.mu.Unlock()
		return
	}
	callbacks := make([]func(), 0, len(set))
	for w := range set {
		callbacks = append(callbacks, w.onChange)
	}
	c.mu.Unlock()
	for _, fn := range callbacks {
		fn()
	}
}

// lost 在后端可能丢了消息时通知全部活跃长连接复查一次，避免遗漏断线期间的撤销信号。
func (c *Credentials) lost(error) {
	c.mu.Lock()
	if len(c.all) == 0 {
		c.mu.Unlock()
		return
	}
	callbacks := make([]func(), 0, len(c.all))
	for w := range c.all {
		callbacks = append(callbacks, w.onChange)
	}
	c.mu.Unlock()
	for _, fn := range callbacks {
		fn()
	}
}
