package devmcp_test

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/go-tenon/tenon/cmd/tenon/internal/agents"
	"github.com/go-tenon/tenon/cmd/tenon/internal/deploy"
	"github.com/go-tenon/tenon/cmd/tenon/internal/dev"
	"github.com/go-tenon/tenon/cmd/tenon/internal/devmcp"
	"github.com/go-tenon/tenon/cmd/tenon/internal/project"
)

// connect 用框架的文档建一个项目目录，启动 tenon mcp 的服务并连上它。
func connect(t *testing.T) (string, *mcp.ClientSession) {
	t.Helper()
	root := t.TempDir()
	if err := agents.SyncDocs(root, "../../../.."); err != nil {
		t.Fatal(err)
	}
	srv, err := devmcp.New(devmcp.Options{Root: root, Version: "v0.0.2", Inspect: func() (*agents.Project, error) {
		return &agents.Project{
			Config: &project.Config{
				Main: "./cmd/blog", URL: "http://localhost:8080",
				Frontends: []project.Frontend{{Name: "admin", Dir: "apps/admin", Base: "/admin/"}},
			},
			Tenon: "v0.0.2",
		}, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	serverSide, clientSide := mcp.NewInMemoryTransports()
	if _, err := srv.Connect(ctx, serverSide, nil); err != nil {
		t.Fatal(err)
	}
	session, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil).Connect(ctx, clientSide, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return root, session
}

// call 调用工具 name，返回结果的文字；isErr 报告是否是错误结果。
func call(t *testing.T, s *mcp.ClientSession, name string, args map[string]any) (text string, isErr bool) {
	t.Helper()
	res, err := s.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			text += tc.Text
		}
	}
	return text, res.IsError
}

// 全部工具都以 tenon_ 开头。
func TestTools(t *testing.T) {
	t.Parallel()
	_, s := connect(t)
	res, err := s.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, tool := range res.Tools {
		names = append(names, tool.Name)
	}
	want := "tenon_app_info tenon_check tenon_deploy_info tenon_deploy_logs tenon_deploy_plan tenon_deploy_status tenon_dev_logs tenon_read_doc tenon_search_docs"
	if got := strings.Join(names, " "); got != want {
		t.Errorf("tools:\n got: %s\nwant: %s", got, want)
	}
}

// tenon_search_docs 按小节检索：AI 编程助手常查的任务、中文的词和代码中的名字都能找到对应的页面或小节，排在前三。
// 这是文档检索的回归用例：改了文档的标题、措辞或拆合页面后，这些查询仍要命中。
func TestSearchDocs(t *testing.T) {
	t.Parallel()
	_, s := connect(t)
	const docs = "tenon/docs/"
	for query, want := range map[string]string{
		"访问策略":               "http/routing.md#access-policies",
		"RequireToken":       "http/routing.md#access-policies",
		"RunInTx":            "database/database.md#transactions",
		"新建迁移":               "database/migrations.md#writing-migrations",
		"上传文件":               "http/clients.md#uploading-files",
		"游标分页":               "http/responses.md#cursor-pagination",
		"生成客户端":              "installation/tooling.md#generate",
		"Go 客户端":             "http/clients.md#go-client",
		"注册接口":               "http/routing.md#operations",
		"校验请求体":              "http/requests.md#json-body",
		"RPC 端点":             "rpc/endpoints.md",
		"跨域":                 "http/middleware.md#cors",
		"App 最低版本":           "http/middleware.md#app-version",
		"public_url":         "http/middleware.md#public-url",
		"对外地址 邮件链接":          "http/middleware.md#public-url",
		"推送进度 SSE":           "http/sse.md",
		"errorx.Define":      "general/errors.md#defining-errors",
		"环境变量":               "general/configuration.md#environment-variables",
		"配置文件":               "general/configuration.md",
		"自定义命令":              "general/commands.md#custom-commands",
		"业务动作":               "concepts/actions.md",
		"依赖装配":               "concepts/dependencies.md",
		"AfterCommit":        "database/database.md#after-commit",
		"软删除":                "database/database.md#soft-deletes",
		"请求 ID 日志":           "general/logging.md",
		"健康检查":               "concepts/lifecycle.md",
		"平滑停止":               "concepts/lifecycle.md",
		"日志脱敏":               "general/logging.md#redacting-sensitive-data",
		"控制时间 synctest":      "testing/testing.md#controlling-time",
		"dbtest":             "testing/testing.md#creating-test-databases",
		"测试接口":               "testing/testing.md",
		"定时任务":               "background/scheduling.md",
		"发布队列任务":             "background/queue.md#publishing-jobs",
		"失败任务":               "background/queue.md#failed-jobs",
		"客户端直传":              "libraries/filesystem.md#direct-upload",
		"缩略图":                "libraries/filesystem.md#thumbnails",
		"邮件模板":               "libraries/mail.md#writing-templates",
		"在线成员":               "libraries/realtime.md",
		"后台导入":               "features/import-export.md#background-imports",
		"后台操作 进度":            "features/operations.md",
		"注销账号":               "features/privacy.md",
		"导出个人数据":             "features/privacy.md",
		"推送到手机":              "features/push.md",
		"Remember 缓存":        "libraries/cache.md#cache-aside",
		"发送邮件":               "libraries/mail.md",
		"WebSocket 实时频道":     "libraries/realtime.md",
		"限流":                 "libraries/rate-limiting.md",
		"原子锁":                "libraries/locks.md",
		"文件存储 S3":            "libraries/filesystem.md",
		"站内通知":               "features/notifications.md",
		"导出 Excel":           "features/import-export.md",
		"功能开关":               "features/flags.md",
		"已保存视图":              "features/saved-views.md",
		"savedview":          "features/saved-views.md",
		"Webhook":            "features/webhooks.md",
		"全文搜索":               "features/search.md",
		"邮箱密码登录":             "security/login.md",
		"API 令牌":             "security/api-tokens.md",
		"多租户":                "security/tenancy.md",
		"运营后台":               "security/tenancy.md",
		"二次验证":               "security/login.md#two-factor",
		"Passkey":            "security/login.md#passkeys",
		"找回密码":               "security/login.md#reset",
		"代为登录":               "security/authentication.md#impersonation",
		"签名链接":               "security/encryption.md#signed-urls",
		"密码哈希":               "security/encryption.md#hashing-passwords",
		"Sign in with Apple": "security/social-login.md#sign-in-with-apple",
		"会话 cookie":          "security/api-tokens.md#browser-sessions",
		"角色与权限":              "security/authorization.md",
		"OAuth":              "security/oauth-server.md",
		"第三方登录":              "security/social-login.md",
		"表单校验":               "frontend/lists-forms.md#forms",
		"侧栏菜单":               "frontend/layout.md#menu",
		"服务端渲染":              "frontend/hosting.md",
		"预渲染":                "frontend/hosting.md#prerendering",
		"增删改查页面":             "frontend/resources.md",
		"多标签":                "frontend/layout.md#tabs",
		"主题 品牌色":             "frontend/conventions.md",
		"Electron 自动更新":      "clients/electron.md#auto-update",
		"原生客户端登录":            "clients/native.md",
		"AI 对话":              "ai/chat.md",
		"审批工具调用":             "ai/chat.md#approval",
		"文档切分":               "ai/documents.md",
		"部署":                 "installation/deployment.md",
		"tenon deploy":       "installation/deployment.md",
		"systemd":            "installation/deployment.md",
		"不停机升级":              "installation/operations.md",
		"自托管":                "installation/self-hosting.md",
		"tenon init":         "installation/installing.md",
		"MCP 工具":             "ai/mcp.md",
		"向量检索":               "ai/vector.md",
	} {
		text, isErr := call(t, s, "tenon_search_docs", map[string]any{"query": query, "limit": 3})
		if isErr || !strings.Contains(text, docs+want) {
			t.Errorf("%s: want %s in the top 3:\n%s", query, docs+want, text)
		}
	}
	if text, _ := call(t, s, "tenon_search_docs", map[string]any{"query": "!!!"}); !strings.Contains(text, "没有找到") {
		t.Errorf("no match:\n%s", text)
	}
}

// tenon_read_doc 读一页的目录、一个小节（连同子小节）或整页；路径可以带 tenon/docs 前缀和锚点。
func TestReadDoc(t *testing.T) {
	t.Parallel()
	_, s := connect(t)
	outline, _ := call(t, s, "tenon_read_doc", map[string]any{"path": "http/routing.md"})
	if !strings.Contains(outline, "# 路由") || !strings.Contains(outline, "- #access-policies 访问策略") {
		t.Errorf("outline:\n%s", outline)
	}
	section, _ := call(t, s, "tenon_read_doc", map[string]any{"path": "tenon/docs/http/routing.md#access-policies"})
	if !strings.Contains(section, "rest.Public()") || strings.Contains(section, "## 组织路由") {
		t.Errorf("section:\n%s", section)
	}
	// 读二级小节时包括它的三级子小节。
	parent, _ := call(t, s, "tenon_read_doc", map[string]any{"path": "concepts/actions.md", "anchor": "entry-points"})
	if !strings.Contains(parent, "### HTTP 接口") || !strings.Contains(parent, "### RPC") || strings.Contains(parent, "## 同步的后续动作") {
		t.Errorf("section with subsections:\n%s", parent)
	}
	full, _ := call(t, s, "tenon_read_doc", map[string]any{"path": "http/routing.md", "full": true})
	// 整页从标题开始，包括第一节和最后一节。
	if !strings.HasPrefix(full, "# 路由") || !strings.Contains(full, `<a id="basic-routing"></a>`) || !strings.Contains(full, `<a id="native-huma-register"></a>`) {
		t.Errorf("full page starts with %q", full[:min(len(full), 40)])
	}
	if text, isErr := call(t, s, "tenon_read_doc", map[string]any{"path": "http/nothing.md"}); !isErr || !strings.Contains(text, "no page") {
		t.Errorf("missing page: %v %s", isErr, text)
	}
	if text, isErr := call(t, s, "tenon_read_doc", map[string]any{"path": "http/routing.md", "anchor": "nothing"}); !isErr || !strings.Contains(text, "no anchor") {
		t.Errorf("missing anchor: %v %s", isErr, text)
	}
}

// tenon_app_info 列出项目的版本、前端和技能。
func TestAppInfo(t *testing.T) {
	t.Parallel()
	_, s := connect(t)
	res, err := s.CallTool(context.Background(), &mcp.CallToolParams{Name: "tenon_app_info", Arguments: map[string]any{}})
	if err != nil {
		t.Fatal(err)
	}
	var info struct {
		Framework string `json:"framework"`
		Main      string `json:"main"`
		Frontends []struct{ Dir, Base string }
		Skills    []string
	}
	b, _ := json.Marshal(res.StructuredContent)
	if err := json.Unmarshal(b, &info); err != nil {
		t.Fatal(err)
	}
	if info.Framework != "v0.0.2" || info.Main != "./cmd/blog" || len(info.Frontends) != 1 || info.Frontends[0].Base != "/admin/" {
		t.Errorf("info %+v", info)
	}
	if !strings.Contains(strings.Join(info.Skills, " "), "tenon-frontend") {
		t.Errorf("skills: %v", info.Skills)
	}
}

// tenon_dev_logs 返回 .tenon/dev.log 的最后几行，可以只保留包含某段文字的行；还没有启动开发环境时提示运行 bun run dev。
func TestDevLogs(t *testing.T) {
	t.Parallel()
	root, s := connect(t)
	if text, _ := call(t, s, "tenon_dev_logs", nil); !strings.Contains(text, "bun run dev") {
		t.Errorf("no log:\n%s", text)
	}
	log := "dev: 打开 http://localhost:8080\n2026-11-19 10:00:00.000 INF started\n2026-11-19 10:00:01.000 ERR boom\ndev: 编译失败：exit status 1\n"
	logPath := filepath.Join(root, dev.LogFile)
	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(logPath, []byte(log), 0o644); err != nil {
		t.Fatal(err)
	}
	if text, _ := call(t, s, "tenon_dev_logs", map[string]any{"lines": 2}); !strings.HasSuffix(text, "ERR boom\ndev: 编译失败：exit status 1") || strings.Contains(text, "INF") {
		t.Errorf("last 2 lines:\n%s", text)
	}
	if text, _ := call(t, s, "tenon_dev_logs", map[string]any{"contains": "ERR"}); !strings.Contains(text, "1 行") || !strings.Contains(text, "ERR boom") {
		t.Errorf("filtered:\n%s", text)
	}
}

// stubDeployRunner 供 TestDeployTools 模拟远端状态和日志输出。
type stubDeployRunner struct{}

// Run 返回模拟的状态与日志响应。
func (stubDeployRunner) Run(_ context.Context, host deploy.Host, script string, _ io.Reader, stdout, _ io.Writer) error {
	switch {
	case strings.Contains(script, "systemctl show -p ActiveState"):
		_, _ = io.WriteString(stdout, "release=v1.2.0\n---unit=blog@http.service\nActiveState=active\nSubState=running\nStatusText=\nActiveEnterTimestamp=Sun\n")
	case strings.Contains(script, "journalctl"):
		_, _ = io.WriteString(stdout, "INF ok "+host.Addr+"\nERR fail "+host.Addr+"\n")
	}
	return nil
}

// Upload 在只读 MCP 工具测试中不执行实际上传。
func (stubDeployRunner) Upload(_ context.Context, _ deploy.Host, _, _ string) error { return nil }

// tenon_deploy_* 四个只读部署观测工具：拓扑、预演计划、状态和多机聚合日志。
func TestDeployTools(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	if err := agents.SyncDocs(root, "../../../.."); err != nil {
		t.Fatal(err)
	}
	srv, err := devmcp.New(devmcp.Options{
		Root:    root,
		Version: "v0.0.2",
		Inspect: func() (*agents.Project, error) {
			return &agents.Project{Config: &project.Config{Main: "./cmd/blog"}, Tenon: "v0.0.2"}, nil
		},
		DeployRunner: func(deploy.Target) (deploy.Runner, func() error, error) {
			return stubDeployRunner{}, nil, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	serverSide, clientSide := mcp.NewInMemoryTransports()
	if _, err := srv.Connect(ctx, serverSide, nil); err != nil {
		t.Fatal(err)
	}
	s, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil).Connect(ctx, clientSide, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })

	// 尚未创建 deploy.yaml 时返回明确引导提示。
	if msg, isErr := call(t, s, "tenon_deploy_info", nil); isErr || !strings.Contains(msg, "tenon add deploy") {
		t.Fatalf("without deploy.yaml: isErr=%v msg=%q", isErr, msg)
	}

	cfg := `app: blog
environments:
  production:
    roles:
      http:
        hosts: [web-1, web-2]
      worker:
        hosts: [worker-1]
`
	if err := os.WriteFile(filepath.Join(root, "deploy.yaml"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}

	resInfo, err := s.CallTool(ctx, &mcp.CallToolParams{Name: "tenon_deploy_info", Arguments: map[string]any{}})
	if err != nil || resInfo.IsError {
		t.Fatalf("tenon_deploy_info: err=%v res=%+v", err, resInfo)
	}
	infoJSON, _ := json.Marshal(resInfo.StructuredContent)
	if !strings.Contains(string(infoJSON), `"migrate_host":"worker-1"`) {
		t.Errorf("tenon_deploy_info:\n%s", infoJSON)
	}

	resPlan, err := s.CallTool(ctx, &mcp.CallToolParams{Name: "tenon_deploy_plan", Arguments: map[string]any{"version": "v2"}})
	if err != nil || resPlan.IsError {
		t.Fatalf("tenon_deploy_plan: err=%v res=%+v", err, resPlan)
	}
	planJSON, _ := json.Marshal(resPlan.StructuredContent)
	if !strings.Contains(string(planJSON), `"http-canary"`) {
		t.Errorf("tenon_deploy_plan:\n%s", planJSON)
	}

	resStatus, err := s.CallTool(ctx, &mcp.CallToolParams{Name: "tenon_deploy_status", Arguments: map[string]any{"role": "http"}})
	if err != nil || resStatus.IsError {
		t.Fatalf("tenon_deploy_status: err=%v res=%+v", err, resStatus)
	}
	statusJSON, _ := json.Marshal(resStatus.StructuredContent)
	if !strings.Contains(string(statusJSON), `"release":"v1.2.0"`) {
		t.Errorf("tenon_deploy_status:\n%s", statusJSON)
	}

	resLogs, err := s.CallTool(ctx, &mcp.CallToolParams{Name: "tenon_deploy_logs", Arguments: map[string]any{"role": "http", "contains": "ERR"}})
	if err != nil || resLogs.IsError {
		t.Fatalf("tenon_deploy_logs: err=%v res=%+v", err, resLogs)
	}
	logsJSON, _ := json.Marshal(resLogs.StructuredContent)
	if !strings.Contains(string(logsJSON), "ERR fail web-1") || !strings.Contains(string(logsJSON), "ERR fail web-2") || strings.Contains(string(logsJSON), "INF ok") {
		t.Errorf("tenon_deploy_logs:\n%s", logsJSON)
	}
}

// TestCheckTool 验证 tenon_check 工具能够运行静态检查并返回结构化违规结果。
func TestCheckTool(t *testing.T) {
	t.Parallel()
	root, s := connect(t)
	badFile := filepath.Join(root, "cmd", "blog", "main.go")
	if err := os.MkdirAll(filepath.Dir(badFile), 0o755); err != nil {
		t.Fatal(err)
	}
	src := `package main

import (
	"context"
	"net"
	"net/http"

	"github.com/go-tenon/tenon/httpx"
)

func run(ctx context.Context, cfg httpx.Config, ln net.Listener, h http.Handler) {
	_ = httpx.Serve(ctx, cfg, ln, h)
}
`
	if err := os.WriteFile(badFile, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := s.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "tenon_check",
		Arguments: map[string]any{"checks": []string{"wiring"}},
	})
	if err != nil || res.IsError {
		t.Fatalf("tenon_check: err=%v res=%+v", err, res)
	}
	raw, _ := json.Marshal(res.StructuredContent)
	if !strings.Contains(string(raw), `"passed":false`) || !strings.Contains(string(raw), "httpx.Serve is missing") {
		t.Errorf("tenon_check result:\n%s", raw)
	}
}
