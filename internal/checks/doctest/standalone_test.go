package doctest_test

import (
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var goBlock = regexp.MustCompile("(?s)```go\n(.*?)```")

// TestStandaloneBlocks 编译 docs 中（入门教程除外）每个以 package 或构建标签开头的代码块：
// 它们都是完整文件，必须能单独编译，自成一体。每个代码块放进临时 module 中
// 自己的目录，用 go vet 检查（包含编译）。入门教程的文件由 cmd/tenon 的 TestTutorial 按章节验证。
func TestStandaloneBlocks(t *testing.T) {
	root, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	n := 0
	docs := filepath.Join(root, "docs")
	err = filepath.WalkDir(docs, func(path string, e fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if e.IsDir() && e.Name() == "tutorial" {
			return filepath.SkipDir
		}
		if e.IsDir() || !strings.HasSuffix(path, ".md") {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(docs, path)
		for i, m := range goBlock.FindAllStringSubmatch(string(b), -1) {
			body := m[1]
			if !strings.HasPrefix(body, "package ") && !strings.HasPrefix(body, "//go:build ") {
				continue
			}
			name := "x.go"
			if strings.Contains(body, "\nfunc Test") {
				name = "x_test.go"
			}
			pkg := strings.NewReplacer("/", "_", ".md", "", "-", "_").Replace(rel)
			blockDir := filepath.Join(pkg, fmt.Sprintf("b%d", i))
			writeFile(t, dir, filepath.Join(blockDir, name), body)
			embedPlaceholders(t, filepath.Join(dir, blockDir), body)
			n++
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if n == 0 {
		t.Skip("no complete files")
	}

	writeFile(t, dir, "go.mod", "module example.com/docs\n\ngo 1.27.4\n\nrequire github.com/go-tenon/tenon v0.0.0\n\nreplace github.com/go-tenon/tenon => "+root+"\n")
	sum, err := os.ReadFile(filepath.Join(root, "go.sum"))
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, dir, "go.sum", string(sum))

	cmd := exec.Command("go", "vet", "-tags", "integration", "./...")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOFLAGS=-mod=mod", "GOWORK=off")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go vet: %v\n%s", err, out)
	}
}

// embedPlaceholders 为代码块中 //go:embed 引用的文件和目录创建占位文件，让它能编译。
func embedPlaceholders(t *testing.T, dir, body string) {
	t.Helper()
	for _, line := range strings.Split(body, "\n") {
		patterns, ok := strings.CutPrefix(strings.TrimSpace(line), "//go:embed ")
		if !ok {
			continue
		}
		for _, p := range strings.Fields(patterns) {
			p = strings.TrimPrefix(p, "all:")
			if strings.ContainsAny(p, "*?[") {
				writeFile(t, dir, strings.NewReplacer("*", "x", "?", "x").Replace(p), "")
			} else {
				writeFile(t, dir, filepath.Join(p, "placeholder.txt"), "")
			}
		}
	}
}

func writeFile(t *testing.T, dir, path, content string) {
	t.Helper()
	full := filepath.Join(dir, path)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
