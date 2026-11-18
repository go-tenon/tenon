// Package agents 实现 tenon agents：让 AI 编程助手（Claude Code、Codex、Cursor、Antigravity、GitHub Copilot）
// 按项目所用的框架版本写代码。
// 它把框架的文档复制到 DocsDir，改写 AGENTS.md 中由 tenon 维护的一段（框架的版本、文档的位置、前端与客户端、
// 依赖的版本和给 AI 读的文档、技能），并为 tenon.json 中的每个助手安装技能、写 MCP 服务的配置。
// AGENTS.md 中这一段以外的内容由项目自己维护，tenon init 生成初稿，之后 tenon agents 不再改动。
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

// File 是 AI 编程助手读取的项目说明。
const File = "AGENTS.md"

// Begin 和 End 之间是 File 中由 tenon agents 维护的一段。
const (
	Begin = "<!-- tenon:begin -->"
	End   = "<!-- tenon:end -->"
)

// Project 是生成 AGENTS.md 中 tenon 那一段需要的项目信息，由 Inspect 读出。
type Project struct {
	Config *project.Config
	Tenon  string // 框架的版本，例如 v0.0.1；replace 到本地目录时是“本地目录 <路径>”。
}

// Sync 运行一次 tenon agents：同步 DocsDir，改写 AGENTS.md 中 tenon 的一段，为 cfg 中的每个助手安装技能、
// 写 MCP 配置，把做了什么写到 out。依赖（Go 的 gopls、Mantine、TanStack 等）给 AI 的资源由开发者按它们的官方方案
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
	if err := WriteFile(dir, p, skills); err != nil {
		return err
	}
	if err := WriteSkills(dir, dirs, skills); err != nil {
		return err
	}
	written, err := WriteMCP(dir, cfg.AgentTargets(), Servers())
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "agents: 已更新 %s 中 tenon 的一段，框架文档在 %s", File, DocsDir)
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
	Doc     string   // 写进 AGENTS.md 的一句话说明
}

// managed 是 tenon agents 维护的 MCP 服务的名称：配置中这些键由它改写，其他服务（例如按官方方案加上的 gopls、
// mantine）保持原样。
var managed = []string{"tenon"}

// Servers 返回 tenon agents 为助手配置的 MCP 服务：tenon mcp。
func Servers() []Server {
	return []Server{{
		Name: "tenon", Command: "go", Args: []string{"tool", "tenon", "mcp"},
		Doc: "Tenon 的开发工具：`tenon_search_docs` 按小节检索框架的文档，`tenon_read_doc` 读一页的目录或一个小节，`tenon_app_info` 列出项目的框架版本、前端、客户端和技能，`tenon_dev_logs` 读 `bun run dev` 中的编译错误和应用日志",
	}}
}

// Block 返回 AGENTS.md 中 tenon 的一段，含 Begin 和 End 两行。skills 是为助手安装的技能，列在其中。
func Block(p *Project, skills []Skill) string {
	var b strings.Builder
	w := func(format string, args ...any) { fmt.Fprintf(&b, format+"\n", args...) }
	w("%s", Begin)
	w("<!-- 这一段由 go tool tenon agents 生成：升级框架、加前端或客户端后运行 bun run agents 更新它，不要手改。 -->")
	w("")
	w("## 框架与文档")
	w("")
	w("- 框架是 `%s` %s。", project.Framework, p.Tenon)
	w("- 框架的文档在 `%s` 中，与上面的版本一致，`bun run dev` 每次启动时更新。先读目录 `%s/README.md`，写某一部分的代码前读对应的页面，写法以这个版本的文档为准。", DocsDir, DocsDir)
	w("- 查框架的文档用 MCP 工具 `tenon_search_docs`、`tenon_read_doc`；用 rg 搜索时指定目录，例如 `rg 迁移 %s`：`.tenon` 被 .gitignore 忽略，在项目根目录搜索时不包含它。", DocsDir)
	w("- 应用的入口是 `%s`，`go run %s --help` 列出应用的命令。", p.Config.Main, p.Config.Main)
	w("- 依赖的版本以 `go.mod` 和各前端的 `package.json` 为准。Go 的依赖用 `go doc` 读与 `go.mod` 一致的版本，例如 `go doc github.com/danielgtaylor/huma/v2.Register`。")
	w("- Mantine、TanStack 等依赖给 AI 的文档、MCP 服务和技能按它们的官方方案接入，见 `README.md` 的“AI 编程助手”一节；它们的写法与 Tenon 的约定冲突时，以本文和 `%s` 为准。", DocsDir)

	if len(p.Config.Frontends)+len(p.Config.Clients) > 0 {
		w("")
		w("## 前端与客户端")
		w("")
		for _, f := range p.Config.Frontends {
			rendering := "只在浏览器中渲染"
			if f.SSR {
				rendering = "在服务端渲染"
			}
			w("- `%s`：挂在 `%s`，%s。", f.Dir, f.Base, rendering)
		}
		for _, c := range p.Config.Clients {
			w("- `%s`：%s 客户端，显示 `%s/%s`。", c.Dir, c.Kind, project.AppsDir, c.Frontend)
		}
	}

	if len(skills) > 0 {
		w("")
		w("## 技能")
		w("")
		w("`go tool tenon agents` 在 %s 中安装了这些技能，做对应的事情之前先读它们：", strings.Join(quoted(SkillDirs(p.Config.AgentTargets())), "、"))
		w("")
		for _, s := range skills {
			w("- `%s`：%s。", s.Name, s.Summary())
		}
	}

	w("")
	w("## MCP 服务")
	w("")
	w("`go tool tenon agents` 为 tenon.json 中 `agents.targets` 列出的助手配置了这些 MCP 服务：")
	w("")
	for _, s := range Servers() {
		w("- `%s`：%s。", s.Name, s.Doc)
	}
	w("%s", End)
	return b.String()
}

// WriteFile 把项目 dir 的 AGENTS.md 中 Begin 到 End 的一段换成 Block(p, skills)。文件中没有这一段时把它加在末尾，
// 没有这个文件时新建它。
func WriteFile(dir string, p *Project, skills []Skill) error {
	file := filepath.Join(dir, File)
	data, err := os.ReadFile(file)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return os.WriteFile(file, []byte(Replace(string(data), Block(p, skills))), 0o644)
}

// quoted 给每一项加上反引号。
func quoted(items []string) []string {
	out := make([]string, len(items))
	for i, s := range items {
		out[i] = "`" + s + "`"
	}
	return out
}

// Replace 把 text 中 Begin 到 End 的一段（这两行各自独占一行）换成 block；没有这一段时把 block 加在末尾，
// 与前文隔一个空行。
func Replace(text, block string) string {
	return replaceLines(text, Begin, End, block)
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
