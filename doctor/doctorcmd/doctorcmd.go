// Package doctorcmd 提供 doctor 命令：执行自检，列出每一项的结果、问题的修改方法和文档。
package doctorcmd

import (
	"encoding/json"

	"github.com/cockroachdb/errors"
	"github.com/spf13/cobra"

	"github.com/go-tenon/tenon/command"
	"github.com/go-tenon/tenon/doctor"
)

// New 返回 doctor 命令，执行 groups 中的检查：
//
//	doctor                 按组列出每一项的结果，问题下面是修改方法
//	doctor --format json   输出 JSON，供脚本和工单使用
//	doctor --strict        提示也算失败，适合在 CI 中检查目标环境
//
// 有检查失败时以 command.ExitUnavailable 退出，只有提示时以 0 退出。
// 和其他需要数据库的命令一样在 command.Bind 中声明：检查组只保存依赖，命令执行时才使用它们。
//
//	doctorcmd.New(a.doctor()...)
func New(groups ...doctor.Group) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "检查运行环境：依赖的版本和设置、迁移、权限，并给出修改方法",
		Args:  command.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			format, err := cmd.Flags().GetString("format")
			if err != nil {
				return err
			}
			strict, err := cmd.Flags().GetBool("strict")
			if err != nil {
				return err
			}
			if format != "text" && format != "json" {
				return command.AsExitError(command.ExitUsage, errors.Newf("invalid --format %q: must be text or json", format))
			}
			report := doctor.Run(cmd.Context(), groups...)
			out := cmd.OutOrStdout()
			if format == "json" {
				enc := json.NewEncoder(out)
				enc.SetIndent("", "  ")
				err = enc.Encode(report)
			} else {
				err = report.WriteText(out)
			}
			if err != nil {
				return err
			}
			failed, warnings := report.Count(doctor.Failed), report.Count(doctor.Warning)
			switch {
			case failed > 0:
				return command.AsExitError(command.ExitUnavailable, errors.Newf("doctor: %d check(s) failed", failed))
			case strict && warnings > 0:
				return command.AsExitError(command.ExitUnavailable, errors.Newf("doctor: %d warning(s) with --strict", warnings))
			}
			return nil
		},
	}
	cmd.Flags().String("format", "text", "输出格式：text 或 json")
	cmd.Flags().Bool("strict", false, "提示也算失败")
	return command.InGroup(cmd, command.GroupOps)
}
