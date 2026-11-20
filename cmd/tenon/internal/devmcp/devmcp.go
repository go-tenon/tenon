// Package devmcp 实现 tenon mcp：开发时给 AI 编程助手用的 MCP 服务，以 stdio 运行。工具名都以 tenon_ 开头，
// 与 Mantine 等其他 MCP 服务的同名工具区分：
//
//	tenon_search_docs  按小节检索 .tenon/docs 中的框架文档
//	tenon_read_doc     读一页文档的目录、一个小节或整页
//	tenon_app_info     项目的框架版本、入口、前端与客户端和技能
//	tenon_dev_logs     tenon dev 运行期间各进程的输出（dev.LogFile）
package devmcp

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/go-tenon/tenon/cmd/tenon/internal/agents"
	"github.com/go-tenon/tenon/cmd/tenon/internal/dev"
	"github.com/go-tenon/tenon/cmd/tenon/internal/project"
)

// instructions 在连接时交给模型，说明工具之间怎样配合。
const instructions = "Tenon 框架的开发工具，文档的版本与项目 go.mod 中的框架版本一致。写 Tenon 的代码之前，" +
	"用 tenon_search_docs 查找相关的文档小节，再用 tenon_read_doc 读它；不确定项目有哪些前端、依赖是什么版本时用 tenon_app_info；" +
	"改完代码后用 tenon_dev_logs 看 bun run dev 中的编译错误和应用日志。"

// Options 是 New 的参数。
type Options struct {
	Root    string                          // 项目的根目录
	Version string                          // 框架的版本，写进服务的信息
	Inspect func() (*agents.Project, error) // 读出项目的信息，tenon_app_info 每次调用时运行，项目的变化随即可见
}

// New 读取 Root 中 .tenon/docs 的文档，返回登记了四个工具的 MCP 服务。
func New(opts Options) (*mcp.Server, error) {
	docsDir := filepath.Join(opts.Root, filepath.FromSlash(agents.DocsDir))
	d, err := loadDocs(docsDir)
	if err != nil {
		return nil, err
	}
	srv := mcp.NewServer(&mcp.Implementation{Name: "tenon", Version: opts.Version}, &mcp.ServerOptions{Instructions: instructions})

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "tenon_search_docs",
		Title:       "检索 Tenon 文档",
		Description: "在项目所用版本的 Tenon 框架文档中按小节检索，返回最相关的小节：标题、路径和锚点、片段。中文和英文都可以，关键词越具体越好，例如“访问策略”“迁移 回滚”“RunInTx”。找到后用 tenon_read_doc 读这一节的全文。",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true},
	}, func(_ context.Context, _ *mcp.CallToolRequest, in searchArgs) (*mcp.CallToolResult, any, error) {
		limit := in.Limit
		if limit <= 0 {
			limit = 8
		}
		if limit > 20 {
			limit = 20
		}
		hits := d.search(in.Query, limit)
		if len(hits) == 0 {
			return text(fmt.Sprintf("没有找到与“%s”相关的小节，换几个关键词再试，或者读目录 %s/README.md。", in.Query, agents.DocsDir)), nil, nil
		}
		var b strings.Builder
		fmt.Fprintf(&b, "与“%s”最相关的 %d 个小节：\n", in.Query, len(hits))
		for i, h := range hits {
			ref := path.Join(agents.DocsDir, h.section.page)
			title := h.section.pageTitle
			if h.section.anchor != "" {
				ref += "#" + h.section.anchor
				title += " › " + h.section.title
			}
			fmt.Fprintf(&b, "\n%d. %s\n   %s\n   %s\n", i+1, title, ref, snippet(h.section, in.Query))
		}
		return text(b.String()), nil, nil
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "tenon_read_doc",
		Title:       "阅读 Tenon 文档",
		Description: "读 Tenon 框架文档中的一页。只给 path 时返回这一页的介绍和全部小节的锚点；给 anchor 时返回这个小节及其子小节；full 为 true 时返回整页。path 可以写 http/routing.md、.tenon/docs/http/routing.md 或带锚点的 http/routing.md#access-policies。",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true},
	}, func(_ context.Context, _ *mcp.CallToolRequest, in readArgs) (*mcp.CallToolResult, any, error) {
		page, anchor, _ := strings.Cut(strings.TrimPrefix(strings.TrimPrefix(in.Path, "./"), agents.DocsDir+"/"), "#")
		if in.Anchor != "" {
			anchor = strings.TrimPrefix(in.Anchor, "#")
		}
		out, err := d.read(agents.DocsDir, page, anchor, in.Full)
		if err != nil {
			return nil, nil, err
		}
		return text(out), nil, nil
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "tenon_app_info",
		Title:       "项目信息",
		Description: "列出当前 Tenon 项目的信息：框架版本、应用入口和地址、前端（目录、挂载路径、是否服务端渲染）、客户端、安装的技能。依赖的版本见 go.mod 和各前端的 package.json。",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true},
	}, func(context.Context, *mcp.CallToolRequest, struct{}) (*mcp.CallToolResult, appInfo, error) {
		p, err := opts.Inspect()
		if err != nil {
			return nil, appInfo{}, err
		}
		info := appInfo{
			Framework: p.Tenon,
			Main:      p.Config.Main,
			URL:       p.Config.URL,
			Docs:      agents.DocsDir,
			Frontends: []frontend{},
			Clients:   []client{},
			Skills:    []string{},
		}
		for _, f := range p.Config.Frontends {
			info.Frontends = append(info.Frontends, frontend{Dir: f.Dir, Base: f.Base, SSR: f.SSR})
		}
		for _, c := range p.Config.Clients {
			info.Clients = append(info.Clients, client{Dir: c.Dir, Kind: c.Kind, Frontend: path.Join(project.AppsDir, c.Frontend)})
		}
		if len(agents.SkillDirs(p.Config.AgentTargets())) > 0 {
			skills, err := agents.Skills(p)
			if err != nil {
				return nil, appInfo{}, err
			}
			for _, s := range skills {
				info.Skills = append(info.Skills, s.Name)
			}
		}
		return nil, info, nil
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "tenon_dev_logs",
		Title:       "开发环境的输出",
		Description: "读 bun run dev（tenon dev）运行期间的输出：Go 的编译错误、应用的日志（text 格式，级别是 DBG、INF、WRN、ERR）、前端开发服务器的输出。改完代码后用它确认重新编译成功、没有报错。返回最后 lines 行（默认 100），contains 只保留包含这段文字的行，例如 ERR 或 编译失败。",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true},
	}, func(_ context.Context, _ *mcp.CallToolRequest, in logArgs) (*mcp.CallToolResult, any, error) {
		file := filepath.Join(opts.Root, filepath.FromSlash(dev.LogFile))
		info, err := os.Stat(file)
		if errors.Is(err, fs.ErrNotExist) {
			return text(fmt.Sprintf("没有 %s：开发环境还没有启动过。在项目根目录运行 bun run dev。", dev.LogFile)), nil, nil
		}
		if err != nil {
			return nil, nil, err
		}
		data, err := os.ReadFile(file)
		if err != nil {
			return nil, nil, err
		}
		lines := in.Lines
		if lines <= 0 {
			lines = 100
		}
		if lines > 1000 {
			lines = 1000
		}
		var kept []string
		for line := range strings.Lines(string(data)) {
			if in.Contains == "" || strings.Contains(line, in.Contains) {
				kept = append(kept, strings.TrimRight(line, "\n"))
			}
		}
		kept = kept[max(len(kept)-lines, 0):]
		header := fmt.Sprintf("%s，最后写入于 %s，%d 行：\n\n", dev.LogFile, info.ModTime().Format(time.DateTime), len(kept))
		return text(header + strings.Join(kept, "\n")), nil, nil
	})
	return srv, nil
}

