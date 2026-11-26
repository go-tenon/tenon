// Package scaffold 生成新项目、新前端、新客户端和多语言骨架：template/project 是项目，template/frontend 是 apps 中的一个前端
// （TanStack Start 和 @go-tenon/ui）。前端按几个互相独立的维度组合：template/app 和 template/site 是按布局加上的页面，
// template/login 是登录页和登录接口，template/api 是调用 API 的客户端（API 的客户端已经生成时加上）。
// 单语言项目（默认，只有一种语言）界面文字直接写在页面代码中；配置多种语言时生成 template/i18n 与 locales/<前端>.<语言>.yaml，
// tenon generate 把它们生成为前端的 src/messages.gen.ts。
// template/client/<种类> 是 clients 中的一个客户端，网页宿主（electron）另外在它显示的前端中加上 template/consent 的授权页。
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

// BunVersion 是生成的 package.json 中 packageManager 的 Bun 版本，与框架的 CI 相同；项目的 CI（setup-bun）读取它。
const BunVersion = "1.4.4"

// defaultLanguage 是未指定语言时的默认源语言。
const defaultLanguage = "zh-CN"

// Options 是项目模板的参数。
type Options struct {
	Module       string   // Go 模块路径
	Name         string   // 应用的名称（页面标题、命令的说明）
	Slug         string   // npm 包名的作用域，例如 todo 时包名是 @todo/web、@todo/client；默认由 Name 得出。
	Languages    []string // 支持的语言，第一项是源语言；为空时默认 ["zh-CN"]
	TenonVersion string   // go.mod 中要求的框架版本。
	TenonDir     string   // 本地的框架目录，设置时 go.mod 用 replace 指向它（开发框架本身时使用）
	GoVersion    string
	BunVersion   string // package.json 中 packageManager 的 Bun 版本，为空时是 BunVersion
}

// SourceLanguage 返回项目的源语言（第一项）；为空时返回默认语言 zh-CN。
func (o Options) SourceLanguage() string {
	if len(o.Languages) == 0 {
		return defaultLanguage
	}
	return o.Languages[0]
}

// AllLanguages 返回项目支持的全部语言；为空时返回 ["zh-CN"]。
func (o Options) AllLanguages() []string {
	if len(o.Languages) == 0 {
		return []string{defaultLanguage}
	}
	return slices.Clone(o.Languages)
}

// MultiLanguage 报告项目是否启用多语言（两种及以上语言）。
func (o Options) MultiLanguage() bool { return len(o.Languages) > 1 }

// Frontend 是前端模板的参数。
type Frontend struct {
	Scope     string    // npm 包名的作用域，与项目根目录 package.json 的 name 相同。
	Name      string    // 前端的名称，即 apps 中的目录名。
	Base      string    // 前端挂载的路径，以 / 开头和结尾，写入 package.json 的 tenon.base。
	Title     string    // 页面标题。
	Layout    string    // 布局，见 Layouts；为空时是 rail。
	Login     string    // 登录页的样式，见 Logins；为空时按布局：frame、rail 是 centered，site 是 none。
	Preset    string    // 样式预设（@go-tenon/ui 的 Preset），见 Presets；为空时按布局：frame 是 compact，rail 是 comfortable，site 是 spacious。
	Render    string    // 渲染方式，见 Renders；为空时按布局：site 是 ssr，frame、rail 是 csr。
	API       string    // 登录页调用的 API 的路径，例如 /admin/api，以 / 开头、不以 / 结尾；为空时是 /api。
	Client    string    // API 生成的 TypeScript 客户端的目录名（packages/client/src/<名称>）；为空时客户端还没有生成，不写 src/api.ts。
	Auth      AuthCalls // 生成的客户端中登录相关接口的方法，见 FindClient。
	Languages []string  // 项目支持的语言，第一项是源语言；为空时默认 ["zh-CN"]。
}

// SourceLanguage 返回前端使用的源语言。
func (f Frontend) SourceLanguage() string {
	if len(f.Languages) == 0 {
		return defaultLanguage
	}
	return f.Languages[0]
}

