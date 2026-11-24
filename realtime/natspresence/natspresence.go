// Package natspresence 是基于 NATS JetStream 键值存储（KV）的在线成员存储，实现 realtime.PresenceStore。
//
//	presence, err := natspresence.New(ctx, nc, natspresence.Options{Bucket: "blog_presence"})
//	defer presence.Close()
//	rt, err := realtime.New(hub, realtime.Options{Presence: presence})
package natspresence

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"log/slog"
	"regexp"
	"sync"
	"time"

	"github.com/cockroachdb/errors"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/go-tenon/tenon/identity"
	"github.com/go-tenon/tenon/logging"
	"github.com/go-tenon/tenon/realtime"
)

var _ realtime.PresenceStore = (*Store)(nil)

// MinTTL 是 Add 接受的最短有效期，受 NATS JetStream 单消息 TTL 最小 1 秒的限制。
const MinTTL = time.Second

// MaxTTL 是 Add 接受的最长有效期，也是 bucket 中记录的保存期限上限。
const MaxTTL = 5 * time.Minute

// markerTTL 是键因 TTL 到期后 NATS 保留删除标记的时长，确保所有实例的 watcher 及时收到过期事件。
const markerTTL = time.Minute

// Options 是 New 的选项。
type Options struct {
	Bucket   string       // 保存在线记录的 KV bucket 的名字，例如 blog_presence。同一个应用的实例必须相同，多个应用或环境共用 NATS 时用它区分。由字母、数字、'_' 和 '-' 组成。
	Replicas int          // bucket 的副本数，不大于 0 时为 1。NATS 集群中可以设为 3，一台服务器故障时在线记录不丢失；单副本时丢失的记录在连接下一次续期时（最多 25 秒）恢复。
	Logger   *slog.Logger // 记录后台同步镜像时的警告；nil 时使用 slog.Default()。
}

var validBucket = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

// Store 在一个 KV bucket 中为每个频道的每个连接保存一个键：<频道>.<连接 ID>，两段都用 base64url 编码，
// 频道名中的 ':' 和 '.' 因此不影响键的格式。值中记录主体和资料，有效期由 NATS JetStream 按消息 TTL 维护。
//
// 每个 Store 通过单个常驻 KV Watcher 在进程内存中维护 bucket 的实时镜像：
// List 直接读取内存镜像，不向 NATS 发起查询；Add 和 Remove 写入 NATS 后立即同步本地镜像。
// bucket 开启 LimitMarkerTTL，键到期时 NATS 服务端自动发出清除标记并推送给全部 watcher，
// 过期判断以 NATS 服务端时钟为准。bucket 保存在服务器内存中，只保留每个键的最新值。使用完毕后调用 Close 停止后台同步。
type Store struct {
	js        jetstream.JetStream
	kv        jetstream.KeyValue
	keyPrefix string
	logger    *slog.Logger

	cancel context.CancelFunc
	done   chan struct{}

	mu       sync.RWMutex
	channels map[string]map[string]cachedEntry
	closed   bool
}

// cachedEntry 是本地内存镜像中的一条在线记录。
type cachedEntry struct {
	entry    realtime.PresenceEntry
	revision uint64
}

// New 创建 bucket（已经存在时按 opts 更新它），完成初始镜像同步并返回存储。
// nc 通常是 natsx.Connect 返回的连接，与队列等组件共用，服务器需要为 NATS 2.15 以上并开启 JetStream。
// nc 为 nil 或 Bucket 不合法时 panic。
func New(ctx context.Context, nc *nats.Conn, opts Options) (*Store, error) {
	if nc == nil {
		panic("natspresence: nil connection")
	}
	if !validBucket.MatchString(opts.Bucket) {
		panic("natspresence: bucket " + opts.Bucket + " must be letters, digits, '_' or '-'")
	}
	js, err := jetstream.New(nc)
	if err != nil {
		return nil, errors.Wrap(err, "natspresence: jetstream")
	}
	kv, err := js.CreateOrUpdateKeyValue(ctx, jetstream.KeyValueConfig{
		Bucket:         opts.Bucket,
		Description:    "tenon realtime presence",
		History:        1,
		TTL:            MaxTTL,
		LimitMarkerTTL: markerTTL,
		Storage:        jetstream.MemoryStorage,
		Replicas:       max(opts.Replicas, 1),
	})
	if err != nil {
		return nil, errors.Wrapf(err, "natspresence: bucket %s", opts.Bucket)
	}

	bgCtx, cancel := context.WithCancel(context.Background())
	w, err := kv.WatchAll(bgCtx)
	if err != nil {
		cancel()
		return nil, errors.Wrap(err, "natspresence: watch")
	}
	initial, err := readSnapshot(ctx, w)
	if err != nil {
		_ = w.Stop()
		cancel()
		return nil, errors.Wrap(err, "natspresence: initial snapshot")
	}

	s := &Store{
		js:        js,
		kv:        kv,
		keyPrefix: "$KV." + opts.Bucket + ".",
		logger:    logging.Or(opts.Logger).With(logging.AttrComponent, "natspresence"),
		cancel:    cancel,
		done:      make(chan struct{}),
		channels:  initial,
	}
	go s.watchLoop(bgCtx, w)
	return s, nil
}

