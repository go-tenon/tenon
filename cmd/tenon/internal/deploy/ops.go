package deploy

import (
	"context"
	"fmt"
	"io"
	"os"
	"path"
	"slices"
	"strings"
)

// Rollback 将环境内所有主机回退到指定版本（version 为空时自动选择当前版本之前的最近一个版本），
// 并触发 systemd 热重载。数据库迁移不会自动撤销。
func Rollback(ctx context.Context, r Runner, t Target, version string, out, errOut io.Writer) error {
	primary := t.MigrateHost()
	if version == "" {
		prev, err := previousRelease(ctx, r, t, primary)
		if err != nil {
			return err
		}
		version = prev
	}
	if !isSafeVersion(version) {
		return fmt.Errorf("deploy: invalid release %q", version)
	}
	if err := acquireLock(ctx, r, t, primary, "rollback-"+version); err != nil {
		return err
	}
	defer func() { _ = releaseLock(context.Background(), r, t, primary) }()

	// 先确认所有主机都保留了目标版本，避免滚动到一半缺文件。
	releaseBin := path.Join(t.Dir, "releases", version, t.App)
	releaseCfg := path.Join(t.Dir, "releases", version, "config")
	hasConfig := false
	for i, h := range t.Hosts {
		check := fmt.Sprintf("test -x %s || { echo 'release %s not found' >&2; exit 1; }; if test -d %s; then echo cfg; fi",
			shellQuote(releaseBin), version, shellQuote(releaseCfg))
		res, err := RunCapture(ctx, r, h, check)
		if err != nil {
			return fmt.Errorf("[%s] 检查回退版本 %s 失败: %w", h.Addr, version, err)
		}
		if i == 0 && strings.TrimSpace(res) == "cfg" {
			hasConfig = true
		}
	}
	for _, h := range t.Hosts {
		fmt.Fprintf(out, "[%s] 回退到版本 %s...\n", h.Addr, version)
		if err := switchHost(ctx, r, t, h, version, hasConfig, out, errOut); err != nil {
			return err
		}
	}
	fmt.Fprintf(out, "已回退到版本 %s。注意：数据库迁移不会自动撤销。\n", version)
	return nil
}

// previousRelease 读取主节点的当前版本和历史版本列表，返回当前版本之前最近的一个可回退版本。
func previousRelease(ctx context.Context, r Runner, t Target, h Host) (string, error) {
	curRaw, err := RunCapture(ctx, r, h, fmt.Sprintf("cat %s 2>/dev/null || true", shellQuote(path.Join(t.Dir, "current_release"))))
	if err != nil {
		return "", err
	}
	current := strings.TrimSpace(curRaw)
	raw, err := RunCapture(ctx, r, h, fmt.Sprintf("ls -1 %s 2>/dev/null || true", shellQuote(path.Join(t.Dir, "releases"))))
	if err != nil {
		return "", err
	}
	var versions []string
	for line := range strings.Lines(raw) {
		if v := strings.TrimSpace(line); v != "" {
			versions = append(versions, v)
		}
	}
	slices.Sort(versions)
	for i := len(versions) - 1; i >= 0; i-- {
		if versions[i] != current {
			return versions[i], nil
		}
	}
	return "", fmt.Errorf("deploy: no previous release available to roll back (current: %q)", current)
}

// Status 输出各目标主机当前的发布版本以及每个 systemd unit 的运行状态。
func Status(ctx context.Context, r Runner, t Target, hostFilter string, out io.Writer) error {
	hosts, err := t.FilterHosts(hostFilter)
	if err != nil {
		return err
	}
	for _, h := range hosts {
		var script strings.Builder
		fmt.Fprintf(&script, "echo \"release=$(cat %s 2>/dev/null || echo none)\"\n", shellQuote(path.Join(t.Dir, "current_release")))
		for _, part := range h.Parts {
			unit := UnitName(t.App, part)
			fmt.Fprintf(&script, "printf 'unit=%s ' ; systemctl show -p ActiveState -p SubState -p StatusText -p ActiveEnterTimestamp %s | tr '\\n' ' ' ; echo ''\n",
				unit, shellQuote(unit))
		}
		res, err := RunCapture(ctx, r, h, script.String())
		if err != nil {
			return fmt.Errorf("[%s] 查询状态失败: %w", h.Addr, err)
		}
		fmt.Fprintf(out, "== %s (%s) ==\n%s", h.Addr, t.EnvName, res)
	}
	return nil
}

// LogsOptions 控制 tenon deploy logs 的查询范围。
type LogsOptions struct {
	Host   string // 指定主机；留空时默认取首台主机
	Part   string // 指定服务部分（如 http）；留空时匹配全部 <app>@*
	Lines  int    // 返回最近多少行，默认 100
	Follow bool   // 持续跟踪输出
}

// Logs 查看远端主机的 journald 日志。
func Logs(ctx context.Context, r Runner, t Target, opts LogsOptions, out, errOut io.Writer) error {
	hosts, err := t.FilterHosts(opts.Host)
	if err != nil {
		return err
	}
	h := hosts[0]
	lines := opts.Lines
	if lines <= 0 {
		lines = 100
	}
	unit := t.App + "@*"
	if opts.Part != "" {
		unit = UnitName(t.App, opts.Part)
	}
	cmd := fmt.Sprintf("sudo journalctl -u %s -n %d --no-pager", shellQuote(unit), lines)
	if opts.Follow {
		cmd += " -f"
	}
	return r.Run(ctx, h, cmd, nil, out, errOut)
}

