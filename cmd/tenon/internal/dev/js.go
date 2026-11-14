package dev

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"

	"github.com/cockroachdb/errors"
)

// JSDir 是项目中框架前端包的目录：SyncJS 把框架 js 中的每个包复制到这里，例如 .tenon/js/client 是
// @go-tenon/client。项目把它们列入 Bun workspace，前端用 workspace:* 依赖。
const JSDir = ".tenon/js"

// SyncJS 把项目 dir 使用的框架版本中 js 下的前端包复制到 dir/JSDir，替换已有的内容。框架被 replace 到本地目录时
// 复制本地的代码。changed 报告各包的 package.json 是否有变化：有变化时要重新 bun install。
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
	if err := os.RemoveAll(dst); err != nil {
		return false, errors.Wrap(err, "dev: sync front-end packages")
	}
	for _, e := range entries {
		if !e.IsDir() || e.Name() == "node_modules" {
			continue
		}
		if _, err := os.Stat(filepath.Join(src, e.Name(), "package.json")); err != nil {
			continue
		}
		if err := copyTree(filepath.Join(src, e.Name()), filepath.Join(dst, e.Name())); err != nil {
			return false, errors.Wrap(err, "dev: sync front-end packages")
		}
	}
	if err := os.WriteFile(filepath.Join(filepath.Dir(dst), ".gitignore"), []byte("*\n"), 0o644); err != nil {
		return false, errors.Wrap(err, "dev: sync front-end packages")
	}
	after := manifests(dst)
	return !slices.EqualFunc(before, after, func(a, b []byte) bool { return bytes.Equal(a, b) }), nil
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
