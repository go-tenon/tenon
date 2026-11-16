// Package natspresence 是基于 NATS JetStream 键值存储（KV）的在线成员存储，实现 realtime.PresenceStore。
//
//	presence, err := natspresence.New(ctx, nc, natspresence.Options{Bucket: "blog_presence"})
//	rt, err := realtime.New(hub, realtime.Options{Presence: presence})
package natspresence

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"regexp"
	"time"

	"github.com/cockroachdb/errors"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/go-tenon/tenon/identity"
	"github.com/go-tenon/tenon/realtime"
)

var _ realtime.PresenceStore = (*Store)(nil)

// MaxTTL 是 Add 接受的最长有效期，也是 bucket 中记录的保存期限：连接所在的进程崩溃时来不及删除的记录，
// 最晚在 MaxTTL 之后由 NATS 删除。有效期到了但还在保存期限内的记录，List 不返回。
const MaxTTL = 5 * time.Minute

// Options 是 New 的选项。
type Options struct {
	// Bucket 是保存在线记录的 KV bucket 的名字，例如 blog_presence。同一个应用的实例必须相同，
	// 多个应用或环境共用 NATS 时用它区分。由字母、数字、'_' 和 '-' 组成。
	Bucket string
	// Replicas 是 bucket 的副本数，不大于 0 时为 1。NATS 集群中可以设为 3，一台服务器故障时在线记录不丢失；
	// 单副本时丢失的记录在连接下一次续期时（最多 25 秒）恢复。
	Replicas int
}

var validBucket = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

// Store 在一个 KV bucket 中为每个频道的每个连接保存一个键：<频道>.<连接 ID>，两段都用 base64url 编码，
// 频道名中的 ':' 和 '.' 因此不影响键的格式。值中记录主体、资料和到期时间。
//
// bucket 保存在服务器内存中，只保留每个键的最新值，记录的保存期限是 MaxTTL。
// 到期时间按应用服务器的时钟计算，各实例的时钟需要同步。
type Store struct {
	kv jetstream.KeyValue
}

// New 创建 bucket（已经存在时按 opts 更新它）并返回存储。nc 通常是 natsx.Connect 返回的连接，与队列等组件共用，
// 服务器需要开启 JetStream。nc 为 nil 或 Bucket 不合法时 panic。
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
		Bucket:      opts.Bucket,
		Description: "tenon realtime presence",
		History:     1,
		TTL:         MaxTTL,
		Storage:     jetstream.MemoryStorage,
		Replicas:    max(opts.Replicas, 1),
	})
	if err != nil {
		return nil, errors.Wrapf(err, "natspresence: bucket %s", opts.Bucket)
	}
	return &Store{kv: kv}, nil
}

type stored struct {
	Subject string `json:"s"`
	Info    []byte `json:"i,omitempty"`
	Expires int64  `json:"e"` // Unix 毫秒
}

var encoding = base64.RawURLEncoding

func channelToken(channel string) string { return encoding.EncodeToString([]byte(channel)) }

func key(channel, client string) string {
	return channelToken(channel) + "." + encoding.EncodeToString([]byte(client))
}

// Add 实现 realtime.PresenceStore。ttl 不在 (0, MaxTTL] 之内时返回错误。
func (s *Store) Add(ctx context.Context, e realtime.PresenceEntry, ttl time.Duration) error {
	if ttl <= 0 || ttl > MaxTTL {
		return errors.Newf("natspresence: ttl %s must be positive and at most %s", ttl, MaxTTL)
	}
	value, err := json.Marshal(stored{Subject: e.Subject.String(), Info: e.Info, Expires: time.Now().Add(ttl).UnixMilli()})
	if err != nil {
		return errors.Wrap(err, "natspresence: encode")
	}
	_, err = s.kv.Put(ctx, key(e.Channel, e.Client), value)
	return errors.Wrap(err, "natspresence: add")
}

// Remove 实现 realtime.PresenceStore。
func (s *Store) Remove(ctx context.Context, channel, client string) error {
	return errors.Wrap(s.kv.Delete(ctx, key(channel, client)), "natspresence: remove")
}

// List 实现 realtime.PresenceStore：读取频道中每个连接的最新记录，跳过已删除和已到期的。
func (s *Store) List(ctx context.Context, channel string) ([]realtime.PresenceEntry, error) {
	w, err := s.kv.WatchFiltered(ctx, []string{channelToken(channel) + ".*"}, jetstream.IgnoreDeletes())
	if err != nil {
		return nil, errors.Wrap(err, "natspresence: list")
	}
	defer func() { _ = w.Stop() }()
	now := time.Now().UnixMilli()
	var out []realtime.PresenceEntry
	for {
		select {
		case <-ctx.Done():
			return nil, errors.Wrap(ctx.Err(), "natspresence: list")
		case e, ok := <-w.Updates():
			if !ok {
				return nil, errors.New("natspresence: list: watcher stopped")
			}
			if e == nil { // 已读完现有的记录
				return out, nil
			}
			if entry, ok := decode(channel, e, now); ok {
				out = append(out, entry)
			}
		}
	}
}

// decode 把 KV 中的一条记录还原为在线记录，记录无效或已到期时 ok 为 false。
func decode(channel string, e jetstream.KeyValueEntry, now int64) (entry realtime.PresenceEntry, ok bool) {
	var st stored
	if json.Unmarshal(e.Value(), &st) != nil || st.Expires <= now {
		return entry, false
	}
	ref, err := identity.ParseRef(st.Subject)
	if err != nil {
		return entry, false
	}
	_, clientToken, found := cutLast(e.Key())
	if !found {
		return entry, false
	}
	client, err := encoding.DecodeString(clientToken)
	if err != nil {
		return entry, false
	}
	return realtime.PresenceEntry{Channel: channel, Client: string(client), Subject: ref, Info: st.Info}, true
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
