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

// RenderUnit 生成与 Tenon 文档一致的 systemd 模板 unit 内容，启用 Type=notify-reload 与 RuntimeDirectory
// 以支持 command.Daemon 的零停机热升级。
func RenderUnit(t Target) string {
	bin := path.Join(t.Dir, "bin", t.App)
	storage := path.Join(t.Dir, "storage")
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
ExecStart=%s serve %%i
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
`, t.App, t.App, t.App, t.ServiceUser, t.Dir, t.EnvFile, bin, storage)
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
		cmd := fmt.Sprintf("sudo install -d -m 755 -o root -g root %s && sudo install -m 644 -o root -g root /dev/stdin %s",
			shellQuote(dir), shellQuote(dp))
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

// systemdRunCommand 构造以服务用户、环境变量文件和工作目录运行一次性命令的 shell 语句。
func systemdRunCommand(t Target, workDir string, argv ...string) string {
	parts := []string{
		"sudo", "systemd-run", "--wait", "--pipe", "--quiet",
		"--uid=" + shellQuote(t.ServiceUser),
		"-p", "EnvironmentFile=" + shellQuote(t.EnvFile),
		"-p", "WorkingDirectory=" + shellQuote(workDir),
	}
	for _, a := range argv {
		parts = append(parts, shellQuote(a))
	}
	return strings.Join(parts, " ")
}
