package ssr

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"runtime"
	"syscall"
	"time"

	"github.com/cockroachdb/errors"
	"golang.org/x/sys/unix"
)

// process 是启动 SSR 进程用到的文件：共用的运行时和这个前端的代码，都在 memfd 中。bundle 要一直被引用到 close：
// *os.File 被回收时终结器会关闭 fd，之后重启时交给子进程的就不是它了。
type process struct {
	runtime string // 运行时的路径，/proc/self/fd/N
	bundle  *os.File
}

func (p *process) close() error { return p.bundle.Close() }

// memfd 把 data 写进一个内存文件：不落盘，关闭后自动释放。
func memfd(name string, data []byte) (*os.File, error) {
	fd, err := unix.MemfdCreate(name, unix.MFD_CLOEXEC)
	if err != nil {
		return nil, errors.Wrap(err, "ssr: memfd_create")
	}
	f := os.NewFile(uintptr(fd), name)
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return nil, errors.Wrapf(err, "ssr: write %s", name)
	}
	return f, nil
}

// load 第一次调用时把运行时写进 memfd，之后返回同一个路径。
func (r *Runtime) load() (string, error) {
	r.once.Do(func() {
		if len(r.executable) == 0 {
			r.err = errors.New("ssr: the runtime is empty (run go tool tenon build)")
			return
		}
		r.file, r.err = memfd(processName, r.executable)
		if r.err == nil {
			r.path = fmt.Sprintf("/proc/self/fd/%d", r.file.Fd())
		}
	})
	return r.path, r.err
}

// prepare 准备启动 SSR 进程：共用的运行时（只写一次）和这个前端的代码，都从内存中交给子进程。
func (h *Host) prepare() (*process, error) {
	runtime, err := h.opts.Runtime.load()
	if err != nil {
		return nil, err
	}
	bundle, err := memfd(processName+"-bundle", h.opts.Bundle)
	if err != nil {
		return nil, err
	}
	// bundle 在 Run 结束时关闭：在此之前每次（重新）启动都把它交给子进程。
	return &process{runtime: runtime, bundle: bundle}, nil
}

// supervise 启动 SSR 进程，意外退出后按退避重启，直到 ctx 取消；取消时先 SIGTERM，超过 StopTimeout 再 SIGKILL。
//
// Pdeathsig 在创建子进程的线程退出时生效，所以锁住这个 goroutine 的线程，子进程运行期间它不会退出：
// Go 进程被强制结束时，SSR 进程随之结束，不会成为孤儿。
func (h *Host) supervise(ctx context.Context, p *process) error {
	runtime.LockOSThread()
	backoff := restartBackoff
	for {
		stdout := &lineWriter{logger: h.logger, level: slog.LevelInfo}
		stderr := &lineWriter{logger: h.logger, level: slog.LevelWarn}
		cmd := &exec.Cmd{
			// 运行时的加载器从 fd 3（BundleFD）读取前端的代码：ExtraFiles 的第一个就是 fd 3。
			ExtraFiles:  []*os.File{p.bundle},
			Path:        p.runtime,
			Args:        []string{processName},
			Env:         h.env(),
			Stdout:      stdout,
			Stderr:      stderr,
			SysProcAttr: &syscall.SysProcAttr{Pdeathsig: syscall.SIGKILL},
		}
		started := time.Now()
		if err := cmd.Start(); err != nil {
			// 不放弃：放弃后页面会一直返回 502。按退避重试，直到 ctx 取消。
			h.logger.Error("ssr failed to start, retrying", "error", err, "backoff", backoff)
			select {
			case <-ctx.Done():
				return errors.Wrap(err, "ssr: start process")
			case <-time.After(backoff):
			}
			backoff = min(backoff*2, maxBackoff)
			continue
		}
		h.pid.Store(int64(cmd.Process.Pid))
		exited := make(chan struct{})
		waitErr := make(chan error, 1)
		go func() {
			err := cmd.Wait()
			stdout.flush()
			stderr.flush()
			close(exited)
			waitErr <- err
		}()
		go h.pollReady(ctx, exited, started)

		select {
		case <-ctx.Done():
			h.ready.Store(false)
			_ = cmd.Process.Signal(syscall.SIGTERM)
			select {
			case <-exited:
			case <-time.After(h.opts.StopTimeout):
				h.logger.Warn("ssr did not stop in time, killing", "timeout", h.opts.StopTimeout)
				_ = cmd.Process.Kill()
				<-exited
			}
			if err := <-waitErr; err != nil {
				h.logger.Info("ssr stopped", "exit", err)
			} else {
				h.logger.Info("ssr stopped")
			}
			return nil
		case <-exited:
			err := <-waitErr
			h.ready.Store(false)
			uptime := time.Since(started)
			if uptime > stableAfter {
				backoff = restartBackoff
			}
			h.restarts.Add(context.Background(), 1)
			h.logger.Error("ssr exited, restarting", "error", err, "uptime", uptime.Round(time.Millisecond), "backoff", backoff)
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

type peerListener struct {
	net.Listener
	uid uint32
}

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
