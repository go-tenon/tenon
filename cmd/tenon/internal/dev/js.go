package dev

import (
	"bytes"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/cockroachdb/errors"

	"github.com/go-tenon/tenon/cmd/tenon/internal/project"
)

// JSDir 是项目中框架前端包的目录：SyncJS 把框架 js 中的每个包同步到这里，例如 tenon/js/client 是
// @go-tenon/client。项目把它们列入 Bun workspace 并提交进仓库，前端用 workspace:* 依赖。
const JSDir = "tenon/js"

// SyncJS 把项目 dir 使用的框架版本中 js 下的前端包同步到 dir/JSDir：内容有变化或缺少的文件才写入，框架中已经没有的
// 文件和包删除，没有变化的文件不碰。所以开发服务器运行时执行 tenon generate，Vite 不会因为文件被重写而重新加载、
// 重新预构建依赖。框架被 replace 到本地目录时同步本地的代码。changed 报告各包的 package.json 是否有变化：
// 有变化时要重新 bun install。
//
// 各包自己的 node_modules 保留：Bun 把包的依赖链接放在其中（例如 @go-tenon/ui 的 react），
// package.json 没有变化时不会重新 bun install，删掉它包就找不到依赖。
func SyncJS(dir string) (changed bool, err error) {
	fw, err := project.FrameworkDir(dir)
	if err != nil {
		return false, err
	}
	src := filepath.Join(fw, "js")
	dst := filepath.Join(dir, filepath.FromSlash(JSDir))
	before := manifests(dst)
	entries, err := os.ReadDir(src)
	if err != nil {
		return false, errors.Wrapf(err, "dev: %s has no front-end packages", fw)
	}
	packages := map[string]bool{}
	for _, e := range entries {
		if !e.IsDir() || e.Name() == "node_modules" {
			continue
		}
		if _, err := os.Stat(filepath.Join(src, e.Name(), "package.json")); err == nil {
			packages[e.Name()] = true
		}
	}
	// 框架中已经没有的包整个删除，其余的包逐个文件同步。
	existing, err := os.ReadDir(dst)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return false, errors.Wrap(err, "dev: sync front-end packages")
	}
	for _, e := range existing {
		if !packages[e.Name()] {
			if err := os.RemoveAll(filepath.Join(dst, e.Name())); err != nil {
				return false, errors.Wrap(err, "dev: sync front-end packages")
			}
		}
	}
	for name := range packages {
		if err := syncPackage(filepath.Join(src, name), filepath.Join(dst, name)); err != nil {
			return false, errors.Wrap(err, "dev: sync front-end packages")
		}
		if err := markBins(filepath.Join(dst, name)); err != nil {
			return false, errors.Wrap(err, "dev: sync front-end packages")
		}
	}
	after := manifests(dst)
	return !slices.EqualFunc(before, after, func(a, b []byte) bool { return bytes.Equal(a, b) }), nil
}

// markBins 给包目录 dir 中 package.json 的 bin 指向的文件加上执行权限，例如 @go-tenon/start 的 tenon-start。
// bun install 链接命令时会这样做，但模块缓存中的文件没有执行权限，复制过来的文件也没有；package.json 没有变化时
// 不会重新 bun install，前端的脚本运行 tenon-start 时就会因为没有执行权限而失败。
func markBins(dir string) error {
	data, err := os.ReadFile(filepath.Join(dir, "package.json"))
	if err != nil {
		return err
	}
	var pkg struct {
		Bin json.RawMessage `json:"bin"`
	}
	if err := json.Unmarshal(data, &pkg); err != nil || len(pkg.Bin) == 0 {
		return err
	}
	// bin 可以是一个路径，也可以是命令名到路径的对象。
	var paths []string
	var one string
	var many map[string]string
	switch {
	case json.Unmarshal(pkg.Bin, &one) == nil:
		paths = []string{one}
	case json.Unmarshal(pkg.Bin, &many) == nil:
		for _, p := range many {
			paths = append(paths, p)
		}
	default:
		return errors.Newf("%s/package.json: bin must be a path or an object of paths", dir)
	}
	for _, p := range paths {
		f := filepath.Join(dir, filepath.FromSlash(p))
		info, err := os.Stat(f)
		if err != nil {
			return err
		}
		if info.Mode().Perm()&0o111 != 0 {
			continue // 已经可以执行：不改动文件，开发服务器不会看到变化
		}
		if err := os.Chmod(f, 0o755); err != nil {
			return err
		}
	}
	return nil
}

