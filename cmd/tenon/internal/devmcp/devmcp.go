// Package devmcp 实现 tenon mcp：开发与运维期间给 AI 编程助手用的 MCP 服务，以 stdio 运行。工具名都以 tenon_ 开头，
// 与 Mantine 等其他 MCP 服务的同名工具区分：
//
//	tenon_search_docs    按小节检索 tenon/docs 中的框架文档
//	tenon_read_doc       读一页文档的目录、一个小节或整页
//	tenon_app_info       项目的框架版本、入口、前端与客户端和技能
//	tenon_check          静态检查项目是否遵守框架约定（文字、时钟、错误码、颜色、依赖、装配）
//	tenon_dev_logs       tenon dev 运行期间各进程的输出（dev.LogFile）
//	tenon_deploy_info    deploy/ 下的所有部署目标实例、角色、主机和发布波次拓扑（不含密钥）
//	tenon_deploy_status  远端目标实例各主机的当前发布版本与 systemd 服务状态
//	tenon_deploy_logs    按目标/角色/主机/关键字聚合拉取远端 journald 日志
//	tenon_deploy_plan    预演某目标实例的发布计划（波次、主机、服务部分、迁移主节点）
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
	"github.com/go-tenon/tenon/cmd/tenon/internal/check"
	"github.com/go-tenon/tenon/cmd/tenon/internal/deploy"
	"github.com/go-tenon/tenon/cmd/tenon/internal/dev"
	"github.com/go-tenon/tenon/cmd/tenon/internal/project"
)

// instructions 在连接时交给模型，说明工具之间怎样配合。
const instructions = "Tenon 框架的开发与部署工具，文档的版本与项目 go.mod 中的框架版本一致。写 Tenon 的代码之前，" +
	"用 tenon_search_docs 查找相关的文档小节，再用 tenon_read_doc 读它；不确定项目有哪些前端、依赖是什么版本时用 tenon_app_info；" +
	"改完代码后用 tenon_check 检查项目约定和框架装配（时钟、错误码译文、硬编码颜色、依赖版本、热重载与事务装配），用 tenon_dev_logs 看 bun run dev 中的编译错误和应用日志；" +
	"排查或准备线上发布时，用 tenon_deploy_info、tenon_deploy_status、tenon_deploy_logs 和 tenon_deploy_plan 查看集群拓扑、状态、多机日志与发布计划，" +
	"真正执行发布或回滚时运行 go tool tenon deploy --format json。"

// Options 是 New 的参数。
type Options struct {
	Root         string                                                     // 项目的根目录
	Version      string                                                     // 框架的版本，写进服务的信息
	Inspect      func() (*agents.Project, error)                            // 读出项目的信息，tenon_app_info 每次调用时运行，项目的变化随即可见
	DeployRunner func(t deploy.Target) (deploy.Runner, func() error, error) // 创建远端部署 Runner；为空时默认使用 deploy.NewSSHRunner
}

