package frontend

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"runtime"
	"syscall"
	"time"

	"github.com/cockroachdb/errors"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"golang.org/x/sys/unix"
)

// executable 是写进内存文件（memfd）的 SSR 可执行文件，几个 SSR 前端的进程共用。file 要一直被引用到 close：
// *os.File 被回收时终结器会关闭 fd，之后重启时就执行不了它了。
type executable struct {
	path string // /proc/self/fd/N
	file *os.File
}

// close 关闭内存文件，释放它占用的内存。
func (e *executable) close() error { return e.file.Close() }

// load 把构建产物中的 ExecutableFile 流式写进内存文件：不落盘，也不在 Go 的内存中另外复制一份（约 100 MB）。
func (h *Host) load() (*executable, error) {
	src, err := h.dist.Open(ExecutableFile)
	if err != nil {
		return nil, errors.Wrapf(err, "frontend: open %s", ExecutableFile)
	}
	defer src.Close()
	fd, err := unix.MemfdCreate(processName, unix.MFD_CLOEXEC)
	if err != nil {
		return nil, errors.Wrap(err, "frontend: memfd_create")
	}
	f := os.NewFile(uintptr(fd), processName)
	if _, err := io.Copy(f, src); err != nil {
		_ = f.Close()
		return nil, errors.Wrapf(err, "frontend: write %s", ExecutableFile)
	}
	return &executable{path: fmt.Sprintf("/proc/self/fd/%d", f.Fd()), file: f}, nil
}

// supervise 启动前端 a 的 SSR 进程，意外退出后按退避重启，直到 ctx 取消；取消时先 SIGTERM，超过 StopTimeout 再 SIGKILL。
//
// Pdeathsig 在创建子进程的线程退出时生效，所以锁住这个 goroutine 的线程，子进程运行期间它不会退出：
// Go 进程被强制结束时，SSR 进程随之结束，不会成为孤儿。
func (h *Host) supervise(ctx context.Context, a *app, exe *executable) error {
	runtime.LockOSThread()
	backoff := restartBackoff
	for {
		stdout := &lineWriter{logger: a.logger, level: slog.LevelInfo}
		stderr := &lineWriter{logger: a.logger, level: slog.LevelWarn}
		cmd := &exec.Cmd{
			Path:        exe.path,
			Args:        []string{processName, a.Name},
			Env:         h.env(a),
			Stdout:      stdout,
			Stderr:      stderr,
			SysProcAttr: &syscall.SysProcAttr{Pdeathsig: syscall.SIGKILL},
		}
		started := time.Now()
		if err := cmd.Start(); err != nil {
			// 不放弃：放弃后页面会一直返回 502。按退避重试，直到 ctx 取消。
			a.logger.Error("ssr failed to start, retrying", "error", err, "backoff", backoff)
			select {
			case <-ctx.Done():
				return errors.Wrap(err, "frontend: start ssr process")
			case <-time.After(backoff):
			}
			backoff = min(backoff*2, maxBackoff)
			continue
		}
		a.pid.Store(int64(cmd.Process.Pid))
		exited := make(chan struct{})
		waitErr := make(chan error, 1)
		go func() {
			err := cmd.Wait()
			stdout.flush()
			stderr.flush()
			close(exited)
			waitErr <- err
		}()
		go pollReady(ctx, a, exited, started)

		select {
		case <-ctx.Done():
			a.ready.Store(false)
			_ = cmd.Process.Signal(syscall.SIGTERM)
			select {
			case <-exited:
			case <-time.After(h.opts.StopTimeout):
				a.logger.Warn("ssr did not stop in time, killing", "timeout", h.opts.StopTimeout)
				_ = cmd.Process.Kill()
				<-exited
			}
			if err := <-waitErr; err != nil {
				a.logger.Info("ssr stopped", "exit", err)
			} else {
				a.logger.Info("ssr stopped")
			}
			return nil
		case <-exited:
			err := <-waitErr
			a.ready.Store(false)
			uptime := time.Since(started)
			if uptime > stableAfter {
				backoff = restartBackoff
			}
			h.restarts.Add(context.Background(), 1, metric.WithAttributes(attribute.String("frontend", a.Name)))
			a.logger.Error("ssr exited, restarting", "error", err, "uptime", uptime.Round(time.Millisecond), "backoff", backoff)
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(backoff):
			}
			backoff = min(backoff*2, maxBackoff)
		}
	}
}

// listenPeer 在抽象 socket 上监听，只接受与本进程同一用户的连接。抽象 socket 没有文件权限，
// 同一网络命名空间中的进程都能连接，所以按对端的 uid 过滤。
func listenPeer(name string) (net.Listener, error) {
	ln, err := net.Listen("unix", name)
	if err != nil {
		return nil, err
	}
	return &peerListener{Listener: ln, uid: uint32(os.Getuid())}, nil
}

// peerListener 只接受与本进程同一用户的连接。
type peerListener struct {
	net.Listener
	uid uint32
}

// Accept 返回下一个同一用户的连接，其他连接直接关闭。
func (l *peerListener) Accept() (net.Conn, error) {
	for {
		c, err := l.Listener.Accept()
		if err != nil {
			return nil, err
		}
		if l.sameUser(c) {
			return c, nil
		}
		_ = c.Close()
	}
}

// sameUser 报告 unix socket 连接对端的用户是否与本进程相同。
func (l *peerListener) sameUser(c net.Conn) bool {
	uc, ok := c.(*net.UnixConn)
	if !ok {
		return false
	}
	raw, err := uc.SyscallConn()
	if err != nil {
		return false
	}
	var cred *unix.Ucred
	var credErr error
	if err := raw.Control(func(fd uintptr) {
		cred, credErr = unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED)
	}); err != nil || credErr != nil {
		return false
	}
	return cred.Uid == l.uid
}
