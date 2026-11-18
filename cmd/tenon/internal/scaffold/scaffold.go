// Package scaffold 生成新项目、新前端和新客户端：template/project 是项目，template/frontend 是 apps 中的一个前端
// （TanStack Start 和 @go-tenon/ui）。前端按几个互相独立的维度组合：template/app 和 template/site 是按布局加上的页面，
// template/login 是登录页和登录接口。template/client/<种类> 是 clients 中的一个客户端，网页宿主（electron）
// 另外在它显示的前端中加上 template/consent 的授权页。
// 每个 .tmpl 文件按参数渲染后写入目标目录，其他文件（例如图标）原样复制；所有文件路径中的模板标记都展开
// （cmd/[[.Slug]] 写成 cmd/todo）。模板的分隔符是 [[ ]]，与 JSX 的 {{ }} 区分开；gitignore.tmpl 写成 .gitignore。
package scaffold

import (
	"bytes"
	"embed"
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
	Module       string // Go 模块路径
	Name         string // 应用的名称（页面标题、命令的说明）
	Slug         string // npm 包名的作用域，例如 todo 时包名是 @todo/web、@todo/client；默认由 Name 得出。
	TenonVersion string // go.mod 中要求的框架版本。
	TenonDir     string // 本地的框架目录，设置时 go.mod 用 replace 指向它（开发框架本身时使用）
	GoVersion    string
}

// Frontend 是前端模板的参数。
type Frontend struct {
	Scope  string // npm 包名的作用域，与项目根目录 package.json 的 name 相同。
	Name   string // 前端的名称，即 apps 中的目录名。
	Base   string // 前端挂载的路径，以 / 开头和结尾，写入 package.json 的 tenon.base。
	Title  string // 页面标题。
	Layout string // 布局，见 Layouts；为空时是 rail。
	Login  string // 登录页的样式，见 Logins；为空时按布局：frame、rail 是 centered，site 是 none。
	Preset string // 样式预设（@go-tenon/ui 的 Preset），见 Presets；为空时按布局：frame 是 compact，rail 是 comfortable，site 是 spacious。
	Render string // 渲染方式，见 Renders；为空时按布局：site 是 ssr，frame、rail 是 csr。
}

// Client 是客户端模板的参数。
type Client struct {
	Scope    string // npm 包名的作用域，与项目根目录 package.json 的 name 相同。
	Name     string // 客户端的名称，即 clients 中的目录名。
	Kind     string // 种类，见 project.Kinds。
	Frontend string // 网页宿主显示的前端的名称。
	Base     string // 这个前端挂载的路径。
	SSR      bool   // 这个前端是否在服务端渲染：授权页在服务端渲染的前端中只在浏览器中渲染。
	Title    string // 应用的名称：窗口标题、安装包的名称。
}

// Layouts 是前端可选的布局：
//   - rail：双栏导航（RailLayout），首页在需要登录的布局路由 _app 之下；
//   - frame：顶栏加侧栏菜单、带多标签（FrameLayout），首页同样在 _app 之下；
//   - site：官网和内容站（SiteLayout），首页是公开的落地页占位。
var Layouts = []string{"rail", "frame", "site"}

// Logins 是登录页可选的样式：centered 是居中的卡片（CenteredAuthLayout），split 是左右分栏（SplitAuthLayout），
// none 不生成登录页和登录接口，页面都是公开的。
var Logins = []string{"centered", "split", "none"}

// Presets 是可选的样式预设，按密度区分：compact 紧凑，comfortable 舒展，spacious 宽松。
var Presets = []string{"compact", "comfortable", "spacious"}

// Renders 是可选的渲染方式：csr 只在浏览器中渲染，程序中不需要 Bun；ssr 在服务端渲染，搜索引擎可以收录，首页构建时预渲染。
var Renders = []string{"csr", "ssr"}

// defaults 是每种布局在各个维度上的默认值：后台的布局有登录页、只在浏览器中渲染，官网没有登录页、在服务端渲染。
var defaults = map[string]Frontend{
	"rail":  {Login: "centered", Preset: "comfortable", Render: "csr"},
	"frame": {Login: "centered", Preset: "compact", Render: "csr"},
	"site":  {Login: "none", Preset: "spacious", Render: "ssr"},
}

// SSR 报告前端是否在服务端渲染，写入 package.json 的 tenon.ssr。
func (f Frontend) SSR() bool { return f.Render == "ssr" }

// HasLogin 报告前端是否有登录页和登录接口。
func (f Frontend) HasLogin() bool { return f.Login != "none" }

