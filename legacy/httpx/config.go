package httpx

import (
	"net/netip"
	"time"
)

// Config 是 HTTP 服务的配置，产品将其嵌入自己的配置结构体，
// 先用 DefaultConfig 填好默认值，再由 config.Load 读取 YAML 覆盖：
//
//	http:
//	  addr: ":8080"
//	  write_timeout: 60s
//	  shutdown_delay: 5s
//	  trusted_proxies: ["10.0.0.0/8", "127.0.0.1/32"]
type Config struct {
	// Addr 为监听地址，默认 ":8080"。
	Addr string `yaml:"addr"`

	// 以下超时与 http.Server 的同名字段含义相同，0 表示不限制。
	//
	// WriteTimeout 对整个响应生效，SSE 等长连接需要在 handler 中用
	// http.NewResponseController(w).SetWriteDeadline 单独放宽。
	ReadHeaderTimeout time.Duration `yaml:"read_header_timeout"` // 默认 5s
	ReadTimeout       time.Duration `yaml:"read_timeout"`        // 默认 30s
	WriteTimeout      time.Duration `yaml:"write_timeout"`       // 默认 60s
	IdleTimeout       time.Duration `yaml:"idle_timeout"`        // 默认 120s

	// ShutdownDelay 为收到停止信号后、开始关闭前的等待时间。
	// 期间 /readyz 返回 503，新请求照常处理，让负载均衡有时间摘除本实例。
	// 部署在 Kubernetes 时建议设为 5s 左右；默认 0。
	ShutdownDelay time.Duration `yaml:"shutdown_delay"`

	// ShutdownTimeout 为等待进行中请求完成的最长时间，超时后强制关闭连接。
	// 默认 15s，0 表示一直等待。
	ShutdownTimeout time.Duration `yaml:"shutdown_timeout"`

	// TrustedProxies 为可信反向代理的网段（CIDR，单个 IP 写作 1.2.3.4/32）。
	// 只有直连对端位于这些网段时，才会读取 X-Forwarded-For / X-Real-IP。
	// 默认为空：不信任任何代理头，客户端 IP 即连接对端地址。
	TrustedProxies []netip.Prefix `yaml:"trusted_proxies"`
}

// DefaultConfig 返回带默认值的 Config。
func DefaultConfig() Config {
	return Config{
		Addr:              ":8080",
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
		ShutdownTimeout:   15 * time.Second,
	}
}
