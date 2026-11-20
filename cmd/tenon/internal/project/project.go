// Package project 读取项目根目录的 tenon.json，补上约定的默认值，并发现项目中的前端和客户端。
package project

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/go-tenon/tenon/config"
)

// File 是项目配置文件的名字。
const File = "tenon.json"

// AppsDir 是前端所在的目录：其中每个带 vite.config 的子目录是一个前端，子目录名是前端的名称。
const AppsDir = "apps"

// ClientsDir 是网页以外的客户端（桌面端、App）所在的目录：其中每个带 ClientFile 的子目录是一个客户端，
// 子目录名是客户端的名称，也是它的 OAuth client ID 和 X-App-Version 中的平台名。
const ClientsDir = "clients"

// ClientFile 是客户端目录中声明种类的文件，各种工具链的客户端都用它，tenon 的命令按它识别客户端。
const ClientFile = "tenon.json"

// Kinds 是 tenon 支持的客户端种类。
var Kinds = []string{"electron"}

// ClientDir 是共用的客户端包；有它的 package.json 时，每个 API 的 TypeScript 客户端生成到 ClientSrc/<名称>。
const (
	ClientDir = "packages/client"
	ClientSrc = ClientDir + "/src"
)

// viteConfigs 是 Vite 配置文件的名字，有其中之一的目录是一个前端。
var viteConfigs = []string{"vite.config.ts", "vite.config.mts", "vite.config.js", "vite.config.mjs"}

// Name 是前端和 API 客户端的名称：小写字母开头，由小写字母、数字和连字符组成。
var Name = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)

// Config 是 tenon.json。所有字段都可以省略，Load 按约定补上默认值。
type Config struct {
	Schema string          `json:"$schema,omitempty"` // 编辑器补全用的 JSON Schema 地址，只供编辑器使用。
	Main   string          `json:"main,omitempty"`    // 应用入口的包，默认是 cmd 下唯一一个有 main.go 的目录，例如 ./cmd/blog。
	Serve  []string        `json:"serve,omitempty"`   // 开发时运行应用的参数，默认 serve --config config/app.yaml,config/local.yaml（local.yaml 存在时才加上它）。
	URL    string          `json:"url,omitempty"`     // 应用的地址，dev 打印它，前端的开发服务器经它调用接口。默认按应用配置中的 http.addr 推出，见 appURL。
	APIs   map[string]*API `json:"apis,omitempty"`    // 按 BasePath 覆盖 API 客户端的约定，例如改名或生成 Go 客户端。没有写的 API 按约定生成。
	Tools  []Tool          `json:"tools,omitempty"`   // 与应用一起运行的其他工具，可空。
	Agents *Agents         `json:"agents,omitempty"`  // 接入哪些 AI 编程助手，见 tenon agents；省略时接入 DefaultTargets。

	Frontends []Frontend `json:"-"` // apps 中的前端，按名称排序。由 Load 从目录中发现，不在 tenon.json 中。
	Clients   []Client   `json:"-"` // clients 中的客户端，按名称排序。由 Load 从目录中发现。
	Client    bool       `json:"-"` // 报告项目中是否有共用的客户端包 packages/client。由 Load 发现，不在 tenon.json 中。
}

// API 覆盖一个 API 客户端的约定。
type API struct {
	Name string `json:"name,omitempty"` // TypeScript 客户端的目录名，默认是 BasePath 中从后往前第一个不是 api、也不是路径参数的段，例如 /api/admin 和 /admin/api 都是 admin。
	Go   string `json:"go,omitempty"`   // Go 客户端的目录，例如 ../api，设置后才生成。
}

// Frontend 是一个 Vite 项目。Base 和 SSR 来自它 package.json 的 tenon 字段，@go-tenon/start 的 Vite 插件读取同一个字段。
type Frontend struct {
	Name string // 前端的名称，即 apps 中的目录名。应用按它取开发服务器的地址。
	Dir  string // 前端的目录，例如 apps/web。
	Base string // 挂载的路径，以 / 开头和结尾，例如 /admin/。
	SSR  bool   // 是否在服务端渲染；有 SSR 前端时 tenon build 才构建 Bun 可执行文件。
}

// Client 是 clients 中的一个客户端，来自它的 ClientFile。
type Client struct {
	Name     string `json:"-"`                  // 客户端的名称，即 clients 中的目录名。
	Dir      string `json:"-"`                  // 客户端的目录，例如 clients/admin-desktop。
	Kind     string `json:"kind"`               // 种类，见 Kinds。
	Frontend string `json:"frontend,omitempty"` // 网页宿主（electron）显示的前端，即 apps 中的目录名。
}

