// Package natsx 按框架的约定连接 NATS。队列、广播和实时频道的在线成员共用一个进程中的同一条连接：
//
//	nc, err := natsx.Connect(cfg.NATS, "blog")
//	w := queue.NewWorker(db, nc, cfg.Queue)
//	hub := broadcast.New(natsbroadcast.New(nc, "blog.broadcast"))
//
// 连接的选项由框架决定：断开后一直重连，每 10 秒探测一次连接，断开和重连记录在日志中。
package natsx

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"github.com/cockroachdb/errors"
	"github.com/nats-io/nats.go"

	"github.com/go-tenon/tenon/logging"
)

// PingInterval 是探测连接的间隔：网络静默中断时，大约两个间隔之后发现，开始重连。
// nats.go 默认 2 分钟，期间其他实例广播的消息都丢了，所以框架缩短它。
const PingInterval = 10 * time.Second

// Config 是 NATS 的连接配置，对应配置文件的 nats 段。
type Config struct {
	URL         string `yaml:"url"`         // NATS 服务器的地址，集群的多个地址用逗号分隔，例如 "nats://10.0.0.1:4222,nats://10.0.0.2:4222"。用户名和密码可以写在地址中：nats://user:password@host:4222。服务器需要开启 JetStream。
	Credentials string `yaml:"credentials"` // NATS 凭据文件（.creds）的路径，服务器使用 JWT 认证时设置，为空时不使用。
}

// DefaultConfig 返回默认配置：本机的 NATS。
func DefaultConfig() Config { return Config{URL: "nats://127.0.0.1:4222"} }

// SetDefaults 实现 config.Defaulter。
func (c *Config) SetDefaults() { *c = DefaultConfig() }

// Validate 检查 URL 不为空。
func (c Config) Validate() error {
	if c.URL == "" {
		return errors.New("url is required")
	}
	return nil
}

// Option 是 Connect 的选项。
type Option func(*connectOptions)

// connectOptions 保存 Connect 的可选参数。
type connectOptions struct {
	logger *slog.Logger
}

// WithLogger 指定记录连接断开、重连和异步错误的日志器，默认使用 slog.Default()。
func WithLogger(logger *slog.Logger) Option {
	return func(o *connectOptions) { o.logger = logger }
}

// Connect 连接 NATS。name 是连接的名字，出现在 NATS 的监控中，例如应用名。
//
// 连接断开后一直重连，所以连接只在 Close 时关闭；进程退出前调用 Close。
// NATS 不可达时立即返回错误，与 database.Open 一样让进程在启动时失败。
func Connect(cfg Config, name string, opts ...Option) (*nats.Conn, error) {
	if err := cfg.Validate(); err != nil {
		return nil, errors.Wrap(err, "natsx")
	}
	var o connectOptions
	for _, opt := range opts {
		if opt != nil {
			opt(&o)
		}
	}
	logger := logging.Or(o.logger).With(logging.AttrComponent, "nats")
	natsOpts := []nats.Option{
		nats.Name(name),
		nats.MaxReconnects(-1),
		nats.ReconnectWait(time.Second),
		nats.PingInterval(PingInterval),
		nats.DisconnectErrHandler(func(_ *nats.Conn, err error) {
			if err != nil {
				logger.Warn("nats disconnected", "error", err)
			}
		}),
		nats.ReconnectHandler(func(nc *nats.Conn) { logger.Info("nats reconnected", "server", nc.ConnectedUrlRedacted()) }),
		nats.ErrorHandler(func(_ *nats.Conn, sub *nats.Subscription, err error) {
			if sub != nil {
				logger.Error("nats error", "subject", sub.Subject, "error", err)
				return
			}
			logger.Error("nats error", "error", err)
		}),
	}
	if cfg.Credentials != "" {
		natsOpts = append(natsOpts, nats.UserCredentials(cfg.Credentials))
	}
	nc, err := nats.Connect(cfg.URL, natsOpts...)
	if err != nil {
		return nil, errors.Wrapf(err, "natsx: connect to %s", redactURL(cfg.URL))
	}
	return nc, nil
}

// pingTimeout 是 ctx 没有截止时间时 Ping 等待服务器回应的时间。
const pingTimeout = 5 * time.Second

// Ping 返回检查 nc 可用的函数，交给 health.Check 的 Ping：连接正常时与服务器往返一次，
// 正在重连或已经关闭时返回错误。ctx 没有截止时间时最多等待 5 秒。
//
//	{Name: "nats", Ping: natsx.Ping(nc)}
func Ping(nc *nats.Conn) func(ctx context.Context) error {
	return func(ctx context.Context) error {
		if s := nc.Status(); s != nats.CONNECTED {
			return errors.Newf("natsx: connection %s", s)
		}
		if _, ok := ctx.Deadline(); !ok {
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, pingTimeout)
			defer cancel()
		}
		return errors.Wrap(nc.FlushWithContext(ctx), "natsx: ping")
	}
}

// redactURL 去掉地址中的用户名和密码，用于错误信息。
func redactURL(s string) string {
	urls := strings.Split(s, ",")
	for i, u := range urls {
		if scheme, rest, ok := strings.Cut(u, "://"); ok {
			if _, host, ok := strings.Cut(rest, "@"); ok {
				urls[i] = scheme + "://***@" + host
			}
		}
	}
	return strings.Join(urls, ",")
}
