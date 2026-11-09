package app

import (
	"os"
	"path/filepath"
	"text/template"

	"github.com/spf13/cobra"
)

var unitTemplate = template.Must(template.New("unit").Parse(`[Unit]
Description={{.Name}}
After=network-online.target postgresql.service
Wants=network-online.target

[Service]
Type=notify
User={{.User}}
Group={{.User}}
WorkingDirectory={{.Dir}}
ExecStart={{.Exec}} serve --config {{.Config}}
EnvironmentFile=-/etc/{{.Name}}/env
Restart=on-failure
RestartSec=5
# 停机时先等待负载均衡摘除（http.shutdown_delay）再等待进行中的请求（http.shutdown_timeout），
# 这里要大于二者之和。
TimeoutStopSec=60
WatchdogSec=30
LimitNOFILE=65536

# 安全加固：只能写 WorkingDirectory 下的 data 目录（本地文件存储）。
NoNewPrivileges=true
ProtectSystem=strict
ProtectHome=true
PrivateTmp=true
PrivateDevices=true
ProtectKernelTunables=true
ProtectKernelModules=true
ProtectControlGroups=true
RestrictSUIDSGID=true
ReadWritePaths={{.Dir}}/data

[Install]
WantedBy=multi-user.target
`))

func systemdCommand(opts Options) *cobra.Command {
	var data struct{ Name, User, Dir, Exec, Config string }
	cmd := &cobra.Command{
		Use:   "systemd",
		Short: "输出 systemd unit 文件",
		Long: `输出运行 serve 的 systemd unit 文件（Type=notify，带看门狗与安全加固）。用法：

  ` + opts.Name + ` systemd > /etc/systemd/system/` + opts.Name + `.service
  systemctl daemon-reload && systemctl enable --now ` + opts.Name + `

环境变量（如 DATABASE_URL）写在 /etc/` + opts.Name + `/env 中，每行 KEY=VALUE。`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			data.Name = opts.Name
			if data.Exec == "" {
				exe, err := os.Executable()
				if err != nil {
					return err
				}
				data.Exec = exe
			}
			if data.Dir == "" {
				data.Dir = filepath.Dir(data.Exec)
			}
			if data.User == "" {
				data.User = opts.Name
			}
			cfgPath, _ := cmd.Flags().GetString("config")
			if !filepath.IsAbs(cfgPath) {
				cfgPath = filepath.Join(data.Dir, cfgPath)
			}
			data.Config = cfgPath
			return unitTemplate.Execute(cmd.OutOrStdout(), data)
		},
	}
	cmd.Flags().StringVar(&data.User, "user", "", "运行服务的系统用户，默认与服务名相同")
	cmd.Flags().StringVar(&data.Dir, "dir", "", "工作目录，默认为可执行文件所在目录")
	cmd.Flags().StringVar(&data.Exec, "exec", "", "可执行文件的绝对路径，默认为当前可执行文件")
	return cmd
}
