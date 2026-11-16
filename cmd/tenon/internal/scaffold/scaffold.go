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
}

// Templates 是 tenon add frontend 的 --template 可选的模板：
//   - default：React、TanStack Router 和 @go-tenon/shell 的最小前端；
//   - admin：运营后台，antd 和 ProComponents，带登录页、侧栏布局和多标签（@go-tenon/admin）。
var Templates = []string{"default", "admin"}

// AdminPackage 是 admin 模板依赖的框架前端包在项目中的位置，加入 Bun workspace。
// 默认不加入：Bun 会安装包的 peer 依赖，不用 antd 的项目不应该因此装上 antd。
const AdminPackage = ".tenon/js/admin"

// AntdMCP 是 admin 模板写入项目 .mcp.json 的 antd MCP 服务：@ant-design/cli 的版本与模板中的 antd 相同。
var AntdMCP = map[string]any{"command": "bunx", "args": []any{"@ant-design/cli@6.6.8", "mcp"}}

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
	switch f.Template {
	case "", "default":
		return render("template/frontend", dir, f)
	case "admin":
		return render("template/admin", dir, f)
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
