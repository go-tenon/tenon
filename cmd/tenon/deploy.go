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
// 支持 deploy/app.yaml + deploy/targets/<name>/ 多独立实例管理，所有子命令均支持 --format json。
func deployCommand() *cobra.Command {
	var deployDir, targetsDir, format, binary, tag, version string
	var targetNames []string
	var allTargets, allowDirty, force, skipDoctor, autoRollback, noCache, dryRun bool

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

	// tryLoadProject 在处于 Tenon 源码仓库时读取项目配置；脱离源码仓库执行产物发布或运维命令时返回 nil。
	tryLoadProject := func() *project.Config {
		cfg, err := project.Load(".")
		if err != nil {
			return nil
		}
		return cfg
	}

	loadOpts := func(cfg *project.Config) deploy.LoadOptions {
		defaultApp := ""
		if cfg != nil && cfg.Main != "" {
			defaultApp = path.Base(cfg.Main)
		}
		return deploy.LoadOptions{
			Dir:        deployDir,
			TargetsDir: targetsDir,
			DefaultApp: defaultApp,
		}
	}

	loadSingleTarget := func() (*project.Config, deploy.Target, error) {
		if err := checkFormat(); err != nil {
			return nil, deploy.Target{}, err
		}
		if len(targetNames) > 1 {
			return nil, deploy.Target{}, fmt.Errorf("this subcommand accepts a single --target, got %d", len(targetNames))
		}
		name := ""
		if len(targetNames) == 1 {
			name = targetNames[0]
		}
		cfg := tryLoadProject()
		target, err := deploy.Load(loadOpts(cfg), name)
		if err != nil {
			return nil, deploy.Target{}, err
		}
		return cfg, target, nil
	}

	loadTargets := func(all bool) (*project.Config, []deploy.Target, error) {
		if err := checkFormat(); err != nil {
			return nil, nil, err
		}
		cfg := tryLoadProject()
		targets, err := deploy.LoadMany(loadOpts(cfg), targetNames, all)
		if err != nil {
			return nil, nil, err
		}
		return cfg, targets, nil
	}

	withRunner := func(target deploy.Target, fn func(*deploy.SSHRunner) error) error {
		r, err := deploy.NewSSHRunner(target)
		if err != nil {
			return err
		}
		defer func() { _ = r.Close() }()
		return fn(r)
	}

	runnerFactory := func(t deploy.Target) (deploy.Runner, func() error, error) {
		r, err := deploy.NewSSHRunner(t)
		if err != nil {
			return nil, nil, err
		}
		return r, r.Close, nil
	}

	cmd := &cobra.Command{
		Use:   "deploy",
		Short: "通过 SSH 和 systemd 执行原生单二进制零停机部署与多实例远端运维",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, targets, err := loadTargets(allTargets)
			if err != nil {
				return err
			}
			var buildFn func(context.Context, string, string, deploy.GitBuildInfo) error
			if binary == "" && tag == "" && !dryRun {
				if cfg == nil {
					if _, loadErr := project.Load("."); loadErr != nil {
						return fmt.Errorf("deploy: local build requires a Tenon project (pass --tag or --binary to deploy outside source repo): %w", loadErr)
					}
				}
				buildFn = func(_ context.Context, arch, outPath string, meta deploy.GitBuildInfo) error {
					return buildBinary(cmd, cfg, outPath, arch, buildOptions{
						Version:    meta.Version,
						Commit:     meta.Commit,
						CommitTime: meta.CommitTime,
						NoCache:    noCache,
					})
				}
			}
			opts := deploy.ReleaseOptions{
				Binary:       binary,
				Tag:          tag,
				Version:      version,
				AllowDirty:   allowDirty,
				Force:        force,
				SkipDoctor:   skipDoctor,
				AutoRollback: autoRollback,
				DryRun:       dryRun,
				Build:        buildFn,
			}
			batch, err := deploy.DeployMany(cmd.Context(), targets, runnerFactory, opts, textOut(cmd), cmd.ErrOrStderr())
			if err != nil {
				return err
			}
			if format == "json" {
				if len(batch.Targets) == 1 && !allTargets {
					return writeJSON(cmd, batch.Targets[0])
				}
				return writeJSON(cmd, batch)
			}
			return nil
		},
	}
	cmd.PersistentFlags().StringVarP(&deployDir, "dir", "d", deploy.DefaultDir, "部署配置根目录（含 app.yaml）")
	cmd.PersistentFlags().StringVar(&targetsDir, "targets-dir", "", "部署实例目录，默认依次取 TENON_DEPLOY_TARGETS 环境变量与 <dir>/targets")
	cmd.PersistentFlags().StringSliceVarP(&targetNames, "target", "t", nil, "目标实例名（deploy/config 支持重复传入或逗号分隔多个目标；仅存在唯一实例时可省略）")
	cmd.PersistentFlags().StringVar(&format, "format", "text", "输出格式：text（给人阅读）或 json（给 AI 编程助手与脚本解析）")
	cmd.Flags().BoolVar(&allTargets, "all", false, "按名称顺序依次发布全部已发现的目标实例（同一架构仅构建一次）")
	cmd.Flags().StringVar(&binary, "binary", "", "直接发布指定的 Linux 二进制，跳过本地构建（适合 CI 构建与发布分离）")
	cmd.Flags().StringVar(&tag, "tag", "", "从 GitHub Release 拉取指定 Git Tag（如 v1.2.0）对应的 Linux 二进制并校验 SHA-256")
	cmd.Flags().StringVar(&version, "version", "", "构建版本号，默认取 --tag 或 git describe --tags --always --dirty")
	cmd.Flags().BoolVar(&allowDirty, "allow-dirty", false, "允许在存在未提交改动的工作区执行本地构建发布")
	cmd.Flags().BoolVar(&force, "force", false, "跳过 No-op 检测，即使二进制与配置均未变化仍创建新发布并切换")
	cmd.Flags().BoolVar(&skipDoctor, "skip-doctor", false, "跳过上线前的 doctor 自检")
	cmd.Flags().BoolVar(&autoRollback, "auto-rollback", false, "多机滚动波次中途失败时自动将已切换主机回退到上一发布")
	cmd.Flags().BoolVar(&noCache, "no-cache", false, "跳过前端构建指纹缓存，强制全量重建 apps/dist")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "只输出发布计划，不修改远端主机")

	// config：仅同步配置文件并热重载，复用线上当前二进制（零编译、零上传二进制、不执行迁移）。
	var cfgAll, cfgForce, cfgSkipDoctor, cfgAutoRollback, cfgDryRun bool
	configCmd := &cobra.Command{
		Use:   "config",
		Short: "仅同步配置文件并零停机热重载，复用线上当前二进制（不编译、不上传二进制、不执行迁移）",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, targets, err := loadTargets(cfgAll)
			if err != nil {
				return err
			}
			batch, err := deploy.DeployConfigMany(cmd.Context(), targets, runnerFactory, deploy.ConfigReleaseOptions{
				Force:        cfgForce,
				SkipDoctor:   cfgSkipDoctor,
				AutoRollback: cfgAutoRollback,
				DryRun:       cfgDryRun,
			}, textOut(cmd), cmd.ErrOrStderr())
			if err != nil {
				return err
			}
			if format == "json" {
				if len(batch.Targets) == 1 && !cfgAll {
					return writeJSON(cmd, batch.Targets[0])
				}
				return writeJSON(cmd, batch)
			}
			return nil
		},
	}
	configCmd.Flags().BoolVar(&cfgAll, "all", false, "按顺序为全部目标实例执行仅配置热发布")
	configCmd.Flags().BoolVar(&cfgForce, "force", false, "即使配置内容未变化仍强制创建发布并重载")
	configCmd.Flags().BoolVar(&cfgSkipDoctor, "skip-doctor", false, "跳过切换前的 doctor 自检")
	configCmd.Flags().BoolVar(&cfgAutoRollback, "auto-rollback", false, "波次中途失败时自动回退已切换主机")
	configCmd.Flags().BoolVar(&cfgDryRun, "dry-run", false, "只输出配置发布计划，不修改远端主机")

	// setup：幂等初始化目标服务器。
	var setupRole, setupHost string
	var setupDryRun bool
	setupCmd := &cobra.Command{
		Use:   "setup",
		Short: "幂等初始化目标主机：检查环境、建系统用户与目录、写入按实例隔离的 systemd 模板 unit 与运维入口",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, target, err := loadSingleTarget()
			if err != nil {
				return err
			}
			hasSSR := target.HasSSR
			if cfg != nil && slices.ContainsFunc(cfg.Frontends, func(f project.Frontend) bool { return f.SSR }) {
				hasSSR = true
			}
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

	// releases：列出主节点留存的历史发布与当前激活版本。
	releasesCmd := &cobra.Command{
		Use:   "releases",
		Short: "查看主节点留存的历史发布清单（序号、Git 版本、配置摘要与发布时间）",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, target, err := loadSingleTarget()
			if err != nil {
				return err
			}
			return withRunner(target, func(r *deploy.SSHRunner) error {
				report, err := deploy.ListReleases(cmd.Context(), r, target)
				if err != nil {
					return err
				}
				if format == "json" {
					return writeJSON(cmd, report)
				}
				deploy.FormatReleases(cmd.OutOrStdout(), report)
				return nil
			})
		},
	}

	// rollback：回退到历史发布（默认按激活历史栈回退，也可指定发布序号或 Git Tag）。
	rollbackCmd := &cobra.Command{
		Use:   "rollback [release|tag]",
		Short: "回退二进制与配置到上一激活发布、指定发布序号（如 41）或 Git Tag（如 v1.2.0）（不撤销数据库迁移）",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			_, target, err := loadSingleTarget()
			if err != nil {
				return err
			}
			ref := ""
			if len(args) == 1 {
				ref = args[0]
			}
			return withRunner(target, func(r *deploy.SSHRunner) error {
				report, err := deploy.Rollback(cmd.Context(), r, target, ref, textOut(cmd), cmd.ErrOrStderr())
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

	// status：查看目标实例各角色与主机版本、systemd 状态。
	var statusRole, statusHost string
	statusCmd := &cobra.Command{
		Use:   "status",
		Short: "查看目标实例各主机当前的发布序号、构建版本、配置摘要与 systemd 服务状态",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, target, err := loadSingleTarget()
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
			_, target, err := loadSingleTarget()
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
			_, target, err := loadSingleTarget()
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
			_, target, err := loadSingleTarget()
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
			_, target, err := loadSingleTarget()
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
			_, target, err := loadSingleTarget()
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

	cmd.AddCommand(configCmd, setupCmd, releasesCmd, rollbackCmd, statusCmd, logsCmd, runCmd, envCmd, maintCmd)
	return cmd
}
