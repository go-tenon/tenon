package command

import (
	"encoding/json"
	"fmt"
	"runtime/debug"
	"strings"

	"github.com/spf13/cobra"
)

// 构建元数据由 tenon build 通过 -ldflags -X 注入，未注入时自动回退到 Go 内置 VCS 信息。
var (
	buildVersion string
	buildCommit  string
	buildTime    string
)

// BuildMetadata 描述当前二进制的构建版本与提交信息。
type BuildMetadata struct {
	Version string `json:"version"`          // 版本号（优先来自 Git Tag，例如 v1.2.0 或 v1.2.0-3-g830eab5）
	Commit  string `json:"commit,omitempty"` // Git 短提交号
	Time    string `json:"time,omitempty"`   // 构建时间（RFC3339）
	Dirty   bool   `json:"dirty,omitempty"`  // 构建时工作区存在未提交改动
}

// String 返回面向终端的单行版本描述。
func (m BuildMetadata) String() string {
	ver := m.Version
	if ver == "" {
		ver = "dev"
	}
	if m.Commit != "" && !strings.Contains(ver, m.Commit) {
		ver += " (" + m.Commit + ")"
	}
	if m.Time != "" {
		ver += " " + m.Time
	}
	return ver
}

// BuildInfo 返回当前二进制的构建信息：优先读取 tenon build 注入的元数据，缺省时从 debug.ReadBuildInfo 补齐。
func BuildInfo() BuildMetadata {
	meta := BuildMetadata{
		Version: strings.TrimSpace(buildVersion),
		Commit:  strings.TrimSpace(buildCommit),
		Time:    strings.TrimSpace(buildTime),
	}
	if strings.HasSuffix(meta.Version, "-dirty") {
		meta.Dirty = true
	}
	if info, ok := debug.ReadBuildInfo(); ok {
		if meta.Version == "" && info.Main.Version != "" && info.Main.Version != "(devel)" {
			meta.Version = info.Main.Version
		}
		for _, s := range info.Settings {
			switch s.Key {
			case "vcs.revision":
				if meta.Commit == "" {
					meta.Commit = s.Value
					if len(meta.Commit) > 7 {
						meta.Commit = meta.Commit[:7]
					}
				}
			case "vcs.time":
				if meta.Time == "" {
					meta.Time = s.Value
				}
			case "vcs.modified":
				if s.Value == "true" {
					meta.Dirty = true
				}
			}
		}
	}
	if meta.Version == "" {
		meta.Version = "dev"
	}
	return meta
}

// Version 返回 version 运维子命令，输出应用的构建版本号、Git 提交与构建时间，支持 --format text|json。
func Version() *cobra.Command {
	var format string
	cmd := leaf("version", "显示应用的构建版本与提交信息", func(cmd *cobra.Command) error {
		meta := BuildInfo()
		switch format {
		case "text":
			_, err := fmt.Fprintln(cmd.OutOrStdout(), meta.String())
			return err
		case "json":
			enc := json.NewEncoder(cmd.OutOrStdout())
			enc.SetIndent("", "  ")
			return enc.Encode(meta)
		default:
			return AsExitError(ExitUsage, fmt.Errorf("command: --format %q must be text or json", format))
		}
	})
	cmd.Flags().StringVar(&format, "format", "text", "输出格式：text 或 json")
	return InGroup(cmd, GroupOps)
}
