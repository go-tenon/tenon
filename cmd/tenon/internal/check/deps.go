package check

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/cockroachdb/errors"
	"golang.org/x/mod/semver"

	"github.com/go-tenon/tenon/cmd/tenon/internal/dev"
	"github.com/go-tenon/tenon/cmd/tenon/internal/project"
)

// pkgManifest 是 package.json 中与依赖检查相关的字段。
type pkgManifest struct {
	Name                 string                             `json:"name"`
	Dependencies         map[string]string                  `json:"dependencies"`
	DevDependencies      map[string]string                  `json:"devDependencies"`
	PeerDependencies     map[string]string                  `json:"peerDependencies"`
	PeerDependenciesMeta map[string]struct{ Optional bool } `json:"peerDependenciesMeta"`
}

// CheckDeps 检查项目中每个前端和客户端的 package.json 声明的依赖版本是否满足所引用框架包（@go-tenon/*）的
// peerDependencies 要求：优先读取 tenon/js，不存在时从当前 go.mod 对应的框架目录读取。
func CheckDeps(root string, cfg *project.Config, exempt []string) ([]Finding, error) {
	if len(cfg.Frontends) == 0 && len(cfg.Clients) == 0 {
		return nil, nil
	}
	fwPkgs, err := loadFrameworkPackages(root)
	if err != nil || len(fwPkgs) == 0 {
		return nil, err
	}
	rootDeps := readAllDeps(filepath.Join(root, "package.json"))

	var dirs []string
	for _, f := range cfg.Frontends {
		dirs = append(dirs, f.Dir)
	}
	for _, c := range cfg.Clients {
		dirs = append(dirs, c.Dir)
	}

	var out []Finding
	for _, dir := range dirs {
		rel := dir + "/package.json"
		if isExempt(rel, exempt) {
			continue
		}
		m, err := readManifest(filepath.Join(root, filepath.FromSlash(rel)))
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, errors.Wrap(err, "check deps")
		}
		used := collectFrameworkDeps(m, fwPkgs)
		for _, fwName := range used {
			fw := fwPkgs[fwName]
			peers := make([]string, 0, len(fw.PeerDependencies))
			for dep := range fw.PeerDependencies {
				peers = append(peers, dep)
			}
			slices.Sort(peers)
			for _, dep := range peers {
				want := fw.PeerDependencies[dep]
				got, ok := lookupDep(m, rootDeps, dep)
				if !ok {
					if fw.PeerDependenciesMeta[dep].Optional {
						continue
					}
					out = append(out, Finding{
						Check:   "deps",
						File:    rel,
						Message: fmt.Sprintf("missing %s (%s requires %s)", dep, fwName, want),
					})
					continue
				}
				if !satisfies(got, want) {
					out = append(out, Finding{
						Check:   "deps",
						File:    rel,
						Message: fmt.Sprintf("%s %q does not satisfy %s peerDependency %q", dep, got, fwName, want),
					})
				}
			}
		}
	}
	return out, nil
}

// loadFrameworkPackages 加载框架前端包的 package.json（按包名索引）：优先从 tenon/js 读取，没有时回退到框架源码目录的 js。
func loadFrameworkPackages(root string) (map[string]pkgManifest, error) {
	jsDir := filepath.Join(root, filepath.FromSlash(dev.JSDir))
	if entries, err := os.ReadDir(jsDir); err != nil || len(entries) == 0 {
		fw, ferr := project.FrameworkDir(root)
		if ferr != nil {
			return nil, nil
		}
		jsDir = filepath.Join(fw, "js")
	}
	entries, err := os.ReadDir(jsDir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, errors.Wrap(err, "check deps: read framework js")
	}
	out := map[string]pkgManifest{}
	for _, e := range entries {
		if !e.IsDir() || e.Name() == "node_modules" {
			continue
		}
		m, err := readManifest(filepath.Join(jsDir, e.Name(), "package.json"))
		if err != nil || m.Name == "" {
			continue
		}
		out[m.Name] = m
	}
	return out, nil
}