// Write 生成项目：一个名为 web 的前端挂在 /（site 布局），带待办事项的示例。dir 必须是空目录或尚未创建。
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
	// 先写前端，项目模板中的示例页面再覆盖它的首页。
	web := Frontend{Scope: opts.Slug, Name: "web", Base: "/", Title: opts.Name, Layout: "site"}
	if err := writeFrontend(filepath.Join(dir, "apps", web.Name), web); err != nil {
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
	return writeFrontend(dir, f)
}

// WriteClient 在项目 root 的 clients/<名称> 中生成客户端 c，这个目录必须尚未创建。网页宿主在它显示的前端中加上授权页
// src/routes/oauth/consent.tsx（用户在浏览器中登录时确认的页面），这个文件已经存在时不覆盖；consent 报告是否加上了。
// 授权页用前端的登录（src/auth.ts），前端没有登录时返回错误。
func WriteClient(root string, c Client) (consent bool, err error) {
	dir := filepath.Join(root, "clients", c.Name)
	if _, err := os.Stat(dir); err == nil {
		return false, fmt.Errorf("%s already exists", filepath.ToSlash(filepath.Join("clients", c.Name)))
	} else if !errors.Is(err, fs.ErrNotExist) {
		return false, err
	}
	fdir := filepath.Join(root, "apps", c.Frontend)
	if _, err := os.Stat(filepath.Join(fdir, "src", "auth.ts")); err != nil {
		return false, fmt.Errorf("apps/%s has no login (src/auth.ts): the %s client signs users in through it", c.Frontend, c.Kind)
	}
	if err := render("template/client/"+c.Kind, dir, c); err != nil {
		return false, err
	}
	page := filepath.Join(fdir, "src", "routes", "oauth", "consent.tsx")
	if _, err := os.Stat(page); err == nil {
		return false, nil
	}
	return true, render("template/consent", fdir, c)
}

// Resolve 返回补上默认值的 f：没有写的维度按布局取默认值，见 defaults。某个维度的值不在可选的范围内时返回错误。
func (f Frontend) Resolve() (Frontend, error) {
	if f.Layout == "" {
		f.Layout = Layouts[0]
	}
	if !slices.Contains(Layouts, f.Layout) {
		return f, fmt.Errorf("layout %q is not one of %v", f.Layout, Layouts)
	}
	def := defaults[f.Layout]
	for _, d := range []struct {
		name   string
		value  *string
		def    string
		values []string
	}{
		{"login", &f.Login, def.Login, Logins},
		{"preset", &f.Preset, def.Preset, Presets},
		{"render", &f.Render, def.Render, Renders},
	} {
		if *d.value == "" {
			*d.value = d.def
		}
		if !slices.Contains(d.values, *d.value) {
			return f, fmt.Errorf("%s %q is not one of %v", d.name, *d.value, d.values)
		}
	}
	return f, nil
}

// writeFrontend 渲染共用的前端模板，再按布局加上 site 或 app（frame、rail）的页面，有登录页时加上 login。
func writeFrontend(dir string, f Frontend) error {
	f, err := f.Resolve()
	if err != nil {
		return err
	}
	if err := render("template/frontend", dir, f); err != nil {
		return err
	}
	pages := "template/app"
	if f.Layout == "site" {
		pages = "template/site"
	}
	if err := render(pages, dir, f); err != nil {
		return err
	}
	if f.HasLogin() {
		return render("template/login", dir, f)
	}
	return nil
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

// render 把 files 中 root 下的模板渲染到 dir（.tmpl 以外的文件原样复制），已有的文件被覆盖。
func render(root, dir string, data any) error {
	expand := func(name, text string) ([]byte, error) {
		tmpl, err := template.New(name).Delims("[[", "]]").Option("missingkey=error").Parse(text)
		if err != nil {
			return nil, err
		}
		var out bytes.Buffer
		err = tmpl.Execute(&out, data)
		return out.Bytes(), err
	}
	err := fs.WalkDir(files, root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		content, err := files.ReadFile(p)
		if err != nil {
			return err
		}
		if strings.HasSuffix(p, ".tmpl") {
			if content, err = expand(p, string(content)); err != nil {
				return err
			}
		}
		// 路径中也可以有模板标记，例如 cmd/[[.Slug]]/main.go 写成 cmd/todo/main.go。
		name, err := expand(p, strings.TrimSuffix(strings.TrimPrefix(p, root+"/"), ".tmpl"))
		if err != nil {
			return err
		}
		rel := string(name)
		if path.Base(rel) == "gitignore" {
			rel = path.Join(path.Dir(rel), ".gitignore")
		}
		return writeFile(filepath.Join(dir, filepath.FromSlash(rel)), content)
	})
	if err != nil {
		return fmt.Errorf("scaffold: %w", err)
	}
	return nil
}

// writeFile 写入文件 p，先创建它所在的目录。
func writeFile(p string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	return os.WriteFile(p, data, 0o644)
}