// Tool 是一个与应用一起运行的工具：开发时运行 Watch，停止后运行一次 Build。
type Tool struct {
	Watch []string `json:"watch"`           // 开发时与应用一起运行的命令，例如 ["esbuild", "--watch"]
	Build []string `json:"build,omitempty"` // tenon dev 停止后运行一次的命令，可以没有
}

// Targets 是 tenon agents 支持的 AI 编程助手：它们都读项目根目录的 AGENTS.md（Claude Code 经 CLAUDE.md 导入），区别在技能目录和 MCP 配置的位置、格式。
// antigravity 是 Google 的 Antigravity（取代了 Gemini CLI）。
var Targets = []string{"claude", "codex", "cursor", "antigravity", "copilot"}

// DefaultTargets 是 tenon.json 没有写 agents.targets 时接入的 AI 编程助手。copilot 要单独装一份技能（.github/skills），
// 用到时再选。
var DefaultTargets = []string{"claude", "codex", "cursor", "antigravity"}

// Agents 是 tenon.json 的 agents：tenon agents 为哪些 AI 编程助手写配置。
type Agents struct {
	Targets []string `json:"targets"`        // AI 编程助手，见 Targets；省略时是 DefaultTargets，空数组时不写任何助手的配置，只更新 .tenon/docs。
	Skip    []string `json:"skip,omitempty"` // 不安装的框架技能，例如 ["tenon-electron"]；项目要改写某个框架技能时，复制一份换个名字，再跳过原来的。
}

// AgentTargets 返回要接入的 AI 编程助手：tenon.json 中的 agents.targets，没有写时是 DefaultTargets。
func (c *Config) AgentTargets() []string {
	if c.Agents == nil || c.Agents.Targets == nil {
		return DefaultTargets
	}
	return c.Agents.Targets
}

// Framework 是框架的模块路径。
const Framework = "github.com/go-tenon/tenon"

// FrameworkDir 返回项目 dir 使用的框架源码目录：通常在模块缓存中，replace 到本地时是本地的目录。
func FrameworkDir(dir string) (string, error) {
	cmd := exec.Command("go", "list", "-m", "-f", "{{.Dir}}", Framework)
	cmd.Dir = dir
	out, err := cmd.Output()
	fw := strings.TrimSpace(string(out))
	if err != nil || fw == "" {
		return "", fmt.Errorf("cannot locate %s with go list: %v", Framework, err)
	}
	return fw, nil
}

// Load 读取 dir 中的 tenon.json（缺省时全部用默认值），补上默认值、发现前端并检查。
func Load(dir string) (*Config, error) {
	cfg := &Config{}
	data, err := os.ReadFile(filepath.Join(dir, File))
	switch {
	case errors.Is(err, fs.ErrNotExist):
	case err != nil:
		return nil, err
	default:
		dec := json.NewDecoder(bytes.NewReader(data))
		dec.DisallowUnknownFields()
		if err := dec.Decode(cfg); err != nil {
			return nil, fmt.Errorf("%s: %w", File, err)
		}
	}
	cfg.defaults(dir)
	if cfg.URL == "" {
		if cfg.URL, err = appURL(dir, cfg.Serve); err != nil {
			return nil, err
		}
	}
	if err := cfg.discover(dir); err != nil {
		return nil, err
	}
	return cfg, cfg.check()
}

// defaults 为没有设置的项填入默认值。
func (c *Config) defaults(dir string) {
	if c.Serve == nil {
		configs := "config/app.yaml"
		if exists(dir, "config/local.yaml") {
			configs += ",config/local.yaml"
		}
		c.Serve = []string{"serve", "--config", configs}
	}
}

