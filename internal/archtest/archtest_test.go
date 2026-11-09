// Package archtest 检查 mygoravel 的包依赖与第一期范围约束（见 docs/directory-structure.md、spec.go）。
//
// 目标包图（只在有实际代码时建包，本测试不要求空包存在）：
//
//	cleanup、config、fault、logging、i18n、identity
//	database、database/migrate、database/dbtest
//	httpx、httpx/rest、command
//	internal/archtest
//
// 依赖规则摘要：
//
//	fault、identity     不依赖 HTTP、数据库或运行层
//	config、logging、i18n 不依赖业务、HTTP、数据库、command、cleanup
//	cleanup             不依赖 database、httpx、command 或具体驱动
//	database*           不依赖 httpx、command
//	httpx*              不依赖 command
//	command             可依赖基础设施
//
// 另外禁止第一期引入后期形态依赖，允许标准 errors 与 cockroachdb/errors 互操作。
package archtest

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

const module = "github.com/ryan053857/mygoravel"

// 第一期允许存在的顶层包（及已知子树）。未列出的顶层包视为未归类失败。
var knownRoots = map[string]bool{
	"cleanup":  true,
	"config":   true,
	"fault":    true,
	"logging":  true,
	"i18n":     true,
	"identity": true,
	"database": true,
	"httpx":    true,
	"command":  true,
	"internal": true,
}

// 不得出现在第一期 go.mod 依赖中的后期形态模块路径前缀。
var forbiddenDepPrefixes = []string{
	"github.com/a-h/templ",
	"github.com/alexedwards/scs",
	"github.com/ThreeDotsLabs/watermill",
	"github.com/go-co-op/gocron",
	"github.com/gorilla/websocket",
	"google.golang.org/grpc",
	"github.com/aws/aws-sdk-go-v2/service/s3",
	"github.com/resend/resend-go",
	"github.com/wneessen/go-mail",
	"github.com/xuri/excelize",
}

func rootOf(pkg string) (string, bool) {
	rel, ok := strings.CutPrefix(pkg, module+"/")
	if !ok {
		if pkg == module {
			return "", true
		}
		return "", false
	}
	root, _, _ := strings.Cut(rel, "/")
	return root, true
}

func under(pkg, prefix string) bool {
	return pkg == prefix || strings.HasPrefix(pkg, prefix+"/")
}

func TestPackageGraph(t *testing.T) {
	cmd := exec.Command("go", "list", "-f", `{{.ImportPath}}{{range .Imports}} {{.}}{{end}}`, module+"/...")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("go list: %v", err)
	}
	text := strings.TrimSpace(string(out))
	if text == "" {
		return
	}
	for _, line := range strings.Split(text, "\n") {
		fields := strings.Fields(line)
		pkg, imports := fields[0], fields[1:]
		root, internal := rootOf(pkg)
		if !internal {
			continue
		}
		if root != "" && !knownRoots[root] {
			t.Errorf("%s: unclassified top-level package; add to knownRoots or remove from phase-1 tree", pkg)
			continue
		}
		for _, imp := range imports {
			assertImportAllowed(t, pkg, imp)
		}
	}
}

func assertImportAllowed(t *testing.T, from, to string) {
	t.Helper()
	if (under(from, module+"/fault") || under(from, module+"/identity")) &&
		(under(to, "net/http") || strings.HasPrefix(to, "github.com/danielgtaylor/huma") || strings.HasPrefix(to, "github.com/go-chi/chi") || under(to, "database/sql") || strings.HasPrefix(to, "github.com/uptrace/bun") || strings.HasPrefix(to, "github.com/jackc/pgx") || strings.HasPrefix(to, "github.com/spf13/cobra")) {
		t.Errorf("%s imports transport/database/runtime package %s", from, to)
	}
	toRoot, toInternal := rootOf(to)
	if !toInternal {
		return
	}
	if toRoot != "" && !knownRoots[toRoot] {
		t.Errorf("%s imports %s: target is outside phase-1 package graph", from, to)
		return
	}

	switch {
	case under(from, module+"/fault"), under(from, module+"/identity"):
		if under(to, module+"/httpx") || under(to, module+"/database") ||
			under(to, module+"/command") || under(to, module+"/cleanup") {
			t.Errorf("%s imports %s: fault/identity must not depend on HTTP, database, or runtime layers", from, to)
		}
	case under(from, module+"/config"), under(from, module+"/logging"), under(from, module+"/i18n"):
		if under(to, module+"/httpx") || under(to, module+"/database") ||
			under(to, module+"/command") || under(to, module+"/cleanup") || under(to, module+"/identity") ||
			under(to, module+"/fault") {
			t.Errorf("%s imports %s: config/logging/i18n must not depend on business or runtime layers", from, to)
		}
	case under(from, module+"/cleanup"):
		if under(to, module+"/database") || under(to, module+"/httpx") ||
			under(to, module+"/command") ||
			under(to, module+"/i18n") || under(to, module+"/identity") {
			t.Errorf("%s imports %s: cleanup must not depend on drivers or runtime layers", from, to)
		}
	case under(from, module+"/database"):
		if under(to, module+"/httpx") || under(to, module+"/command") {
			t.Errorf("%s imports %s: database must not depend on httpx/command", from, to)
		}
	case under(from, module+"/httpx"):
		if under(to, module+"/command") {
			t.Errorf("%s imports %s: httpx must not depend on command", from, to)
		}
	}
}

func TestNoPhase1ForbiddenDeps(t *testing.T) {
	// 只检查直接依赖；cockroachdb/errors 等允许依赖的传递闭包不计入违规。
	cmd := exec.Command("go", "list", "-m", "-f", `{{if not .Indirect}}{{.Path}}{{end}}`, "all")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("go list -m: %v", err)
	}
	for _, line := range strings.Split(string(out), "\n") {
		path := strings.TrimSpace(line)
		if path == "" || path == module {
			continue
		}
		for _, prefix := range forbiddenDepPrefixes {
			if path == prefix || strings.HasPrefix(path, prefix+"/") {
				t.Errorf("phase-1 module must not depend on later-form package %s", path)
			}
		}
	}
}

func TestNoEmptyPhase1Packages(t *testing.T) {
	root := moduleRoot(t)
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		if !knownRoots[name] || name == "internal" {
			continue
		}
		if !hasGoFiles(filepath.Join(root, name)) {
			t.Errorf("%s/: empty package directory is not allowed; create only with real code", name)
		}
	}
}

func moduleRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	dir := filepath.Dir(file)
	for {
		mod := filepath.Join(dir, "go.mod")
		if data, err := os.ReadFile(mod); err == nil && strings.Contains(string(data), "module "+module) {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("module root not found")
		}
		dir = parent
	}
}

func hasGoFiles(dir string) bool {
	var found bool
	_ = filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil || found {
			return err
		}
		if !d.IsDir() && strings.HasSuffix(d.Name(), ".go") {
			found = true
			return filepath.SkipAll
		}
		return nil
	})
	return found
}
