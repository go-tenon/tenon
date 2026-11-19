// Package agents 实现 tenon agents：让 AI 编程助手（Claude Code、Codex、Cursor、Antigravity、GitHub Copilot）
// 按项目所用的框架版本写代码。
// 它把框架的文档复制到 DocsDir，为 tenon.json 中的每个助手安装技能、写 MCP 服务的配置。
// 项目根目录的 AGENTS.md 由 tenon init 生成初稿，之后归项目自己维护。
package agents

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/go-tenon/tenon/cmd/tenon/internal/project"
)

// DocsDir 是项目中框架文档的目录：SyncDocs 把项目所用版本的框架 docs 复制到这里。
const DocsDir = ".tenon/docs"

// Project 是项目的信息：选择安装哪些技能、tenon_app_info 列出的内容，由 Inspect 读出。
type Project struct {
	Config *project.Config
	Tenon  string // 框架的版本，例如 v0.0.1；replace 到本地目录时是“本地目录 <路径>”。
}

// Sync 运行一次 tenon agents：同步 DocsDir，为 cfg 中的每个助手安装技能、写 MCP 配置，把做了什么写到 out。依赖（Go 的 gopls、Mantine、TanStack 等）给 AI 的资源由开发者按它们的官方方案
// 接入，见项目 README 中的“AI 编程助手”。
func Sync(dir string, cfg *project.Config, out io.Writer) error {
	fw, err := project.FrameworkDir(dir)
	if err != nil {
		return err
	}
	if err := SyncDocs(dir, fw); err != nil {
		return err
	}
	p := &Project{Config: cfg, Tenon: version(fw)}
	var skills []Skill
	dirs := SkillDirs(cfg.AgentTargets())
	if len(dirs) > 0 {
		if skills, err = Skills(p); err != nil {
			return err
		}
	}
	if err := WriteSkills(dir, dirs, skills); err != nil {
		return err
	}
	written, err := WriteMCP(dir, cfg.AgentTargets(), Servers())
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "agents: 框架文档在 %s", DocsDir)
	if len(dirs) > 0 {
		fmt.Fprintf(out, "，技能在 %s", strings.Join(dirs, "、"))
	}
	if len(written) > 0 {
		fmt.Fprintf(out, "，MCP 配置：%s", strings.Join(written, "、"))
	}
	fmt.Fprintln(out)
	return nil
}

// SyncDocs 把框架目录 fw 中的 docs 复制到项目 dir 的 DocsDir，替换已有的内容。
// .tenon 被 .gitignore 忽略，ripgrep 这类搜索工具在项目根目录搜索时跳过它；DocsDir 中的 .ignore 让指定这个目录
// 搜索时（例如 rg 迁移 .tenon/docs）能搜到文档。
func SyncDocs(dir, fw string) error {
	dst := filepath.Join(dir, filepath.FromSlash(DocsDir))
	if err := os.RemoveAll(dst); err != nil {
		return fmt.Errorf("agents: sync docs: %w", err)
	}
	if err := os.CopyFS(dst, os.DirFS(filepath.Join(fw, "docs"))); err != nil {
		return fmt.Errorf("agents: sync docs: %w", err)
	}
	if err := os.WriteFile(filepath.Join(dst, ".ignore"), []byte("!*\n"), 0o644); err != nil {
		return fmt.Errorf("agents: sync docs: %w", err)
	}
	return os.WriteFile(filepath.Join(filepath.Dir(dst), ".gitignore"), []byte("*\n"), 0o644)
}

// Inspect 读出项目 dir 的框架版本。
func Inspect(dir string, cfg *project.Config) (*Project, error) {
	fw, err := project.FrameworkDir(dir)
	if err != nil {
		return nil, err
	}
	return &Project{Config: cfg, Tenon: version(fw)}, nil
}

// version 返回框架目录 fw 对应的版本：模块缓存中的目录名以 @<版本> 结尾，例如 .../tenon@v0.0.1；
// replace 到本地目录时是“本地目录 <fw>”。
func version(fw string) string {
	if _, v, ok := strings.Cut(filepath.Base(fw), "@"); ok {
		return v
	}
	return "本地目录 " + fw
}

// Server 是一个以 stdio 运行的 MCP 服务。
type Server struct {
	Name    string   // 服务的名称，也是它在各助手配置中的键。
	Command string   // 启动它的命令
	Args    []string // 命令的参数
}

// managed 是 tenon agents 维护的 MCP 服务的名称：配置中这些键由它改写，其他服务（例如按官方方案加上的 gopls、
// mantine）保持原样。
var managed = []string{"tenon"}

