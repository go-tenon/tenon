// Package project 读取项目根目录的 tenon.json，补上约定的默认值，并发现项目中的前端。
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
	// Schema 是编辑器补全用的 JSON Schema 地址，只供编辑器使用。
	Schema string `json:"$schema,omitempty"`
	// Main 是应用入口的包，默认 ./cmd/api。
	Main string `json:"main,omitempty"`
	// Serve 是开发时运行应用的参数，默认 serve --config config/app.yaml,config/local.yaml（local.yaml 存在时才加上它）。
	Serve []string `json:"serve,omitempty"`
	// URL 是应用的地址，dev 打印它，templ 的代理访问它，默认 http://localhost:8080。
	URL string `json:"url,omitempty"`
	// APIs 按 BasePath 覆盖 API 客户端的约定，例如改名或生成 Go 客户端。没有写的 API 按约定生成。
	APIs map[string]*API `json:"apis,omitempty"`
	// Templ 是服务端渲染的页面；项目中有 .templ 文件时默认启用。
	Templ *Templ `json:"templ,omitempty"`
	// Tailwind 是用 Tailwind 生成的样式表，可空。
	Tailwind *Tailwind `json:"tailwind,omitempty"`
	// Tools 是与应用一起运行的其他工具，可空。
	Tools []Tool `json:"tools,omitempty"`

	// Frontends 是 apps 中的前端，按名称排序。由 Load 从目录中发现，不在 tenon.json 中。
	Frontends []Frontend `json:"-"`
	// Client 报告项目中是否有共用的客户端包 packages/client。由 Load 发现，不在 tenon.json 中。
	Client bool `json:"-"`
}

// API 覆盖一个 API 客户端的约定。
type API struct {
	// Name 是 TypeScript 客户端的目录名，默认是 BasePath 的最后一段，例如 /api/admin 是 admin。
	Name string `json:"name,omitempty"`
	// Go 是 Go 客户端的目录，例如 ../api，设置后才生成。
	Go string `json:"go,omitempty"`
}

// Frontend 是一个 Vite 项目。
type Frontend struct {
	// Name 是前端的名称，即 apps 中的目录名。应用按它取开发服务器的地址。
	Name string
	// Dir 是前端的目录，例如 apps/web，其中有 frontend.go（go:embed all:dist）。
	Dir string
}

// Templ 配置服务端渲染页面的开发。
type Templ struct {
	// ProxyPort 是浏览器访问的端口，templ 在这里代理应用并注入自动刷新的脚本，默认 7331。
	ProxyPort int `json:"proxyPort,omitempty"`
	// Static 是静态文件目录，其中的文件变化时刷新浏览器，可空。
	Static string `json:"static,omitempty"`
}

// Tailwind 是 Tailwind standalone CLI 的输入和输出。
type Tailwind struct {
	Input  string `json:"input"`
	Output string `json:"output"`
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

func (c *Config) defaults(dir string) {
	if c.Main == "" {
		c.Main = "./cmd/api"
	}
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
	if c.Templ == nil && hasTempl(dir) {
		c.Templ = &Templ{}
	}
	if c.Templ != nil && c.Templ.ProxyPort == 0 {
		c.Templ.ProxyPort = 7331
	}
}

// discover 找出 apps 中的前端和共用的客户端包。
func (c *Config) discover(dir string) error {
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
		c.Frontends = append(c.Frontends, Frontend{Name: e.Name(), Dir: fdir})
	}
	c.Client = exists(dir, path.Join(ClientDir, "package.json"))
	return nil
}

func (c *Config) check() error {
	if t := c.Tailwind; t != nil && (t.Input == "" || t.Output == "") {
		return fmt.Errorf("%s: tailwind needs input and output", File)
	}
	for i, t := range c.Tools {
		if len(t.Watch) == 0 {
			return fmt.Errorf("%s: tools[%d].watch is empty", File, i)
		}
	}
	if !strings.HasPrefix(c.URL, "http://") && !strings.HasPrefix(c.URL, "https://") {
		return fmt.Errorf("%s: url %q must start with http:// or https://", File, c.URL)
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

func exists(dir, p string) bool {
	_, err := os.Stat(filepath.Join(dir, filepath.FromSlash(p)))
	return err == nil
}

// hasTempl 报告 dir 中是否有 .templ 文件。
func hasTempl(dir string) bool {
	found := false
	_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		switch {
		case found:
			return fs.SkipAll
		case err != nil:
			return nil
		case d.IsDir() && p != dir && SkipDir(d.Name()):
			return filepath.SkipDir
		case !d.IsDir() && strings.HasSuffix(p, ".templ"):
			found = true
		}
		return nil
	})
	return found
}
