package httpx

import (
	"context"
	"log/slog"
	"net/http"

	goi18n "github.com/nicksnyder/go-i18n/v2/i18n"
)

// DefaultsOption 是 Defaults 的选项：Logger、Languages、BodyLimitFor、BeforeAccessLog、WithCompress、WithContext。
type DefaultsOption interface{ applyDefaults(*defaultsOptions) }

// ServeOption 是 Serve 和 ListenAndServe 的选项：Logger、OnReady、SkipDrainDelay。
type ServeOption interface{ applyServe(*serveOptions) }

// defaultsOptions 是 Defaults 的选项。
type defaultsOptions struct {
	logger          *slog.Logger
	languages       *goi18n.Bundle
	bodyLimitFor    func(*http.Request) int64
	beforeAccessLog []func(http.Handler) http.Handler
	compress        bool
	ctxWraps        []func(context.Context) context.Context
}

// serveOptions 是 Serve 的选项。
type serveOptions struct {
	logger         *slog.Logger
	onReady        []func()
	skipDrainDelay func() bool
}

// serveOptionFunc 把函数适配为 ServeOption。
type serveOptionFunc func(*serveOptions)

// applyServe 应用选项函数。
func (f serveOptionFunc) applyServe(s *serveOptions) { f(s) }

// OnReady 登记服务器开始接收连接后的回调 fn（常传入 command.Daemon.DeferReady 返回的函数），
// 让不停机升级在 SSR 等前置启动完成且 HTTP 开始接收连接后再向 systemd 报告就绪。多次传入按顺序依次执行。
func OnReady(fn func()) ServeOption {
	return serveOptionFunc(func(s *serveOptions) {
		if fn != nil {
			s.onReady = append(s.onReady, fn)
		}
	})
}

// SkipDrainDelay 登记在停机时判断是否跳过 cfg.DrainDelay 的函数 fn（常传入 command.Daemon.HandedOver）：
// 返回 true 时立即关闭监听并排空，避免热重载交接等待负载均衡摘流。
func SkipDrainDelay(fn func() bool) ServeOption {
	return serveOptionFunc(func(s *serveOptions) { s.skipDrainDelay = fn })
}

// LoggerOption 指定日志记录器，既是 DefaultsOption 也是 ServeOption，见 Logger。
type LoggerOption struct{ logger *slog.Logger }

// applyDefaults 设置 Defaults 的日志器。
func (o LoggerOption) applyDefaults(d *defaultsOptions) { d.logger = o.logger }

// applyServe 设置 Serve 的日志器。
func (o LoggerOption) applyServe(s *serveOptions) { s.logger = o.logger }

// Logger 让 Defaults 的访问日志和 panic 记录、Serve 的排空和停止记录写到 l，默认是 slog.Default()。
// 通常只在测试中使用：应用用 slog.SetDefault 设置全局的记录器。
func Logger(l *slog.Logger) LoggerOption { return LoggerOption{logger: l} }

// languagesOption 是 Languages 返回的选项。
type languagesOption struct{ bundle *goi18n.Bundle }

// applyDefaults 设置 Defaults 使用的消息目录。
func (o languagesOption) applyDefaults(d *defaultsOptions) { d.languages = o.bundle }

// Languages 让 Defaults 中的 Locale 按 bundle 协商请求语言、翻译错误消息，见 i18n 包。
// 不传时只启用框架内置的简体中文消息（zh-CN）。
func Languages(bundle *goi18n.Bundle) DefaultsOption { return languagesOption{bundle: bundle} }

// bodyLimitForOption 是 BodyLimitFor 返回的选项。
type bodyLimitForOption struct{ fn func(*http.Request) int64 }

// applyDefaults 设置 Defaults 中按请求覆盖请求体上限的函数。
func (o bodyLimitForOption) applyDefaults(d *defaultsOptions) { d.bodyLimitFor = o.fn }

// BodyLimitFor 按请求动态决定请求体字节上限（例如只为文件上传路径放宽上限，其余路径保持默认的 1 MiB）。
// fn 返回大于 0 的值时替代 cfg.BodyLimit.MaxBytes，返回不大于 0 的值时沿用默认上限。
func BodyLimitFor(fn func(*http.Request) int64) DefaultsOption {
	return bodyLimitForOption{fn: fn}
}

// defaultsOptionFunc 把函数适配为 DefaultsOption。
type defaultsOptionFunc func(*defaultsOptions)

// applyDefaults 应用默认中间件选项函数。
func (f defaultsOptionFunc) applyDefaults(d *defaultsOptions) { f(d) }

// BeforeAccessLog 在 Defaults 中的 Locale 之后、AccessLog 之前插入中间件，
// 适合在访问日志记录前清洗查询参数中的密钥（例如把 ?key= 移入请求头），无需拆开重写整套默认中间件。
func BeforeAccessLog(mws ...func(http.Handler) http.Handler) DefaultsOption {
	return defaultsOptionFunc(func(d *defaultsOptions) {
		for _, mw := range mws {
			if mw != nil {
				d.beforeAccessLog = append(d.beforeAccessLog, mw)
			}
		}
	})
}

// WithCompress 在 Defaults 的 Recover 之后自动安装响应压缩中间件（Compress）。
func WithCompress() DefaultsOption {
	return defaultsOptionFunc(func(d *defaultsOptions) { d.compress = true })
}

// WithContext 在 Defaults 末尾安装请求上下文包装中间件（Context），例如传入 database.TrackPrimary
// 为每个请求开启读写分离写后读主库跟踪。
func WithContext(fn func(context.Context) context.Context) DefaultsOption {
	return defaultsOptionFunc(func(d *defaultsOptions) {
		if fn != nil {
			d.ctxWraps = append(d.ctxWraps, fn)
		}
	})
}
