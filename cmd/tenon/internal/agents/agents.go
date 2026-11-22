// Package agents 实现 tenon agents：让 AI 编程助手（Claude Code、Codex、Cursor、Antigravity、GitHub Copilot）
// 按项目所用的框架版本写代码。
// 它把框架的文档复制到 DocsDir，同步项目根目录 AGENTS.md 中由框架维护的约定区块，为 tenon.json 中的每个助手
// 安装技能、写 MCP 服务的配置。
package agents

import (
	"bytes"
	_ "embed"
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

//go:embed conventions.md
var conventions string

// AgentsFile 是项目根目录中给人和 AI 编程助手阅读的说明文件。
const AgentsFile = "AGENTS.md"

// AGENTS.md 中，agentsBegin 到 agentsEnd 之间是由 tenon agents 按框架版本维护的约定区块。
const (
	agentsBegin = "<!-- tenon:begin -->"
	agentsEnd   = "<!-- tenon:end -->"
)

// DocsDir 是项目中框架文档的目录：SyncDocs 把项目所用版本的框架 docs 同步到这里，随仓库提交。
const DocsDir = "tenon/docs"

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
	syncedAgents, err := SyncAgentsMD(dir, cfg)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "agents: 框架文档在 %s", DocsDir)
	if syncedAgents {
		fmt.Fprintf(out, "，约定已同步到 %s", AgentsFile)
	}
	if len(dirs) > 0 {
		fmt.Fprintf(out, "，技能在 %s", strings.Join(dirs, "、"))
	}
	if len(written) > 0 {
		fmt.Fprintf(out, "，MCP 配置：%s", strings.Join(written, "、"))
	}
	fmt.Fprintln(out)
	return nil
}

// SyncAgentsMD 把当前框架版本的通用约定写入项目 dir 根目录 AGENTS.md 的托管区块（agentsBegin 到 agentsEnd 之间），
// 按 cfg.MultiLanguage 写入单语言或多语言的界面文字与错误提示约定，区块之外的项目说明保持原样。
// 文件中还没有标记时把托管区块追加到末尾；文件不存在时什么也不做，返回 false。
func SyncAgentsMD(dir string, cfg *project.Config) (bool, error) {
	p := filepath.Join(dir, AgentsFile)
	data, err := os.ReadFile(p)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("agents: read %s: %w", AgentsFile, err)
	}
	body := renderConventions(cfg)
	block := agentsBegin + "\n<!-- 这一段由 go tool tenon agents 按框架版本维护，不要手改。 -->\n\n" +
		strings.TrimSpace(body) + "\n" + agentsEnd + "\n"
	updated := replaceLines(string(data), agentsBegin, agentsEnd, block)
	if updated == string(data) {
		return true, nil
	}
	if err := os.WriteFile(p, []byte(updated), 0o644); err != nil {
		return false, fmt.Errorf("agents: write %s: %w", AgentsFile, err)
	}
	return true, nil
}

// renderConventions 按项目是否启用多语言展开 conventions.md 中的占位符。
func renderConventions(cfg *project.Config) string {
	errRule := "业务错误用 `errorx.Define(kind, code, \"默认提示\")` 定义稳定错误码与源语言提示；无需为源语言另建翻译文件。"
	uiRule := "界面文字与菜单标题直接写在页面代码中，无需抽到 `locales/`；需要多语言时运行 `go tool tenon add language <语言>`，见 `tenon/docs/frontend/i18n.md`。"
	if cfg != nil && cfg.MultiLanguage() {
		errRule = "业务错误用 `errorx.Define(kind, code, \"源语言提示\")` 定义稳定错误码与源语言提示，其余语言的译文写在 `locales/<语言>.yaml` 中。"
		uiRule = "界面文字写在 `locales/<前端>.<语言>.yaml`，页面用 `useT`，菜单标题写消息 ID，见 `tenon/docs/frontend/i18n.md`。"
	}
	r := strings.NewReplacer("{{error_i18n_rule}}", errRule, "{{frontend_i18n_rule}}", uiRule)
	return r.Replace(conventions)
}

// SyncDocs 把框架目录 fw 中的 docs 增量同步到项目 dir 的 DocsDir：内容变化或缺少的文件才写入，框架中已删除的文件清理。
func SyncDocs(dir, fw string) error {
	src := filepath.Join(fw, "docs")
	dst := filepath.Join(dir, filepath.FromSlash(DocsDir))
	want := map[string]bool{}
	err := filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		want[rel] = true
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		if old, err := os.ReadFile(target); err == nil && bytes.Equal(old, b) {
			return nil
		}
		return os.WriteFile(target, b, 0o644)
	})
	if err != nil {
		return fmt.Errorf("agents: sync docs: %w", err)
	}
	err = filepath.WalkDir(dst, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p == dst {
			return nil
		}
		rel, err := filepath.Rel(dst, p)
		if err != nil {
			return err
		}
		if want[rel] {
			return nil
		}
		if err := os.RemoveAll(p); err != nil {
			return err
		}
		if d.IsDir() {
			return filepath.SkipDir
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("agents: sync docs: %w", err)
	}
	return nil
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
