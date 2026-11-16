package natsrpc

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"log/slog"
	"math"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/cockroachdb/errors"
	"github.com/danielgtaylor/huma/v2"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/micro"
	"github.com/nats-io/nuid"
	"go.opentelemetry.io/otel/propagation"

	"github.com/go-tenon/tenon/errorx"
	"github.com/go-tenon/tenon/form"
	"github.com/go-tenon/tenon/i18n"
	"github.com/go-tenon/tenon/identity"
	"github.com/go-tenon/tenon/internal/apischema"
	"github.com/go-tenon/tenon/logging"
)

// Serve 在 nc 上提供 s 的全部端点，直到 ctx 被取消，然后平滑停止并返回 nil；订阅失败时返回错误。
// nc 通常是 natsx.Connect 返回的连接，Serve 不关闭它。它是进程的一个运行部分，见 command.Daemon。
//
// 每个端点订阅自己的 subject，同一个服务的实例组成同一个队列组，每个请求只交给其中一个实例；
// 同时还应答服务协议的 $SRV.PING、$SRV.INFO 和 $SRV.STATS。
//
// 停止时先不再接收新的请求，处理完已经收到的；最多等待 Options.ShutdownTimeout，超时后取消处理中的调用的 ctx。
// 第二次调用时 panic。
func (s *Server) Serve(ctx context.Context, nc *nats.Conn) error {
	if nc == nil {
		panic("natsrpc: nil nats connection")
	}
	s.mu.Lock()
	if s.started {
		s.mu.Unlock()
		panic("natsrpc: Serve called twice")
	}
	s.started = true
	endpoints := s.endpoints
	s.mu.Unlock()

	r := &runner{
		Server:  s,
		id:      nuid.Next(),
		started: time.Now().UTC(),
		slots:   make(chan struct{}, s.opts.Concurrency),
	}
	// 调用的 ctx 不随 Serve 的 ctx 取消：停止时让处理中的调用完成，超时后才取消。
	root, cancel := context.WithCancel(context.WithoutCancel(ctx))
	defer cancel()

	var subs []*nats.Subscription
	unsubscribe := func() {
		for _, sub := range subs {
			_ = sub.Unsubscribe()
		}
	}
	for _, ep := range endpoints {
		sub, err := nc.QueueSubscribe(ep.subject, micro.DefaultQueueGroup, func(m *nats.Msg) { r.dispatch(root, ep, m) })
		if err != nil {
			unsubscribe()
			return errors.Wrapf(err, "natsrpc: subscribe %s", ep.subject)
		}
		subs = append(subs, sub)
	}
	control, err := r.subscribeControl(nc, endpoints)
	if err != nil {
		unsubscribe()
		return err
	}
	if err := nc.FlushTimeout(5 * time.Second); err != nil {
		unsubscribe()
		for _, sub := range control {
			_ = sub.Unsubscribe()
		}
		return errors.Wrap(err, "natsrpc: subscribe")
	}

	<-ctx.Done()
	// 先不再接收新的请求：Drain 让已经到达客户端的请求照常处理。
	for _, sub := range subs {
		_ = sub.Drain()
	}
	for _, sub := range control {
		_ = sub.Unsubscribe()
	}
	deadline := time.Now().Add(s.opts.ShutdownTimeout)
	for _, sub := range subs {
		for sub.IsValid() && time.Now().Before(deadline) {
			time.Sleep(10 * time.Millisecond)
		}
	}
	done := make(chan struct{})
	go func() {
		r.inflight.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Until(deadline)):
		cancel()
		<-done
	}
	return nil
}

// runner 是一次 Serve 的状态：实例 ID、启动时间、并发名额和处理中的调用。
type runner struct {
	*Server
	id       string
	started  time.Time
	slots    chan struct{}
	inflight sync.WaitGroup
}

