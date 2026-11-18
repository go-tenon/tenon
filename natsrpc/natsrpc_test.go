package natsrpc_test

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/fstest"
	"time"

	"github.com/cockroachdb/errors"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/micro"

	"github.com/go-tenon/tenon/errorx"
	"github.com/go-tenon/tenon/i18n"
	"github.com/go-tenon/tenon/identity"
	"github.com/go-tenon/tenon/internal/natstest"
	"github.com/go-tenon/tenon/logging"
	"github.com/go-tenon/tenon/logging/logtest"
	"github.com/go-tenon/tenon/natsrpc"
)

var errSlugTaken = errorx.Define(errorx.Conflict, "article.slug_taken")

var (
	editor = identity.Subject{Type: "editor", ID: "e1", Permissions: []string{"articles:*"}}
	reader = identity.Subject{Type: "editor", ID: "e2", Permissions: []string{"articles:read"}}
	// acmeEditor 的凭据绑定了租户 acme，只能在这个租户中使用。
	acmeEditor = identity.Subject{Type: "editor", ID: "e3", Tenant: "acme", Permissions: []string{"articles:*"}}
)

type PublishInput struct {
	Title string `json:"title" minLength:"1" maxLength:"20"`
	Slug  string `json:"slug,omitempty"`
	Fail  string `json:"fail,omitempty" enum:"conflict,internal,panic,busy"`
	Sleep int    `json:"sleep,omitempty" doc:"处理前等待的毫秒数"`
}

type Published struct {
	Title     string `json:"title"`
	By        string `json:"by"`
	Tenant    string `json:"tenant"`
	RequestID string `json:"request_id"`
	Deadline  int64  `json:"deadline_ms"` // 剩余的处理时间
}

// fixture 是一个运行中的 RPC 服务和调用它的连接。
type fixture struct {
	prefix string
	nc     *nats.Conn
	rec    *logtest.Recorder
	calls  atomic.Int64
}

func (f *fixture) subject(name string) string { return f.prefix + "." + name }

// prefix 返回测试独用的 subject 前缀。
func prefix() string { return "t" + strings.ToLower(rand.Text()[:10]) + ".v1" }

func tokens(_ context.Context, token string) (identity.Subject, error) {
	switch token {
	case "editor":
		return editor, nil
	case "reader":
		return reader, nil
	case "acme-editor":
		return acmeEditor, nil
	case "broken":
		return identity.Subject{}, errorx.Define(errorx.Unavailable, "test.auth_down").New()
	}
	return identity.Subject{}, nil
}

func newServer(f *fixture, opts natsrpc.Options) *natsrpc.Server {
	opts.Prefix = f.prefix
	opts.Logger = f.rec.Logger()
	if opts.Validate == nil {
		opts.Validate = tokens
		opts.Accounts = identity.Accounts{"editor": nil}
	}
	srv := natsrpc.New(opts)
	natsrpc.Register(srv, natsrpc.Endpoint{Name: "articles.publish", Description: "发布文章", Permissions: []string{"articles:publish"}},
		func(ctx context.Context, in PublishInput) (*Published, error) {
			f.calls.Add(1)
			if in.Sleep > 0 {
				select {
				case <-time.After(time.Duration(in.Sleep) * time.Millisecond):
				case <-ctx.Done():
					return nil, ctx.Err()
				}
			}
			switch in.Fail {
			case "conflict":
				return nil, errSlugTaken.New(errorx.Public("slug", in.Slug), errorx.Field("slug"))
			case "internal":
				return nil, errors.New("database password is hunter2")
			case "panic":
				panic("boom")
			case "busy":
				return nil, errorx.Define(errorx.ResourceExhausted, "rate_limit.exceeded").New(errorx.RetryAfter(1500 * time.Millisecond))
			}
			sub, _ := identity.FromContext(ctx)
			deadline, _ := ctx.Deadline()
			return &Published{Title: in.Title, By: sub.ID, Tenant: identity.TenantFromContext(ctx),
				RequestID: logging.RequestID(ctx), Deadline: time.Until(deadline).Milliseconds()}, nil
		})
	natsrpc.Register(srv, natsrpc.Endpoint{Name: "health.ping", Public: true, Idempotent: true},
		func(context.Context, struct{}) (struct{}, error) { return struct{}{}, nil })
	return srv
}

