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
	"sync"
)

// Runner 抽象远端 POSIX shell 命令执行与流式数据传输，生产调用系统 ssh，测试用内存替身验证编排逻辑。
type Runner interface {
	// Run 在远端主机 host 执行 POSIX shell 脚本 script，stdin 为标准输入，stdout/stderr 接收输出。
	Run(ctx context.Context, host Host, script string, stdin io.Reader, stdout, stderr io.Writer) error
}

// SSHRunner 调用本机系统 ssh 执行远端操作，启用 ControlMaster 连接复用，
// 天然兼容 ~/.ssh/config、ProxyJump 与 SSH Agent。
type SSHRunner struct {
	user         string          // SSH 登录用户
	port         int             // SSH 端口，0 表示不显式传 -p
	identityFile string          // SSH 私钥路径，为空时不传 -i
	controlDir   string          // ControlPath 所在的临时目录
	mu           sync.Mutex      // 保护 destinations
	destinations map[string]bool // 本次会话中建立过连接的目标地址集合
}

// NewSSHRunner 为目标环境 t 创建 SSHRunner，使用结束后应调用 Close 退出 ControlMaster 并清理控制目录。
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
		destinations: map[string]bool{},
	}, nil
}

// ControlPath 返回传给 ssh 的 ControlPath 模板路径。
func (r *SSHRunner) ControlPath() string {
	return filepath.Join(r.controlDir, "%C")
}

// Close 优雅退出全部已建立的 ControlMaster 后台连接并清理控制套接字目录。
func (r *SSHRunner) Close() error {
	if r.controlDir == "" {
		return nil
	}
	r.mu.Lock()
	dests := make([]string, 0, len(r.destinations))
	for d := range r.destinations {
		dests = append(dests, d)
	}
	r.mu.Unlock()
	for _, dest := range dests {
		args := []string{"-O", "exit", "-o", "ControlPath=" + r.ControlPath()}
		if r.port > 0 {
			args = append(args, "-p", strconv.Itoa(r.port))
		}
		args = append(args, dest)
		_ = exec.Command("ssh", args...).Run()
	}
	return os.RemoveAll(r.controlDir)
}

// Run 通过系统 ssh 在远端主机执行 script。
func (r *SSHRunner) Run(ctx context.Context, host Host, script string, stdin io.Reader, stdout, stderr io.Writer) error {
	dest := r.destination(host)
	r.mu.Lock()
	r.destinations[dest] = true
	r.mu.Unlock()

	args := r.commonArgs()
	var cmd *exec.Cmd
	if stdin != nil {
		// 调用方提供 stdin 时通过命令参数执行 script，保留 stdin 给远端命令读取。
		args = append(args, dest, script)
		cmd = exec.CommandContext(ctx, "ssh", args...)
		cmd.Stdin = stdin
	} else {
		args = append(args, dest, "sh -s")
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

// commonArgs 返回 ssh 共用的连接复用与认证选项参数。
func (r *SSHRunner) commonArgs() []string {
	args := []string{
		"-o", "BatchMode=yes",
		"-o", "ControlMaster=auto",
		"-o", "ControlPersist=60s",
		"-o", "ControlPath=" + r.ControlPath(),
	}
	if r.port > 0 {
		args = append(args, "-p", strconv.Itoa(r.port))
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
