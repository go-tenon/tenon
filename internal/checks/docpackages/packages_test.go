// Package docpackages_test 检查文档开头的“包：”一行：列出的包都存在，框架的每个公开的包都出现在某一页中。
// AI 编程助手按包找文档，改框架的人按包找要更新的页面。
package docpackages_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// root 是仓库的根目录。
const root = "../../.."

var (
	packageLine = regexp.MustCompile(`(?m)^包：(.+)$`)
	codeSpan    = regexp.MustCompile("`([^`]+)`")
)

// listed 返回 docs 中每一页“包：”一行列出的包，键是包，值是列出它的页面。
func listed(t *testing.T) map[string][]string {
	t.Helper()
	out := map[string][]string{}
	err := filepath.WalkDir(filepath.Join(root, "docs"), func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".md") {
			return err
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(filepath.Join(root, "docs"), path)
		for _, m := range packageLine.FindAllStringSubmatch(string(b), -1) {
			for _, p := range codeSpan.FindAllStringSubmatch(m[1], -1) {
				out[p[1]] = append(out[p[1]], filepath.ToSlash(rel))
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// TestListedPackagesExist 检查“包：”一行中的每个包都存在：Go 包是模块中有 Go 代码的目录；
// @go-tenon/<名称> 是 js/<名称> 中的前端包。
func TestListedPackagesExist(t *testing.T) {
	t.Parallel()
	for pkg, pages := range listed(t) {
		pattern := filepath.Join(root, filepath.FromSlash(pkg), "*.go")
		if name, ok := strings.CutPrefix(pkg, "@go-tenon/"); ok {
			pattern = filepath.Join(root, "js", name, "package.json")
		}
		if files, _ := filepath.Glob(pattern); len(files) == 0 {
			t.Errorf("%s: package %s does not exist", strings.Join(pages, ", "), pkg)
		}
	}
}

// TestPackagesDocumented 检查框架的每个公开的 Go 包都列在某一页的“包：”中。
// 公开的包是 internal 和 cmd 以外有非测试 Go 代码的目录。新增包时在讲它的那一页开头列出它。
func TestPackagesDocumented(t *testing.T) {
	t.Parallel()
	pkgs := listed(t)
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || !d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		rel = filepath.ToSlash(rel)
		parts := strings.Split(rel, "/")
		if slices.Contains(parts, "internal") || slices.Contains(parts, "testdata") || slices.Contains(parts, "node_modules") ||
			parts[0] == "cmd" || parts[0] == "js" || parts[0] == "docs" || strings.HasPrefix(parts[0], ".") {
			return fs.SkipDir
		}
		files, _ := filepath.Glob(filepath.Join(path, "*.go"))
		hasCode := slices.ContainsFunc(files, func(f string) bool { return !strings.HasSuffix(f, "_test.go") })
		if rel != "." && hasCode && pkgs[rel] == nil {
			t.Errorf("package %s is not listed in the “包：” line of any page in docs", rel)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
