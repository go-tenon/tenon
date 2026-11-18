package agents_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-tenon/tenon/cmd/tenon/internal/agents"
	"github.com/go-tenon/tenon/cmd/tenon/internal/project"
)

// sample 返回一个有 SSR 前端和 Electron 客户端、用到 Huma 和 NATS 的项目。
func sample() *agents.Project {
	return &agents.Project{
		Config: &project.Config{
			Main:      "./cmd/blog",
			Frontends: []project.Frontend{{Name: "web", Dir: "apps/web", Base: "/", SSR: true}, {Name: "admin", Dir: "apps/admin", Base: "/admin/"}},
			Clients:   []project.Client{{Name: "desk", Dir: "clients/desk", Kind: "electron", Frontend: "admin"}},
		},
		Tenon: "v0.0.2",
		Modules: map[string]string{
			"github.com/danielgtaylor/huma/v2": "v2.40.0",
			"github.com/nats-io/nats.go":       "v1.55.0",
		},
		NPM: map[string]string{"@mantine/core": "9.8.1", "electron": "45.2.3"},
	}
}

// Block 写出框架的版本、文档的位置、前端与客户端，只列出项目用到的依赖。
func TestBlock(t *testing.T) {
	t.Parallel()
	block := agents.Block(sample())
	for _, want := range []string{
		agents.Begin + "\n", agents.End + "\n",
		"`github.com/go-tenon/tenon` v0.0.2",
		"`.tenon/docs/README.md`",
		"`go run ./cmd/blog --help`",
		"- `apps/web`：挂在 `/`，在服务端渲染。",
		"- `apps/admin`：挂在 `/admin/`，只在浏览器中渲染。",
		"- `clients/desk`：electron 客户端，显示 `apps/admin`。",
		"| Huma | v2.40.0 | https://huma.rocks |",
		"| NATS | v1.55.0 | https://docs.nats.io/llms.txt |",
		"| Mantine | 9.8.1 | https://mantine.dev/llms.txt；MCP 服务 `mantine` |",
		"| Electron | 45.2.3 |",
		"| Bun（JavaScript 运行环境） | — |",
		"- `gopls`：", "- `mantine`：",
	} {
		if !strings.Contains(block, want) {
			t.Errorf("block missing %q:\n%s", want, block)
		}
	}
	for _, absent := range []string{"Qdrant", "TanStack Query", "Bun（ORM）"} {
		if strings.Contains(block, absent) {
			t.Errorf("block lists %s, which the project does not use", absent)
		}
	}

	// 没有前端的项目：没有前端一节，也没有 Mantine 的 MCP 服务。
	p := sample()
	p.Config.Frontends, p.Config.Clients, p.NPM = nil, nil, map[string]string{}
	block = agents.Block(p)
	if strings.Contains(block, "## 前端与客户端") || strings.Contains(block, "mantine") {
		t.Errorf("project without frontends:\n%s", block)
	}
}

// Replace 只换掉独占一行的标记之间的内容，前后的内容保持原样；没有标记时加在末尾。
func TestReplace(t *testing.T) {
	t.Parallel()
	block := agents.Begin + "\nnew\n" + agents.End + "\n"
	text := "# App\n\n说明中提到 `" + agents.Begin + "`。\n\n" + agents.Begin + "\nold\n" + agents.End + "\n\n## 约定\n"
	want := "# App\n\n说明中提到 `" + agents.Begin + "`。\n\n" + block + "\n## 约定\n"
	if got := agents.Replace(text, block); got != want {
		t.Errorf("replace:\n%s\nwant:\n%s", got, want)
	}
	if got := agents.Replace(want, block); got != want {
		t.Errorf("second replace changed the text:\n%s", got)
	}
	if got := agents.Replace("# App\n", block); got != "# App\n\n"+block {
		t.Errorf("append:\n%s", got)
	}
	if got := agents.Replace("", block); got != block {
		t.Errorf("new file:\n%s", got)
	}
}

// WriteMCP 改写 tenon 维护的服务，保留项目自己的服务和设置；再写一次结果不变。
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
	write(".mcp.json", `{"mcpServers": {"db": {"command": "psql-mcp"}, "mantine": {"command": "old"}}}`)
	write(".codex/config.toml", "model = \"gpt\"\n\n[mcp_servers.db]\ncommand = \"psql-mcp\"\n")
	servers := agents.Servers(sample())

	written, err := agents.WriteMCP(dir, project.Targets, servers)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(written, " ") != ".mcp.json .codex/config.toml .cursor/mcp.json" {
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
	if s := claude.MCPServers; s["db"].Command != "psql-mcp" || s["gopls"].Command != "gopls" ||
		s["mantine"].Command != "bunx" || strings.Join(s["mantine"].Args, " ") != "@mantine/mcp-server" {
		t.Errorf(".mcp.json %+v", s)
	}
	codex := read(".codex/config.toml")
	for _, want := range []string{"model = \"gpt\"\n", "[mcp_servers.db]\n", "[mcp_servers.gopls]\ncommand = \"gopls\"\nargs = [\"mcp\"]\n",
		"[mcp_servers.mantine]\ncommand = \"bunx\"\nargs = [\"@mantine/mcp-server\"]\n"} {
		if !strings.Contains(codex, want) {
			t.Errorf(".codex/config.toml missing %q:\n%s", want, codex)
		}
	}
	if !strings.Contains(read(".cursor/mcp.json"), `"gopls"`) {
		t.Error(".cursor/mcp.json has no gopls")
	}

	before := read(".mcp.json") + read(".codex/config.toml")
	if _, err := agents.WriteMCP(dir, project.Targets, servers); err != nil {
		t.Fatal(err)
	}
	if after := read(".mcp.json") + read(".codex/config.toml"); after != before {
		t.Errorf("second write changed the files:\n%s", after)
	}

	// 去掉前端以后，mantine 从配置中删除，项目自己的服务保留。
	if _, err := agents.WriteMCP(dir, []string{"claude", "codex"}, servers[:1]); err != nil {
		t.Fatal(err)
	}
	if got := read(".mcp.json") + read(".codex/config.toml"); strings.Contains(got, "mantine") || !strings.Contains(got, "psql-mcp") {
		t.Errorf("after removing mantine:\n%s", got)
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