// collectFrameworkDeps 返回 m 直接和间接依赖的框架包名，按名称排序。
func collectFrameworkDeps(m pkgManifest, fwPkgs map[string]pkgManifest) []string {
	seen := map[string]bool{}
	var queue []string
	enqueue := func(deps map[string]string) {
		for name := range deps {
			if _, ok := fwPkgs[name]; ok && !seen[name] {
				seen[name] = true
				queue = append(queue, name)
			}
		}
	}
	enqueue(m.Dependencies)
	enqueue(m.DevDependencies)
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		enqueue(fwPkgs[cur].Dependencies)
	}
	out := make([]string, 0, len(seen))
	for name := range seen {
		out = append(out, name)
	}
	slices.Sort(out)
	return out
}

// readManifest 读取并解析 package.json。
func readManifest(p string) (pkgManifest, error) {
	data, err := os.ReadFile(p)
	if err != nil {
		return pkgManifest{}, err
	}
	var m pkgManifest
	if err := json.Unmarshal(data, &m); err != nil {
		return pkgManifest{}, fmt.Errorf("%s: %w", p, err)
	}
	return m, nil
}

// readAllDeps 读取根目录 package.json 中的 dependencies 和 devDependencies，文件不存在时返回 nil。
func readAllDeps(p string) map[string]string {
	m, err := readManifest(p)
	if err != nil {
		return nil
	}
	out := map[string]string{}
	for k, v := range m.DevDependencies {
		out[k] = v
	}
	for k, v := range m.Dependencies {
		out[k] = v
	}
	return out
}

// lookupDep 在应用自身的 dependencies、devDependencies 及根目录依赖中查找 dep 的版本声明。
func lookupDep(m pkgManifest, rootDeps map[string]string, dep string) (string, bool) {
	if v, ok := m.Dependencies[dep]; ok {
		return v, true
	}
	if v, ok := m.DevDependencies[dep]; ok {
		return v, true
	}
	if v, ok := rootDeps[dep]; ok {
		return v, true
	}
	return "", false
}

// satisfies 报告应用声明的版本范围 got 是否满足框架的 peerDependency 范围 want。
// 支持 ^x.y.z、~x.y.z、>=x.y.z 和精确版本 x.y.z；无法按 semver 解析的特殊协议（workspace:*、*、catalog: 等）视为通过。
func satisfies(got, want string) bool {
	got = strings.TrimSpace(got)
	want = strings.TrimSpace(want)
	if got == "" || got == "*" || got == "latest" || strings.HasPrefix(got, "workspace:") || strings.HasPrefix(got, "catalog:") {
		return true
	}
	gotOp, gotVer, ok := parseVersion(got)
	if !ok {
		return true
	}
	wantOp, wantVer, ok := parseVersion(want)
	if !ok {
		return true
	}
	if semver.Compare(gotVer, wantVer) < 0 {
		return false
	}
	// ^x.y.z（x > 0）要求主版本一致；~x.y.z 要求主次版本一致。
	switch wantOp {
	case "^":
		if semver.Major(wantVer) != "v0" && semver.Major(gotVer) != semver.Major(wantVer) {
			return false
		}
		if semver.Major(wantVer) == "v0" && semver.MajorMinor(gotVer) != semver.MajorMinor(wantVer) {
			return false
		}
	case "~":
		if semver.MajorMinor(gotVer) != semver.MajorMinor(wantVer) {
			return false
		}
	case "":
		if gotOp == "" && semver.Compare(gotVer, wantVer) != 0 {
			return false
		}
	}
	return true
}

// parseVersion 拆出 npm 版本声明中的操作符（^、~、>=、= 或空）和规范的 semver（带 v 前缀）。
func parseVersion(s string) (op, ver string, ok bool) {
	for _, prefix := range []string{">=", "^", "~", "="} {
		if strings.HasPrefix(s, prefix) {
			op = prefix
			s = strings.TrimSpace(strings.TrimPrefix(s, prefix))
			break
		}
	}
	if s == "" {
		return "", "", false
	}
	v := "v" + strings.TrimPrefix(s, "v")
	if !semver.IsValid(v) {
		return "", "", false
	}
	return op, v, true
}
