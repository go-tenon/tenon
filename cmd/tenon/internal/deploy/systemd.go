package deploy

import (
	"context"
	"fmt"
	"io"
	"path"
	"slices"
	"strings"
)

// UnitName 返回单个服务部分的 systemd unit 名称，例如 blog@http.service。
func UnitName(app, part string) string {
	return fmt.Sprintf("%s@%s.service", app, part)
}

// TemplateUnitPath 返回模板 unit 文件在目标机上的路径，例如 /etc/systemd/system/blog@.service。
func TemplateUnitPath(app string) string {
	return fmt.Sprintf("/etc/systemd/system/%s@.service", app)
}

// DropInPath 返回某个服务部分的 systemd drop-in 覆盖文件路径。
func DropInPath(app, part string) string {
	return fmt.Sprintf("/etc/systemd/system/%s@%s.service.d/override.conf", app, part)
}

// WrapperPath 返回安装在目标主机 PATH 中的运维包装脚本路径，例如 /usr/local/bin/blog。
func WrapperPath(app string) string {
	return path.Join("/usr/local/bin", app)
}

// RenderWrapper 生成安装在 /usr/local/bin/<app> 的包装脚本：
// 以服务用户、工作目录、环境变量文件和当前部署配置（APP_CONFIG）运行二进制；
// 终端交互时自动启用 --pty，管道或非交互场景使用 --pipe；命令行显式传入 --config 时优先级高于 APP_CONFIG。
func RenderWrapper(t Target) string {
	bin := path.Join(t.Dir, "bin", t.App)
	var b strings.Builder
	b.WriteString("#!/usr/bin/env bash\n")
	b.WriteString("set -euo pipefail\n")
	fmt.Fprintf(&b, "# 由 tenon deploy setup 生成，以服务用户、工作目录和部署配置运行 %s。\n", t.App)
	b.WriteString("run=()\n")
	b.WriteString("[[ $(id -u) -eq 0 ]] || run=(sudo)\n")
	b.WriteString("mode=(--pipe)\n")
	b.WriteString("[[ -t 0 && -t 1 ]] && mode=(--pty)\n")
	b.WriteString("exec \"${run[@]}\" systemd-run --wait --quiet \"${mode[@]}\" \\\n")
	fmt.Fprintf(&b, "  --uid=%s \\\n", shellQuote(t.ServiceUser))
	fmt.Fprintf(&b, "  -p %s \\\n", shellQuote("EnvironmentFile="+t.EnvFile))
	fmt.Fprintf(&b, "  -p %s \\\n", shellQuote("WorkingDirectory="+t.Dir))
	if cfgFlag := t.ConfigFlagValue(); cfgFlag != "" {
		fmt.Fprintf(&b, "  -p %s \\\n", shellQuote("Environment=APP_CONFIG="+cfgFlag))
	}
	fmt.Fprintf(&b, "  %s \"$@\"\n", shellQuote(bin))
	return b.String()
}

// RenderUnit 生成与 Tenon 文档一致的 systemd 模板 unit 内容，启用 Type=notify-reload 与 RuntimeDirectory
// 以支持 command.Daemon 的零停机热升级；配置了 config_files 时自动附加 --config 参数。
func RenderUnit(t Target) string {
	bin := path.Join(t.Dir, "bin", t.App)
	storage := path.Join(t.Dir, "storage")
	execStart := bin + " serve %i"
	if cfgFlag := t.ConfigFlagValue(); cfgFlag != "" {
		execStart = fmt.Sprintf("%s --config %s serve %%i", bin, cfgFlag)
	}
	return fmt.Sprintf(`[Unit]
Description=%s %%i
After=network-online.target
Wants=network-online.target

[Service]
Type=notify-reload
NotifyAccess=all
RuntimeDirectory=%s/%%i
PIDFile=/run/%s/%%i/daemon.pid
User=%s
WorkingDirectory=%s
EnvironmentFile=%s
ExecStart=%s
Restart=on-failure
RestartSec=5s
RestartPreventExitStatus=2 3
TimeoutStopSec=45s
NoNewPrivileges=yes
ProtectSystem=strict
ReadWritePaths=%s
PrivateTmp=yes

[Install]
WantedBy=multi-user.target
`, t.App, t.App, t.App, t.ServiceUser, t.Dir, t.EnvFile, execStart, storage)
}