// AllLanguages 返回项目支持的全部语言。
func (f Frontend) AllLanguages() []string {
	if len(f.Languages) == 0 {
		return []string{defaultLanguage}
	}
	return slices.Clone(f.Languages)
}

// MultiLanguage 报告前端是否启用多语言（生成 src/i18n.ts 与 locales/<前端>.<语言>.yaml）。
func (f Frontend) MultiLanguage() bool { return len(f.Languages) > 1 }

// AuthCalls 是生成的客户端中登录相关接口的方法，写成“分组.方法”，例如 auth.login；客户端中没有这个接口时为空。
type AuthCalls struct {
	Login  string // POST /auth/login
	Logout string // POST /auth/logout
	Me     string // GET /me
}

// Client 是客户端模板的参数。
type Client struct {
	Scope     string   // npm 包名的作用域，与项目根目录 package.json 的 name 相同。
	Name      string   // 客户端的名称，即 clients 中的目录名。
	Kind      string   // 种类，见 project.Kinds。
	Frontend  string   // 网页宿主显示的前端的名称。
	Base      string   // 这个前端挂载的路径。
	SSR       bool     // 这个前端是否在服务端渲染：授权页在服务端渲染的前端中只在浏览器中渲染。
	Title     string   // 应用的名称：窗口标题、安装包的名称。
	Languages []string // 项目支持的语言；多于一种时授权页使用 useT 并向翻译文件追加文字。
}

// AllLanguages 返回项目支持的全部语言。
func (c Client) AllLanguages() []string {
	if len(c.Languages) == 0 {
		return []string{defaultLanguage}
	}
	return slices.Clone(c.Languages)
}

// MultiLanguage 报告授权页是否按多语言方式生成。
func (c Client) MultiLanguage() bool { return len(c.Languages) > 1 }