// dispatch 在 NATS 的订阅回调中执行：取得一个并发名额，在新的 goroutine 中处理请求。
// 名额用完时回调在这里等待，后续的请求在 NATS 客户端中排队。
func (r *runner) dispatch(root context.Context, ep *endpoint, m *nats.Msg) {
	if m.Reply == "" {
		r.logger.Warn("rpc request without a reply subject dropped", "rpc.endpoint", ep.spec.Name)
		return
	}
	r.slots <- struct{}{}
	r.inflight.Add(1)
	go func() {
		defer func() {
			<-r.slots
			r.inflight.Done()
		}()
		start := time.Now()
		resp, failed := r.handle(root, ep, m)
		if err := m.RespondMsg(resp); err != nil {
			r.logger.Warn("rpc respond failed", "rpc.endpoint", ep.spec.Name, "error", err)
		}
		ep.stats.record(time.Since(start), failed)
	}()
}

// handle 处理一个请求，返回响应；failed 是失败的描述，成功时为空。
func (r *runner) handle(root context.Context, ep *endpoint, m *nats.Msg) (resp *nats.Msg, failed string) {
	resp = &nats.Msg{Header: nats.Header{}}
	ctx := root
	id := m.Header.Get(HeaderRequestID)
	if !validRequestID(id) {
		id = rand.Text()
	}
	ctx = logging.WithRequestID(ctx, id)
	ctx = logging.WithAttrs(ctx, slog.String("rpc.endpoint", ep.spec.Name))
	resp.Header.Set(HeaderRequestID, id)
	ctx = propagation.TraceContext{}.Extract(ctx, propagation.HeaderCarrier(m.Header))

	timeout := r.opts.Timeout
	if ms, err := strconv.ParseInt(m.Header.Get(HeaderTimeout), 10, 64); err == nil && ms > 0 && time.Duration(ms)*time.Millisecond < timeout {
		timeout = time.Duration(ms) * time.Millisecond
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	if b := r.opts.Languages; b != nil {
		locale := i18n.Match(b, m.Header.Get(HeaderAcceptLanguage))
		ctx = i18n.WithLocale(ctx, b, locale)
		resp.Header.Set(HeaderContentLanguage, locale)
	}
	if r.opts.Tenant != nil {
		ctx = identity.WithTenant(ctx, r.opts.Tenant(m.Header))
	}

	fail := func(p *Problem, err error) (*nats.Msg, string) {
		if err != nil {
			if d := errorx.RetryAfterOf(err); d > 0 {
				resp.Header.Set(HeaderRetryAfter, strconv.Itoa(int(math.Ceil(d.Seconds()))))
			}
		}
		resp.Header.Set(HeaderErrorCode, strconv.Itoa(p.Status))
		resp.Header.Set(HeaderError, p.Title)
		resp.Data, _ = json.Marshal(p)
		return resp, strconv.Itoa(p.Status) + " " + p.Title
	}

	if !ep.spec.Public {
		sub, err := r.authenticate(ctx, ep, m.Header)
		if err != nil {
			if identity.Failed(err) {
				r.logger.ErrorContext(ctx, "authentication backend failed", "error", err)
			}
			return fail(r.problem(ctx, ep, err), err)
		}
		ctx = identity.WithSubject(ctx, sub)
	}

	data, fields, err := ep.handle(ctx, m.Data)
	switch {
	case errors.Is(err, apischema.ErrInvalidJSON):
		return fail(&Problem{Status: http.StatusBadRequest, Title: http.StatusText(http.StatusBadRequest), Detail: "request body is " + err.Error()}, nil)
	case len(fields) > 0:
		detail := http.StatusText(http.StatusUnprocessableEntity)
		if msg := i18n.T(ctx, "validation.failed", nil); msg != "validation.failed" {
			detail = msg
		}
		return fail(&Problem{Status: http.StatusUnprocessableEntity, Title: http.StatusText(http.StatusUnprocessableEntity), Detail: detail, Errors: fields}, nil)
	case err != nil && ctx.Err() != nil && (errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled)):
		// 处理超过了时限（调用方的 X-Timeout 或 Options.Timeout），或者服务停止时等待超时被取消：
		// 不是代码的错误，处理可能完成了一部分，结果不确定，调用方按超时处理。
		status := http.StatusGatewayTimeout
		r.logger.WarnContext(ctx, "rpc call did not finish in time", "rpc.endpoint", ep.spec.Name, "error", err)
		return fail(&Problem{Status: status, Title: http.StatusText(status), Detail: http.StatusText(status)}, nil)
	case err != nil:
		return fail(r.problem(ctx, ep, err), err)
	}
	resp.Data = data
	return resp, ""
}

