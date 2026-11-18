// Package project 读取项目根目录的 tenon.json，补上约定的默认值，并发现项目中的前端和客户端。
package project

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
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
	URL    string          `json:"url,omitempty"`     // 应用的地址，dev 打印它，前端的开发服务器经它调用接口，默认 http://localhost:8080。
	APIs   map[string]*API `json:"apis,omitempty"`    // 按 BasePath 覆盖 API 客户端的约定，例如改名或生成 Go 客户端。没有写的 API 按约定生成。
	Tools  []Tool          `json:"tools,omitempty"`   // 与应用一起运行的其他工具，可空。

	Frontends []Frontend `json:"-"` // apps 中的前端，按名称排序。由 Load 从目录中发现，不在 tenon.json 中。
	Clients   []Client   `json:"-"` // clients 中的客户端，按名称排序。由 Load 从目录中发现。
	Client    bool       `json:"-"` // 报告项目中是否有共用的客户端包 packages/client。由 Load 发现，不在 tenon.json 中。
}

// API 覆盖一个 API 客户端的约定。
type API struct {
	Name string `json:"name,omitempty"` // TypeScript 客户端的目录名，默认是 BasePath 的最后一段，例如 /api/admin 是 admin。
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
	Watch []string `json:"watch"`
	Build []string `json:"build,omitempty"`
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
	if c.URL == "" {
		c.URL = "http://localhost:8080"
	}
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