// Deploy 是原生二进制多实例部署骨架模板的参数（deploy/app.yaml、deploy/example 与初始目标）。
type Deploy struct {
	Slug     string // 构建产物名，例如 blog。
	Target   string // 初始目标目录名（位于 deploy/targets/<Target>），为空时默认 prod。
	Instance string // 初始目标的远端实例名，为空时与 Slug 相同。
	User     string // 默认 SSH 登录用户，为空时默认 deploy。
	Host     string // 初始目标主机地址或 ~/.ssh/config 别名，为空时写占位地址。
	HasSSR   bool   // 项目是否包含服务端渲染前端，写入 deploy/app.yaml。
	Force    bool   // 为真时覆盖已存在的部署文件；为假时已有文件存在则报错。
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

// GeneratedAuth 报告登录、退出和读取当前用户是否调用生成的客户端：前端有登录页，API 的客户端已经生成，
// 并且其中有这三个接口。否则 src/auth.ts 按约定的路径自己发请求。
func (f Frontend) GeneratedAuth() bool {
	return f.HasLogin() && f.Client != "" && f.Auth.Login != "" && f.Auth.Logout != "" && f.Auth.Me != ""
}

// clientLine 匹配生成的客户端 api.ts 中的三种行：分组的开头（4 个空格缩进）、注释中的“方法 路径”、方法的定义（6 个空格缩进）。
var clientLine = regexp.MustCompile(`^(?:    (\w+): \{$|\s+\* (GET|POST|PUT|PATCH|DELETE) (\S+)$|      (\w+): )`)

// FindClient 读取项目 root 中名为 name 的 API 生成的 TypeScript 客户端（packages/client/src/<name>/api.ts），
// 返回其中登录相关接口的方法。客户端还没有生成时 ok 为 false。
func FindClient(root, name string) (calls AuthCalls, ok bool, err error) {
	data, err := os.ReadFile(filepath.Join(root, "packages", "client", "src", name, "api.ts"))
	if errors.Is(err, fs.ErrNotExist) {
		return calls, false, nil
	} else if err != nil {
		return calls, false, err
	}
	targets := map[string]*string{"POST /auth/login": &calls.Login, "POST /auth/logout": &calls.Logout, "GET /me": &calls.Me}
	var group, route string
	for line := range strings.Lines(string(data)) {
		m := clientLine.FindStringSubmatch(strings.TrimRight(line, "\n"))
		switch {
		case m == nil:
		case m[1] != "":
			group, route = m[1], ""
		case m[2] != "":
			route = m[2] + " " + m[3]
		case route != "":
			if p := targets[route]; p != nil && *p == "" {
				*p = group + "." + m[4]
			}
			route = ""
		}
	}
	return calls, true, nil
}

// Write 生成项目：一个名为 web 的前端挂在 /（site 布局），带待办事项的示例。dir 必须是空目录或尚未创建。
func Write(dir string, opts Options) error {
	if opts.GoVersion == "" {
		opts.GoVersion = GoVersion
	}
	if opts.BunVersion == "" {
		opts.BunVersion = BunVersion
	}
	if opts.Slug == "" {
		opts.Slug = Slug(opts.Name)
	}
	if entries, err := os.ReadDir(dir); err == nil && len(entries) > 0 {
		return fmt.Errorf("%s is not empty", dir)
	} else if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	// 先写前端，项目模板中的示例页面再覆盖前端的首页；多语言项目另外写入示例翻译文件与 locales/embed.go。
	web := Frontend{Scope: opts.Slug, Name: "web", Base: "/", Title: opts.Name, Layout: "site", Languages: opts.Languages}
	if err := writeFrontend(dir, web); err != nil {
		return err
	}
	if err := render("template/project", dir, opts); err != nil {
		return err
	}
	if opts.MultiLanguage() {
		return writeProjectLocales(dir, opts)
	}
	return nil
}

// WriteFrontend 在项目 root 的 apps/<名称> 中生成一个前端；多语言项目同时写入 locales/<名称>.<语言>.yaml。这个目录必须尚未创建。
func WriteFrontend(root string, f Frontend) error {
	dir := filepath.Join(root, "apps", f.Name)
	if _, err := os.Stat(dir); err == nil {
		return fmt.Errorf("%s already exists", filepath.ToSlash(filepath.Join("apps", f.Name)))
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return writeFrontend(root, f)
}

// WriteClient 在项目 root 的 clients/<名称> 中生成客户端 c，这个目录必须尚未创建。网页宿主在它显示的前端中加上授权页
// src/routes/oauth/consent.tsx（用户在浏览器中登录时确认的页面），这个文件已经存在时不覆盖；consent 报告是否加上了。
// 授权页用前端的登录（src/auth.ts），前端没有登录时返回错误。多语言项目将授权页的文字追加到 locales/<前端>.<语言>.yaml 的末尾。
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
	if err := render("template/consent", fdir, c); err != nil {
		return false, err
	}
	if c.MultiLanguage() {
		return true, appendConsentMessages(root, c)
	}
	return true, nil
}

// WriteDeploy 在项目 root 中生成多实例原生二进制部署骨架：deploy/app.yaml、deploy/example/ 与初始目标 deploy/targets/<Target>/，
// 并确保项目 .gitignore 包含 deploy/targets/。Force 为假且任一目标文件已存在时返回错误；返回写入的文件相对路径列表。
func WriteDeploy(root string, d Deploy) ([]string, error) {
	if d.Slug == "" {
		return nil, fmt.Errorf("deploy: slug is required")
	}
	if d.Target == "" {
		d.Target = "prod"
	}
	if d.Instance == "" {
		d.Instance = d.Slug
	}
	if d.User == "" {
		d.User = "deploy"
	}
	if d.Host == "" {
		d.Host = "203.0.113.10"
	}
	targets := []string{
		"deploy/app.yaml",
		"deploy/example/target.yaml",
		"deploy/example/config.yaml",
		path.Join("deploy/targets", d.Target, "target.yaml"),
		path.Join("deploy/targets", d.Target, "config.yaml"),
	}
	if !d.Force {
		var existing []string
		for _, f := range targets {
			if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(f))); err == nil {
				existing = append(existing, f)
			} else if !errors.Is(err, fs.ErrNotExist) {
				return nil, err
			}
		}
		if len(existing) > 0 {
			return nil, fmt.Errorf("%s already exists (pass --force to overwrite)", strings.Join(existing, ", "))
		}
	}
	if err := render("template/deploy", root, d); err != nil {
		return nil, err
	}
	if err := ensureGitignoreEntry(root, "deploy/targets/"); err != nil {
		return nil, err
	}
	return targets, nil
}

