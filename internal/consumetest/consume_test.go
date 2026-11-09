//go:build integration

package consumetest_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestExternalModuleConsumesFramework 在临时独立 module 中消费框架公开包。
// 发布前使用 replace 指向本仓库候选；打 tag 后应改为真实版本并去掉 replace。
func TestExternalModuleConsumesFramework(t *testing.T) {
	root := moduleRoot(t)
	dir := t.TempDir()

	main := `package main

import (
	"fmt"

	"github.com/ryan053857/mygoravel/cleanup"
	"github.com/ryan053857/mygoravel/config"
	"github.com/ryan053857/mygoravel/fault"
	"github.com/ryan053857/mygoravel/httpx/rest"
	"github.com/ryan053857/mygoravel/identity"
	"github.com/ryan053857/mygoravel/logging"
)

func main() {
	_ = cleanup.Stack{}
	_ = config.Secret("x")
	_ = fault.Define(fault.NotFound, "demo.not_found")
	_ = identity.Subject{ID: "u"}
	_ = logging.New(logging.Options{})
	api := rest.New(rest.Options{Title: "consumer", Mode: rest.ModeOpenAPI})
	b, err := api.OpenAPIJSON()
	if err != nil {
		panic(err)
	}
	fmt.Print(len(b) > 0)
}
`
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(main), 0o644); err != nil {
		t.Fatal(err)
	}
	mod := "module consumer.example\n\ngo 1.27.3\n\nrequire github.com/ryan053857/mygoravel v0.0.0\n\nreplace github.com/ryan053857/mygoravel => " +
		filepath.ToSlash(root) + "\n"
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(mod), 0o644); err != nil {
		t.Fatal(err)
	}

	tidy := exec.Command("go", "mod", "tidy")
	tidy.Dir = dir
	tidy.Env = append(os.Environ(), "GOWORK=off")
	if out, err := tidy.CombinedOutput(); err != nil {
		t.Fatalf("tidy: %v\n%s", err, out)
	}
	build := exec.Command("go", "run", ".")
	build.Dir = dir
	build.Env = append(os.Environ(), "GOWORK=off")
	out, err := build.CombinedOutput()
	if err != nil {
		t.Fatalf("run: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "true") {
		t.Fatalf("unexpected output %q", out)
	}
}

func moduleRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("caller")
	}
	dir := filepath.Dir(file)
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			data, _ := os.ReadFile(filepath.Join(dir, "go.mod"))
			if strings.Contains(string(data), "module github.com/ryan053857/mygoravel") {
				return dir
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("root not found")
		}
		dir = parent
	}
}