// NeedsInstall 报告项目 dir 是否需要 bun install：没有 node_modules，或者根 package.json 的 workspaces 中的某个框架
// 前端包声明了依赖（例如 @go-tenon/ui 的 react），但它自己的 node_modules 不存在（例如刚 clone 的项目，node_modules 被
// .gitignore 忽略）。不在 workspaces 中的框架包（例如没有 Electron 客户端的项目中的 @go-tenon/electron）不会被安装，不检查。
func NeedsInstall(dir string) bool {
	if _, err := os.Stat(filepath.Join(dir, "node_modules")); err != nil {
		return true
	}
	for _, pkg := range workspaces(dir) {
		rel, err := filepath.Rel(filepath.Join(dir, filepath.FromSlash(JSDir)), pkg)
		if err != nil || strings.HasPrefix(rel, "..") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(pkg, "package.json"))
		if err != nil {
			continue
		}
		var m struct {
			Dependencies     map[string]string `json:"dependencies"`
			PeerDependencies map[string]string `json:"peerDependencies"`
		}
		if json.Unmarshal(b, &m) != nil || len(m.Dependencies)+len(m.PeerDependencies) == 0 {
			continue
		}
		if _, err := os.Stat(filepath.Join(pkg, "node_modules")); err != nil {
			return true
		}
	}
	return false
}

// workspaces 返回项目 dir 的根 package.json 中 workspaces 列出的目录（展开通配符）。workspaces 可以是数组，
// 也可以是带 packages 的对象。
func workspaces(dir string) []string {
	b, err := os.ReadFile(filepath.Join(dir, "package.json"))
	if err != nil {
		return nil
	}
	var m struct {
		Workspaces json.RawMessage `json:"workspaces"`
	}
	if json.Unmarshal(b, &m) != nil || len(m.Workspaces) == 0 {
		return nil
	}
	var patterns []string
	if json.Unmarshal(m.Workspaces, &patterns) != nil {
		var obj struct {
			Packages []string `json:"packages"`
		}
		if json.Unmarshal(m.Workspaces, &obj) != nil {
			return nil
		}
		patterns = obj.Packages
	}
	var dirs []string
	for _, p := range patterns {
		matches, _ := filepath.Glob(filepath.Join(dir, filepath.FromSlash(p)))
		dirs = append(dirs, matches...)
	}
	return dirs
}

// isTest 报告 name 是否是包的测试文件（例如 form.test.tsx）：它们只在框架中运行，可能依赖项目中没有的包，不复制。
func isTest(name string) bool {
	for _, ext := range []string{".test.ts", ".test.tsx"} {
		if strings.HasSuffix(name, ext) {
			return true
		}
	}
	return false
}

// syncPackage 让包目录 dst 与框架中的 src 一致：内容不同或缺少的文件才写入，src 中没有的文件和目录删除；
// 跳过 project.SkipDir 中的目录和测试文件（isTest），保留 dst 的 node_modules（Bun 链接的依赖）。
func syncPackage(src, dst string) error {
	want := map[string]bool{} // dst 中应有的文件和目录，相对于包目录
	err := filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && path != src && project.SkipDir(d.Name()) {
			return filepath.SkipDir
		}
		if !d.IsDir() && isTest(d.Name()) {
			return nil
		}
		rel, _ := filepath.Rel(src, path)
		want[rel] = true
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if old, err := os.ReadFile(target); err == nil && bytes.Equal(old, b) {
			return nil
		}
		return os.WriteFile(target, b, 0o644)
	})
	if err != nil {
		return err
	}
	return filepath.WalkDir(dst, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dst, path)
		switch {
		case path == dst:
			return nil
		case d.IsDir() && rel == "node_modules":
			return filepath.SkipDir
		case want[rel]:
			return nil
		}
		if err := os.RemoveAll(path); err != nil {
			return err
		}
		if d.IsDir() {
			return filepath.SkipDir
		}
		return nil
	})
}

// manifests 返回 dir 中各包的 package.json，按包名排序；把包名写在内容前面，增删包也算变化。
func manifests(dir string) [][]byte {
	var out [][]byte
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if b, err := os.ReadFile(filepath.Join(dir, e.Name(), "package.json")); err == nil {
			out = append(out, append([]byte(e.Name()+"\n"), b...))
		}
	}
	return out
}