// RunRemote 在远端主机以真实服务用户、环境变量文件和工作目录运行一次性应用命令（例如 doctor、queue status）。
func RunRemote(ctx context.Context, r Runner, t Target, hostFilter string, args []string, out, errOut io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("deploy: command arguments are required")
	}
	hosts, err := t.FilterHosts(hostFilter)
	if err != nil {
		return err
	}
	h := hosts[0]
	if hostFilter == "" {
		h = t.MigrateHost()
	}
	liveBin := path.Join(t.Dir, "bin", t.App)
	argv := append([]string{liveBin}, args...)
	return r.Run(ctx, h, systemdRunCommand(t, t.Dir, argv...), nil, out, errOut)
}

// EnvPush 将本地环境变量文件通过标准输入安全写入目标主机的 env_file（权限 600，属主 root）；
// restart 为真时顺带重启目标主机的服务使新环境变量立即生效。
func EnvPush(ctx context.Context, r Runner, t Target, localFile, hostFilter string, restart bool, out, errOut io.Writer) error {
	data, err := os.ReadFile(localFile)
	if err != nil {
		return fmt.Errorf("deploy: read %s: %w", localFile, err)
	}
	hosts, err := t.FilterHosts(hostFilter)
	if err != nil {
		return err
	}
	envDir := path.Dir(t.EnvFile)
	writeCmd := fmt.Sprintf("sudo install -d -m 750 -o root -g root %s && sudo install -m 600 -o root -g root /dev/stdin %s",
		shellQuote(envDir), shellQuote(t.EnvFile))
	for _, h := range hosts {
		if err := r.Run(ctx, h, writeCmd, strings.NewReader(string(data)), io.Discard, errOut); err != nil {
			return fmt.Errorf("[%s] 写入 %s 失败: %w", h.Addr, t.EnvFile, err)
		}
		fmt.Fprintf(out, "[%s] 已更新 %s\n", h.Addr, t.EnvFile)
		if restart {
			for _, part := range h.Parts {
				unit := UnitName(t.App, part)
				if _, err := RunCapture(ctx, r, h, "sudo systemctl restart "+shellQuote(unit)); err != nil {
					return fmt.Errorf("[%s] 重启 %s 失败: %w", h.Addr, unit, err)
				}
				fmt.Fprintf(out, "[%s] 已重启 %s\n", h.Addr, unit)
			}
		}
	}
	if !restart {
		fmt.Fprintln(out, "提示：热重载继承旧进程的环境变量；传入 --restart 或执行 systemctl restart 后生效。")
	}
	return nil
}

// EnvPull 读取远端主机的环境变量文件内容并输出到 out。
func EnvPull(ctx context.Context, r Runner, t Target, hostFilter string, out io.Writer) error {
	hosts, err := t.FilterHosts(hostFilter)
	if err != nil {
		return err
	}
	h := hosts[0]
	if hostFilter == "" {
		h = t.MigrateHost()
	}
	content, err := RunCapture(ctx, r, h, "sudo cat "+shellQuote(t.EnvFile))
	if err != nil {
		return fmt.Errorf("[%s] 读取 %s 失败: %w", h.Addr, t.EnvFile, err)
	}
	_, err = io.WriteString(out, content)
	return err
}

// Maintenance 在全部主机上创建或删除 maintenance_file，控制反向代理的 503 维护页。
func Maintenance(ctx context.Context, r Runner, t Target, on bool, out io.Writer) error {
	if t.MaintenanceFile == "" {
		return fmt.Errorf("deploy: maintenance_file is not configured in environment %q", t.EnvName)
	}
	for _, h := range t.Hosts {
		var cmd string
		action := "关闭维护模式"
		if on {
			action = "开启维护模式"
			cmd = fmt.Sprintf("sudo install -d -m 755 %s && sudo touch %s",
				shellQuote(path.Dir(t.MaintenanceFile)), shellQuote(t.MaintenanceFile))
		} else {
			cmd = "sudo rm -f " + shellQuote(t.MaintenanceFile)
		}
		if _, err := RunCapture(ctx, r, h, cmd); err != nil {
			return fmt.Errorf("[%s] %s失败: %w", h.Addr, action, err)
		}
		fmt.Fprintf(out, "[%s] 已%s（%s）\n", h.Addr, action, t.MaintenanceFile)
	}
	return nil
}

// Unlock 强制清除主节点上遗留的发布锁目录（用于上次发布异常中断后的恢复）。
func Unlock(ctx context.Context, r Runner, t Target, out io.Writer) error {
	primary := t.MigrateHost()
	if err := releaseLock(ctx, r, t, primary); err != nil {
		return fmt.Errorf("[%s] 释放发布锁失败: %w", primary.Addr, err)
	}
	fmt.Fprintf(out, "[%s] 已释放发布锁 %s\n", primary.Addr, lockPath(t))
	return nil
}
