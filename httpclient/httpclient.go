// Package httpclient 构造适合生产环境的标准库 *http.Client。
//
// 与 http.DefaultClient 相比：
//
//   - 有整体超时（默认 30s）；
//   - 每个 host 保留更多空闲连接，高并发调用同一下游时能复用连接；
//   - 每次请求记录一条日志（method、host、path、status、duration），
//     不记录 query 与请求/响应体；
//   - 传输层错误归入 errs 分类：超时为 DeadlineExceeded，
//     连接失败、DNS 失败、连接被断开为 Unavailable，调用方取消为 Canceled。
//
// 非 2xx 响应不是传输层错误，调用方用 Check 检查：
//
//	resp, err := client.Do(req)
//	if err != nil {
//		return err
//	}
//	defer resp.Body.Close()
//	if err := httpclient.Check(resp); err != nil {
//		return err
//	}
//	return json.NewDecoder(resp.Body).Decode(&out)
//
// 本包不做自动重试：非幂等请求重试不安全，请求体也未必可重放。
// 需要时在调用处用 backoff 等库，根据 errs.CodeOf 判断是否值得重试。
package httpclient

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/shellphy-kit/tools/errs"
)

// DefaultTimeout 是 Options.Timeout 为零时使用的整体超时。
const DefaultTimeout = 30 * time.Second

// Options 是 New 的参数，零值即可用。
type Options struct {
	// Timeout 为整个请求（含读取响应体）的超时，0 表示 DefaultTimeout，
	// 负数表示不设超时（例如下载大文件时改用 context 控制）。
	Timeout time.Duration

	// Transport 为底层 RoundTripper，nil 表示 NewTransport()。
	// 需要叠加链路追踪等中间件时传入，例如
	// otelhttp.NewTransport(httpclient.NewTransport())。
	Transport http.RoundTripper

	// Logger 用于请求日志，nil 表示每次请求时取 slog.Default()。
	Logger *slog.Logger
}

// New 返回按 opts 配置的 *http.Client。
func New(opts Options) *http.Client {
	timeout := opts.Timeout
	switch {
	case timeout == 0:
		timeout = DefaultTimeout
	case timeout < 0:
		timeout = 0
	}
	base := opts.Transport
	if base == nil {
		base = NewTransport()
	}
	return &http.Client{
		Timeout:   timeout,
		Transport: &transport{next: base, logger: opts.Logger},
	}
}

// NewTransport 返回 http.DefaultTransport 的副本，并把每个 host 的空闲连接数
// 从默认的 2 提高到 100。返回值可以继续按需修改。
func NewTransport() *http.Transport {
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.MaxIdleConnsPerHost = 100
	return t
}

// transport 记录请求日志，并给传输层错误加上 errs 分类。
type transport struct {
	next   http.RoundTripper
	logger *slog.Logger
}

func (t *transport) RoundTrip(req *http.Request) (*http.Response, error) {
	start := time.Now()
	resp, err := t.next.RoundTrip(req)
	err = classify(err)

	ctx := req.Context()
	attrs := []slog.Attr{
		slog.String("method", req.Method),
		slog.String("host", req.URL.Host),
		slog.String("path", req.URL.Path),
		slog.Duration("duration", time.Since(start)),
	}
	level := slog.LevelDebug
	msg := "http client request"
	switch {
	case err != nil:
		msg = "http client request failed"
		attrs = append(attrs, slog.Any("err", err))
		if errs.CodeOf(err) != errs.Canceled {
			level = slog.LevelWarn
		}
	case resp.StatusCode >= 500:
		level = slog.LevelWarn
		attrs = append(attrs, slog.Int("status", resp.StatusCode))
	default:
		attrs = append(attrs, slog.Int("status", resp.StatusCode))
	}

	logger := t.logger
	if logger == nil {
		logger = slog.Default()
	}
	logger.LogAttrs(ctx, level, msg, attrs...)

	return resp, err
}

// classify 给传输层错误加上 errs 分类。context 取消与超时不处理，
// 因为 errs.CodeOf 已能识别；无法判断的错误原样返回（即 Internal）。
func classify(err error) error {
	if err == nil ||
		errors.Is(err, context.Canceled) ||
		errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	if ne, ok := errors.AsType[net.Error](err); ok {
		if ne.Timeout() {
			return errs.Wrap(err, errs.DeadlineExceeded, "")
		}
		return errs.Wrap(err, errs.Unavailable, "")
	}
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		// 下游在返回响应前断开了连接。
		return errs.Wrap(err, errs.Unavailable, "")
	}
	return err
}
