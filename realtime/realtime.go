package realtime

import (
	"context"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/centrifugal/centrifuge"
	"github.com/cockroachdb/errors"
	"github.com/prometheus/client_golang/prometheus"
	"go.opentelemetry.io/otel/metric"

	"github.com/go-tenon/tenon/broadcast"
	"github.com/go-tenon/tenon/httpx"
	"github.com/go-tenon/tenon/identity"
	"github.com/go-tenon/tenon/logging"
)

// credentialInterval 是连接期间重新校验凭据的间隔，与 SSE 相同。
var credentialInterval = 30 * time.Second

// requestTokenKey 是连接请求中带来的 token 在 context 中的键：Authorization: Bearer 请求头，没有时是浏览器的会话 cookie。
type requestTokenKey struct{}

// credentialTimeout 是一次凭据校验或订阅授权的时限。
const credentialTimeout = 10 * time.Second

// Options 配置 Realtime，零值可用。
type Options struct {
	Authenticate   identity.TokenValidator      // 校验连接的 token（依次取 SDK 传入的 token、连接请求的 Authorization: Bearer 请求头、浏览器的会话 cookie），返回主体，通常与 rest.RequireToken 使用同一个校验器；token 无效时 SDK 停止重连，系统故障时客户端稍后重连。连接期间定期重新调用，token 被撤销后连接结束。nil 时拒绝带 token 的连接
	Accounts       identity.Accounts            // 用 token 连接时接受的账号体系和各自的 Resolver，通常与 rest.Options.Accounts 相同：按主体的 Type 调用 Resolver 得到连接的主体；Type 不在其中、或 Resolver 返回系统故障以外的错误时拒绝连接。连接期间与 Authenticate 一起定期重新调用，用户被停用或移出租户后连接结束。设置了 Authenticate 时必须设置
	SessionCookie  httpx.SessionCookie          // 浏览器的会话 cookie，与 rest.Options.SessionCookie 相同；零值是 httpx.DefaultSessionCookieName。
	Tenant         func(r *http.Request) string // 从连接请求中取出它所在的租户，例如路径参数 chi.URLParam(r, "tenant")、子域名或请求头，结果写入 identity.WithTenant：Resolver 据此加载权限，绑定了其他租户的 token 被拒绝。与 rest.Options.Tenant 的作用相同。nil 时使用外层中间件已经写入的租户，没有时是平台本身（""）。
	Credentials    identity.CredentialWatcher   // 让连接在收到复查信号时立即重新校验凭据，撤销 token、退出登录后连接随即结束；nil 时只按 30 秒的间隔定期校验。通常是 broadcast.NewCredentials 的返回值，与 rest.Options.Credentials 相同。
	TrustedOrigins []string                     // 允许跨站建立连接的来源，例如 https://app.example.com。浏览器发起的连接默认只接受同源请求；没有 Origin 请求头、或带 Authorization: Bearer 请求头的客户端（App、桌面端、服务器）全部放行。
	Presence       PresenceStore                // 保存 presence 频道的在线成员，所有实例共用同一个存储，即 natspresence.New。注册 presence 频道时必须设置；不停机升级时新旧进程同时运行，单实例部署同样需要共用的存储。
	PresenceGrace  time.Duration                // 连接离开 presence 频道后保留在线记录的时间：期间主体重新加入时，其他成员看不到离开和加入。零值为 DefaultPresenceGrace（10 秒），负数表示不保留，最长 MaxPresenceGrace（30 秒）。
	MeterProvider  metric.MeterProvider         // MeterProvider 非 nil 时记录 realtime.connections 和 realtime.subscriptions 两个指标。
	Logger         *slog.Logger                 // 记录连接和授权中的错误；nil 表示 slog.Default()。
}

// Realtime 是嵌入的 Centrifuge 节点。每个进程创建一个，可以并发使用。
type Realtime struct {
	opts     Options
	hub      *broadcast.Hub
	node     *centrifuge.Node
	logger   *slog.Logger
	origins  map[string]struct{}
	presence PresenceStore
	members  *membership
	recheck  time.Duration // 凭据复查的间隔，创建时取自 credentialInterval
	stop     func()
	metrics  metric.Registration

	mu    sync.RWMutex
	rules []channelRule
}