// appURL 返回开发时应用的地址：从 tenon dev 运行应用时加载的配置文件（serve 参数中的 --config，没有时是环境变量
// APP_CONFIG，再没有时是 config/app.yaml，存在时叠加 config/local.yaml）读出 http.addr，端口不变，主机换成 localhost。
// 配置中没有 http.addr 时是 http://localhost:8080，与应用的默认值相同。先读取项目根目录的 .env，
// 配置中 ${PORT} 这类引用与应用读到的值相同。改了应用的端口，前端的开发服务器跟着调用新的地址。
func appURL(dir string, serve []string) (string, error) {
	if err := config.LoadEnv(filepath.Join(dir, ".env")); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return "", err
	}
	var files []string
	for i, a := range serve {
		switch {
		case a == "--config" && i+1 < len(serve):
			files = append(files, strings.Split(serve[i+1], ",")...)
		case strings.HasPrefix(a, "--config="):
			files = append(files, strings.Split(strings.TrimPrefix(a, "--config="), ",")...)
		}
	}
	if len(files) == 0 {
		if v := os.Getenv("APP_CONFIG"); v != "" {
			files = strings.Split(v, ",")
		} else {
			files = []string{"config/app.yaml", "config/local.yaml"}
		}
	}
	var paths []string
	for _, f := range files {
		if f = strings.TrimSpace(f); f != "" && exists(dir, f) {
			paths = append(paths, filepath.Join(dir, filepath.FromSlash(f)))
		}
	}
	addr, ok, err := config.Value("http.addr", paths...)
	if err != nil {
		return "", fmt.Errorf("%s: url: %w (or set url in %s)", File, err, File)
	}
	if !ok {
		return "http://localhost:8080", nil
	}
	_, port, err := net.SplitHostPort(addr)
	if err != nil || port == "" || port == "0" {
		return "", fmt.Errorf("%s: url: http.addr %q in the app config has no fixed port; set url in %s", File, addr, File)
	}
	return "http://localhost:" + port, nil
}

// discover 找出应用的入口（没有设置 Main 时）、apps 中的前端和共用的客户端包。
func (c *Config) discover(dir string) error {
	if c.Main == "" {
		mains, err := filepath.Glob(filepath.Join(dir, "cmd", "*", "main.go"))
		if err != nil {
			return err
		}
		if len(mains) != 1 {
			return fmt.Errorf("%s: found %d cmd/<name>/main.go, want exactly one; set main in %s", dir, len(mains), File)
		}
		c.Main = "./cmd/" + filepath.Base(filepath.Dir(mains[0]))
	}
	entries, err := os.ReadDir(filepath.Join(dir, AppsDir))
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	for _, e := range entries {
		if !e.IsDir() || SkipDir(e.Name()) {
			continue
		}
		fdir := path.Join(AppsDir, e.Name())
		if !slices.ContainsFunc(viteConfigs, func(f string) bool { return exists(dir, path.Join(fdir, f)) }) {
			continue
		}
		if !Name.MatchString(e.Name()) {
			return fmt.Errorf("%s: frontend name %q must match %s", fdir, e.Name(), Name)
		}
		f, err := readFrontend(dir, fdir)
		if err != nil {
			return err
		}
		if i := slices.IndexFunc(c.Frontends, func(o Frontend) bool { return o.Base == f.Base }); i >= 0 {
			return fmt.Errorf("%s and %s are both mounted at %s", c.Frontends[i].Dir, fdir, f.Base)
		}
		c.Frontends = append(c.Frontends, f)
	}
	c.Client = exists(dir, path.Join(ClientDir, "package.json"))

	entries, err = os.ReadDir(filepath.Join(dir, ClientsDir))
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	for _, e := range entries {
		cdir := path.Join(ClientsDir, e.Name())
		if !e.IsDir() || SkipDir(e.Name()) || !exists(dir, path.Join(cdir, ClientFile)) {
			continue
		}
		if !Name.MatchString(e.Name()) {
			return fmt.Errorf("%s: client name %q must match %s", cdir, e.Name(), Name)
		}
		cl, err := readClient(dir, cdir)
		if err != nil {
			return err
		}
		c.Clients = append(c.Clients, cl)
	}
	return nil
}

// readClient 读取客户端目录 cdir 中的 ClientFile。
func readClient(dir, cdir string) (Client, error) {
	file := path.Join(cdir, ClientFile)
	data, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(file)))
	if err != nil {
		return Client{}, err
	}
	cl := Client{Name: path.Base(cdir), Dir: cdir}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&cl); err != nil {
		return Client{}, fmt.Errorf("%s: %w", file, err)
	}
	return cl, nil
}

// FindClient 返回名为 name 的客户端。
func (c *Config) FindClient(name string) (Client, bool) {
	i := slices.IndexFunc(c.Clients, func(cl Client) bool { return cl.Name == name })
	if i < 0 {
		return Client{}, false
	}
	return c.Clients[i], true
}

