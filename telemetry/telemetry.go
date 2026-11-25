package telemetry

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/contrib/instrumentation/runtime"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploghttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/log"
	lognoop "go.opentelemetry.io/otel/log/noop"
	"go.opentelemetry.io/otel/metric"
	metricnoop "go.opentelemetry.io/otel/metric/noop"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.43.0"
	"go.opentelemetry.io/otel/trace"
	tracenoop "go.opentelemetry.io/otel/trace/noop"
)

// Config 是推送配置，对应配置文件的 telemetry 段；Endpoint 为空时不推送后端数据，Browser 未配置时不启用浏览器观测。
type Config struct {
	Endpoint    string            `yaml:"endpoint"`     // OTLP/HTTP 接收端的基础地址，例如 http://openobserve:5080/api/default；日志、指标、链路分别发往其下的 /v1/logs、/v1/metrics、/v1/traces。
	Headers     map[string]string `yaml:"headers"`      // 附加到每个请求，通常是认证信息，例如 {Authorization: "Basic ${O2_TOKEN}"}。
	Service     string            `yaml:"service"`      // service.name；空值取可执行文件名。
	Version     string            `yaml:"version"`      // service.version；空值取构建信息中的主模块版本，取不到时省略。
	SampleRatio float64           `yaml:"sample_ratio"` // 新链路的采样比例，0 到 1；接在调用方链路上的 span 跟随调用方的决定。
	Browser     BrowserConfig     `yaml:"browser"`      // 浏览器端观测（OpenObserve RUM 与日志）；Site 与 ClientToken 为空时不启用。
}

// BrowserConfig 是浏览器端 OpenObserve（RUM 与日志）的运行时配置；Site 与 ClientToken 均为空时不启用。
type BrowserConfig struct {
	Site         string `yaml:"site" json:"site,omitempty"`                  // OpenObserve 域名或完整地址，例如 https://o2.example.com 或 o2.example.com；为空时不启用。
	ClientToken  string `yaml:"client_token" json:"clientToken,omitempty"`   // OpenObserve RUM 客户端写入令牌（公开可下发到浏览器）；为空时不启用。
	Organization string `yaml:"organization" json:"organization,omitempty"`  // OpenObserve 组织标识；空值默认 default。
	Environment  string `yaml:"environment" json:"environment,omitempty"`    // 环境名，例如 production、staging；空值默认 production。
	InsecureHTTP bool   `yaml:"insecure_http" json:"insecureHTTP,omitempty"` // Site 不含协议时是否使用 HTTP 上报；默认 false（HTTPS）。
}

// BrowserRuntime 是下发给浏览器的公开观测配置，已规范化主机地址、协议与默认值。
type BrowserRuntime struct {
	Site         string `json:"site"`                   // 不含协议的 OpenObserve 主机地址，例如 o2.example.com 或 127.0.0.1:5080。
	ClientToken  string `json:"clientToken"`            // OpenObserve RUM 客户端写入令牌。
	Organization string `json:"organization"`           // OpenObserve 组织标识。
	Service      string `json:"service"`                // 前端服务名，例如 app-web、app-admin。
	Environment  string `json:"environment"`            // 环境名，例如 production。
	InsecureHTTP bool   `json:"insecureHTTP,omitempty"` // 是否使用 HTTP 上报。
}

// Enabled 报告浏览器端观测是否已配置完整（Site 与 ClientToken 均非空）。
func (b BrowserConfig) Enabled() bool {
	return strings.TrimSpace(b.Site) != "" && strings.TrimSpace(b.ClientToken) != ""
}

// Validate 检查浏览器端观测配置：Site 与 ClientToken 均为空时跳过，只配置其中一项或地址非法时返回错误。
func (b BrowserConfig) Validate() error {
	site := strings.TrimSpace(b.Site)
	token := strings.TrimSpace(b.ClientToken)
	if site == "" && token == "" {
		return nil
	}
	if site == "" || token == "" {
		return fmt.Errorf("browser: site and client_token must both be set")
	}
	if _, _, err := parseBrowserSite(site, b.InsecureHTTP); err != nil {
		return fmt.Errorf("browser: %w", err)
	}
	return nil
}

// Origin 返回浏览器上报目标的 origin（scheme://host），用于放行 CSP connect-src；未启用或地址无效时返回空字符串。
func (b BrowserConfig) Origin() string {
	if !b.Enabled() {
		return ""
	}
	host, insecure, err := parseBrowserSite(b.Site, b.InsecureHTTP)
	if err != nil {
		return ""
	}
	if insecure {
		return "http://" + host
	}
	return "https://" + host
}