// ensureGitignoreEntry 若项目 root 下存在 .gitignore 且尚未包含 entry，则将其追加到末尾。
func ensureGitignoreEntry(root, entry string) error {
	p := filepath.Join(root, ".gitignore")
	data, err := os.ReadFile(p)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	for line := range strings.Lines(string(data)) {
		if strings.TrimSpace(line) == entry {
			return nil
		}
	}
	var b strings.Builder
	b.Write(data)
	if len(data) > 0 && data[len(data)-1] != '\n' {
		b.WriteByte('\n')
	}
	b.WriteString(entry)
	b.WriteByte('\n')
	return os.WriteFile(p, []byte(b.String()), 0o644)
}

// AddLanguage 为已有项目启用或追加语言 lang（langs 为更新后的完整语言列表，第一项是源语言；frontends 为 apps 中的前端名称）：
// 确保 locales/embed.go、locales/<lang>.yaml、每个前端的翻译文件与 src/i18n.ts 存在，已有的文件不覆盖，返回新创建的文件路径。
func AddLanguage(root, lang string, langs, frontends []string) ([]string, error) {
	source := defaultLanguage
	if len(langs) > 0 {
		source = langs[0]
	}
	var created []string
	writeIfMissing := func(rel string, data []byte) error {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if _, err := os.Stat(p); err == nil {
			return nil
		} else if !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		if err := writeFile(p, data); err != nil {
			return err
		}
		created = append(created, rel)
		return nil
	}
	if err := writeIfMissing("locales/embed.go", []byte(localesEmbedGo)); err != nil {
		return nil, err
	}
	serverHeader := fmt.Sprintf("# 服务端 %s 翻译文件：为 errorx.Define、enum、Resource、Settings 补充该语言的译文。\n", lang)
	if err := writeIfMissing("locales/"+lang+".yaml", []byte(serverHeader)); err != nil {
		return nil, err
	}
	for _, name := range frontends {
		// 从单语言升级到多语言时，先补上源语言的前端翻译文件，再创建新增语言的文件。
		for _, l := range []string{source, lang} {
			rel := fmt.Sprintf("locales/%s.%s.yaml", name, l)
			content := fmt.Sprintf("# 前端 %s 的界面文字（%s）。tenon generate 把它生成为 apps/%s/src/messages.gen.ts。\napp.%s: %q\n", name, l, name, name, name)
			if err := writeIfMissing(rel, []byte(content)); err != nil {
				return nil, err
			}
		}
		i18nRel := fmt.Sprintf("apps/%s/src/i18n.ts", name)
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(i18nRel))); errors.Is(err, fs.ErrNotExist) {
			if err := render("template/i18n", filepath.Join(root, "apps", name), Frontend{Name: name, Languages: langs}); err != nil {
				return nil, err
			}
			created = append(created, i18nRel)
		} else if err != nil {
			return nil, err
		}
	}
	return created, nil
}

// localesEmbedGo 是 locales/embed.go 的内容。
const localesEmbedGo = `// Package locales 嵌入应用的翻译文件：业务错误码、枚举、资源、设置与界面文字。
package locales

import "embed"

//go:embed *.yaml
var FS embed.FS
`

// isChinese 报告语言标签是否是中文。
func isChinese(lang string) bool {
	return strings.HasPrefix(strings.ToLower(lang), "zh")
}

