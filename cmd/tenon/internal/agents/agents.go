// Package agents 实现 tenon agents：让 AI 编程助手（Claude Code、Codex、Cursor）按项目所用的框架版本写代码。
// 它把框架的文档复制到 DocsDir，改写 AGENTS.md 中由 tenon 维护的一段（框架的版本、文档的位置、前端与客户端、
// 依赖的版本和给 AI 读的文档、技能），并为 tenon.json 中的每个助手安装技能、写 MCP 服务的配置。
// AGENTS.md 中这一段以外的内容由项目自己维护，tenon init 生成初稿，之后 tenon agents 不再改动。
package agents

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
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
	Config  *project.Config
	Tenon   string            // 框架的版本，例如 v0.0.1；replace 到本地目录时是“本地目录 <路径>”。
	Modules map[string]string // 应用的构建用到的 Go 模块的版本，按模块路径。
	NPM     map[string]string // 前端和客户端依赖的 npm 包的版本：安装了的是 node_modules 中的版本，否则是 package.json 中写的范围。
}

// Sync 运行一次 tenon agents：同步 DocsDir，改写 AGENTS.md 中 tenon 的一段，为 cfg 中的每个助手安装技能、
// 写 MCP 配置，把做了什么写到 out。
func Sync(dir string, cfg *project.Config, out io.Writer) error {
	fw, err := project.FrameworkDir(dir)
	if err != nil {
		return err
	}
	if err := SyncDocs(dir, fw); err != nil {
		return err
	}
	p, err := Inspect(dir, cfg)
	if err != nil {
		return err
	}
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
	written, err := WriteMCP(dir, cfg.AgentTargets(), Servers(p))
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
	if len(written) > 0 {
		if _, err := exec.LookPath("gopls"); err != nil {
			fmt.Fprintln(out, "agents: 没有找到 gopls，MCP 服务 gopls 需要它：go install golang.org/x/tools/gopls@latest")
		}
	}
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

// Inspect 读出项目 dir 的框架版本、应用的构建用到的 Go 模块，以及前端和客户端依赖的 npm 包。
func Inspect(dir string, cfg *project.Config) (*Project, error) {
	p := &Project{Config: cfg, Modules: map[string]string{}, NPM: map[string]string{}}
	// 只列出构建中实际用到的模块：-e 让有编译错误的包不影响模块的列表。
	cmd := exec.Command("go", "list", "-e", "-deps", "-f",
		"{{with .Module}}{{.Path}} {{.Version}}{{with .Replace}} {{.Path}}{{end}}{{end}}", "./...")
	cmd.Dir = dir
	cmd.Stderr = io.Discard
	b, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("agents: go list: %w", err)
	}
	sc := bufio.NewScanner(bytes.NewReader(b))
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) < 2 {
			continue // 主模块没有版本
		}
		p.Modules[fields[0]] = fields[1]
		if fields[0] == project.Framework {
			p.Tenon = fields[1]
			if len(fields) == 3 {
				p.Tenon = "本地目录 " + fields[2]
			}
		}
	}
	if p.Tenon == "" {
		return nil, fmt.Errorf("agents: %s does not use %s", dir, project.Framework)
	}
	// 前端和客户端的 package.json 中写的依赖；安装过的取 node_modules 中的版本。
	var dirs []string
	for _, f := range cfg.Frontends {
		dirs = append(dirs, f.Dir)
	}
	for _, c := range cfg.Clients {
		dirs = append(dirs, c.Dir)
	}
	for _, d := range dirs {
		data, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(d), "package.json"))
		if err != nil {
			return nil, err
		}
		var pkg struct {
			Dependencies    map[string]string `json:"dependencies"`
			DevDependencies map[string]string `json:"devDependencies"`
		}
		if err := json.Unmarshal(data, &pkg); err != nil {
			return nil, fmt.Errorf("%s/package.json: %w", d, err)
		}
		for name, version := range pkg.Dependencies {
			p.NPM[name] = version
		}
		for name, version := range pkg.DevDependencies {
			p.NPM[name] = version
		}
	}
	for name := range p.NPM {
		for _, d := range append([]string{"."}, dirs...) {
			data, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(d), "node_modules", filepath.FromSlash(name), "package.json"))
			if err != nil {
				continue
			}
			var pkg struct {
				Version string `json:"version"`
			}
			if json.Unmarshal(data, &pkg) == nil && pkg.Version != "" {
				p.NPM[name] = pkg.Version
				break
			}
		}
	}
	return p, nil
}

// source 是一个依赖和它给 AI 读的文档。项目用到它时（Go 模块在构建中、npm 包写在 package.json 中）列入 AGENTS.md。
type source struct {
	name   string // 显示的名称
	module string // Go 模块路径
	npm    string // npm 包名
	always bool   // 每个项目都列出，没有版本，例如 Bun
	llms   string // 给 AI 读的文档目录（llms.txt）
	docs   string // 没有 llms.txt 时的文档地址
	mcp    string // 项目配置的 MCP 服务，见 Servers
}