// New 读取 Root 中 tenon/docs 的文档，返回登记了开发与只读部署观测工具的 MCP 服务。
func New(opts Options) (*mcp.Server, error) {
	docsDir := filepath.Join(opts.Root, filepath.FromSlash(agents.DocsDir))
	d, err := loadDocs(docsDir)
	if err != nil {
		return nil, err
	}
	newRunner := opts.DeployRunner
	if newRunner == nil {
		newRunner = func(t deploy.Target) (deploy.Runner, func() error, error) {
			r, err := deploy.NewObserveSSHRunner(t)
			if err != nil {
				return nil, nil, err
			}
			return r, r.Close, nil
		}
	}
	resolveDeployOpts := func(dir, targetsDir string) (deploy.LoadOptions, error) {
		p, err := opts.Inspect()
		if err != nil {
			return deploy.LoadOptions{}, err
		}
		if dir == "" {
			dir = deploy.DefaultDir
		}
		if !filepath.IsAbs(dir) {
			dir = filepath.Join(opts.Root, dir)
		}
		if targetsDir != "" && !filepath.IsAbs(targetsDir) {
			targetsDir = filepath.Join(opts.Root, targetsDir)
		}
		return deploy.LoadOptions{
			Dir:        dir,
			TargetsDir: targetsDir,
			DefaultApp: path.Base(p.Config.Main),
		}, nil
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
		Description: "读 Tenon 框架文档中的一页。只给 path 时返回这一页的介绍和全部小节的锚点；给 anchor 时返回这个小节及其子小节；full 为 true 时返回整页。path 可以写 http/routing.md、tenon/docs/http/routing.md 或带锚点的 http/routing.md#access-policies。",
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
		Name:        "tenon_check",
		Title:       "静态检查项目约定",
		Description: "运行 tenon check 静态检查（text、clock、errors、colors、deps、wiring）：检查前端写死文字和颜色、SQL 数据库时钟、errorx.Define 译文、前端 peerDependencies 版本以及框架装配遗漏（热重载选项、事务内 WithDB、搜索主题、限流/锁清理、请求体上限、SSR requestMiddleware）。改完代码和提交前调用。",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true},
	}, func(_ context.Context, _ *mcp.CallToolRequest, in checkArgs) (*mcp.CallToolResult, checkResult, error) {
		p, err := opts.Inspect()
		if err != nil {
			return nil, checkResult{}, err
		}
		found, err := check.Run(opts.Root, p.Config, in.Checks)
		if err != nil {
			return nil, checkResult{}, err
		}
		res := checkResult{
			Passed:   len(found) == 0,
			Count:    len(found),
			Findings: make([]checkFinding, 0, len(found)),
		}
		for _, f := range found {
			res.Findings = append(res.Findings, checkFinding{
				Check:   f.Check,
				File:    f.File,
				Line:    f.Line,
				Message: f.Message,
			})
		}
		return nil, res, nil
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

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "tenon_deploy_info",
		Title:       "部署拓扑信息",
		Description: "读取项目的 deploy/app.yaml 与 deploy/targets/*，返回所有部署目标实例的拓扑：实例名、角色、主机列表、各主机运行的服务部分、主迁移节点和滚动发布波次（不含密钥）。排查或准备发布之前先调用它。",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true},
	}, func(_ context.Context, _ *mcp.CallToolRequest, in deployInfoArgs) (*mcp.CallToolResult, any, error) {
		loadOpts, err := resolveDeployOpts(in.Dir, in.TargetsDir)
		if err != nil {
			return nil, nil, err
		}
		if _, err := os.Stat(deploy.AppFilePath(loadOpts.Dir)); errors.Is(err, fs.ErrNotExist) {
			return text("当前项目还没有 deploy/app.yaml：运行 go tool tenon add deploy 生成原生二进制多实例部署骨架。"), nil, nil
		}
		topo, err := deploy.LoadTopology(loadOpts)
		if err != nil {
			return nil, nil, err
		}
		return nil, topo, nil
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "tenon_deploy_status",
		Title:       "线上部署状态",
		Description: "通过 SSH 并发查询远端目标实例各主机当前的发布版本与每个 systemd unit 的运行状态（ActiveState、SubState、StatusText）。可按 target、role、host 过滤。",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in deployStatusArgs) (*mcp.CallToolResult, any, error) {
		loadOpts, err := resolveDeployOpts(in.Dir, in.TargetsDir)
		if err != nil {
			return nil, nil, err
		}
		target, err := deploy.Load(loadOpts, in.Target)
		if err != nil {
			return nil, nil, err
		}
		r, closeFn, err := newRunner(target)
		if err != nil {
			return nil, nil, err
		}
		if closeFn != nil {
			defer func() { _ = closeFn() }()
		}
		report, err := deploy.CollectStatus(ctx, r, target, in.Role, in.Host)
		if err != nil {
			return nil, nil, err
		}
		return nil, report, nil
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "tenon_deploy_logs",
		Title:       "聚合拉取线上日志",
		Description: "通过 SSH 并发拉取单台或多台线上主机的 journald 日志。支持按 target、role、host、part、since（如 15m、1h）、contains（如 ERR 或 X-Request-ID）过滤，每台默认返回 100 行（最多 1000 行）。",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in deployLogsArgs) (*mcp.CallToolResult, any, error) {
		loadOpts, err := resolveDeployOpts(in.Dir, in.TargetsDir)
		if err != nil {
			return nil, nil, err
		}
		target, err := deploy.Load(loadOpts, in.Target)
		if err != nil {
			return nil, nil, err
		}
		r, closeFn, err := newRunner(target)
		if err != nil {
			return nil, nil, err
		}
		if closeFn != nil {
			defer func() { _ = closeFn() }()
		}
		lines := in.Lines
		if lines <= 0 {
			lines = 100
		}
		if lines > 1000 {
			lines = 1000
		}
		report, err := deploy.CollectLogs(ctx, r, target, deploy.LogsOptions{
			Role:     in.Role,
			Host:     in.Host,
			Part:     in.Part,
			Lines:    lines,
			Since:    in.Since,
			Contains: in.Contains,
		})
		if err != nil {
			return nil, nil, err
		}
		return nil, report, nil
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "tenon_deploy_plan",
		Title:       "预演发布计划",
		Description: "生成某目标实例的只读发布计划（不构建也不修改远端）：列出实例名、目标架构、主迁移节点、各波次（后端优先、入口金丝雀后批量滚动）及各主机将运行的服务部分。执行真实发布前先调用它向用户确认。",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in deployPlanArgs) (*mcp.CallToolResult, any, error) {
		loadOpts, err := resolveDeployOpts(in.Dir, in.TargetsDir)
		if err != nil {
			return nil, nil, err
		}
		target, err := deploy.Load(loadOpts, in.Target)
		if err != nil {
			return nil, nil, err
		}
		plan, err := deploy.Plan(ctx, target, deploy.ReleaseOptions{Version: in.Version})
		if err != nil {
			return nil, nil, err
		}
		return nil, plan, nil
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

// checkArgs 是 tenon_check 的参数。
type checkArgs struct {
	Checks []string `json:"checks,omitempty" jsonschema:"只运行指定的检查项（text、clock、errors、colors、deps、wiring），留空运行全部启用的检查项"`
}

// checkResult 是 tenon_check 的结构化结果。
type checkResult struct {
	Passed   bool           `json:"passed"`   // 全部检查通过时为 true
	Count    int            `json:"count"`    // 发现的问题总数
	Findings []checkFinding `json:"findings"` // 每条问题的文件、行号和说明
}

// checkFinding 是 tenon_check 的单条检查问题。
type checkFinding struct {
	Check   string `json:"check"`
	File    string `json:"file"`
	Line    int    `json:"line,omitempty"`
	Message string `json:"message"`
}

// deployInfoArgs 是 tenon_deploy_info 的参数。
type deployInfoArgs struct {
	Dir        string `json:"dir,omitempty" jsonschema:"部署配置根目录，默认 deploy"`
	TargetsDir string `json:"targets_dir,omitempty" jsonschema:"目标实例目录，留空时依次取 TENON_DEPLOY_TARGETS 与 <dir>/targets"`
}

// deployStatusArgs 是 tenon_deploy_status 的参数。
type deployStatusArgs struct {
	Dir        string `json:"dir,omitempty" jsonschema:"部署配置根目录，默认 deploy"`
	TargetsDir string `json:"targets_dir,omitempty" jsonschema:"目标实例目录"`
	Target     string `json:"target,omitempty" jsonschema:"目标实例名（仅存在唯一实例时可省略，多实例时必填）"`
	Role       string `json:"role,omitempty" jsonschema:"按角色过滤，例如 http、worker"`
	Host       string `json:"host,omitempty" jsonschema:"按主机地址过滤"`
}

// deployLogsArgs 是 tenon_deploy_logs 的参数。
type deployLogsArgs struct {
	Dir        string `json:"dir,omitempty" jsonschema:"部署配置根目录，默认 deploy"`
	TargetsDir string `json:"targets_dir,omitempty" jsonschema:"目标实例目录"`
	Target     string `json:"target,omitempty" jsonschema:"目标实例名（仅存在唯一实例时可省略，多实例时必填）"`
	Role       string `json:"role,omitempty" jsonschema:"按角色过滤主机，留空聚合全部主机"`
	Host       string `json:"host,omitempty" jsonschema:"只查看指定主机"`
	Part       string `json:"part,omitempty" jsonschema:"只查看指定服务部分（如 http），留空查看全部部分"`
	Lines      int    `json:"lines,omitempty" jsonschema:"每台主机返回最后几行，默认 100，最多 1000"`
	Since      string `json:"since,omitempty" jsonschema:"时间范围，例如 15m、1h"`
	Contains   string `json:"contains,omitempty" jsonschema:"只保留包含该文字的行，例如 ERR 或 X-Request-ID"`
}

// deployPlanArgs 是 tenon_deploy_plan 的参数。
type deployPlanArgs struct {
	Dir        string `json:"dir,omitempty" jsonschema:"部署配置根目录，默认 deploy"`
	TargetsDir string `json:"targets_dir,omitempty" jsonschema:"目标实例目录"`
	Target     string `json:"target,omitempty" jsonschema:"目标实例名（仅存在唯一实例时可省略，多实例时必填）"`
	Version    string `json:"version,omitempty" jsonschema:"可选的发布版本号"`
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
