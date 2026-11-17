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

// process 是写入 memfd 的 SSR 可执行文件。
type process struct{ path string }

// prepare 把可执行文件写进 memfd：从内存执行，不落盘，进程退出后自动释放。
func (h *Host) prepare() (*process, error) {
	fd, err := unix.MemfdCreate(processName, unix.MFD_CLOEXEC)
	if err != nil {
		return nil, errors.Wrap(err, "ssr: memfd_create")
	}
	f := os.NewFile(uintptr(fd), processName)
	if _, err := f.Write(h.opts.Executable); err != nil {
		_ = f.Close()
		return nil, errors.Wrap(err, "ssr: write executable")
	}
	// f 不关闭：进程的生命周期内一直从它启动 SSR 进程。
	runtime.KeepAlive(f)
	return &process{path: fmt.Sprintf("/proc/self/fd/%d", fd)}, nil
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
			Path:        p.path,
			Args:        []string{processName},
			Env:         append(append(os.Environ(), h.opts.Env...), EnvSocket+"="+h.socket, EnvAPISocket+"="+h.api),
			Stdout:      stdout,
			Stderr:      stderr,
			SysProcAttr: &syscall.SysProcAttr{Pdeathsig: syscall.SIGKILL},
		}
		started := time.Now()
		if err := cmd.Start(); err != nil {
			return errors.Wrap(err, "ssr: start process")
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
