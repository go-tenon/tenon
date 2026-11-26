package deploy

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"strconv"
	"strings"
	"time"
)

// ReleaseKind 标识一次发布的类型：完整发布、仅配置更新或回滚。
const (
	ReleaseKindFull     = "full"
	ReleaseKindConfig   = "config"
	ReleaseKindRollback = "rollback"
)

// Manifest 是写入远端 releases/<id>/manifest.json 的不可变发布清单，将代码版本（Git Tag）与发布序号分离。
type Manifest struct {
	ReleaseID    int       `json:"release_id"`              // 严格单调递增的发布序号（1, 2, 3...）
	Version      string    `json:"version"`                 // 构建版本号（优先来自 Git Tag，例如 v1.2.0 或 v1.2.0-3-g830eab5）
	Commit       string    `json:"commit,omitempty"`        // Git 短提交号
	Dirty        bool      `json:"dirty,omitempty"`         // 构建时工作区存在未提交改动
	BinSHA256    string    `json:"bin_sha256"`              // 二进制 SHA-256（对应远端 artifacts/<sha256>）
	ConfigSHA256 string    `json:"config_sha256,omitempty"` // 配置文件内容的确定性 SHA-256
	Kind         string    `json:"kind"`                    // full / config / rollback
	FromRelease  int       `json:"from_release,omitempty"`  // 本次发布前运行的发布序号
	DeployedAt   time.Time `json:"deployed_at"`             // 发布时间（UTC）
	DeployedBy   string    `json:"deployed_by,omitempty"`   // 发布操作者标识
}

// HistoryEntry 记录远端 history.jsonl 中的单次版本切换流水，用于准确计算回滚目标。
type HistoryEntry struct {
	ReleaseID int       `json:"release_id"`   // 切换到的发布序号
	Version   string    `json:"version"`      // 对应的构建版本号
	Kind      string    `json:"kind"`         // full / config / rollback
	At        time.Time `json:"at"`           // 切换时间（UTC）
	By        string    `json:"by,omitempty"` // 操作者标识
}

// GitBuildInfo 是从本地 Git 仓库提取的版本与提交元数据。
type GitBuildInfo struct {
	InRepo     bool   // 当前目录是否位于 Git 仓库内
	Version    string // git describe --tags --always --dirty 的结果，缺省为 dev
	Commit     string // 短提交号
	CommitTime string // HEAD 提交时间（UTC RFC3339），用于可复现构建的时间注入
	Dirty      bool   // 工作区是否存在未提交改动
	Repo       string // 从 origin 推导出的 GitHub owner/repo（非 GitHub 时为空）
}

// FormatReleaseID 将正整数发布序号格式化为固定宽度的目录名（例如 1 -> 0001，10000 -> 10000）。
func FormatReleaseID(id int) string {
	return fmt.Sprintf("%04d", id)
}

// ParseReleaseID 解析发布目录名或用户输入的序号（支持 "0042"、"42"、"#42"）。
func ParseReleaseID(s string) (int, bool) {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "#")
	if s == "" {
		return 0, false
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < 1 {
		return 0, false
	}
	return n, true
}

// InspectGit 读取 dir 所在 Git 仓库的标签版本、提交号、工作区脏状态与 origin 仓库名。
func InspectGit(ctx context.Context, dir string) GitBuildInfo {
	runGit := func(args ...string) (string, error) {
		c := exec.CommandContext(ctx, "git", args...)
		if dir != "" {
			c.Dir = dir
		}
		out, err := c.Output()
		return strings.TrimSpace(string(out)), err
	}
	if _, err := runGit("rev-parse", "--is-inside-work-tree"); err != nil {
		return GitBuildInfo{Version: "dev"}
	}
	info := GitBuildInfo{InRepo: true}
	if sha, err := runGit("rev-parse", "--short", "HEAD"); err == nil {
		info.Commit = sha
	}
	if ct, err := runGit("show", "-s", "--format=%cI", "HEAD"); err == nil && ct != "" {
		if parsed, perr := time.Parse(time.RFC3339, ct); perr == nil {
			info.CommitTime = parsed.UTC().Format(time.RFC3339)
		}
	}
	if status, err := runGit("status", "--porcelain"); err == nil && status != "" {
		info.Dirty = true
	}
	if desc, err := runGit("describe", "--tags", "--always", "--dirty"); err == nil && desc != "" {
		info.Version = desc
	} else if info.Commit != "" {
		info.Version = info.Commit
		if info.Dirty {
			info.Version += "-dirty"
		}
	} else {
		info.Version = "dev"
	}
	if remote, err := runGit("remote", "get-url", "origin"); err == nil {
		info.Repo = ParseGitHubRepo(remote)
	}
	return info
}

// ParseGitHubRepo 从 GitHub HTTPS 或 SSH 远程地址中提取 owner/repo；非 GitHub 地址返回空字符串。
func ParseGitHubRepo(remoteURL string) string {
	u := strings.TrimSpace(remoteURL)
	u = strings.TrimSuffix(u, ".git")
	var slug string
	switch {
	case strings.HasPrefix(u, "https://github.com/"):
		slug = strings.TrimPrefix(u, "https://github.com/")
	case strings.HasPrefix(u, "http://github.com/"):
		slug = strings.TrimPrefix(u, "http://github.com/")
	case strings.HasPrefix(u, "git@github.com:"):
		slug = strings.TrimPrefix(u, "git@github.com:")
	case strings.HasPrefix(u, "ssh://git@github.com/"):
		slug = strings.TrimPrefix(u, "ssh://git@github.com/")
	default:
		return ""
	}
	slug = strings.Trim(slug, "/")
	parts := strings.Split(slug, "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return ""
	}
	return parts[0] + "/" + parts[1]
}

// CurrentOperator 返回当前发布者的简短标识（CI 环境取 GITHUB_ACTOR，本机取 user@host）。
func CurrentOperator() string {
	if actor := strings.TrimSpace(os.Getenv("GITHUB_ACTOR")); actor != "" {
		return actor + "@ci"
	}
	name := ""
	if u, err := user.Current(); err == nil && u.Username != "" {
		name = u.Username
	}
	host, _ := os.Hostname()
	switch {
	case name != "" && host != "":
		return name + "@" + host
	case name != "":
		return name
	default:
		return host
	}
}

// ShortSHA 返回十六进制哈希的前 8 位，用于终端表格与人类可读摘要。
func ShortSHA(sum string) string {
	if len(sum) <= 8 {
		return sum
	}
	return sum[:8]
}