// start 在新的连接上运行一个实例，测试结束时停止它。
func start(t *testing.T, f *fixture, opts natsrpc.Options) {
	t.Helper()
	srv := newServer(f, opts)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- srv.Serve(ctx, natstest.Connect(t)) }()
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil {
			t.Errorf("serve: %v", err)
		}
	})
	waitReady(t, f)
}

// waitReady 等到服务能应答：订阅在 Serve 返回之前在后台完成。
func waitReady(t *testing.T, f *fixture) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
		_, err := natsrpc.Call[struct{}](ctx, f.nc, f.subject("health.ping"), struct{}{})
		cancel()
		if err == nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("service not ready: %v", err)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func setup(t *testing.T, opts natsrpc.Options) *fixture {
	t.Helper()
	f := &fixture{prefix: prefix(), nc: natstest.Connect(t), rec: logtest.New()}
	start(t, f, opts)
	return f
}

func publish(t *testing.T, f *fixture, in PublishInput, opts ...natsrpc.CallOption) (*Published, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return natsrpc.Call[*Published](ctx, f.nc, f.subject("articles.publish"), in, opts...)
}

func remote(t *testing.T, err error) *natsrpc.RemoteError {
	t.Helper()
	var r *natsrpc.RemoteError
	if !errors.As(err, &r) {
		t.Fatalf("not a remote error: %v", err)
	}
	return r
}

func TestCall(t *testing.T) {
	t.Parallel()
	f := setup(t, natsrpc.Options{Tenant: func(h nats.Header) string { return h.Get("X-Tenant") }})
	ctx := logging.WithRequestID(context.Background(), "req-42")
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	out, err := natsrpc.Call[*Published](ctx, f.nc, f.subject("articles.publish"), PublishInput{Title: "hello"},
		natsrpc.Token("acme-editor"), natsrpc.Header("X-Tenant", "acme"))
	if err != nil {
		t.Fatal(err)
	}
	// 主体、租户和请求 ID 都传到了处理函数；处理的时限是调用方剩余的时间（2 秒以内）。
	if out.Title != "hello" || out.By != "e3" || out.Tenant != "acme" || out.RequestID != "req-42" || out.Deadline <= 0 || out.Deadline > 2000 {
		t.Fatalf("out %+v", out)
	}
	// 凭据绑定的租户与请求的租户不同：拒绝。
	_, err = natsrpc.Call[*Published](ctx, f.nc, f.subject("articles.publish"), PublishInput{Title: "hello"},
		natsrpc.Token("acme-editor"), natsrpc.Header("X-Tenant", "globex"))
	if errorx.KindOf(err) != errorx.PermissionDenied {
		t.Fatalf("other tenant: %v", err)
	}
	// 没有返回值的端点：响应体为空。
	if _, err := natsrpc.Call[struct{}](ctx, f.nc, f.subject("health.ping"), struct{}{}); err != nil {
		t.Fatal(err)
	}
}

func TestAuthentication(t *testing.T) {
	t.Parallel()
	f := setup(t, natsrpc.Options{})
	for name, tc := range map[string]struct {
		token  string
		status int
		code   string
		kind   errorx.Kind
	}{
		"no token":           {"", 401, "auth.unauthenticated", errorx.Unauthenticated},
		"unknown token":      {"nobody", 401, "auth.unauthenticated", errorx.Unauthenticated},
		"missing permission": {"reader", 403, "auth.forbidden", errorx.PermissionDenied},
		"backend down":       {"broken", 503, "test.auth_down", errorx.Unavailable},
	} {
		var opts []natsrpc.CallOption
		if tc.token != "" {
			opts = append(opts, natsrpc.Token(tc.token))
		}
		_, err := publish(t, f, PublishInput{Title: "x"}, opts...)
		r := remote(t, err)
		if r.Problem.Status != tc.status || errorx.CodeOf(err) != tc.code || errorx.KindOf(err) != tc.kind {
			t.Errorf("%s: %+v code=%s kind=%v", name, r.Problem, errorx.CodeOf(err), errorx.KindOf(err))
		}
	}
	if f.calls.Load() != 0 {
		t.Fatal("handler called without authorization")
	}
	if !f.rec.Has(slog.LevelError, "authentication backend failed") {
		t.Fatal("backend failure not logged")
	}
}