// writeProjectLocales 为多语言新项目写入 locales/embed.go、附加语言的服务端翻译文件和各语言的 web 示例翻译文件。
func writeProjectLocales(root string, opts Options) error {
	if err := writeFile(filepath.Join(root, "locales", "embed.go"), []byte(localesEmbedGo)); err != nil {
		return err
	}
	for _, lang := range opts.AllLanguages()[1:] {
		msg := "Todo not found"
		if isChinese(lang) {
			msg = "待办事项不存在"
		}
		content := fmt.Sprintf("# 服务端 %s 翻译文件：源语言默认提示已写在 Go 定义中，这里补充 %s 译文。\ntodo.not_found: %s\n", lang, lang, msg)
		if err := writeFile(filepath.Join(root, "locales", lang+".yaml"), []byte(content)); err != nil {
			return err
		}
	}
	for _, lang := range opts.AllLanguages() {
		var content string
		if isChinese(lang) {
			content = fmt.Sprintf(
				"# 前端 web 的界面文字（%s）。tenon generate 把它生成为 apps/web/src/messages.gen.ts，页面用 useT 取出。\n"+
					"app.web: %q\ntodo.title: 待办\ntodo.placeholder: 要做什么？\ntodo.add: 添加\ntodo.delete: 删除\n",
				lang, opts.Name,
			)
		} else {
			content = fmt.Sprintf(
				"# Frontend web UI messages (%s). tenon generate outputs apps/web/src/messages.gen.ts.\n"+
					"app.web: %q\ntodo.title: Todos\ntodo.placeholder: What needs to be done?\ntodo.add: Add\ntodo.delete: Delete\n",
				lang, opts.Name,
			)
		}
		if err := writeFile(filepath.Join(root, "locales", "web."+lang+".yaml"), []byte(content)); err != nil {
			return err
		}
	}
	return nil
}

// writeFrontendLocales 为多语言前端 f 写入各语言的初始翻译文件 locales/<名称>.<语言>.yaml。
func writeFrontendLocales(root string, f Frontend) error {
	for _, lang := range f.AllLanguages() {
		var b strings.Builder
		if isChinese(lang) {
			fmt.Fprintf(&b, "# 前端 %s 的界面文字（%s）。tenon generate 把它生成为 apps/%s/src/messages.gen.ts，页面用 useT 取出。\n", f.Name, lang, f.Name)
			fmt.Fprintf(&b, "app.%s: %q\n", f.Name, f.Title)
			if f.Layout == "site" {
				if f.HasLogin() {
					b.WriteString("home.login: 登录\n")
				} else {
					b.WriteString("home.get_started: 开始使用\n")
				}
				b.WriteString("home.placeholder: 这里是首页的占位，换成自己的落地页。\n")
			} else {
				b.WriteString("home.title: 首页\nhome.start: 从这里开始。\n")
			}
			if f.HasLogin() {
				b.WriteString("signin.title: 登录\n")
			}
		} else {
			fmt.Fprintf(&b, "# Frontend %s UI messages (%s). tenon generate outputs apps/%s/src/messages.gen.ts.\n", f.Name, lang, f.Name)
			fmt.Fprintf(&b, "app.%s: %q\n", f.Name, f.Title)
			if f.Layout == "site" {
				if f.HasLogin() {
					b.WriteString("home.login: Sign in\n")
				} else {
					b.WriteString("home.get_started: Get started\n")
				}
				b.WriteString("home.placeholder: This is a placeholder home page. Replace it with your own landing page.\n")
			} else {
				b.WriteString("home.title: Home\nhome.start: Start here.\n")
			}
			if f.HasLogin() {
				b.WriteString("signin.title: Sign in\n")
			}
		}
		if err := writeFile(filepath.Join(root, "locales", f.Name+"."+lang+".yaml"), []byte(b.String())); err != nil {
			return err
		}
	}
	return nil
}

