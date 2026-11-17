package natsrpc

import (
	"context"
	"log/slog"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/nats-io/nats.go"
	goi18n "github.com/nicksnyder/go-i18n/v2/i18n"

	"github.com/go-tenon/tenon/identity"
	"github.com/go-tenon/tenon/internal/apischema"
	"github.com/go-tenon/tenon/logging"
)

// 默认值，见 Options。
const (
	DefaultTimeout         = 30 * time.Second
	DefaultConcurrency     = 100
	DefaultShutdownTimeout = 10 * time.Second
	defaultVersion         = "0.0.0"
)

// subjectPart 是前缀和端点名的格式：由 . 分隔的段，每段是小写字母、数字、_ 和 -。
var subjectPart = regexp.MustCompile(`^[a-z0-9_-]+(\.[a-z0-9_-]+)*$`)

// semver 是服务版本的格式，见 https://semver.org。
var semver = regexp.MustCompile(`^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)(-[0-9A-Za-z.-]+)?(\+[0-9A-Za-z.-]+)?$`)

// Options 是 New 的选项。
type Options struct {
	Prefix          string                     // 端点 subject 的前缀，通常是应用名加 API 的版本，例如 blog.v1，必填。不兼容的修改通过新的前缀发布，见 docs/rpc/protocol.md。
	Version         string                     // 服务的版本，格式为语义化版本，出现在服务发现中；为空时是 0.0.0。
	Description     string                     // 说明这个 API，出现在服务发现和 AsyncAPI 文档中。
	Permissions     []identity.Permission      // 应用的权限目录，例如 authz.Catalog，与 rest.Options.Permissions 相同。它写入 AsyncAPI 文档的 x-tenon-permissions（按名称排序），客户端据此生成权限常量和说明；nil 时不写。
	Validate        identity.TokenValidator    // 校验请求头 Authorization: Bearer 中的 token，与 REST 用同一个校验器，例如 apitoken.Store 的 Validate；有需要登录的端点时必填
	Accounts        identity.Accounts          // 接受的账号体系和各自的 Resolver，与 rest.Options.Accounts 相同；有需要登录的端点时必填
	Tenant          func(h nats.Header) string // 从请求头中取出本次调用所在的租户，例如 X-Tenant；nil 表示平台本身（""）。凭据绑定了租户时，两者必须相同。
	Languages       *goi18n.Bundle             // 消息目录，设置后按请求头 Accept-Language 协商错误提示的语言，与 httpx.Languages 相同。
	Timeout         time.Duration              // 每次调用的处理上限，调用方的 X-Timeout 更短时以它为准；不大于 0 时为 DefaultTimeout。
	Concurrency     int                        // 同时处理的调用数上限，超过时新的请求在 NATS 客户端中排队；不大于 0 时为 DefaultConcurrency。
	ShutdownTimeout time.Duration              // 停止时等待处理中的调用的最长时间，超时后取消它们的 ctx；不大于 0 时为 DefaultShutdownTimeout。
	Logger          *slog.Logger               // 记录内部错误和认证后端的故障，nil 表示 slog.Default()。
}

// Endpoint 描述一个端点。
type Endpoint struct {
	Name        string   // 端点名，由 . 分隔的小写单词，例如 articles.publish。subject 是前缀加端点名，例如 blog.v1.articles.publish。
	Description string   // 说明端点做什么，出现在服务发现中。
	Public      bool     // 不需要登录。否则调用方必须带有效的 token，并拥有 Permissions 中的全部权限。
	Permissions []string // 调用这个端点需要的全部权限，用 identity.Subject.Can 检查。
	Idempotent  bool     // 用相同的参数重复调用没有额外的效果，调用方超时后可以重试。
}

// Server 是一个 API：登记的端点，以及调用它们时的认证、参数校验和错误转换。用 Serve 在 NATS 连接上提供它。
type Server struct {
	opts      Options
	logger    *slog.Logger
	service   string // 服务协议中的服务名：前缀中的 . 换成 _
	endpoints []*endpoint
	names     map[string]bool // 服务协议中的端点名，用于检查重复

	mu      sync.Mutex
	started bool
}

// endpoint 是登记的一个端点。
type endpoint struct {
	spec    Endpoint
	subject string
	name    string // 服务协议中的端点名：端点名中的 . 换成 _
	input   *apischema.Schema
	inType  reflect.Type
	outType reflect.Type
	empty   bool // 返回值是 struct{}：成功时响应体为空
	handle  func(ctx context.Context, body []byte) (data []byte, fields []FieldError, err error)

	stats endpointStats
}

