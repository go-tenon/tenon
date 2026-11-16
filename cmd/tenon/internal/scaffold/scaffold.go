// Package scaffold 生成新项目和新前端：template/project 是项目，template/frontend 是 apps 中的一个前端。
// 每个 .tmpl 文件按参数渲染后写入目标目录。模板的分隔符是 [[ ]]，与 JSX 的 {{ }} 区分开；gitignore.tmpl 写成 .gitignore。
package scaffold

import (
	"bytes"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"text/template"
)

//go:embed all:template
var files embed.FS

// GoVersion 是生成的 go.mod 中的 go 版本，与框架要求的最低版本相同。
const GoVersion = "1.27.4"

// Options 是项目模板的参数。
type Options struct {
	// Module 是 Go 模块路径，Name 是应用的名称（页面标题、命令的说明）。
	Module, Name string
	// Slug 是 npm 包名的作用域，例如 todo 时包名是 @todo/web、@todo/client；默认由 Name 得出。
	Slug string
	// TenonVersion 是 go.mod 中要求的框架版本。
	TenonVersion string
	// 设置了 TenonDir 时，go.mod 用 replace 指向这个本地的框架目录（开发框架本身时使用）。
	TenonDir  string
	GoVersion string
}

// Frontend 是前端模板的参数。
type Frontend struct {
	// Scope 是 npm 包名的作用域，与项目根目录 package.json 的 name 相同。
	Scope string
	// Name 是前端的名称，即 apps 中的目录名。
	Name string
	// Base 是前端挂载的路径，以 / 开头和结尾，写入 Vite 的 base。
	Base string
	// Title 是页面标题。
	Title string
	// Template 是前端的模板，见 Templates；为空时是 default。
	Template string
	// Layout 是默认布局，见 Layouts；为空时是模板的第一种布局。default 模板没有布局。
	Layout string
}

// Layouts 是 admin 和 console 模板可选的布局，第一种是默认的。布局预览页（src/routes/_app/layouts.tsx）列出全部，
// 用户可以在它们之间切换；--layout 选的那一种排在第一位，成为默认布局。
var Layouts = map[string][]string{
	"admin":   {"side", "top", "mix", "rail", "centered"},
	"console": {"inset", "side", "floating", "top", "mix", "rail", "centered"},
}

// LayoutOrder 返回模板的全部布局，默认布局（f.Layout，为空时是第一种）排在第一位。
func (f Frontend) LayoutOrder() []string {
	all := Layouts[f.Template]
	if f.Layout == "" || !slices.Contains(all, f.Layout) {
		return all
	}
	out := []string{f.Layout}
	for _, l := range all {
		if l != f.Layout {
			out = append(out, l)
		}
	}
	return out
}

// LayoutArray 返回 LayoutOrder 的 TypeScript 数组字面量，例如 ["side", "top"]，写入模板的 src/layouts.ts。
func (f Frontend) LayoutArray() string {
	quoted := make([]string, 0, len(Layouts[f.Template]))
	for _, l := range f.LayoutOrder() {
		quoted = append(quoted, strconv.Quote(l))
	}
	return "[" + strings.Join(quoted, ", ") + "]"
}

// checkLayout 检查 f.Layout 是模板可选的布局。
func (f Frontend) checkLayout() error {
	if f.Layout == "" {
		return nil
	}
	all, ok := Layouts[f.Template]
	if !ok {
		return fmt.Errorf("template %q has no layouts; --layout is for the admin and console templates", f.templateName())
	}
	if !slices.Contains(all, f.Layout) {
		return fmt.Errorf("layout %q is not one of %v for the %s template", f.Layout, all, f.Template)
	}
	return nil
}

func (f Frontend) templateName() string {
	if f.Template == "" {
		return "default"
	}
	return f.Template
}

// Templates 是 tenon add frontend 的 --template 可选的模板：
//   - default：React、TanStack Router 和 @go-tenon/shell 的最小前端；
//   - admin：运营后台，antd 和 ProComponents，带登录页、侧栏布局和多标签（@go-tenon/admin）；
//   - console：shadcn 和 Tailwind 的控制台，带登录页、侧栏布局、多标签和 tenon registry 的组件。
var Templates = []string{"default", "admin", "console"}

// AdminPackage 是 admin 模板依赖的框架前端包在项目中的位置，加入 Bun workspace。
// 默认不加入：Bun 会安装包的 peer 依赖，不用 antd 的项目不应该因此装上 antd。
const AdminPackage = ".tenon/js/admin"

// AntdMCP 是 admin 模板写入项目 .mcp.json 的 antd MCP 服务：@ant-design/cli 的版本与模板中的 antd 相同。
var AntdMCP = map[string]any{"command": "bunx", "args": []any{"@ant-design/cli@6.6.8", "mcp"}}

