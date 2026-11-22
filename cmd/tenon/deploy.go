package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"path"
	"slices"

	"github.com/spf13/cobra"

	"github.com/go-tenon/tenon/cmd/tenon/internal/deploy"
	"github.com/go-tenon/tenon/cmd/tenon/internal/project"
)

// deployCommand 返回 tenon deploy 命令及其子命令：基于 SSH 和 systemd 执行原生单二进制零停机部署与远端运维，
// 所有子命令均支持 --format json 供 AI 编程助手和脚本直接解析。
func deployCommand() *cobra.Command {
	var file, envName, format, binary, version string
	var skipDoctor, dryRun bool

	checkFormat := func() error {
		if format != "text" && format != "json" {
			return fmt.Errorf("--format %q: must be text or json", format)
		}
		return nil
	}

	// textOut 在 --format json 时把人类进度提示静音，保证 stdout 只输出单个合法 JSON 对象。
	textOut := func(cmd *cobra.Command) io.Writer {
		if format == "json" {
			return io.Discard
		}
		return cmd.OutOrStdout()
	}

	writeJSON := func(cmd *cobra.Command, v any) error {
		enc := json.NewEncoder(cmd.OutOrStdout())
		enc.SetIndent("", "  ")
		enc.SetEscapeHTML(false)
		return enc.Encode(v)
	}

	loadTarget := func() (*project.Config, deploy.Target, error) {
		if err := checkFormat(); err != nil {
			return nil, deploy.Target{}, err
		}
		cfg, err := project.Load(".")
		if err != nil {
			return nil, deploy.Target{}, err
		}
		target, err := deploy.Load(file, path.Base(cfg.Main), envName)
		if err != nil {
			return nil, deploy.Target{}, err
		}
		return cfg, target, nil
	}

	withRunner := func(target deploy.Target, fn func(*deploy.SSHRunner) error) error {
		r, err := deploy.NewSSHRunner(target)
		if err != nil {
			return err
		}
		defer func() { _ = r.Close() }()
		return fn(r)
	}

	cmd := &cobra.Command{
		Use:   "deploy",
		Short: "通过 SSH 和 systemd 执行原生单二进制零停机部署与远端运维",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, target, err := loadTarget()
			if err != nil {
				return err
			}
			return withRunner(target, func(r *deploy.SSHRunner) error {
				report, err := deploy.Deploy(cmd.Context(), r, target, deploy.ReleaseOptions{
					Binary:     binary,
					Version:    version,
					SkipDoctor: skipDoctor,
					DryRun:     dryRun,
					Build: func(_ context.Context, arch, outPath string) error {
						return buildBinary(cmd, cfg, outPath, arch)
					},
				}, textOut(cmd), cmd.ErrOrStderr())
				if err != nil {
					return err
				}
				if format == "json" {
					return writeJSON(cmd, report)
				}
				return nil
			})
		},
	}
	cmd.PersistentFlags().StringVarP(&file, "file", "f", deploy.DefaultFile, "部署配置文件路径")
	cmd.PersistentFlags().StringVarP(&envName, "env", "e", "", "目标环境名，默认 production（只有一个环境时自动选用）")
	cmd.PersistentFlags().StringVar(&format, "format", "text", "输出格式：text（给人阅读）或 json（给 AI 编程助手与脚本解析）")
	cmd.Flags().StringVar(&binary, "binary", "", "直接发布指定的 Linux 二进制，跳过本地构建（适合 CI 构建与发布分离）")
	cmd.Flags().StringVar(&version, "version", "", "发布版本号，默认时间戳加当前 Git 短提交号")
	cmd.Flags().BoolVar(&skipDoctor, "skip-doctor", false, "跳过上线前的 doctor 自检")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "只输出发布计划，不修改远端主机")

	// setup：幂等初始化目标服务器。
	var setupRole, setupHost string
	var setupDryRun bool
	setupCmd := &cobra.Command{
		Use:   "setup",
		Short: "幂等初始化目标主机：检查环境、建系统用户与目录、写入 systemd 模板 unit、角色 drop-in 和 env 文件骨架",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, target, err := loadTarget()
			if err != nil {
				return err
			}
			hasSSR := slices.ContainsFunc(cfg.Frontends, func(f project.Frontend) bool { return f.SSR })
			return withRunner(target, func(r *deploy.SSHRunner) error {
				report, err := deploy.Setup(cmd.Context(), r, target, deploy.SetupOptions{
					Role:   setupRole,
					Host:   setupHost,
					HasSSR: hasSSR,
					DryRun: setupDryRun,
				}, textOut(cmd))
				if err != nil {
					return err
				}
				if format == "json" {
					return writeJSON(cmd, report)
				}
				return nil
			})
		},
	}
	setupCmd.Flags().StringVar(&setupRole, "role", "", "只初始化指定角色的主机，留空表示全部角色")
	setupCmd.Flags().StringVar(&setupHost, "host", "", "只初始化指定主机，留空表示全部主机")
	setupCmd.Flags().BoolVar(&setupDryRun, "dry-run", false, "只输出初始化计划，不修改远端主机")

	// rollback：回退到历史版本。
	rollbackCmd := &cobra.Command{
		Use:   "rollback [version]",
		Short: "回退二进制与配置到上一版本或指定版本并按波次热重载（不撤销数据库迁移）",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			_, target, err := loadTarget()
			if err != nil {
				return err
			}
			ver := ""
			if len(args) == 1 {
				ver = args[0]
			}
			return withRunner(target, func(r *deploy.SSHRunner) error {
				report, err := deploy.Rollback(cmd.Context(), r, target, ver, textOut(cmd), cmd.ErrOrStderr())
				if err != nil {
					return err
				}
				if format == "json" {
					return writeJSON(cmd, report)
				}
				return nil
			})
		},
	}

	// status：查看集群各角色与主机版本、systemd 状态。
	var statusRole, statusHost string
	statusCmd := &cobra.Command{
		Use:   "status",
		Short: "查看各目标主机当前的发布版本与 systemd 服务状态",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, target, err := loadTarget()
			if err != nil {
				return err
			}
			return withRunner(target, func(r *deploy.SSHRunner) error {
				report, err := deploy.CollectStatus(cmd.Context(), r, target, statusRole, statusHost)
				if err != nil {
					return err
				}
				if format == "json" {
					return writeJSON(cmd, report)
				}
				deploy.FormatStatus(cmd.OutOrStdout(), report)
				return nil
			})
		},
	}
	statusCmd.Flags().StringVar(&statusRole, "role", "", "只查询指定角色的主机")
	statusCmd.Flags().StringVar(&statusHost, "host", "", "只查询指定主机")

	// logs：查看单机或多机实时汇流的 journald 日志。
	var logsOpts deploy.LogsOptions
	logsCmd := &cobra.Command{
		Use:   "logs",
		Short: "查看单机或多台主机实时汇流的 journald 日志",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, target, err := loadTarget()
			if err != nil {
				return err
			}
			if format == "json" && logsOpts.Follow {
				return fmt.Errorf("--format json cannot be combined with --follow")
			}
			return withRunner(target, func(r *deploy.SSHRunner) error {
				if format == "json" {
					report, err := deploy.CollectLogs(cmd.Context(), r, target, logsOpts)
					if err != nil {
						return err
					}
					return writeJSON(cmd, report)
				}
				return deploy.Logs(cmd.Context(), r, target, logsOpts, cmd.OutOrStdout(), cmd.ErrOrStderr())
			})
		},
	}
	logsCmd.Flags().StringVar(&logsOpts.Role, "role", "", "按角色筛选主机，多台主机自动并发汇流并带主机前缀")
	logsCmd.Flags().StringVar(&logsOpts.Host, "host", "", "只查看指定主机，留空时汇流全部匹配主机")
	logsCmd.Flags().StringVar(&logsOpts.Part, "part", "", "指定服务部分（如 http），留空查看全部部分")
	logsCmd.Flags().IntVarP(&logsOpts.Lines, "lines", "n", 100, "每台主机显示最近的行数")
	logsCmd.Flags().StringVar(&logsOpts.Since, "since", "", "时间范围，传给 journalctl --since（例如 10m、1h）")
	logsCmd.Flags().StringVar(&logsOpts.Contains, "contains", "", "只保留包含该文字的行（例如 ERR 或请求 ID）")
	logsCmd.Flags().BoolVarP(&logsOpts.Follow, "follow", "F", false, "持续跟踪最新日志")

	// run：以真实服务用户和环境变量执行一次性应用命令。
	var runRole, runHost string
	runCmd := &cobra.Command{
		Use:   "run -- <args...>",
		Short: "在远端以服务用户、环境变量和工作目录执行一次性应用命令（例如 doctor、queue status）",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			_, target, err := loadTarget()
			if err != nil {
				return err
			}
			return withRunner(target, func(r *deploy.SSHRunner) error {
				return deploy.RunRemote(cmd.Context(), r, target, runRole, runHost, args, cmd.OutOrStdout(), cmd.ErrOrStderr())
			})
		},
	}
	runCmd.Flags().StringVar(&runRole, "role", "", "目标角色，留空时默认在主迁移节点执行")
	runCmd.Flags().StringVar(&runHost, "host", "", "目标主机，留空时默认在主迁移节点执行")

	// env：管理远端环境变量文件。
	envCmd := &cobra.Command{Use: "env", Short: "同步或读取远端环境变量文件"}
	var envRole, envHost string
	var envRestart bool
	envPushCmd := &cobra.Command{
		Use:   "push <file>",
		Short: "将本地环境变量文件安全写入远端 env_file（权限 600）",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			_, target, err := loadTarget()
			if err != nil {
				return err
			}
			return withRunner(target, func(r *deploy.SSHRunner) error {
				report, err := deploy.EnvPush(cmd.Context(), r, target, args[0], envRole, envHost, envRestart, textOut(cmd), cmd.ErrOrStderr())
				if err != nil {
					return err
				}
				if format == "json" {
					return writeJSON(cmd, report)
				}
				return nil
			})
		},
	}
	envPushCmd.Flags().StringVar(&envRole, "role", "", "只更新指定角色的主机，留空更新全部主机")
	envPushCmd.Flags().StringVar(&envHost, "host", "", "只更新指定主机，留空更新全部主机")
	envPushCmd.Flags().BoolVar(&envRestart, "restart", false, "写入后重启服务使新环境变量立即生效")

	var envPullRole, envPullHost string
	envPullCmd := &cobra.Command{
		Use:   "pull",
		Short: "读取远端 env_file 并输出到标准输出",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, target, err := loadTarget()
			if err != nil {
				return err
			}
			return withRunner(target, func(r *deploy.SSHRunner) error {
				return deploy.EnvPull(cmd.Context(), r, target, envPullRole, envPullHost, cmd.OutOrStdout())
			})
		},
	}
	envPullCmd.Flags().StringVar(&envPullRole, "role", "", "目标角色")
	envPullCmd.Flags().StringVar(&envPullHost, "host", "", "目标主机，默认主迁移节点")
	envCmd.AddCommand(envPushCmd, envPullCmd)

	// maintenance：开关反向代理维护页标记文件。
	var maintRole, maintHost string
	maintCmd := &cobra.Command{
		Use:   "maintenance <on|off>",
		Short: "在入口主机创建或删除 maintenance_file，开关反向代理 503 维护页",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			var on bool
			switch args[0] {
			case "on":
				on = true
			case "off":
				on = false
			default:
				return fmt.Errorf("maintenance expects 'on' or 'off', got %q", args[0])
			}
			_, target, err := loadTarget()
			if err != nil {
				return err
			}
			return withRunner(target, func(r *deploy.SSHRunner) error {
				report, err := deploy.Maintenance(cmd.Context(), r, target, maintRole, maintHost, on, textOut(cmd))
				if err != nil {
					return err
				}
				if format == "json" {
					return writeJSON(cmd, report)
				}
				return nil
			})
		},
	}
	maintCmd.Flags().StringVar(&maintRole, "role", "", "只操作指定角色的主机，留空默认仅操作入口主机")
	maintCmd.Flags().StringVar(&maintHost, "host", "", "只操作指定主机")

	// unlock：释放异常中断残留的发布锁。
	unlockCmd := &cobra.Command{
		Use:   "unlock",
		Short: "释放主节点上残留的发布锁（用于上次发布异常中断后的恢复）",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, target, err := loadTarget()
			if err != nil {
				return err
			}
			return withRunner(target, func(r *deploy.SSHRunner) error {
				report, err := deploy.Unlock(cmd.Context(), r, target, textOut(cmd))
				if err != nil {
					return err
				}
				if format == "json" {
					return writeJSON(cmd, report)
				}
				return nil
			})
		},
	}

	cmd.AddCommand(setupCmd, rollbackCmd, statusCmd, logsCmd, runCmd, envCmd, maintCmd, unlockCmd)
	return cmd
}