// New 创建没有端点的 Server。Prefix 或 Version 不合法、Accounts 不合法时 panic。
func New(opts Options) *Server {
	if !subjectPart.MatchString(opts.Prefix) {
		panic("natsrpc: Prefix must be dot-separated lowercase letters, digits, '_' or '-', e.g. blog.v1; got " + strconv.Quote(opts.Prefix))
	}
	if opts.Version == "" {
		opts.Version = defaultVersion
	}
	if !semver.MatchString(opts.Version) {
		panic("natsrpc: Version must be a semantic version, e.g. 1.2.0; got " + strconv.Quote(opts.Version))
	}
	if len(opts.Accounts) > 0 {
		if err := opts.Accounts.Validate(); err != nil {
			panic(err)
		}
	}
	if opts.Timeout <= 0 {
		opts.Timeout = DefaultTimeout
	}
	if opts.Concurrency <= 0 {
		opts.Concurrency = DefaultConcurrency
	}
	if opts.ShutdownTimeout <= 0 {
		opts.ShutdownTimeout = DefaultShutdownTimeout
	}
	return &Server{
		opts:    opts,
		logger:  logging.Or(opts.Logger).With(logging.AttrComponent, "rpc"),
		service: strings.ReplaceAll(opts.Prefix, ".", "_"),
		names:   map[string]bool{},
	}
}

// Register 把 fn 登记为端点 e。fn 通常是业务动作的方法值，例如 article.NewPublish(db).RPC。
//
// I 必须是结构体，请求体按它的 Huma 标签（doc、minLength、enum、format 等）校验，与 REST 的请求体相同，
// 同一个类型可以同时用作两者。O 是返回值，编码为 JSON 作为响应体；O 是 struct{} 时响应体为空。
//
// fn 返回的错误按分类转换为状态码和错误响应，见 docs/rpc/protocol.md；系统故障和 panic 记录在日志中，
// 响应中不含内部信息。
//
// 名称不合法或重复、fn 为 nil、I 不是结构体、在 Serve 之后登记、需要登录却没有设置 Validate 和 Accounts，
// 或者 Public 的端点声明了 Permissions 时 panic。
func Register[I, O any](s *Server, e Endpoint, fn func(context.Context, I) (O, error)) {
	switch {
	case s == nil:
		panic("natsrpc: nil server")
	case fn == nil:
		panic("natsrpc: nil handler for endpoint " + strconv.Quote(e.Name))
	case !subjectPart.MatchString(e.Name):
		panic("natsrpc: endpoint name must be dot-separated lowercase letters, digits, '_' or '-', e.g. articles.publish; got " + strconv.Quote(e.Name))
	case e.Public && len(e.Permissions) > 0:
		panic("natsrpc: public endpoint " + strconv.Quote(e.Name) + " cannot require permissions")
	case !e.Public && (s.opts.Validate == nil || len(s.opts.Accounts) == 0):
		panic("natsrpc: endpoint " + strconv.Quote(e.Name) + " requires login: set Options.Validate and Options.Accounts, or mark it Public")
	}
	name := strings.ReplaceAll(e.Name, ".", "_")
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.started {
		panic("natsrpc: Register(" + strconv.Quote(e.Name) + ") after Serve")
	}
	if s.names[name] {
		panic("natsrpc: duplicate endpoint " + strconv.Quote(e.Name))
	}
	input := apischema.New(reflect.TypeFor[I](), huma.ModeWriteToServer)
	if input.Type != "object" {
		panic("natsrpc: endpoint " + strconv.Quote(e.Name) + " input must be a struct")
	}
	e.Permissions = slices.Clone(e.Permissions)
	ep := &endpoint{
		spec:    e,
		subject: s.opts.Prefix + "." + e.Name,
		name:    name,
		input:   input,
		inType:  reflect.TypeFor[I](),
		outType: reflect.TypeFor[O](),
		empty:   reflect.TypeFor[O]() == reflect.TypeFor[struct{}](),
	}
	ep.handle = func(ctx context.Context, body []byte) ([]byte, []FieldError, error) {
		var in I
		details, err := input.Validate(body, &in, "body")
		if err != nil {
			return nil, nil, err
		}
		if len(details) > 0 {
			return nil, describe(ctx, details), nil
		}
		out, err := call(ctx, fn, in)
		if err != nil {
			return nil, nil, err
		}
		if ep.empty {
			return nil, nil, nil
		}
		data, err := marshal(out)
		return data, nil, err
	}
	s.endpoints = append(s.endpoints, ep)
	s.names[name] = true
}

// Endpoints 返回登记的全部端点，按名称排序，用于检查暴露了哪些端点和它们需要的权限。
func (s *Server) Endpoints() []Endpoint {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Endpoint, 0, len(s.endpoints))
	for _, ep := range s.endpoints {
		e := ep.spec
		e.Permissions = slices.Clone(e.Permissions)
		out = append(out, e)
	}
	slices.SortFunc(out, func(a, b Endpoint) int { return strings.Compare(a.Name, b.Name) })
	return out
}

// Subject 返回端点名为 name 的端点的 subject，例如 blog.v1.articles.publish。
func (s *Server) Subject(name string) string { return s.opts.Prefix + "." + name }