// sources 是框架依赖的社区组件，按后端、前端、客户端的顺序。2026-11 时有 llms.txt 的写 llms，其余写文档首页。
var sources = []source{
	{name: "Huma", module: "github.com/danielgtaylor/huma/v2", docs: "https://huma.rocks"},
	{name: "chi", module: "github.com/go-chi/chi/v5", docs: "https://go-chi.io"},
	{name: "Cobra", module: "github.com/spf13/cobra", docs: "https://cobra.dev"},
	{name: "Bun（ORM）", module: "github.com/uptrace/bun", docs: "https://bun.uptrace.dev"},
	{name: "pgx", module: "github.com/jackc/pgx/v5", docs: "https://pkg.go.dev/github.com/jackc/pgx/v5"},
	{name: "goose", module: "github.com/pressly/goose/v3", docs: "https://pressly.github.io/goose/"},
	{name: "NATS", module: "github.com/nats-io/nats.go", llms: "https://docs.nats.io/llms.txt"},
	{name: "Centrifuge", module: "github.com/centrifugal/centrifuge", llms: "https://centrifugal.dev/llms.txt"},
	{name: "Qdrant", module: "github.com/qdrant/go-client", llms: "https://qdrant.tech/llms.txt"},
	{name: "MCP Go SDK", module: "github.com/modelcontextprotocol/go-sdk", llms: "https://modelcontextprotocol.io/llms.txt"},
	{name: "Bun（JavaScript 运行环境）", always: true, llms: "https://bun.com/llms.txt"},
	{name: "React", npm: "react", llms: "https://react.dev/llms.txt"},
	{name: "Mantine", npm: "@mantine/core", llms: "https://mantine.dev/llms.txt", mcp: "mantine"},
	{name: "TanStack Router", npm: "@tanstack/react-router", llms: "https://tanstack.com/router/latest/llms.txt"},
	{name: "TanStack Start", npm: "@tanstack/react-start", llms: "https://tanstack.com/start/latest/llms.txt"},
	{name: "TanStack Query", npm: "@tanstack/react-query", llms: "https://tanstack.com/query/latest/llms.txt"},
	{name: "Vite", npm: "vite", llms: "https://vite.dev/llms.txt"},
	{name: "Electron", npm: "electron", docs: "https://www.electronjs.org/docs/latest"},
}

// Server 是一个以 stdio 运行的 MCP 服务。
type Server struct {
	Name    string   // 服务的名称，也是它在各助手配置中的键。
	Command string   // 启动它的命令
	Args    []string // 命令的参数
	Doc     string   // 写进 AGENTS.md 的一句话说明
}

// managed 是 tenon agents 维护的 MCP 服务的名称：配置中这些键由它改写或删除，其他键保持原样。
var managed = []string{"tenon", "gopls", "mantine"}

// Servers 返回项目 p 要配置的 MCP 服务：总是有 tenon mcp 和 Go 的语言服务 gopls，有 Mantine 时加上它的文档服务。
func Servers(p *Project) []Server {
	servers := []Server{{
		Name: "tenon", Command: "go", Args: []string{"tool", "tenon", "mcp"},
		Doc: "Tenon 的开发工具：`tenon_search_docs` 按小节检索框架的文档，`tenon_read_doc` 读一页的目录或一个小节，`tenon_app_info` 列出项目的版本、前端、客户端和依赖，`tenon_dev_logs` 读 `bun run dev` 中的编译错误和应用日志",
	}, {
		Name: "gopls", Command: "gopls", Args: []string{"mcp"},
		Doc: "Go 的语言服务：在项目和依赖（与 go.mod 的版本一致）中查找符号、读文档、检查编译错误。需要安装 gopls：`go install golang.org/x/tools/gopls@latest`",
	}}
	if _, ok := p.NPM["@mantine/core"]; ok {
		servers = append(servers, Server{
			Name: "mantine", Command: "bunx", Args: []string{"@mantine/mcp-server"},
			Doc: "搜索和阅读 Mantine 的文档，查组件的属性和 hook 的签名",
		})
	}
	return servers
}

// Dependency 是项目用到的一个社区组件。
type Dependency struct {
	Name    string `json:"name"`              // 显示的名称
	Version string `json:"version,omitempty"` // 项目中的版本；没有版本的（例如 Bun）为空
	Docs    string `json:"docs"`              // 给 AI 读的 llms.txt，没有时是文档首页
	MCP     string `json:"mcp,omitempty"`     // 项目配置的 MCP 服务，没有时为空
}

// Dependencies 返回项目 p 用到的社区组件，按 sources 的顺序：Go 模块在应用的构建中、npm 包写在前端或客户端的
// package.json 中时列出。
func Dependencies(p *Project) []Dependency {
	servers := Servers(p)
	var deps []Dependency
	for _, s := range sources {
		version, ok := "", s.always
		switch {
		case s.module != "":
			version, ok = p.Modules[s.module]
		case s.npm != "":
			version, ok = p.NPM[s.npm]
		}
		if !ok {
			continue
		}
		d := Dependency{Name: s.name, Version: version, Docs: s.docs}
		if s.llms != "" {
			d.Docs = s.llms
		}
		if s.mcp != "" && slices.ContainsFunc(servers, func(sv Server) bool { return sv.Name == s.mcp }) {
			d.MCP = s.mcp
		}
		deps = append(deps, d)
	}
	return deps
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

	w("")
	w("## 依赖的文档")
	w("")
	w("项目用到的社区组件、项目中的版本和给 AI 读的文档。网上的文档是最新的版本，与项目的版本不一致时以项目的版本为准。Go 的依赖用 `go doc` 读与 go.mod 一致的版本，例如 `go doc github.com/danielgtaylor/huma/v2.Register`。")
	w("")
	w("| 依赖 | 版本 | 文档 |")
	w("| --- | --- | --- |")
	for _, d := range Dependencies(p) {
		version, doc := d.Version, d.Docs
		if version == "" {
			version = "—"
		}
		if d.MCP != "" {
			doc += "；MCP 服务 `" + d.MCP + "`"
		}
		w("| %s | %s | %s |", d.Name, version, doc)
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
	for _, s := range Servers(p) {
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
	"claude": {file: ".mcp.json", key: "mcpServers"},
	"codex":  {file: ".codex/config.toml"},
	"cursor": {file: ".cursor/mcp.json", key: "mcpServers"},
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