func TestValidation(t *testing.T) {
	t.Parallel()
	f := setup(t, natsrpc.Options{})
	_, err := publish(t, f, PublishInput{Title: strings.Repeat("x", 30)}, natsrpc.Token("editor"))
	r := remote(t, err)
	if r.Problem.Status != 422 || len(r.Problem.Errors) != 1 || r.Problem.Errors[0].Location != "body.title" ||
		r.Problem.Errors[0].Code != "validation.max_length" || errorx.KindOf(err) != errorx.InvalidArgument {
		t.Fatalf("too long: %+v", r.Problem)
	}

	// 请求体不是合法的 JSON：用原始的 NATS 请求发送。
	req := nats.NewMsg(f.subject("articles.publish"))
	req.Data = []byte("{not json")
	req.Header.Set(natsrpc.HeaderAuthorization, "Bearer editor")
	resp, err := f.nc.RequestMsg(req, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Header.Get(natsrpc.HeaderErrorCode) != "400" || resp.Header.Get(natsrpc.HeaderError) != "Bad Request" {
		t.Fatalf("invalid json: %v %s", resp.Header, resp.Data)
	}
	if f.calls.Load() != 0 {
		t.Fatal("handler called with invalid input")
	}
}

func TestErrors(t *testing.T) {
	t.Parallel()
	languages, err := i18n.New(i18n.LocaleEn, fstest.MapFS{
		"zh-CN.yaml": {Data: []byte("article.slug_taken: slug“{{.slug}}”已被使用\n")},
		"en.yaml":    {Data: []byte("article.slug_taken: slug {{.slug}} is taken\n")},
	})
	if err != nil {
		t.Fatal(err)
	}
	f := setup(t, natsrpc.Options{Languages: languages})
	editorToken := natsrpc.Token("editor")

	// 业务错误：状态码、错误码、公开参数和按 Accept-Language 翻译的文案；本地定义的同名模板可以用 errors.Is 匹配。
	_, err = publish(t, f, PublishInput{Title: "x", Slug: "hello", Fail: "conflict"}, editorToken, natsrpc.Header(natsrpc.HeaderAcceptLanguage, "zh-CN"))
	r := remote(t, err)
	if r.Problem.Status != 409 || r.Problem.Code != "article.slug_taken" || r.Problem.Detail != "slug“hello”已被使用" ||
		r.Problem.Params["slug"] != "hello" || r.RequestID == "" {
		t.Fatalf("conflict: %+v", r)
	}
	if !errors.Is(err, errSlugTaken) || errorx.KindOf(err) != errorx.Conflict || errorx.PublicOf(err)["slug"] != "hello" {
		t.Fatalf("conflict not mapped back: %v", err)
	}
	// 业务错误指出的字段：errors 中带着同一个错误码的那一项，调用方用 errorx.FieldOf 取回。
	if len(r.Problem.Errors) != 1 || r.Problem.Errors[0].Location != "body.slug" || r.Problem.Errors[0].Code != "article.slug_taken" ||
		r.Problem.Errors[0].Message != r.Problem.Detail || errorx.FieldOf(err) != "slug" {
		t.Fatalf("field: %+v %q", r.Problem.Errors, errorx.FieldOf(err))
	}

	// Retry-After 随错误带回。
	_, err = publish(t, f, PublishInput{Title: "x", Fail: "busy"}, editorToken)
	if errorx.KindOf(err) != errorx.ResourceExhausted || errorx.RetryAfterOf(err) != 2*time.Second {
		t.Fatalf("busy: %v retry=%v", err, errorx.RetryAfterOf(err))
	}

	// 内部错误和 panic：响应不含内部信息，完整错误带着请求 ID 记录在日志中。
	for _, fail := range []string{"internal", "panic"} {
		_, err := publish(t, f, PublishInput{Title: "x", Fail: fail}, editorToken)
		r := remote(t, err)
		if r.Problem.Status != 500 || r.Problem.Code != "" || strings.Contains(err.Error(), "hunter2") || strings.Contains(err.Error(), "boom") ||
			errorx.KindOf(err) != errorx.Internal {
			t.Fatalf("%s: %v", fail, err)
		}
	}
	logged := 0
	for _, rec := range f.rec.Records() {
		if rec.Message == "rpc call failed" && rec.Attr("rpc.endpoint") == "articles.publish" && rec.Attr(logging.AttrRequestID) != nil {
			logged++
		}
	}
	if logged != 2 {
		t.Fatalf("logged %d internal errors", logged)
	}
}

func TestNoResponders(t *testing.T) {
	t.Parallel()
	nc := natstest.Connect(t)
	_, err := natsrpc.Call[struct{}](context.Background(), nc, prefix()+".nothing", struct{}{})
	if !errors.Is(err, natsrpc.ErrNoResponders) || errorx.KindOf(err) != errorx.Unavailable {
		t.Fatalf("err %v", err)
	}
}

func TestTimeout(t *testing.T) {
	t.Parallel()
	f := setup(t, natsrpc.Options{Timeout: 300 * time.Millisecond})
	// 调用方先超时：服务端按 X-Timeout 同时结束处理，调用方看到的可能是自己的超时，也可能是服务端的 504，两者一样处理。
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	_, err := natsrpc.Call[*Published](ctx, f.nc, f.subject("articles.publish"), PublishInput{Title: "x", Sleep: 2000}, natsrpc.Token("editor"))
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("caller timeout: %v", err)
	}
	// 服务端的时限更短：返回 504，记 Warn 日志。
	_, err = publish(t, f, PublishInput{Title: "x", Sleep: 2000}, natsrpc.Token("editor"))
	if r := remote(t, err); r.Problem.Status != 504 || !errors.Is(err, context.DeadlineExceeded) || errorx.KindOf(err) != errorx.Unavailable {
		t.Fatalf("server timeout: %v", err)
	}
	if f.rec.Has(slog.LevelError, "rpc call failed") || !f.rec.Has(slog.LevelWarn, "rpc call did not finish in time") {
		t.Fatalf("records %+v", f.rec.Records())
	}
}