// Close 停止后台 KV 镜像同步。可以重复调用。
func (s *Store) Close() error {
	s.mu.Lock()
	already := s.closed
	s.closed = true
	s.mu.Unlock()
	if already {
		return nil
	}
	s.cancel()
	<-s.done
	return nil
}

// readSnapshot 从新建立的 watcher 读取初始全量快照，直到收到 nil 哨兵。
func readSnapshot(ctx context.Context, w jetstream.KeyWatcher) (map[string]map[string]cachedEntry, error) {
	snapshot := map[string]map[string]cachedEntry{}
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case e, ok := <-w.Updates():
			if !ok {
				return nil, errors.New("watcher stopped during snapshot")
			}
			if e == nil {
				return snapshot, nil
			}
			if e.Operation() != jetstream.KeyValuePut {
				continue
			}
			ce, ok := decode(e)
			if !ok {
				continue
			}
			putEntry(snapshot, ce)
		}
	}
}

// watchLoop 持续把 KV 变更同步到本地内存镜像；watcher 中断时退避重建并用新快照原子替换镜像。
func (s *Store) watchLoop(ctx context.Context, w jetstream.KeyWatcher) {
	defer close(s.done)

	backoff := 100 * time.Millisecond
	for {
		if w != nil {
			s.consume(ctx, w)
			_ = w.Stop()
			w = nil
			if ctx.Err() != nil {
				return
			}
			s.logger.Warn("presence watcher stopped; reconnecting")
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		next, err := s.kv.WatchAll(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			s.logger.Warn("presence watcher reconnect failed", "error", err)
			backoff = min(backoff*2, 5*time.Second)
			continue
		}
		snapshot, err := readSnapshot(ctx, next)
		if err != nil {
			_ = next.Stop()
			if ctx.Err() != nil {
				return
			}
			s.logger.Warn("presence snapshot rebuild failed", "error", err)
			backoff = min(backoff*2, 5*time.Second)
			continue
		}
		s.mu.Lock()
		s.channels = snapshot
		s.mu.Unlock()
		s.logger.Info("presence watcher reconnected")
		w = next
		backoff = 100 * time.Millisecond
	}
}

// consume 消费单个 watcher 的实时更新，直到 watcher 关闭或 ctx 取消。
func (s *Store) consume(ctx context.Context, w jetstream.KeyWatcher) {
	for {
		select {
		case <-ctx.Done():
			return
		case e, ok := <-w.Updates():
			if !ok {
				return
			}
			if e == nil {
				continue
			}
			s.applyUpdate(e)
		}
	}
}

// applyUpdate 把来自 KV watcher 的单条变更应用到本地内存镜像。
// NATS 键过期产生的 KeyValuePurge 与显式删除的 KeyValueDelete 都移除对应条目。
func (s *Store) applyUpdate(e jetstream.KeyValueEntry) {
	channel, client, ok := decodeKey(e.Key())
	if !ok {
		return
	}
	rev := e.Revision()
	if e.Operation() != jetstream.KeyValuePut {
		s.mu.Lock()
		if cur, exists := s.channels[channel][client]; exists && cur.revision <= rev {
			deleteEntry(s.channels, channel, client)
		}
		s.mu.Unlock()
		return
	}
	ce, ok := decode(e)
	if !ok {
		s.mu.Lock()
		if cur, exists := s.channels[channel][client]; exists && cur.revision <= rev {
			deleteEntry(s.channels, channel, client)
		}
		s.mu.Unlock()
		return
	}
	s.mu.Lock()
	if cur, exists := s.channels[channel][client]; !exists || cur.revision <= rev {
		putEntry(s.channels, ce)
	}
	s.mu.Unlock()
}

// stored 是保存在 KV 中的一个在线成员。
type stored struct {
	Subject string `json:"s"`
	Info    []byte `json:"i,omitempty"`
}

var encoding = base64.RawURLEncoding

// channelToken 返回频道在 KV 键中的编码。
func channelToken(channel string) string { return encoding.EncodeToString([]byte(channel)) }

// key 返回频道 channel 中连接 client 的 KV 键。
func key(channel, client string) string {
	return channelToken(channel) + "." + encoding.EncodeToString([]byte(client))
}

// Add 实现 realtime.PresenceStore：携带单消息 TTL 写入 NATS 并立即同步本地内存镜像。
// ttl 不在 [MinTTL, MaxTTL] 之内时返回错误。
func (s *Store) Add(ctx context.Context, e realtime.PresenceEntry, ttl time.Duration) error {
	if ttl < MinTTL || ttl > MaxTTL {
		return errors.Newf("natspresence: ttl %s must be between %s and %s", ttl, MinTTL, MaxTTL)
	}
	value, err := json.Marshal(stored{Subject: e.Subject.String(), Info: e.Info})
	if err != nil {
		return errors.Wrap(err, "natspresence: encode")
	}
	pa, err := s.js.Publish(ctx, s.keyPrefix+key(e.Channel, e.Client), value, jetstream.WithMsgTTL(ttl))
	if err != nil {
		return errors.Wrap(err, "natspresence: add")
	}
	ce := cachedEntry{entry: e, revision: pa.Sequence}
	s.mu.Lock()
	if cur, exists := s.channels[e.Channel][e.Client]; !exists || cur.revision <= pa.Sequence {
		putEntry(s.channels, ce)
	}
	s.mu.Unlock()
	return nil
}

// Remove 实现 realtime.PresenceStore：删除 NATS 键并立即同步本地内存镜像。
func (s *Store) Remove(ctx context.Context, channel, client string) error {
	if err := s.kv.Delete(ctx, key(channel, client)); err != nil {
		return errors.Wrap(err, "natspresence: remove")
	}
	s.mu.Lock()
	deleteEntry(s.channels, channel, client)
	s.mu.Unlock()
	return nil
}

// List 实现 realtime.PresenceStore：从本地内存镜像读取频道中的在线记录。
func (s *Store) List(ctx context.Context, channel string) ([]realtime.PresenceEntry, error) {
	if err := ctx.Err(); err != nil {
		return nil, errors.Wrap(err, "natspresence: list")
	}
	s.mu.RLock()
	clients := s.channels[channel]
	if len(clients) == 0 {
		s.mu.RUnlock()
		return nil, nil
	}
	out := make([]realtime.PresenceEntry, 0, len(clients))
	for _, ce := range clients {
		out = append(out, ce.entry)
	}
	s.mu.RUnlock()
	return out, nil
}

// putEntry 把 ce 写入按频道和连接 ID 分层的映射中。
func putEntry(channels map[string]map[string]cachedEntry, ce cachedEntry) {
	clients := channels[ce.entry.Channel]
	if clients == nil {
		clients = map[string]cachedEntry{}
		channels[ce.entry.Channel] = clients
	}
	clients[ce.entry.Client] = ce
}

// deleteEntry 从映射中删除频道 channel 的连接 client，空频道随之移除。
func deleteEntry(channels map[string]map[string]cachedEntry, channel, client string) {
	clients := channels[channel]
	if clients == nil {
		return
	}
	delete(clients, client)
	if len(clients) == 0 {
		delete(channels, channel)
	}
}

// decodeKey 把 KV 键还原为频道名和连接 ID。
func decodeKey(k string) (channel, client string, ok bool) {
	chToken, clientToken, found := cutLast(k)
	if !found {
		return "", "", false
	}
	chBytes, err := encoding.DecodeString(chToken)
	if err != nil {
		return "", "", false
	}
	clientBytes, err := encoding.DecodeString(clientToken)
	if err != nil {
		return "", "", false
	}
	return string(chBytes), string(clientBytes), true
}

// decode 把 KV 中的一条记录还原为缓存条目，记录无效时 ok 为 false。
func decode(e jetstream.KeyValueEntry) (ce cachedEntry, ok bool) {
	channel, client, ok := decodeKey(e.Key())
	if !ok {
		return ce, false
	}
	var st stored
	if json.Unmarshal(e.Value(), &st) != nil {
		return ce, false
	}
	ref, err := identity.ParseRef(st.Subject)
	if err != nil {
		return ce, false
	}
	return cachedEntry{
		entry:    realtime.PresenceEntry{Channel: channel, Client: client, Subject: ref, Info: st.Info},
		revision: e.Revision(),
	}, true
}

// cutLast 在最后一个 '.' 处切开 key。
func cutLast(key string) (before, after string, found bool) {
	for i := len(key) - 1; i >= 0; i-- {
		if key[i] == '.' {
			return key[:i], key[i+1:], true
		}
	}
	return key, "", false
}
