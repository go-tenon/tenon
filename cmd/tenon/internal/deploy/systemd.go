package deploy

import (
	"fmt"
	"path"
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