// searchArgs 是 tenon_search_docs 的参数。
type searchArgs struct {
	Query string `json:"query" jsonschema:"要找的内容，例如 访问策略、迁移 回滚、RunInTx"`
	Limit int    `json:"limit,omitempty" jsonschema:"最多返回几个小节，默认 8，最多 20"`
}

// readArgs 是 tenon_read_doc 的参数。
type readArgs struct {
	Path   string `json:"path" jsonschema:"文档页的路径，例如 http/routing.md，可以带 #锚点"`
	Anchor string `json:"anchor,omitempty" jsonschema:"小节的锚点，例如 access-policies；不写时返回这一页的目录"`
	Full   bool   `json:"full,omitempty" jsonschema:"为 true 时返回整页"`
}

// logArgs 是 tenon_dev_logs 的参数。
type logArgs struct {
	Lines    int    `json:"lines,omitempty" jsonschema:"返回最后几行，默认 100，最多 1000"`
	Contains string `json:"contains,omitempty" jsonschema:"只保留包含这段文字的行，例如 ERR"`
}

// appInfo 是 tenon_app_info 的结果。
type appInfo struct {
	Framework string     `json:"framework"` // 框架的版本
	Main      string     `json:"main"`      // 应用入口的包
	URL       string     `json:"url"`       // 开发时应用的地址
	Docs      string     `json:"docs"`      // 框架文档的目录
	Frontends []frontend `json:"frontends"` // 前端
	Clients   []client   `json:"clients"`   // 网页以外的客户端
	Skills    []string   `json:"skills"`    // 安装的技能
}

// frontend 是 appInfo 中的一个前端。
type frontend struct {
	Dir  string `json:"dir"`  // 目录，例如 apps/admin
	Base string `json:"base"` // 挂载的路径
	SSR  bool   `json:"ssr"`  // 是否在服务端渲染
}

// client 是 appInfo 中的一个客户端。
type client struct {
	Dir      string `json:"dir"`      // 目录，例如 clients/desk
	Kind     string `json:"kind"`     // 种类，例如 electron
	Frontend string `json:"frontend"` // 显示的前端的目录
}

// text 返回只有一段文字的工具结果。
func text(s string) *mcp.CallToolResult {
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: s}}}
}