// RenderDropIn 渲染单个服务部分的 systemd drop-in（override.conf），仅包含非空覆盖项。
func RenderDropIn(d PartDropIn) string {
	if d.Empty() {
		return ""
	}
	var b strings.Builder
	b.WriteString("[Service]\n")
	if d.StopTimeout != "" {
		fmt.Fprintf(&b, "TimeoutStopSec=%s\n", d.StopTimeout)
	}
	keys := make([]string, 0, len(d.Env))
	for k := range d.Env {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	for _, k := range keys {
		escaped := strings.ReplaceAll(d.Env[k], `\`, `\\`)
		escaped = strings.ReplaceAll(escaped, `"`, `\"`)
		fmt.Fprintf(&b, "Environment=\"%s=%s\"\n", k, escaped)
	}
	return b.String()
}

// syncDropIns 在目标主机同步每个 part 的 systemd drop-in 文件（新增、更新或清理已移除的覆盖项），
// 有变更时触发 daemon-reload。
func syncDropIns(ctx context.Context, r Runner, t Target, h Host) error {
	changed := false
	for _, part := range h.Parts {
		di := h.DropIns[part]
		dp := DropInPath(t.App, part)
		if di.Empty() {
			// 若曾经配置过 drop-in，现已清空则删除它。
			res, err := RunCapture(ctx, r, h, fmt.Sprintf("if sudo test -f %s; then sudo rm -f %s && echo removed; fi", shellQuote(dp), shellQuote(dp)))
			if err != nil {
				return fmt.Errorf("[%s] 清理 %s 失败: %w", h.Addr, dp, err)
			}
			if strings.TrimSpace(res) == "removed" {
				changed = true
			}
			continue
		}
		dir := path.Dir(dp)
		content := RenderDropIn(di)
		cmd := fmt.Sprintf("sudo install -d -m 755 -o root -g root %s && %s",
			shellQuote(dir), installFromStdinCommand("644", dp))
		if err := r.Run(ctx, h, cmd, strings.NewReader(content), io.Discard, io.Discard); err != nil {
			return fmt.Errorf("[%s] 写入 %s 失败: %w", h.Addr, dp, err)
		}
		changed = true
	}
	if changed {
		if _, err := RunCapture(ctx, r, h, "sudo systemctl daemon-reload"); err != nil {
			return fmt.Errorf("[%s] systemctl daemon-reload 失败: %w", h.Addr, err)
		}
	}
	return nil
}

// syncUnit 在目标主机按需同步 systemd 模板 unit 文件，内容变化时写入并执行 daemon-reload，返回是否发生了更新。
func syncUnit(ctx context.Context, r Runner, t Target, h Host) (bool, error) {
	unitPath := TemplateUnitPath(t.App)
	content := RenderUnit(t)
	script := fmt.Sprintf(`set -e
tmp=$(mktemp)
trap 'rm -f "$tmp"' EXIT
cat > "$tmp"
if ! sudo cmp -s "$tmp" %s; then
  sudo install -m 644 -o root -g root "$tmp" %s
  sudo systemctl daemon-reload
  echo updated
fi`, shellQuote(unitPath), shellQuote(unitPath))
	var out, errBuf strings.Builder
	if err := r.Run(ctx, h, script, strings.NewReader(content), &out, &errBuf); err != nil {
		msg := strings.TrimSpace(errBuf.String())
		if msg != "" {
			return false, fmt.Errorf("[%s] 同步 %s 失败: %w: %s", h.Addr, unitPath, err, msg)
		}
		return false, fmt.Errorf("[%s] 同步 %s 失败: %w", h.Addr, unitPath, err)
	}
	return strings.TrimSpace(out.String()) == "updated", nil
}

// installFromStdinCommand 构造把标准输入写入远端目标文件的命令：经 0600 临时文件中转并自动清理，
// 兼容 GNU coreutils 与 Ubuntu 26.04 uutils coreutils 重复覆盖已存在文件。
func installFromStdinCommand(mode, target string) string {
	return fmt.Sprintf(`umask 077 && tmp=$(mktemp) && trap 'rm -f "$tmp"' EXIT && cat > "$tmp" && sudo install -m %s -o root -g root "$tmp" %s`,
		mode, shellQuote(target))
}

// systemdRunCommand 构造以服务用户、环境变量文件和工作目录运行一次性命令的 shell 语句；
// 配置了 config_files 且调用方未显式传入 --config 时自动附加 --config 参数。
func systemdRunCommand(t Target, workDir string, argv ...string) string {
	parts := []string{
		"sudo", "systemd-run", "--wait", "--pipe", "--quiet",
		"--uid=" + shellQuote(t.ServiceUser),
		"-p", "EnvironmentFile=" + shellQuote(t.EnvFile),
		"-p", "WorkingDirectory=" + shellQuote(workDir),
	}
	if len(argv) > 0 {
		parts = append(parts, shellQuote(argv[0]))
		if cfgFlag := t.ConfigFlagValue(); cfgFlag != "" && !hasConfigFlag(argv[1:]) {
			parts = append(parts, shellQuote("--config"), shellQuote(cfgFlag))
		}
		for _, a := range argv[1:] {
			parts = append(parts, shellQuote(a))
		}
	}
	return strings.Join(parts, " ")
}

// hasConfigFlag 报告参数列表中是否已显式传入 --config 选项。
func hasConfigFlag(args []string) bool {
	for _, a := range args {
		if a == "--" {
			return false
		}
		if a == "--config" || strings.HasPrefix(a, "--config=") {
			return true
		}
	}
	return false
}
