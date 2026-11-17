package ssr

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"sync"
	"time"
)

// 重启的退避：第一次 100ms，之后翻倍，最多 5 秒；进程运行超过 stableAfter 后从头计算。测试中改小。
var (
	restartBackoff = 100 * time.Millisecond
	maxBackoff     = 5 * time.Second
	stableAfter    = 10 * time.Second
)

// processName 是 SSR 进程的 argv[0]，ps 中显示为它。
const processName = "tenon-ssr"

// pollReady 等待 SSR 进程的 socket 可以连接，或者 exited 关闭。
func (h *Host) pollReady(ctx context.Context, exited <-chan struct{}, started time.Time) {
	for {
		c, err := dialSocket(ctx, h.socket)
		if err == nil {
			_ = c.Close()
			h.ready.Store(true)
			h.logger.Info("ssr ready", "pid", h.pid.Load(), "startup", time.Since(started).Round(time.Millisecond))
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-exited:
			return
		case <-time.After(10 * time.Millisecond):
		}
	}
}

// lineWriter 把 SSR 进程的输出按行写入日志。
type lineWriter struct {
	mu     sync.Mutex
	buf    bytes.Buffer
	logger *slog.Logger
	level  slog.Level
}

// Write 按行转发 SSR 进程的输出。
func (w *lineWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.buf.Write(p)
	for {
		i := bytes.IndexByte(w.buf.Bytes(), '\n')
		if i < 0 {
			break
		}
		line := string(bytes.TrimRight(w.buf.Next(i+1), "\r\n"))
		if line != "" {
			w.logger.Log(context.Background(), w.level, line)
		}
	}
	// 没有换行的超长输出也及时写出，不无限累积。
	if w.buf.Len() > 64<<10 {
		w.logger.Log(context.Background(), w.level, w.buf.String())
		w.buf.Reset()
	}
	return len(p), nil
}

// flush 写出最后一行没有换行的输出。
func (w *lineWriter) flush() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.buf.Len() > 0 {
		w.logger.Log(context.Background(), w.level, w.buf.String())
		w.buf.Reset()
	}
}

// env 是 SSR 进程的环境变量：继承 Go 程序的环境，加上 Options.Env 和两个 socket；没有 Options.API 时不传 API_SOCKET。
func (h *Host) env() []string {
	env := append(append(os.Environ(), h.opts.Env...), EnvSocket+"="+h.socket)
	if h.opts.API != nil {
		env = append(env, EnvAPISocket+"="+h.api)
	}
	return env
}
