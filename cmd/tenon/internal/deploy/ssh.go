package deploy

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// Runner 抽象远端命令执行与文件上传，便于生产调用系统 ssh/scp，测试中用内存替身验证编排逻辑。
type Runner interface {
	// Run 在远端主机 host 执行 POSIX shell 脚本 script，stdin 为标准输入，stdout/stderr 接收输出。
	Run(ctx context.Context, host Host, script string, stdin io.Reader, stdout, stderr io.Writer) error
	// Upload 将本地文件 localPath 上传到远端主机 host 的 remotePath。
	Upload(ctx context.Context, host Host, localPath, remotePath string) error
}

// SSHRunner 调用本机系统 ssh/scp 执行远端操作，启用 ControlMaster 连接复用，
// 天然兼容 ~/.ssh/config、ProxyJump 与 SSH Agent。
type SSHRunner struct {
	user         string // SSH 登录用户
	port         int    // SSH 端口，0 表示不显式传 -p/-P
	identityFile string // SSH 私钥路径，为空时不传 -i
	controlDir   string // ControlPath 所在的临时目录
}

// NewSSHRunner 为目标环境 t 创建 SSHRunner，使用结束后应调用 Close 清理控制套接字目录。
// 优先在 /tmp 下使用短目录名，保证拼接 %C 后的 ControlPath 在 macOS 上也低于 Unix socket 104 字节路径上限。
func NewSSHRunner(t Target) (*SSHRunner, error) {
	dir, err := os.MkdirTemp("/tmp", "tn-*")
	if err != nil {
		dir, err = os.MkdirTemp("", "tn-*")
	}
	if err != nil {
		return nil, fmt.Errorf("deploy: create ssh control dir: %w", err)
	}
	return &SSHRunner{
		user:         t.User,
		port:         t.Port,
		identityFile: t.IdentityFile,
		controlDir:   dir,
	}, nil
}

// ControlPath 返回传给 ssh/scp 的 ControlPath 模板路径。
func (r *SSHRunner) ControlPath() string {
	return filepath.Join(r.controlDir, "%C")
}

// Close 清理 ControlMaster 的控制套接字目录。
func (r *SSHRunner) Close() error {
	if r.controlDir == "" {
		return nil
	}
	return os.RemoveAll(r.controlDir)
}

// Run 通过系统 ssh 在远端主机执行 script。
func (r *SSHRunner) Run(ctx context.Context, host Host, script string, stdin io.Reader, stdout, stderr io.Writer) error {
	args := r.commonArgs(false)
	var cmd *exec.Cmd
	if stdin != nil {
		// 调用方提供 stdin 时通过命令参数执行 script，保留 stdin 给远端命令读取。
		args = append(args, r.destination(host), script)
		cmd = exec.CommandContext(ctx, "ssh", args...)
		cmd.Stdin = stdin
	} else {
		args = append(args, r.destination(host), "sh -s")
		cmd = exec.CommandContext(ctx, "ssh", args...)
		cmd.Stdin = strings.NewReader(script + "\n")
	}
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("ssh %s: %w", host.Addr, err)
	}
	return nil
}

// Upload 通过系统 scp 将本地文件上传到远端路径。
func (r *SSHRunner) Upload(ctx context.Context, host Host, localPath, remotePath string) error {
	args := r.commonArgs(true)
	args = append(args, localPath, r.destination(host)+":"+remotePath)
	var stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, "scp", args...)
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg != "" {
			return fmt.Errorf("scp %s -> %s:%s: %w: %s", localPath, host.Addr, remotePath, err, msg)
		}
		return fmt.Errorf("scp %s -> %s:%s: %w", localPath, host.Addr, remotePath, err)
	}
	return nil
}

// commonArgs 返回 ssh/scp 共用的选项参数；forSCP 为真时端口标志使用 -P，否则使用 -p。
func (r *SSHRunner) commonArgs(forSCP bool) []string {
	args := []string{
		"-o", "BatchMode=yes",
		"-o", "ControlMaster=auto",
		"-o", "ControlPersist=60s",
		"-o", "ControlPath=" + r.ControlPath(),
	}
	if r.port > 0 {
		flag := "-p"
		if forSCP {
			flag = "-P"
		}
		args = append(args, flag, strconv.Itoa(r.port))
	}
	if r.identityFile != "" {
		args = append(args, "-i", r.identityFile)
	}
	return args
}

// destination 返回带用户的目标地址，例如 deploy@203.0.113.10；若 host.Addr 已包含 @ 则原样返回。
func (r *SSHRunner) destination(host Host) string {
	if strings.Contains(host.Addr, "@") || r.user == "" {
		return host.Addr
	}
	return r.user + "@" + host.Addr
}

// RunCapture 执行远端脚本并返回标准输出；失败时把标准错误附加到错误信息中。
func RunCapture(ctx context.Context, r Runner, host Host, script string) (string, error) {
	var stdout, stderr bytes.Buffer
	err := r.Run(ctx, host, script, nil, &stdout, &stderr)
	if err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return stdout.String(), fmt.Errorf("%w: %s", err, msg)
		}
		return stdout.String(), err
	}
	return stdout.String(), nil
}

// shellQuote 将 s 转义为 POSIX sh 安全的单引号字符串。
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