// Servers 返回 tenon agents 为助手配置的 MCP 服务：tenon mcp。
func Servers() []Server {
	return []Server{{
		Name: "tenon", Command: "go", Args: []string{"tool", "tenon", "mcp"},
	}}
}

// replaceLines 把 text 中从内容为 begin 的一行到内容为 end 的一行（含这两行）换成 block；没有这一段时把 block
// 加在末尾，与前文隔一个空行。block 以换行结尾。
func replaceLines(text, begin, end, block string) string {
	lines := strings.SplitAfter(text, "\n")
	i := slices.IndexFunc(lines, func(l string) bool { return strings.TrimSpace(l) == begin })
	if i >= 0 {
		if j := slices.IndexFunc(lines[i:], func(l string) bool { return strings.TrimSpace(l) == end }); j >= 0 {
			return strings.Join(lines[:i], "") + block + strings.Join(lines[i+j+1:], "")
		}
	}
	if text = strings.TrimRight(text, "\n"); text != "" {
		text += "\n\n"
	}
	return text + block
}

// target 是一种 AI 编程助手的 MCP 配置文件。
type target struct {
	file string // 配置文件在项目中的路径
	key  string // JSON 中放 MCP 服务的键；TOML 的配置为空
}

// targets 是 project.Targets 中每个助手的 MCP 配置。
var targets = map[string]target{
	"claude":      {file: ".mcp.json", key: "mcpServers"},
	"codex":       {file: ".codex/config.toml"},
	"cursor":      {file: ".cursor/mcp.json", key: "mcpServers"},
	"antigravity": {file: ".agents/mcp_config.json", key: "mcpServers"},
	"copilot":     {file: ".vscode/mcp.json", key: "servers"},
}

// WriteMCP 为 names 中的每个助手把 servers 写进它在项目 dir 中的 MCP 配置，返回写了的文件。
// 配置中 managed 以外的服务和其他设置保持原样。
func WriteMCP(dir string, names []string, servers []Server) ([]string, error) {
	var written []string
	for _, name := range names {
		t := targets[name]
		file := filepath.Join(dir, filepath.FromSlash(t.file))
		data, err := os.ReadFile(file)
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return nil, err
		}
		err = nil
		if t.key != "" {
			data, err = mergeJSON(data, t.key, servers)
		} else {
			data = []byte(mergeTOML(string(data), servers))
		}
		if err != nil {
			return nil, fmt.Errorf("%s: %w", t.file, err)
		}
		if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
			return nil, err
		}
		if err := os.WriteFile(file, data, 0o644); err != nil {
			return nil, err
		}
		written = append(written, t.file)
	}
	return written, nil
}

// mergeJSON 把 servers 写进 JSON 配置 data 的 key 中：先删掉 managed 中的服务，再加上 servers。
func mergeJSON(data []byte, key string, servers []Server) ([]byte, error) {
	doc := map[string]any{}
	if len(bytes.TrimSpace(data)) > 0 {
		if err := json.Unmarshal(data, &doc); err != nil {
			return nil, err
		}
	}
	m, _ := doc[key].(map[string]any)
	if m == nil {
		m = map[string]any{}
	}
	for _, name := range managed {
		delete(m, name)
	}
	for _, s := range servers {
		m[s.Name] = struct {
			Command string   `json:"command"`
			Args    []string `json:"args"`
		}{s.Command, s.Args}
	}
	doc[key] = m
	out, err := json.MarshalIndent(doc, "", "  ")
	return append(out, '\n'), err
}

// TOML 的配置中，tomlBegin 到 tomlEnd 之间是 tenon agents 维护的 MCP 服务。
const (
	tomlBegin = "# tenon:begin"
	tomlEnd   = "# tenon:end"
)

// mergeTOML 把 servers 写成 Codex 的 [mcp_servers.<名称>] 表，放在 text 中 tomlBegin 到 tomlEnd 之间；
// 没有这一段时加在末尾。这一段只有完整的表，加在末尾时不影响前面的设置。
func mergeTOML(text string, servers []Server) string {
	var b strings.Builder
	b.WriteString(tomlBegin + "\n# 这一段由 go tool tenon agents 生成，不要手改。\n")
	for _, s := range servers {
		args := make([]string, len(s.Args))
		for i, a := range s.Args {
			args[i] = fmt.Sprintf("%q", a)
		}
		fmt.Fprintf(&b, "\n[mcp_servers.%s]\ncommand = %q\nargs = [%s]\n", s.Name, s.Command, strings.Join(args, ", "))
	}
	b.WriteString(tomlEnd + "\n")
	return replaceLines(text, tomlBegin, tomlEnd, b.String())
}
