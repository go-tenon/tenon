package natsrpc

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/cockroachdb/errors"
	"github.com/nats-io/nats.go"
	"go.opentelemetry.io/otel/propagation"

	"github.com/go-tenon/tenon/errorx"
	"github.com/go-tenon/tenon/i18n"
	"github.com/go-tenon/tenon/logging"
)

// DefaultCallTimeout 是 ctx 没有截止时间时 Call 等待响应的时间。
const DefaultCallTimeout = 10 * time.Second

// ErrNoResponders 表示 subject 上没有在线的实例：服务没有运行，或者 subject 写错了。请求没有送到，可以稍后重试。
var ErrNoResponders = errorx.Define(errorx.Unavailable, "rpc.no_responders")

// CallOption 调整一次 Call。
type CallOption func(*nats.Msg)

// Token 设置请求头 Authorization: Bearer <token>。
func Token(token string) CallOption {
	return func(m *nats.Msg) { m.Header.Set(HeaderAuthorization, "Bearer "+token) }
}

// Header 设置一个请求头，例如多租户应用约定的 X-Tenant。名字区分大小写。
func Header(key, value string) CallOption {
	return func(m *nats.Msg) { m.Header.Set(key, value) }
}

// RemoteError 是服务端返回的错误响应。Call 返回的错误包装着它，用 errors.As 取出。
type RemoteError struct {
	Subject   string  // 调用的端点。
	Problem   Problem // 错误响应体；响应体不是 JSON 时只有 Status 和 Title，取自响应头。
	RequestID string  // 这次调用的 ID，与服务端日志关联。
}

func (e *RemoteError) Error() string {
	msg := fmt.Sprintf("natsrpc: %s: %d %s", e.Subject, e.Problem.Status, e.Problem.Title)
	if e.Problem.Code != "" {
		msg += " (" + e.Problem.Code + ")"
	}
	if e.Problem.Detail != "" && e.Problem.Detail != e.Problem.Title {
		msg += ": " + e.Problem.Detail
	}
	if e.RequestID != "" {
		msg += " [request " + e.RequestID + "]"
	}
	return msg
}

// Call 调用 subject 上的端点：把 in 编码为 JSON 作为请求体，成功时把响应体解码为 Out；Out 是 struct{} 时忽略响应体。
// nc 通常是 natsx.Connect 返回的连接。
//
// ctx 的剩余时间就是等待响应的时间，没有截止时间时是 DefaultCallTimeout，它同时作为 X-Timeout 告诉服务端。
// ctx 中的请求 ID（logging.RequestID）、链路和语言（i18n.Locale）随请求带上，一次请求和它引起的远程调用因此共用请求 ID。
//
// 失败时返回的错误：
//   - 服务端返回了错误：包装着 *RemoteError。errorx.CodeOf 是服务端的错误码，errorx.KindOf 是状态码对应的分类，
//     errors.Is 按错误码匹配本地用 errorx.Define 定义的同名模板；errorx.PublicOf、errorx.RetryAfterOf 也照常可用。
//   - 没有在线的实例：满足 errors.Is(err, ErrNoResponders)。
//   - 超时或 ctx 被取消：满足 errors.Is(err, context.DeadlineExceeded) 或 context.Canceled。服务端返回 504
//     （处理超过了时限）时同样满足 errors.Is(err, context.DeadlineExceeded)。结果不确定，只有幂等的端点可以直接重试。
func Call[Out any](ctx context.Context, nc *nats.Conn, subject string, in any, opts ...CallOption) (Out, error) {
	var out Out
	body, err := json.Marshal(in)
	if err != nil {
		return out, errors.Wrapf(err, "natsrpc: encode request to %s", subject)
	}
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, DefaultCallTimeout)
		defer cancel()
	}
	req := nats.NewMsg(subject)
	req.Data = body
	if id := logging.RequestID(ctx); id != "" {
		req.Header.Set(HeaderRequestID, id)
	}
	if locale := i18n.Locale(ctx); locale != "" {
		req.Header.Set(HeaderAcceptLanguage, locale)
	}
	propagation.TraceContext{}.Inject(ctx, propagation.HeaderCarrier(req.Header))
	if deadline, ok := ctx.Deadline(); ok {
		req.Header.Set(HeaderTimeout, strconv.FormatInt(max(time.Until(deadline).Milliseconds(), 1), 10))
	}
	for _, opt := range opts {
		opt(req)
	}

	resp, err := nc.RequestMsgWithContext(ctx, req)
	switch {
	case errors.Is(err, nats.ErrNoResponders):
		return out, ErrNoResponders.Wrap(errors.Wrapf(err, "natsrpc: %s", subject))
	case err != nil:
		return out, errors.Wrapf(err, "natsrpc: call %s", subject)
	}
	if code := resp.Header.Get(HeaderErrorCode); code != "" {
		return out, remoteError(subject, code, resp)
	}
	if _, empty := any(out).(struct{}); empty || len(resp.Data) == 0 {
		return out, nil
	}
	if err := json.Unmarshal(resp.Data, &out); err != nil {
		return out, errors.Wrapf(err, "natsrpc: decode response from %s", subject)
	}
	return out, nil
}

// remoteError 把错误响应还原为 errorx 的错误，包装着 *RemoteError。
func remoteError(subject, code string, resp *nats.Msg) error {
	remote := &RemoteError{Subject: subject, RequestID: resp.Header.Get(HeaderRequestID)}
	if json.Unmarshal(resp.Data, &remote.Problem) != nil || remote.Problem.Status == 0 {
		status, _ := strconv.Atoi(code)
		remote.Problem = Problem{Status: status, Title: resp.Header.Get(HeaderError)}
	}
	var opts []errorx.Option
	for k, v := range remote.Problem.Params {
		opts = append(opts, errorx.Public(k, v))
	}
	if secs, err := strconv.Atoi(resp.Header.Get(HeaderRetryAfter)); err == nil && secs > 0 {
		opts = append(opts, errorx.RetryAfter(time.Duration(secs)*time.Second))
	}
	err := errorx.Define(kindOf(remote.Problem.Status), remote.Problem.Code).Wrap(remote, opts...)
	if remote.Problem.Status == http.StatusGatewayTimeout {
		err = errors.Mark(err, context.DeadlineExceeded) // 与调用方自己的超时一样处理
	}
	return err
}