// BrowserRuntime 为指定前端生成下发到浏览器的公开观测配置；未启用或配置非法时 ok 为 false。
func (c Config) BrowserRuntime(frontendName string) (BrowserRuntime, bool) {
	if !c.Browser.Enabled() {
		return BrowserRuntime{}, false
	}
	host, insecure, err := parseBrowserSite(c.Browser.Site, c.Browser.InsecureHTTP)
	if err != nil {
		return BrowserRuntime{}, false
	}
	service := strings.TrimSpace(c.Service)
	name := strings.TrimSpace(frontendName)
	switch {
	case service != "" && name != "":
		service = service + "-" + name
	case service == "":
		service = cmp.Or(name, executableName())
	}
	return BrowserRuntime{
		Site:         host,
		ClientToken:  strings.TrimSpace(c.Browser.ClientToken),
		Organization: cmp.Or(strings.TrimSpace(c.Browser.Organization), "default"),
		Service:      service,
		Environment:  cmp.Or(strings.TrimSpace(c.Browser.Environment), "production"),
		InsecureHTTP: insecure,
	}, true
}

// parseBrowserSite 规范化 OpenObserve 站点地址，返回不含协议的主机地址与是否使用 HTTP。
func parseBrowserSite(raw string, insecureHTTP bool) (host string, insecure bool, err error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return "", false, fmt.Errorf("site is empty")
	}
	if strings.Contains(s, "://") {
		u, err := url.Parse(s)
		if err != nil {
			return "", false, fmt.Errorf("site: %w", err)
		}
		if (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return "", false, fmt.Errorf("site must be an http or https URL or host")
		}
		if (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" {
			return "", false, fmt.Errorf("site must not contain path, query, or fragment")
		}
		return u.Host, u.Scheme == "http", nil
	}
	u, err := url.Parse("https://" + s)
	if err != nil || u.Host == "" || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" {
		return "", false, fmt.Errorf("site must be a valid host or http(s) URL")
	}
	return u.Host, insecureHTTP, nil
}

// DefaultConfig 返回默认配置：不推送，开启时记录全部链路。
func DefaultConfig() Config {
	return Config{SampleRatio: 1}
}

// SetDefaults 实现 config.Defaulter，使 config.Load 自动填充默认值。
func (c *Config) SetDefaults() { *c = DefaultConfig() }

// Validate 检查接收地址、采样比例与浏览器端观测配置。
func (c Config) Validate() error {
	if c.SampleRatio < 0 || c.SampleRatio > 1 {
		return fmt.Errorf("sample_ratio must be between 0 and 1")
	}
	if err := c.Browser.Validate(); err != nil {
		return err
	}
	if c.Endpoint == "" {
		return nil
	}
	u, err := url.Parse(c.Endpoint)
	if err != nil {
		return fmt.Errorf("endpoint: %w", err)
	}
	if (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fmt.Errorf("endpoint must be an http or https URL")
	}
	return nil
}

// Providers 是交给框架组件和日志的三个 Provider。
type Providers struct {
	Meters  metric.MeterProvider
	Tracers trace.TracerProvider
	Logs    log.LoggerProvider

	shutdown []func(context.Context) error
}