// New 创建 Realtime 并开始把 hub 收到的消息转给客户端，直到 Shutdown。hub 由应用创建和关闭，
// 所有实例必须使用相同的后端。hub 为 nil、TrustedOrigins 格式不正确、PresenceGrace 超过上限，
// 或设置了 Authenticate 而 Accounts 为空、Accounts 的名称不合法时 panic。
func New(hub *broadcast.Hub, opts Options) (*Realtime, error) {
	if hub == nil {
		panic("realtime: nil hub")
	}
	rt := &Realtime{
		opts:    opts,
		hub:     hub,
		logger:  logging.Or(opts.Logger).With(logging.AttrComponent, "realtime"),
		origins: map[string]struct{}{},
		recheck: credentialInterval,
	}
	if opts.Authenticate != nil && len(opts.Accounts) == 0 {
		panic("realtime: Accounts is required with Authenticate")
	}
	if err := opts.Accounts.Validate(); err != nil {
		panic(errors.Wrap(err, "realtime"))
	}
	if err := opts.SessionCookie.Validate(); err != nil {
		panic(errors.Wrap(err, "realtime"))
	}
	if opts.PresenceGrace > MaxPresenceGrace {
		panic("realtime: PresenceGrace is longer than MaxPresenceGrace")
	}
	if rt.presence = opts.Presence; rt.presence == nil {
		rt.presence = noPresence{}
	}
	for _, origin := range opts.TrustedOrigins {
		u, err := url.Parse(origin)
		if err != nil || u.Scheme == "" || u.Host == "" || origin != u.Scheme+"://"+u.Host {
			panic("realtime: trusted origin must look like https://app.example.com, got " + origin)
		}
		rt.origins[strings.ToLower(origin)] = struct{}{}
	}
	node, err := centrifuge.New(centrifuge.Config{
		LogLevel:   centrifuge.LogLevelInfo,
		LogHandler: rt.log,
		// Centrifuge 默认把指标注册到 Prometheus 的全局 registry，这里给它一个独立的。
		Metrics: centrifuge.MetricsConfig{RegistererGatherer: prometheus.NewRegistry()},
	})
	if err != nil {
		return nil, errors.Wrap(err, "realtime: create node")
	}
	rt.node = node
	b := &bridge{hub: hub, node: node, logger: rt.logger, wake: make(chan struct{}, 1), done: make(chan struct{})}
	node.SetBroker(b)
	node.SetController(b)
	b.members = newMembership(opts.PresenceGrace, rt.presence, b.sendNow, rt.logger)
	rt.members = b.members
	node.SetPresenceManager(presenceManager{store: rt.presence, members: b.members})
	node.OnConnecting(rt.connecting)
	node.OnConnect(rt.connected)
	if err := node.Run(); err != nil {
		return nil, errors.Wrap(err, "realtime: run node")
	}
	go b.run()
	unlisten := hub.Listen(b.deliver, b.lost)
	rt.stop = func() {
		b.members.close()
		unlisten()
		b.stop()
	}
	if opts.MeterProvider != nil {
		if rt.metrics, err = rt.observe(opts.MeterProvider); err != nil {
			_ = rt.Shutdown(context.Background())
			return nil, err
		}
	}
	return rt, nil
}

// Channel 登记频道规则。pattern 是频道名，可以带 {name} 占位符，例如 chat:{conversation}；
// 占位符匹配一段字母、数字、_ 和 -，匹配到的值交给规则的 Authorizer。
// 订阅时使用第一条匹配的规则，只有匹配规则的频道才能订阅。
//
// 频道就是 Hub 的主题：登记规则之后，发布到同名主题的消息会发给订阅它的客户端。
// 多租户应用的频道名要带上租户，并在 Authorizer 中与主体的租户比较。
// pattern 不合法，或者没有设置 Options.Presence 而 rule 是 Presence 时 panic。
func (rt *Realtime) Channel(pattern string, rule Rule) {
	c, err := compile(pattern, rule)
	if err != nil {
		panic(err)
	}
	if rule.presence != nil && rt.opts.Presence == nil {
		panic("realtime: presence channel " + pattern + " needs Options.Presence")
	}
	rt.mu.Lock()
	defer rt.mu.Unlock()
	rt.rules = append(rt.rules, c)
}

// match 返回频道 channel 符合的第一条规则和其中的参数。
func (rt *Realtime) match(channel string) (Rule, Params, bool) {
	rt.mu.RLock()
	defer rt.mu.RUnlock()
	for _, c := range rt.rules {
		if p, ok := c.match(channel); ok {
			return c.rule, p, true
		}
	}
	return Rule{}, nil, false
}