// FindFrontend 返回名为 name 的前端。
func (c *Config) FindFrontend(name string) (Frontend, bool) {
	i := slices.IndexFunc(c.Frontends, func(f Frontend) bool { return f.Name == name })
	if i < 0 {
		return Frontend{}, false
	}
	return c.Frontends[i], true
}

// readFrontend 读取前端目录 fdir 中 package.json 的 tenon 字段。
func readFrontend(dir, fdir string) (Frontend, error) {
	file := path.Join(fdir, "package.json")
	data, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(file)))
	if err != nil {
		return Frontend{}, err
	}
	var pkg struct {
		Tenon struct {
			Base string `json:"base"`
			SSR  bool   `json:"ssr"`
		} `json:"tenon"`
	}
	if err := json.Unmarshal(data, &pkg); err != nil {
		return Frontend{}, fmt.Errorf("%s: %w", file, err)
	}
	base := pkg.Tenon.Base
	if !strings.HasPrefix(base, "/") || !strings.HasSuffix(base, "/") {
		return Frontend{}, fmt.Errorf(`%s: tenon.base %q must start and end with /, for example "/admin/"`, file, base)
	}
	return Frontend{Name: path.Base(fdir), Dir: fdir, Base: base, SSR: pkg.Tenon.SSR}, nil
}

// check 校验配置，返回第一个不合法之处。
func (c *Config) check() error {
	for i, t := range c.Tools {
		if len(t.Watch) == 0 {
			return fmt.Errorf("%s: tools[%d].watch is empty", File, i)
		}
	}
	if !strings.HasPrefix(c.URL, "http://") && !strings.HasPrefix(c.URL, "https://") {
		return fmt.Errorf("%s: url %q must start with http:// or https://", File, c.URL)
	}
	for _, cl := range c.Clients {
		file := path.Join(cl.Dir, ClientFile)
		if !slices.Contains(Kinds, cl.Kind) {
			return fmt.Errorf("%s: kind %q is not one of %v", file, cl.Kind, Kinds)
		}
		if _, ok := c.FindFrontend(cl.Frontend); !ok {
			return fmt.Errorf("%s: frontend %q is not in %s", file, cl.Frontend, AppsDir)
		}
	}
	if c.Agents != nil {
		for _, t := range c.Agents.Targets {
			if !slices.Contains(Targets, t) {
				return fmt.Errorf("%s: agents.targets: %q is not one of %v", File, t, Targets)
			}
		}
	}
	for base, api := range c.APIs {
		if !strings.HasPrefix(base, "/") {
			return fmt.Errorf("%s: apis key %q must be a base path starting with /", File, base)
		}
		if api == nil {
			return fmt.Errorf("%s: apis[%q] is empty", File, base)
		}
		if api.Name != "" && !Name.MatchString(api.Name) {
			return fmt.Errorf("%s: apis[%q].name %q must match %s", File, base, api.Name, Name)
		}
	}
	return nil
}

// HasConfigFlag 报告 serve 的参数中是否传了 --config。
func HasConfigFlag(args []string) bool {
	return slices.ContainsFunc(args, func(a string) bool { return a == "--config" || strings.HasPrefix(a, "--config=") })
}

// SkipDir 报告扫描项目时是否跳过这个目录：隐藏目录、依赖、测试数据和前端的构建产物。
func SkipDir(name string) bool {
	return strings.HasPrefix(name, ".") || name == "node_modules" || name == "vendor" || name == "testdata" || name == "dist"
}

// exists 报告 dir 中的路径 p 是否存在。
func exists(dir, p string) bool {
	_, err := os.Stat(filepath.Join(dir, filepath.FromSlash(p)))
	return err == nil
}

// ClientName 返回 BasePath 为 base 的 API 的 TypeScript 客户端目录名：tenon.json 的 apis 中写了 name 时用它，
// 否则是 base 中从后往前第一个不是 api、也不是路径参数的段（与 clientgen.DefaultTSDir 相同），例如 /admin/api 是 admin、/api 是 api。
func (c *Config) ClientName(base string) string {
	if api := c.APIs[base]; api != nil && api.Name != "" {
		return api.Name
	}
	segs := strings.Split(strings.Trim(base, "/"), "/")
	for i := len(segs) - 1; i >= 0; i-- {
		if s := segs[i]; s != "" && s != "api" && !strings.HasPrefix(s, "{") {
			return s
		}
	}
	return path.Base("/" + strings.Trim(base, "/"))
}
