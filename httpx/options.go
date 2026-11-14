package httpx

import (
	"log/slog"

	goi18n "github.com/nicksnyder/go-i18n/v2/i18n"
)

// DefaultsOption 是 Defaults 的选项：Logger、Languages。
type DefaultsOption interface{ applyDefaults(*defaultsOptions) }

// ServeOption 是 Serve 和 ListenAndServe 的选项：Logger、OnDraining。
type ServeOption interface{ applyServe(*serveOptions) }

type defaultsOptions struct {
	logger    *slog.Logger
	languages *goi18n.Bundle
}

type serveOptions struct {
	logger     *slog.Logger
	onDraining func()
}

// LoggerOption 指定日志记录器，既是 DefaultsOption 也是 ServeOption，见 Logger。
type LoggerOption struct{ logger *slog.Logger }

func (o LoggerOption) applyDefaults(d *defaultsOptions) { d.logger = o.logger }
func (o LoggerOption) applyServe(s *serveOptions)       { s.logger = o.logger }

// Logger 让 Defaults 的访问日志和 panic 记录、Serve 的排空和停止记录写到 l，默认是 slog.Default()。
// 通常只在测试中使用：应用用 slog.SetDefault 设置全局的记录器。
func Logger(l *slog.Logger) LoggerOption { return LoggerOption{logger: l} }

type languagesOption struct{ bundle *goi18n.Bundle }

func (o languagesOption) applyDefaults(d *defaultsOptions) { d.languages = o.bundle }

// Languages 让 Defaults 中的 Locale 按 bundle 协商请求语言、翻译错误消息，见 i18n 包。
// 不传时只使用框架内置的中英文消息，默认语言为 zh-CN。
func Languages(bundle *goi18n.Bundle) DefaultsOption { return languagesOption{bundle: bundle} }

type onDrainingOption struct{ fn func() }

func (o onDrainingOption) applyServe(s *serveOptions) { s.onDraining = o.fn }

// OnDraining 让 Serve 在开始排空请求之前调用 fn，例如 Draining.Set，让就绪检查随即失败。
func OnDraining(fn func()) ServeOption { return onDrainingOption{fn: fn} }
