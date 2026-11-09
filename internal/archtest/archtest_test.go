// Package archtest 检查 go-kit 的分层依赖：
//
//	基础设施（config、logger、database、httpx/…、support/… 等）  只能依赖基础设施
//	app                                                          只能依赖基础设施
//	modules/…                                                    只能依赖基础设施与其他模块
//
// starter、internal 不受限制。新增顶层包时需要在 layerOf 中归类。
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

var infraRoots = []string{"config", "logger", "database", "httpx", "support", "mail", "storage", "messaging"}

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
	case "starter", "internal":
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
			if to == unrestricted && from != unrestricted {
				t.Errorf("%s imports %s: starter/internal must not be imported", pkg, imp)
			} else if !allowed(from, to) {
				t.Errorf("%s imports %s: violates layering", pkg, imp)
			}
		}
	}
}