// appendConsentMessages 把授权页的文字追加到多语言前端各语言的 locales/<frontend>.<lang>.yaml 末尾。
func appendConsentMessages(root string, c Client) error {
	const zhConsent = `
# 授权页（src/routes/oauth/consent.tsx）
consent.title: 授权
consent.sign_in: "在{{.client}}上登录"
consent.failed: "无法授权（{{.reason}}）。"
consent.invalid: 请求无效或已过期
consent.return: "确认后回到{{.client}}。"
consent.continue: 继续
consent.requests: "{{.client}} 请求访问你的账号："
consent.redirect: "授权后将跳转到 {{.host}}"
consent.deny: 拒绝
consent.allow: 允许
`
	const enConsent = `
# OAuth consent page (src/routes/oauth/consent.tsx)
consent.title: Authorize
consent.sign_in: "Sign in to {{.client}}"
consent.failed: "Unable to authorize ({{.reason}})."
consent.invalid: Request is invalid or expired
consent.return: "You will be returned to {{.client}} after confirming."
consent.continue: Continue
consent.requests: "{{.client}} requests access to your account:"
consent.redirect: "After authorizing, you will be redirected to {{.host}}"
consent.deny: Deny
consent.allow: Allow
`
	for _, lang := range c.AllLanguages() {
		p := filepath.Join(root, "locales", c.Frontend+"."+lang+".yaml")
		old, err := os.ReadFile(p)
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		if len(old) > 0 && !bytes.HasSuffix(old, []byte("\n")) {
			old = append(old, '\n')
		}
		chunk := enConsent
		if isChinese(lang) {
			chunk = zhConsent
		}
		if err := writeFile(p, append(old, chunk...)); err != nil {
			return err
		}
	}
	return nil
}

// Resolve 返回补上默认值的 f：没有写的维度按布局取默认值，见 defaults。某个维度的值不在可选的范围内时返回错误。
func (f Frontend) Resolve() (Frontend, error) {
	if f.API == "" {
		f.API = "/api"
	}
	if !strings.HasPrefix(f.API, "/") || strings.HasSuffix(f.API, "/") {
		return f, fmt.Errorf("api %q must start with / and not end with /", f.API)
	}
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

// writeFrontend 在项目 root 的 apps/<名称> 中渲染共用的前端模板，再按布局加上 site 或 app（frame、rail）的页面，
// 有登录页时加上 login；多语言项目同时写入 src/i18n.ts 和 locales/<名称>.<语言>.yaml。
func writeFrontend(root string, f Frontend) error {
	f, err := f.Resolve()
	if err != nil {
		return err
	}
	dir := filepath.Join(root, "apps", f.Name)
	if err := render("template/frontend", dir, f); err != nil {
		return err
	}
	if f.MultiLanguage() {
		if err := writeFrontendLocales(root, f); err != nil {
			return err
		}
		if err := render("template/i18n", dir, f); err != nil {
			return err
		}
	}
	pages := "template/app"
	if f.Layout == "site" {
		pages = "template/site"
	}
	if err := render(pages, dir, f); err != nil {
		return err
	}
	if f.Client != "" {
		if err := render("template/api", dir, f); err != nil {
			return err
		}
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

// expand 按 data 渲染名为 name 的模板 text，分隔符是 [[ ]]。
func expand(name, text string, data any) ([]byte, error) {
	tmpl, err := template.New(name).Delims("[[", "]]").Option("missingkey=error").Parse(text)
	if err != nil {
		return nil, err
	}
	var out bytes.Buffer
	err = tmpl.Execute(&out, data)
	return out.Bytes(), err
}

// render 把 files 中 root 下的模板渲染到 dir（.tmpl 以外的文件原样复制），已有的文件被覆盖。
func render(root, dir string, data any) error {
	err := fs.WalkDir(files, root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		content, err := files.ReadFile(p)
		if err != nil {
			return err
		}
		if strings.HasSuffix(p, ".tmpl") {
			if content, err = expand(p, string(content), data); err != nil {
				return err
			}
		}
		// 路径中也可以有模板标记，例如 cmd/[[.Slug]]/main.go 写成 cmd/todo/main.go。
		name, err := expand(p, strings.TrimSuffix(strings.TrimPrefix(p, root+"/"), ".tmpl"), data)
		if err != nil {
			return err
		}
		rel := string(name)
		switch path.Base(rel) {
		case "gitignore":
			rel = path.Join(path.Dir(rel), ".gitignore")
		case "gitattributes":
			rel = path.Join(path.Dir(rel), ".gitattributes")
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
