package uilayertest_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// layers 是 js/ui/src 下每个目录的层次：上层只引用同层或更低层的目录。blocks 和 features 同在最上层，
// 但彼此不引用：布局只描述样子，功能由应用放进布局。
var layers = map[string]int{
	"i18n":       0,
	"theme":      1,
	"app":        2,
	"components": 3,
	"blocks":     4,
	"features":   4,
}

var relImport = regexp.MustCompile(`(?m)^(?:import|export)\b[^"]*?from "(\.[^"]*)"|^import "(\.[^"]*)"`)

// TestLayers 检查 @go-tenon/ui 的分层：每个源文件都在某一层的目录中，相对导入只指向同层或更低层，
// blocks 和 features 之间不互相引用。根目录的 index.ts 汇总导出，不检查。
func TestLayers(t *testing.T) {
	t.Parallel()
	root := "../../../js/ui/src"
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !(strings.HasSuffix(path, ".ts") || strings.HasSuffix(path, ".tsx")) {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		if rel == "index.ts" {
			return nil
		}
		from := strings.Split(filepath.ToSlash(rel), "/")[0]
		fromLayer, ok := layers[from]
		if !ok {
			t.Errorf("%s: 不在任何一层的目录中（%v）", rel, keys())
			return nil
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, m := range relImport.FindAllStringSubmatch(string(src), -1) {
			spec := m[1] + m[2]
			target, _ := filepath.Rel(root, filepath.Join(filepath.Dir(path), spec))
			to := strings.Split(filepath.ToSlash(target), "/")[0]
			toLayer, ok := layers[to]
			switch {
			case !ok:
				t.Errorf("%s: 引用 %s，它不在任何一层的目录中", rel, spec)
			case toLayer > fromLayer:
				t.Errorf("%s: %s 引用了上层的 %s", rel, from, spec)
			case toLayer == fromLayer && from != to:
				t.Errorf("%s: %s 和 %s 互相不引用，引用了 %s", rel, from, to, spec)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// keys 返回各层的目录名，用于错误信息。
func keys() []string {
	out := make([]string, 0, len(layers))
	for k := range layers {
		out = append(out, k)
	}
	return out
}