// ShadcnMCP 是 console 模板写入项目 .mcp.json 的 shadcn MCP 服务，版本与模板中的 shadcn 相同。
var ShadcnMCP = map[string]any{"command": "bunx", "args": []any{"shadcn@4.23.0", "mcp"}}

// ConsoleRegistryItems 是 console 模板从 tenon 的 shadcn registry（框架的 js/registry，复制在项目的 .tenon/js/registry）
// 加入前端的条目：布局、部件、页面、资源表格、表单和资源页面。通知、Markdown 和 AI 对话需要其他 npm 包，用到时用 shadcn add 加入。
var ConsoleRegistryItems = []string{
	"tenon-sidebar-layout", "tenon-topnav-layout", "tenon-mixed-layout", "tenon-rail-layout", "tenon-page-layouts", "tenon-tab-bar",
	"tenon-color-mode", "tenon-user-menu", "tenon-tenant-switcher", "tenon-command-menu", "tenon-dialogs",
	"tenon-page", "tenon-data-table", "tenon-form", "tenon-login-form", "tenon-resource-page",
}

// registryImport 匹配 registry 源码中对项目内文件的导入，例如 "@/components/ui/button"、"@/hooks/use-mobile"。
var registryImport = regexp.MustCompile(`from "@/((?:components/(?:ui|tenon)|hooks|lib)/[\w-]+)"`)

// InstallRegistry 把 registry（项目中 .tenon/js/registry 的位置）中的条目 items 复制到前端目录 dir：
// 条目的文件，以及它们导入的 shadcn 组件、tenon 组件、hooks 和 lib（逐层展开），与 shadcn add 加入的内容相同。
// 已有的文件不覆盖。
func InstallRegistry(registry, dir string, items []string) error {
	var manifest struct {
		Items []registryItem `json:"items"`
	}
	data, err := os.ReadFile(filepath.Join(registry, "registry.json"))
	if err != nil {
		return fmt.Errorf("scaffold: install registry: %w", err)
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		return fmt.Errorf("scaffold: install registry: registry.json: %w", err)
	}
	queue := []string{"src/lib/utils.ts"}
	for _, name := range items {
		i := slices.IndexFunc(manifest.Items, func(it registryItem) bool { return it.Name == name })
		if i < 0 {
			return fmt.Errorf("scaffold: install registry: no item %q", name)
		}
		for _, f := range manifest.Items[i].Files {
			queue = append(queue, "src/components/tenon/"+f)
		}
	}
	seen := map[string]bool{}
	for len(queue) > 0 {
		rel := queue[0]
		queue = queue[1:]
		if seen[rel] {
			continue
		}
		seen[rel] = true
		src, err := os.ReadFile(filepath.Join(registry, filepath.FromSlash(rel)))
		if err != nil {
			return fmt.Errorf("scaffold: install registry: %w", err)
		}
		for _, m := range registryImport.FindAllStringSubmatch(string(src), -1) {
			target, ok := registrySource(registry, "src/"+m[1])
			if !ok {
				return fmt.Errorf("scaffold: install registry: %s imports @/%s, which is not in the registry", rel, m[1])
			}
			queue = append(queue, target)
		}
		target := filepath.Join(dir, filepath.FromSlash(rel))
		if _, err := os.Stat(target); err == nil {
			continue
		}
		if err := writeFile(target, src); err != nil {
			return err
		}
	}
	return nil
}

// registryItem 是 registry.json 中的一个条目：files 是 src/components/tenon 中的文件名。
type registryItem struct {
	Name  string   `json:"name"`
	Files []string `json:"files"`
}

// registrySource 返回导入路径（不带扩展名）对应的文件。
func registrySource(registry, rel string) (string, bool) {
	for _, ext := range []string{".tsx", ".ts"} {
		if _, err := os.Stat(filepath.Join(registry, filepath.FromSlash(rel+ext))); err == nil {
			return rel + ext, true
		}
	}
	return "", false
}

// Package 是嵌入构建产物的 Go 包名：前端的名称去掉连字符。
func (f Frontend) Package() string { return strings.ReplaceAll(f.Name, "-", "") }

// Write 生成项目：一个名为 web 的前端挂在 /，带待办事项的示例。dir 必须是空目录或尚未创建。
func Write(dir string, opts Options) error {
	if opts.GoVersion == "" {
		opts.GoVersion = GoVersion
	}
	if opts.Slug == "" {
		opts.Slug = Slug(opts.Name)
	}
	if entries, err := os.ReadDir(dir); err == nil && len(entries) > 0 {
		return fmt.Errorf("%s is not empty", dir)
	} else if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	// 先写前端，项目模板中的示例页面再覆盖它的 App.tsx 和 app.css。
	web := Frontend{Scope: opts.Slug, Name: "web", Base: "/", Title: opts.Name}
	if err := render("template/frontend", filepath.Join(dir, "apps", web.Name), web); err != nil {
		return err
	}
	return render("template/project", dir, opts)
}

