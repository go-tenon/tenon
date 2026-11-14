package doctest_test

import (
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// standaloneDirs 是已按参考页写法改写的文档目录或文件。这些文档中以 package 或构建标签开头的
// 代码块都是完整文件，必须能单独编译，不依赖其他代码块或其他文档。
var standaloneDirs = []string{"concepts", "general", "http", "responses", "database", "testing", "installation",
	"libraries"}

// TestStandaloneBlocks 把每个完整文件放进临时 module 中自己的目录，用 go vet 检查（包含编译）。
func TestStandaloneBlocks(t *testing.T) {
	root, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	n := 0
	for _, d := range standaloneDirs {
		docs := filepath.Join(root, "docs", d)
		err := filepath.WalkDir(docs, func(path string, e fs.DirEntry, err error) error {
			if err != nil || e.IsDir() || !strings.HasSuffix(path, ".md") {
				return err
			}
			b, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			rel, _ := filepath.Rel(filepath.Join(root, "docs"), path)
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
	}
	if n == 0 {
		t.Skip("no complete files")
	}

	writeFile(t, dir, "go.mod", "module example.com/docs\n\ngo 1.27.3\n\nrequire github.com/go-tenon/tenon v0.0.0\n\nreplace github.com/go-tenon/tenon => "+root+"\n")
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
