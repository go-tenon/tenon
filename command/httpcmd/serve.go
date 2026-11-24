package httpcmd

import (
	"context"
	"net/http"

	"github.com/go-tenon/tenon/command"
	"github.com/go-tenon/tenon/httpx"
)

// ServeOptions 返回与常驻进程 d 协同零停机热重载的 httpx.Serve 选项：
// 延迟向 systemd 报告就绪直到 HTTP 开始接收连接（httpx.OnReady），并在热重载交接时跳过负载均衡摘流等待（httpx.SkipDrainDelay）。
// d 为 nil 时返回 nil。
func ServeOptions(d *command.Daemon) []httpx.ServeOption {
	if d == nil {
		return nil
	}
	return []httpx.ServeOption{
		httpx.OnReady(d.DeferReady()),
		httpx.SkipDrainDelay(d.HandedOver),
	}
}

// Serve 用 d 监听 cfg.Addr 并返回在 ctx 取消前运行 handler 的函数，自动接好热重载就绪与摘流跳过选项。
// 适合不需要托管前端或已在外部完成 pages.Run 包装的 HTTP 服务；opts 追加在默认热重载选项之后。
func Serve(d *command.Daemon, cfg httpx.Config, handler http.Handler, opts ...httpx.ServeOption) (func(context.Context) error, error) {
	ln, err := d.Listen("tcp", cfg.Addr)
	if err != nil {
		return nil, err
	}
	allOpts := append(ServeOptions(d), opts...)
	return func(ctx context.Context) error {
		return httpx.Serve(ctx, cfg, ln, handler, allOpts...)
	}, nil
}