// WriteFrontend 在项目 root 的 apps/<名称> 中生成一个前端。这个目录必须尚未创建。
func WriteFrontend(root string, f Frontend) error {
	dir := filepath.Join(root, "apps", f.Name)
	if _, err := os.Stat(dir); err == nil {
		return fmt.Errorf("%s already exists", filepath.ToSlash(filepath.Join("apps", f.Name)))
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	if err := f.checkLayout(); err != nil {
		return err
	}
	switch f.Template {
	case "", "default":
		return render("template/frontend", dir, f)
	case "admin":
		return render("template/admin", dir, f)
	case "console":
		return render("template/console", dir, f)
	}
	return fmt.Errorf("template %q is not one of %v", f.Template, Templates)
}

// AddMCPServer 在 root/.mcp.json 的 mcpServers 中加入 name，已有同名的服务时不修改；文件不存在时新建。
func AddMCPServer(root, name string, server map[string]any) error {
	p := filepath.Join(root, ".mcp.json")
	doc := map[string]any{}
	if data, err := os.ReadFile(p); err == nil {
		if err := json.Unmarshal(data, &doc); err != nil {
			return fmt.Errorf(".mcp.json: %w", err)
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	servers, _ := doc["mcpServers"].(map[string]any)
	if servers == nil {
		servers = map[string]any{}
	}
	if _, ok := servers[name]; ok {
		return nil
	}
	servers[name] = server
	doc["mcpServers"] = servers
	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(p, append(data, '\n'), 0o644)
}

// workspaces 是 package.json 中的 workspaces 数组。
var workspaces = regexp.MustCompile(`(?s)"workspaces"\s*:\s*\[(.*?)\]`)

// AddWorkspace 把 member（例如 apps/admin）加入 root/package.json 的 workspaces，按路径排序。
func AddWorkspace(root, member string) error {
	p := filepath.Join(root, "package.json")
	data, err := os.ReadFile(p)
	if err != nil {
		return err
	}
	m := workspaces.FindSubmatchIndex(data)
	if m == nil {
		return fmt.Errorf("package.json has no workspaces")
	}
	var members []string
	for _, item := range strings.Split(string(data[m[2]:m[3]]), ",") {
		if item = strings.TrimSpace(item); item != "" {
			s, err := strconv.Unquote(item)
			if err != nil {
				return fmt.Errorf("package.json workspaces: %w", err)
			}
			members = append(members, s)
		}
	}
	if slices.Contains(members, member) {
		return nil
	}
	members = append(members, member)
	slices.Sort(members)
	var b strings.Builder
	b.WriteString(`"workspaces": [`)
	for i, s := range members {
		if i > 0 {
			b.WriteString(",")
		}
		b.WriteString("\n    " + strconv.Quote(s))
	}
	b.WriteString("\n  ]")
	out := slices.Concat(data[:m[0]], []byte(b.String()), data[m[1]:])
	return os.WriteFile(p, out, 0o644)
}

// Slug 把 name 转成 npm 包名可用的形式：小写，字母和数字之外的字符换成连字符；为空时是 app。
func Slug(name string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(name) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case b.Len() > 0 && !strings.HasSuffix(b.String(), "-"):
			b.WriteByte('-')
		}
	}
	if s := strings.TrimSuffix(b.String(), "-"); s != "" {
		return s
	}
	return "app"
}

// render 把 files 中 root 下的模板渲染到 dir，已有的文件被覆盖。
func render(root, dir string, data any) error {
	err := fs.WalkDir(files, root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		src, err := files.ReadFile(p)
		if err != nil {
			return err
		}
		tmpl, err := template.New(p).Delims("[[", "]]").Option("missingkey=error").Parse(string(src))
		if err != nil {
			return err
		}
		var out bytes.Buffer
		if err := tmpl.Execute(&out, data); err != nil {
			return err
		}
		rel := strings.TrimSuffix(strings.TrimPrefix(p, root+"/"), ".tmpl")
		if path.Base(rel) == "gitignore" {
			rel = path.Join(path.Dir(rel), ".gitignore")
		}
		return writeFile(filepath.Join(dir, filepath.FromSlash(rel)), out.Bytes())
	})
	if err != nil {
		return fmt.Errorf("scaffold: %w", err)
	}
	return nil
}

func writeFile(p string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	return os.WriteFile(p, data, 0o644)
}
