package httpx

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-tenon/tenon/health"
	"github.com/go-tenon/tenon/logging"
)

// ReadyOptions 是 Ready 的选项。
type ReadyOptions struct {
	Draining *Draining      // Serve 开始排空时设置的标志，与传给 OnDraining 的是同一个；为 nil 时跳过排空检查。
	Checks   []health.Check // 要检查的依赖。只放处理请求必需的依赖，以及标为 Optional 的降级依赖。
	Timeout  time.Duration  // 每项检查的超时，不大于 0 时为 health.DefaultTimeout。
	Logger   *slog.Logger   // 记录检查结果的变化，nil 表示 slog.Default()。
}

// readyResult 是就绪检查的响应体。
type readyResult struct {
	Status string            `json:"status"`           // ready、draining 或 unavailable
	Checks map[string]string `json:"checks,omitempty"` // 每项的结果：ok 或 failed
}

// Ready 返回就绪检查的处理器，通常注册为 /readyz，部署平台和负载均衡器据此决定是否向这个实例转发请求。
//
// 正在排空时直接返回 503；否则用 health.Checker 执行全部检查，必需的检查都通过时返回 200，
// 有一项失败时返回 503。响应体列出每项的结果，例如 {"status":"ready","checks":{"database":"ok","cache":"failed"}}；
// 失败的原因不出现在响应中，而是记录在日志里，日志只记录变化，见 health.Checker.Run。
// 名称为空、重复或 Ping 为 nil 时 panic。
func Ready(opts ReadyOptions) http.Handler {
	return &ready{
		draining: opts.Draining,
		checker: health.New(health.Options{
			Checks:  opts.Checks,
			Timeout: opts.Timeout,
			Logger:  logging.Or(opts.Logger).With(logging.AttrComponent, "http"),
		}),
	}
}

// ready 是就绪检查的处理器。
type ready struct {
	draining *Draining
	checker  *health.Checker
}

// ServeHTTP 输出就绪状态：排空中或依赖检查失败时返回 503。
func (r *ready) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	res := readyResult{Status: "ready"}
	if r.draining != nil && r.draining.Load() {
		res.Status = "draining"
	} else {
		report := r.checker.Run(req.Context())
		res.Checks = make(map[string]string, len(report.Results))
		for _, result := range report.Results {
			res.Checks[result.Name] = "ok"
			if result.Err != nil {
				res.Checks[result.Name] = "failed"
			}
		}
		if !report.Ready {
			res.Status = "unavailable"
		}
	}
	status := http.StatusOK
	if res.Status != "ready" {
		status = http.StatusServiceUnavailable
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	if req.Method != http.MethodHead {
		_ = json.NewEncoder(w).Encode(res)
	}
}
