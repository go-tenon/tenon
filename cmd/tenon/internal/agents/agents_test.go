package agents_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/go-tenon/tenon/cmd/tenon/internal/agents"
	"github.com/go-tenon/tenon/cmd/tenon/internal/project"
)

// sample 返回一个有 SSR 前端和 Electron 客户端的项目。
func sample() *agents.Project {
	return &agents.Project{
		Config: &project.Config{
			Main:      "./cmd/blog",
			Frontends: []project.Frontend{{Name: "web", Dir: "apps/web", Base: "/", SSR: true}, {Name: "admin", Dir: "apps/admin", Base: "/admin/"}},
			Clients:   []project.Client{{Name: "desk", Dir: "clients/desk", Kind: "electron", Frontend: "admin"}},
		},
		Tenon: "v0.0.2",
	}
}

// WriteMCP 只改写 tenon 一项，保留项目自己的服务（例如按官方方法加上的 gopls、mantine）和设置；再写一次结果不变。
func TestWriteMCP(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	write := func(name, content string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, name)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	read := func(name string) string {
		t.Helper()
		b, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	write(".mcp.json", `{"mcpServers": {"gopls": {"command": "gopls", "args": ["mcp"]}, "tenon": {"command": "old"}}}`)
	write(".codex/config.toml", "model = \"gpt\"\n\n[mcp_servers.gopls]\ncommand = \"gopls\"\n")
	servers := agents.Servers()

	written, err := agents.WriteMCP(dir, project.Targets, servers)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(written, " ") != ".mcp.json .codex/config.toml .cursor/mcp.json .agents/mcp_config.json .vscode/mcp.json" {
		t.Errorf("written %v", written)
	}
	var claude struct {
		MCPServers map[string]struct {
			Command string   `json:"command"`
			Args    []string `json:"args"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal([]byte(read(".mcp.json")), &claude); err != nil {
		t.Fatal(err)
	}
	if s := claude.MCPServers; s["gopls"].Command != "gopls" || len(s) != 2 ||
		s["tenon"].Command != "go" || strings.Join(s["tenon"].Args, " ") != "tool tenon mcp" {
		t.Errorf(".mcp.json %+v", s)
	}
	codex := read(".codex/config.toml")
	for _, want := range []string{"model = \"gpt\"\n", "[mcp_servers.gopls]\ncommand = \"gopls\"\n",
		"[mcp_servers.tenon]\ncommand = \"go\"\nargs = [\"tool\", \"tenon\", \"mcp\"]\n"} {
		if !strings.Contains(codex, want) {
			t.Errorf(".codex/config.toml missing %q:\n%s", want, codex)
		}
	}
	for file, key := range map[string]string{".cursor/mcp.json": "mcpServers", ".agents/mcp_config.json": "mcpServers", ".vscode/mcp.json": "servers"} {
		var doc map[string]map[string]struct {
			Command string `json:"command"`
		}
		if err := json.Unmarshal([]byte(read(file)), &doc); err != nil || doc[key]["tenon"].Command != "go" || len(doc[key]) != 1 {
			t.Errorf("%s: %v\n%s", file, err, read(file))
		}
	}

	before := read(".mcp.json") + read(".codex/config.toml")
	if _, err := agents.WriteMCP(dir, project.Targets, servers); err != nil {
		t.Fatal(err)
	}
	if after := read(".mcp.json") + read(".codex/config.toml"); after != before {
		t.Errorf("second write changed the files:\n%s", after)
	}
}

// SyncDocs 用框架的 docs 替换 .tenon/docs，指定这个目录搜索时 .ignore 让 ripgrep 不跳过它。
func TestSyncDocs(t *testing.T) {
	t.Parallel()
	fw, dir := t.TempDir(), t.TempDir()
	if err := os.MkdirAll(filepath.Join(fw, "docs", "http"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fw, "docs", "http", "routing.md"), []byte("# 路由\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	stale := filepath.Join(dir, ".tenon", "docs", "removed.md")
	if err := os.MkdirAll(filepath.Dir(stale), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stale, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := agents.SyncDocs(dir, fw); err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]string{
		".tenon/docs/http/routing.md": "# 路由\n",
		".tenon/docs/.ignore":         "!*\n",
		".tenon/.gitignore":           "*\n",
	} {
		if b, err := os.ReadFile(filepath.Join(dir, name)); err != nil || string(b) != want {
			t.Errorf("%s = %q, %v", name, b, err)
		}
	}
	if _, err := os.Stat(stale); err == nil {
		t.Error("a page removed from the framework is still in .tenon/docs")
	}
}

// Skills 按项目的内容和 agents.skip 选出技能：没有前端时不装 tenon-frontend，没有 Electron 客户端时不装 tenon-electron。
func TestSkills(t *testing.T) {
	t.Parallel()
	names := func(p *agents.Project) string {
		t.Helper()
		skills, err := agents.Skills(p)
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, s := range skills {
			out = append(out, s.Name)
		}
		return strings.Join(out, " ")
	}
	all := "tenon-action tenon-auth tenon-background tenon-database tenon-electron tenon-frontend tenon-http-api tenon-mcp-tools tenon-testing"
	if got := names(sample()); got != all {
		t.Errorf("full project: %s", got)
	}
	p := sample()
	p.Config.Frontends, p.Config.Clients = nil, nil
	if got := names(p); strings.Contains(got, "tenon-frontend") || strings.Contains(got, "tenon-electron") {
		t.Errorf("project without frontends: %s", got)
	}
	p = sample()
	p.Config.Agents = &project.Agents{Skip: []string{"tenon-mcp-tools"}}
	if got := names(p); strings.Contains(got, "tenon-mcp-tools") {
		t.Errorf("skipped skill installed: %s", got)
	}
	p.Config.Agents.Skip = []string{"tenon-nothing"}
	if _, err := agents.Skills(p); err == nil || !strings.Contains(err.Error(), "tenon-nothing") {
		t.Errorf("unknown skip: %v", err)
	}
}

// SkillDirs：Claude Code 读 .claude/skills，Codex 读 .agents/skills，Cursor 两个都读。
func TestSkillDirs(t *testing.T) {
	t.Parallel()
	for targets, want := range map[string]string{
		"claude codex cursor": ".claude/skills .agents/skills",
		"claude antigravity":  ".claude/skills .agents/skills",
		"claude copilot":      ".claude/skills .github/skills",
		"claude cursor":       ".claude/skills",
		"cursor":              ".agents/skills",
		"codex":               ".agents/skills",
		"":                    "",
	} {
		if got := strings.Join(agents.SkillDirs(strings.Fields(targets)), " "); got != want {
			t.Errorf("%q: %q, want %q", targets, got, want)
		}
	}
}

// WriteSkills 替换框架的技能，删除框架不再提供的，项目自己的技能保持原样。
func TestWriteSkills(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	for _, f := range []string{".claude/skills/tenon-old/SKILL.md", ".claude/skills/release/SKILL.md"} {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, f)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, f), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	skills, err := agents.Skills(sample())
	if err != nil {
		t.Fatal(err)
	}
	if err := agents.WriteSkills(dir, []string{".claude/skills", ".agents/skills"}, skills); err != nil {
		t.Fatal(err)
	}
	for f, exists := range map[string]bool{
		".claude/skills/tenon-action/SKILL.md":   true,
		".agents/skills/tenon-frontend/SKILL.md": true,
		".claude/skills/release/SKILL.md":        true,
		".claude/skills/tenon-old":               false,
	} {
		if _, err := os.Stat(filepath.Join(dir, f)); (err == nil) != exists {
			t.Errorf("%s exists = %v, want %v", f, err == nil, exists)
		}
	}
}

// 技能、AGENTS.md 和 README.md 的模板中写的 .tenon/docs/<页面>#<锚点> 都存在于框架的 docs 中，
// 提到的其他技能（技能 `tenon-x`）也存在。改了文档的路径或锚点时，这个测试指出要同步修改的技能。
func TestSkillReferences(t *testing.T) {
	t.Parallel()
	docs := "../../../../docs"
	ref := regexp.MustCompile(`\.tenon/docs/([A-Za-z0-9_/.-]+\.md)(?:#([a-z0-9-]+))?`)
	mention := regexp.MustCompile("技能 `(tenon-[a-z-]+)`")
	all, err := agents.AllSkills()
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	sources := map[string]string{}
	for _, s := range all {
		names[s.Name] = true
		sources[s.Name] = string(s.Content)
	}
	for _, name := range []string{"AGENTS.md.tmpl", "README.md.tmpl"} {
		tmpl, err := os.ReadFile("../scaffold/template/project/" + name)
		if err != nil {
			t.Fatal(err)
		}
		sources[name] = string(tmpl)
	}
	for source, text := range sources {
		for _, m := range ref.FindAllStringSubmatch(text, -1) {
			page, err := os.ReadFile(filepath.Join(docs, filepath.FromSlash(m[1])))
			if err != nil {
				t.Errorf("%s: %s does not exist", source, m[0])
				continue
			}
			if m[2] != "" && !strings.Contains(string(page), `<a id="`+m[2]+`"></a>`) {
				t.Errorf("%s: %s has no anchor %q", source, m[1], m[2])
			}
		}
		for _, m := range mention.FindAllStringSubmatch(text, -1) {
			if !names[m[1]] {
				t.Errorf("%s mentions skill %s, which does not exist", source, m[1])
			}
		}
	}
	if len(all) < 9 {
		t.Errorf("only %d skills", len(all))
	}
}
