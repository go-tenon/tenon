package deploy

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"path"
	"strings"
)

// lockPath 返回主节点上的 flock 锁文件路径。
func lockPath(t Target) string {
	return path.Join(t.Dir, ".deploy.lock")
}

// acquireLock 在主节点通过绑定 SSH 会话生命周期的 flock 获取非阻塞排他发布锁：
// 本地进程异常终止或网络断开后，远端进程自动退出并由内核释放锁。
func acquireLock(ctx context.Context, r Runner, t Target, host Host, holder string) (func(), error) {
	lp := lockPath(t)
	script := fmt.Sprintf(`set -e
sudo sh -c 'exec 9>"$1"
if ! flock -n 9; then
  holder=$(cat "$1" 2>/dev/null || true)
  echo "another deploy is in progress ($holder)" >&2
  exit 1
fi
printf "%%s\n" "$2" >&9
echo LOCKED
read _ || true' _ %s %s`, shellQuote(lp), shellQuote(holder))

	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	var errBuf bytes.Buffer

	lockCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	done := make(chan struct{})
	var runErr error
	go func() {
		runErr = r.Run(lockCtx, host, script, inR, outW, &errBuf)
		_ = outW.Close()
		close(done)
	}()

	readyCh := make(chan bool, 1)
	go func() {
		sc := bufio.NewScanner(outR)
		for sc.Scan() {
			if strings.TrimSpace(sc.Text()) == "LOCKED" {
				readyCh <- true
				_, _ = io.Copy(io.Discard, outR)
				return
			}
		}
		readyCh <- false
	}()

	select {
	case <-ctx.Done():
		_ = inW.Close()
		cancel()
		<-done
		return nil, ctx.Err()
	case ok := <-readyCh:
		if !ok {
			_ = inW.Close()
			cancel()
			<-done
			msg := strings.TrimSpace(errBuf.String())
			if msg != "" {
				return nil, fmt.Errorf("[%s] 获取发布锁失败: %s", host.Addr, msg)
			}
			if runErr != nil {
				return nil, fmt.Errorf("[%s] 获取发布锁失败: %w", host.Addr, runErr)
			}
			return nil, fmt.Errorf("[%s] 获取发布锁失败: unexpected EOF", host.Addr)
		}
	case <-done:
		_ = inW.Close()
		cancel()
		msg := strings.TrimSpace(errBuf.String())
		if msg != "" {
			return nil, fmt.Errorf("[%s] 获取发布锁失败: %s", host.Addr, msg)
		}
		if runErr != nil {
			return nil, fmt.Errorf("[%s] 获取发布锁失败: %w", host.Addr, runErr)
		}
		return nil, fmt.Errorf("[%s] 获取发布锁失败: lock process exited early", host.Addr)
	}

	release := func() {
		_, _ = io.WriteString(inW, "\n")
		_ = inW.Close()
		cancel()
		<-done
	}
	return release, nil
}
