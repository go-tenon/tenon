// Package archtest 检查 go-kit 的约定（见 docs/conventions.md）：
//
//	基础设施（config、logger、i18n、authz、database、httpx/…、support/… 等）  只能依赖基础设施
//	app                                                                      只能依赖基础设施
//	modules/…                                                                只能依赖基础设施，模块之间互不依赖
//
// examples、internal 不受限制。新增顶层包时需要在 layerOf 中归类。
// 另外检查 go-kit 自身的代码（不含 examples 与测试）不使用 fmt.Errorf（统一用 cockroachdb/errors）。
package archtest

import (
	"os/exec"
	"strings"
	"testing"
)

const module = "github.com/ryan053857/go-kit"

type layer int

const (
	unrestricted layer = iota
	infra
	appLayer
	modules
)

var infraRoots = []string{"config", "logger", "i18n", "authz", "database", "httpx", "support", "mail", "storage", "messaging"}

func layerOf(pkg string) (layer, bool) {
	rel, ok := strings.CutPrefix(pkg, module+"/")
	if !ok {
		return 0, false // 外部包
	}
	root, _, _ := strings.Cut(rel, "/")
	switch root {
	case "app":
		return appLayer, true
	case "modules":
		return modules, true
	case "examples", "starter", "internal":
		return unrestricted, true
	}
	for _, r := range infraRoots {
		if root == r {
			return infra, true
		}
	}
	return -1, true // 未归类
}

func allowed(from, to layer) bool {
	switch from {
	case infra:
		return to == infra
	case appLayer:
		return to == infra || to == appLayer
	case modules:
		return to == infra || to == modules
	}
	return true
}

func TestLayers(t *testing.T) {
	cmd := exec.Command("go", "list", "-f", `{{.ImportPath}}{{range .Imports}} {{.}}{{end}}`, module+"/...")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("go list: %v", err)
	}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		fields := strings.Fields(line)
		pkg, imports := fields[0], fields[1:]
		from, _ := layerOf(pkg)
		if from == -1 {
			t.Errorf("%s: unclassified top-level package, add it to layerOf", pkg)
			continue
		}
		for _, imp := range imports {
			to, internal := layerOf(imp)
			if !internal {
				continue
			}
			switch {
			case to == unrestricted && from != unrestricted:
				t.Errorf("%s imports %s: examples/internal must not be imported", pkg, imp)
			case !allowed(from, to):
				t.Errorf("%s imports %s: violates layering", pkg, imp)
			case from == modules && to == modules && moduleOf(pkg) != moduleOf(imp):
				t.Errorf("%s imports %s: modules must not depend on each other", pkg, imp)
			}
		}
	}
}

// moduleOf 返回模块包所属的模块名，如 modules/account/x → account。
func moduleOf(pkg string) string {
	rel := strings.TrimPrefix(pkg, module+"/modules/")
	name, _, _ := strings.Cut(rel, "/")
	return name
}

func TestNoFmtErrorf(t *testing.T) {
	out, err := exec.Command("git", "grep", "-n", "fmt.Errorf(", "--", "*.go", ":!*_test.go", ":!examples", ":!starter", ":!internal").Output()
	if err != nil && len(out) == 0 {
		return // 没有匹配（git grep 退出码 1）
	}
	for line := range strings.Lines(string(out)) {
		if strings.Contains(line, "//") && strings.Index(line, "//") < strings.Index(line, "fmt.Errorf(") {
			continue // 注释
		}
		t.Errorf("use cockroachdb/errors instead of fmt.Errorf: %s", strings.TrimSpace(line))
	}
}