// Unsubscribe 取消主体 ref 在所有实例上对 channel 的订阅，用于主体失去访问权限之后，
// 例如被移出会话。客户端收到通知后保持退订。presence 频道中，其他成员立即收到离开事件，
// 跳过 Options.PresenceGrace。它独立于数据库事务，在业务数据提交之后调用。
func (rt *Realtime) Unsubscribe(ref identity.Ref, channel string) error {
	if err := ref.Validate(); err != nil {
		return errors.Wrap(err, "realtime")
	}
	if err := rt.node.Unsubscribe(ref.String(), channel); err != nil {
		return errors.Wrap(err, "realtime: unsubscribe")
	}
	return errors.Wrap(rt.members.expel(channel, ref), "realtime: remove presence")
}

// Handler 返回接受 WebSocket 连接的处理器，客户端 SDK 连接到它所在的地址。
//
// Options.Authenticate 校验连接的 token，依次取 SDK 传入的 token、连接请求的 Authorization: Bearer 请求头、
// 浏览器的会话 cookie；都没有的连接是匿名的，只能订阅 Public 频道。
// 浏览器的连接只接受同源的页面（以及 TrustedOrigins），其他网站的页面不能借用户的 cookie 建立连接。
// 带 Authorization 请求头的连接不检查来源：请求头由客户端自己设置，浏览器不会自动附加，例如桌面端的宿主
// 在 app:// 页面的连接请求上加上它。
// 连接脱离 Timeout 中间件的超时和 http.Server.WriteTimeout。
func (rt *Realtime) Handler() http.Handler {
	ws := centrifuge.NewWebsocketHandler(rt.node, centrifuge.WebsocketConfig{CheckOrigin: rt.checkOrigin})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if tenant := rt.opts.Tenant; tenant != nil {
			r = r.WithContext(identity.WithTenant(r.Context(), tenant(r)))
		}
		token := httpx.BearerToken(r.Header.Get("Authorization"))
		if token == "" {
			token = rt.opts.SessionCookie.Token(r.Header.Get("Cookie"))
		}
		if token != "" {
			r = r.WithContext(context.WithValue(r.Context(), requestTokenKey{}, token))
		}
		ws.ServeHTTP(hijackable(w), r)
	})
}

// hijackable 沿着 Unwrap 找到实现 http.Hijacker 的 ResponseWriter：
// Centrifuge 直接断言 http.Hijacker，中间件包装过的 ResponseWriter 通常只提供 Unwrap。
func hijackable(w http.ResponseWriter) http.ResponseWriter {
	for cur := w; ; {
		if _, ok := cur.(http.Hijacker); ok {
			return cur
		}
		u, ok := cur.(interface{ Unwrap() http.ResponseWriter })
		if !ok {
			return w
		}
		cur = u.Unwrap()
	}
}

// checkOrigin 检查 WebSocket 请求的来源：没有 Origin、带 Bearer 请求头、与请求同源或在允许的来源中时通过。
func (rt *Realtime) checkOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" || httpx.BearerToken(r.Header.Get("Authorization")) != "" {
		return true
	}
	u, err := url.Parse(origin)
	if err != nil {
		return false
	}
	if strings.EqualFold(u.Host, r.Host) {
		return true
	}
	_, ok := rt.origins[strings.ToLower(u.Scheme+"://"+u.Host)]
	return ok
}

// Shutdown 断开全部客户端并停止节点，客户端会重连到其他实例。在关闭 Hub 之前调用。
func (rt *Realtime) Shutdown(ctx context.Context) error {
	if rt.metrics != nil {
		_ = rt.metrics.Unregister()
	}
	err := rt.node.Shutdown(ctx)
	rt.stop()
	return errors.Wrap(err, "realtime: shutdown")
}

// log 把 Centrifuge 的日志写到 slog。
func (rt *Realtime) log(e centrifuge.LogEntry) {
	// Centrifuge 的 Info 记录的是被拒绝的订阅、客户端断开这类日常事件，按 Debug 输出。
	level := slog.LevelDebug
	switch e.Level {
	case centrifuge.LogLevelWarn:
		level = slog.LevelWarn
	case centrifuge.LogLevelError:
		level = slog.LevelError
	}
	attrs := make([]any, 0, len(e.Fields)*2)
	for k, v := range e.Fields {
		attrs = append(attrs, k, v)
	}
	rt.logger.Log(context.Background(), level, e.Message, attrs...)
}