// authenticate 按 identity.Establish 确立主体，规则与 HTTP 接口的 rest.RequireBearer 相同。
func (r *runner) authenticate(ctx context.Context, ep *endpoint, h nats.Header) (identity.Subject, error) {
	token := bearerToken(h.Get(HeaderAuthorization))
	authenticate := func(ctx context.Context) (identity.Subject, error) {
		if token == "" {
			return identity.Subject{}, nil
		}
		return r.opts.Validate(ctx, token)
	}
	return identity.Establish(ctx, authenticate, r.opts.Accounts.Resolve, ep.spec.Permissions...)
}

// problem 把 err 转换为错误响应：业务错误带错误码、公开参数和按请求语言翻译的文案；
// 系统故障和没有错误码的错误记录在日志中，响应只有状态码的描述。
func (r *runner) problem(ctx context.Context, ep *endpoint, err error) *Problem {
	code := errorx.CodeOf(err)
	kind := errorx.KindOf(err)
	status := statusOf(kind)
	if code == "" {
		status = http.StatusInternalServerError
	}
	if code == "" || kind.Systemic() {
		r.logger.ErrorContext(ctx, "rpc call failed", "rpc.endpoint", ep.spec.Name, "error", err)
	}
	p := &Problem{Status: status, Title: http.StatusText(status), Detail: http.StatusText(status), Code: code}
	if code == "" {
		return p
	}
	if msg, _ := i18n.ErrorMessage(ctx, err); msg != "" {
		p.Detail = msg
	}
	if params := errorx.PublicOf(err); len(params) > 0 {
		if _, jerr := json.Marshal(params); jerr == nil {
			p.Params = params
		}
	}
	return p
}

// describe 把参数校验的错误转换为字段错误，提示按 ctx 的语言翻译，规则与 HTTP 接口相同。
func describe(ctx context.Context, details []*huma.ErrorDetail) []FieldError {
	out := make([]FieldError, len(details))
	for i, d := range details {
		fe := form.Describe(ctx, d)
		value := d.Value
		if fe.Location != d.Location {
			value = nil // 必填错误的位置换成了缺少的字段，原来的值是父对象
		}
		out[i] = FieldError{Message: fe.Message, Location: fe.Location, Value: value, Code: fe.Code}
	}
	return out
}

// call 调用 fn，把 panic 转换为错误。
func call[I, O any](ctx context.Context, fn func(context.Context, I) (O, error), in I) (out O, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = errors.Newf("natsrpc: panic: %v", r)
		}
	}()
	return fn(ctx, in)
}

func marshal(v any) ([]byte, error) {
	data, err := json.Marshal(v)
	return data, errors.Wrap(err, "natsrpc: encode response")
}

// bearerToken 取出 Authorization: Bearer 后的 token，Bearer 大小写不敏感；不是 Bearer 时返回空字符串。
func bearerToken(h string) string {
	scheme, token, ok := strings.Cut(strings.TrimSpace(h), " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") {
		return ""
	}
	return strings.TrimSpace(token)
}

// validRequestID 报告 id 是否是合法的请求 ID：1–128 个不含空格的可打印 ASCII 字符，与 HTTP 接口相同。
func validRequestID(id string) bool {
	if len(id) == 0 || len(id) > 128 {
		return false
	}
	for _, c := range []byte(id) {
		if c <= ' ' || c > '~' {
			return false
		}
	}
	return true
}