// 同一个实例并发处理请求。
func TestConcurrency(t *testing.T) {
	t.Parallel()
	f := setup(t, natsrpc.Options{})
	start := time.Now()
	var wg sync.WaitGroup
	for range 10 {
		wg.Go(func() {
			if _, err := publish(t, f, PublishInput{Title: "x", Sleep: 300}, natsrpc.Token("editor")); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("10 calls of 300ms took %v", d)
	}
}

// 两个实例组成队列组，请求分到两个实例上；服务发现列出两个实例的端点和统计。
func TestDiscoveryAndLoadBalancing(t *testing.T) {
	t.Parallel()
	f := &fixture{prefix: prefix(), nc: natstest.Connect(t), rec: logtest.New()}
	start(t, f, natsrpc.Options{Version: "1.2.0", Description: "博客"})
	start(t, f, natsrpc.Options{Version: "1.2.0", Description: "博客"})
	for range 40 {
		if _, err := publish(t, f, PublishInput{Title: "x"}, natsrpc.Token("editor")); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := publish(t, f, PublishInput{Title: "x"}); err == nil {
		t.Fatal("unauthenticated call succeeded")
	}
	service := strings.ReplaceAll(f.prefix, ".", "_")

	infos := collect[micro.Info](t, f.nc, "$SRV.INFO."+service, 2)
	for _, info := range infos {
		if info.Name != service || info.Version != "1.2.0" || info.Description != "博客" || info.Type != micro.InfoResponseType || len(info.Endpoints) != 2 {
			t.Fatalf("info %+v", info)
		}
		ep := info.Endpoints[0]
		if ep.Name != "articles_publish" || ep.Subject != f.subject("articles.publish") || ep.QueueGroup != "q" ||
			ep.Metadata[natsrpc.MetadataPermissions] != "articles:publish" || ep.Metadata[natsrpc.MetadataIdempotent] != "false" ||
			ep.Metadata[natsrpc.MetadataDescription] != "发布文章" || info.Endpoints[1].Metadata[natsrpc.MetadataIdempotent] != "true" {
			t.Fatalf("endpoint %+v", ep)
		}
	}
	if infos[0].ID == infos[1].ID {
		t.Fatal("instances share an ID")
	}

	stats := collect[micro.Stats](t, f.nc, "$SRV.STATS."+service, 2)
	requests, errs := 0, 0
	for _, s := range stats {
		ep := s.Endpoints[0]
		if ep.NumRequests == 0 {
			t.Fatalf("an instance handled no requests: %+v", ep)
		}
		requests += ep.NumRequests
		errs += ep.NumErrors
	}
	if requests != 41 || errs != 1 {
		t.Fatalf("requests=%d errors=%d", requests, errs)
	}
	if pings := collect[micro.Ping](t, f.nc, "$SRV.PING", 0); len(pings) < 2 {
		t.Fatalf("pings %+v", pings)
	}
}

// collect 发送一次服务发现的请求，收集 want 个应答（want 为 0 时收集 300ms 内的全部应答）。
func collect[T any](t *testing.T, nc *nats.Conn, subject string, want int) []T {
	t.Helper()
	inbox := nats.NewInbox()
	sub, err := nc.SubscribeSync(inbox)
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Unsubscribe()
	if err := nc.PublishRequest(subject, inbox, nil); err != nil {
		t.Fatal(err)
	}
	var out []T
	deadline := time.Now().Add(2 * time.Second)
	if want == 0 {
		deadline = time.Now().Add(300 * time.Millisecond)
	}
	for want == 0 || len(out) < want {
		m, err := sub.NextMsg(time.Until(deadline))
		if err != nil {
			if want == 0 {
				return out
			}
			t.Fatalf("%s: got %d of %d replies: %v", subject, len(out), want, err)
		}
		var v T
		if err := json.Unmarshal(m.Data, &v); err != nil {
			t.Fatal(err)
		}
		out = append(out, v)
	}
	return out
}

// 停止时处理完已经收到的请求，之后不再应答。
func TestGracefulStop(t *testing.T) {
	t.Parallel()
	f := &fixture{prefix: prefix(), nc: natstest.Connect(t), rec: logtest.New()}
	srv := newServer(f, natsrpc.Options{})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- srv.Serve(ctx, natstest.Connect(t)) }()
	waitReady(t, f)

	result := make(chan error, 1)
	go func() {
		_, err := publish(t, f, PublishInput{Title: "x", Sleep: 500}, natsrpc.Token("editor"))
		result <- err
	}()
	time.Sleep(100 * time.Millisecond) // 请求已经在处理中
	cancel()
	if err := <-result; err != nil {
		t.Fatalf("in-flight call: %v", err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if _, err := natsrpc.Call[struct{}](context.Background(), f.nc, f.subject("health.ping"), struct{}{}); !errors.Is(err, natsrpc.ErrNoResponders) {
		t.Fatalf("after stop: %v", err)
	}
}

func TestRegisterPanics(t *testing.T) {
	t.Parallel()
	ok := func(context.Context, struct{}) (struct{}, error) { return struct{}{}, nil }
	public := natsrpc.Endpoint{Name: "a.b", Public: true}
	for name, f := range map[string]func(){
		"bad prefix":  func() { natsrpc.New(natsrpc.Options{Prefix: "Blog.V1"}) },
		"bad version": func() { natsrpc.New(natsrpc.Options{Prefix: "blog.v1", Version: "1.0"}) },
		"bad name": func() {
			natsrpc.Register(natsrpc.New(natsrpc.Options{Prefix: "blog.v1"}), natsrpc.Endpoint{Name: "a..b", Public: true}, ok)
		},
		"wildcard": func() {
			natsrpc.Register(natsrpc.New(natsrpc.Options{Prefix: "blog.v1"}), natsrpc.Endpoint{Name: "a.*", Public: true}, ok)
		},
		"nil handler": func() {
			natsrpc.Register[struct{}, struct{}](natsrpc.New(natsrpc.Options{Prefix: "blog.v1"}), public, nil)
		},
		"needs login": func() {
			natsrpc.Register(natsrpc.New(natsrpc.Options{Prefix: "blog.v1"}), natsrpc.Endpoint{Name: "a.b"}, ok)
		},
		"public perms": func() {
			natsrpc.Register(natsrpc.New(natsrpc.Options{Prefix: "blog.v1"}), natsrpc.Endpoint{Name: "a.b", Public: true, Permissions: []string{"x"}}, ok)
		},
		"not a struct": func() {
			natsrpc.Register(natsrpc.New(natsrpc.Options{Prefix: "blog.v1"}), public, func(context.Context, string) (struct{}, error) { return struct{}{}, nil })
		},
		"duplicate": func() {
			srv := natsrpc.New(natsrpc.Options{Prefix: "blog.v1"})
			natsrpc.Register(srv, public, ok)
			natsrpc.Register(srv, natsrpc.Endpoint{Name: "a_b", Public: true}, ok) // 服务协议中的名字都是 a_b
		},
	} {
		t.Run(name, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Fatal("expected panic")
				}
			}()
			f()
		})
	}
}

func TestEndpoints(t *testing.T) {
	t.Parallel()
	f := &fixture{prefix: "blog.v1", rec: logtest.New()}
	srv := newServer(f, natsrpc.Options{})
	eps := srv.Endpoints()
	if len(eps) != 2 || eps[0].Name != "articles.publish" || eps[0].Permissions[0] != "articles:publish" || !eps[1].Public {
		t.Fatalf("endpoints %+v", eps)
	}
	if srv.Subject("articles.publish") != "blog.v1.articles.publish" {
		t.Fatal(srv.Subject("articles.publish"))
	}
}
