package dev

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/cockroachdb/errors"
)

// JSDir 是项目中框架前端包的目录：SyncJS 把框架 js 中的每个包复制到这里，例如 .tenon/js/client 是
// @go-tenon/client。项目把它们列入 Bun workspace，前端用 workspace:* 依赖。
const JSDir = ".tenon/js"

// SyncJS 把项目 dir 使用的框架版本中 js 下的前端包复制到 dir/JSDir，替换已有的内容。框架被 replace 到本地目录时
// 复制本地的代码。changed 报告各包的 package.json 是否有变化：有变化时要重新 bun install。
//
// 各包自己的 node_modules 保留：Bun 把包的依赖链接放在其中（例如 @go-tenon/ui 的 react），
// package.json 没有变化时不会重新 bun install，删掉它包就找不到依赖。
func SyncJS(dir string) (changed bool, err error) {
	fw, err := frameworkDir(dir)
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
	// 框架中已经没有的包整个删除，其余的包删除 node_modules 之外的内容，再复制新的。
	existing, _ := os.ReadDir(dst)
	for _, e := range existing {
		if err := clearPackage(filepath.Join(dst, e.Name()), packages[e.Name()]); err != nil {
			return false, errors.Wrap(err, "dev: sync front-end packages")
		}
	}
	for name := range packages {
		if err := copyInto(filepath.Join(src, name), filepath.Join(dst, name), isTest); err != nil {
			return false, errors.Wrap(err, "dev: sync front-end packages")
		}
	}
	if err := os.WriteFile(filepath.Join(filepath.Dir(dst), ".gitignore"), []byte("*\n"), 0o644); err != nil {
		return false, errors.Wrap(err, "dev: sync front-end packages")
	}
	after := manifests(dst)
	return !slices.EqualFunc(before, after, func(a, b []byte) bool { return bytes.Equal(a, b) }), nil
}

// NeedsInstall 报告项目 dir 是否需要 bun install：没有 node_modules，或者某个框架前端包声明了依赖
// （例如 @go-tenon/ui 的 react），但它自己的 node_modules 不存在（例如刚 clone 的项目，.tenon 被 .gitignore 忽略）。
func NeedsInstall(dir string) bool {
	if _, err := os.Stat(filepath.Join(dir, "node_modules")); err != nil {
		return true
	}
	root := filepath.Join(dir, filepath.FromSlash(JSDir))
	entries, _ := os.ReadDir(root)
	for _, e := range entries {
		b, err := os.ReadFile(filepath.Join(root, e.Name(), "package.json"))
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
		if _, err := os.Stat(filepath.Join(root, e.Name(), "node_modules")); err != nil {
			return true
		}
	}
	return false
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

// clearPackage 删除包目录 dir；keepDeps 为 true 时保留其中的 node_modules。
func clearPackage(dir string, keepDeps bool) error {
	if !keepDeps {
		return os.RemoveAll(dir)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if e.Name() == "node_modules" {
			continue
		}
		if err := os.RemoveAll(filepath.Join(dir, e.Name())); err != nil {
			return err
		}
	}
	return nil
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