// Open 按 cfg 创建推送日志、指标和链路的 Provider，三者共用一个 resource：
// service.name、service.version、service.instance.id（每个进程不同）、主机名和进程号。
// 指标中还有 Go 运行时的内存、GC、goroutine 等指标（go.memory.used、go.goroutine.count 等）。
// 用完后调用 Shutdown 发出缓冲中的数据。
//
// cfg.Endpoint 为空时返回空操作的 Provider，调用方按同样的方式使用它。
//
// 开启推送时，Open 设置 OpenTelemetry 的全局错误处理：导出失败用 slog.Default() 记录为 component=telemetry 的错误日志。
func Open(ctx context.Context, cfg Config) (*Providers, error) {
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("telemetry: %w", err)
	}
	if cfg.Endpoint == "" {
		return &Providers{
			Meters:  metricnoop.NewMeterProvider(),
			Tracers: tracenoop.NewTracerProvider(),
			Logs:    lognoop.NewLoggerProvider(),
		}, nil
	}

	res, err := newResource(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("telemetry: resource: %w", err)
	}
	base := strings.TrimSuffix(cfg.Endpoint, "/")
	headers := compactHeaders(cfg.Headers)

	traces, err := otlptracehttp.New(ctx,
		otlptracehttp.WithEndpointURL(base+"/v1/traces"),
		otlptracehttp.WithHeaders(headers),
		otlptracehttp.WithCompression(otlptracehttp.GzipCompression))
	if err != nil {
		return nil, fmt.Errorf("telemetry: trace exporter: %w", err)
	}
	metrics, err := otlpmetrichttp.New(ctx,
		otlpmetrichttp.WithEndpointURL(base+"/v1/metrics"),
		otlpmetrichttp.WithHeaders(headers),
		otlpmetrichttp.WithCompression(otlpmetrichttp.GzipCompression))
	if err != nil {
		return nil, fmt.Errorf("telemetry: metric exporter: %w", err)
	}
	logs, err := otlploghttp.New(ctx,
		otlploghttp.WithEndpointURL(base+"/v1/logs"),
		otlploghttp.WithHeaders(headers),
		otlploghttp.WithCompression(otlploghttp.GzipCompression))
	if err != nil {
		return nil, fmt.Errorf("telemetry: log exporter: %w", err)
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithResource(res),
		sdktrace.WithBatcher(traces),
		sdktrace.WithSampler(sdktrace.ParentBased(sdktrace.TraceIDRatioBased(cfg.SampleRatio))),
	)
	mp := sdkmetric.NewMeterProvider(
		sdkmetric.WithResource(res),
		sdkmetric.WithReader(sdkmetric.NewPeriodicReader(metrics)),
	)
	if err := runtime.Start(runtime.WithMeterProvider(mp)); err != nil {
		return nil, fmt.Errorf("telemetry: runtime metrics: %w", err)
	}
	lp := sdklog.NewLoggerProvider(
		sdklog.WithResource(res),
		sdklog.WithProcessor(sdklog.NewBatchProcessor(logs)),
	)
	// 导出失败直接写到本地 stderr 且限频，避免经 slog.Default() 再次进入 OTLP 日志导出器形成自激循环。
	otel.SetErrorHandler(newStderrErrorHandler())

	// 日志最后关闭，前两者关闭时的错误还能推送出去。
	return &Providers{
		Meters:   mp,
		Tracers:  tp,
		Logs:     lp,
		shutdown: []func(context.Context) error{tp.Shutdown, mp.Shutdown, lp.Shutdown},
	}, nil
}

// Shutdown 发出缓冲中的数据并停止推送；ctx 到期时放弃剩余的数据。没有开启推送时直接返回。
func (p *Providers) Shutdown(ctx context.Context) error {
	var errs []error
	for _, fn := range p.shutdown {
		errs = append(errs, fn(ctx))
	}
	if err := errors.Join(errs...); err != nil {
		return fmt.Errorf("telemetry: shutdown: %w", err)
	}
	return nil
}

// newResource 返回描述本服务的 OpenTelemetry 资源：服务名、版本和运行环境。
func newResource(ctx context.Context, cfg Config) (*resource.Resource, error) {
	attrs := []resource.Option{
		resource.WithAttributes(
			semconv.ServiceName(cmp.Or(cfg.Service, executableName())),
			semconv.ServiceInstanceID(uuid.NewString()),
		),
		resource.WithHost(),
		resource.WithProcessPID(),
		resource.WithTelemetrySDK(),
	}
	if v := cmp.Or(cfg.Version, buildVersion()); v != "" {
		attrs = append(attrs, resource.WithAttributes(semconv.ServiceVersion(v)))
	}
	return resource.New(ctx, attrs...)
}

// executableName 返回可执行文件名，作为未配置 Service 时的服务名。
func executableName() string {
	if exe, err := os.Executable(); err == nil {
		return filepath.Base(exe)
	}
	return filepath.Base(os.Args[0])
}

// errorLogInterval 是 OpenTelemetry 导出失败写 stderr 的最小间隔，防止后端不可用时刷屏。
const errorLogInterval = time.Minute

// newStderrErrorHandler 返回直接写 stderr 且按 errorLogInterval 节流的 OpenTelemetry 错误处理器。
func newStderrErrorHandler() otel.ErrorHandler {
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	var (
		mu         sync.Mutex
		next       time.Time
		suppressed int
	)
	return otel.ErrorHandlerFunc(func(err error) {
		mu.Lock()
		now := time.Now()
		if now.Before(next) {
			suppressed++
			mu.Unlock()
			return
		}
		dropped := suppressed
		next, suppressed = now.Add(errorLogInterval), 0
		mu.Unlock()
		if dropped > 0 {
			logger.Error("telemetry export failed", "component", "telemetry", "error", err, "suppressed", dropped)
			return
		}
		logger.Error("telemetry export failed", "component", "telemetry", "error", err)
	})
}

// compactHeaders 过滤值为空的请求头条目，避免环境变量未设置时发送空白认证头。
func compactHeaders(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		if strings.TrimSpace(v) == "" {
			continue
		}
		out[k] = v
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// buildVersion 返回主模块版本；本地构建的 (devel) 视为无版本。
func buildVersion() string {
	info, ok := debug.ReadBuildInfo()
	if !ok || info.Main.Version == "(devel)" {
		return ""
	}
	return info.Main.Version
}
