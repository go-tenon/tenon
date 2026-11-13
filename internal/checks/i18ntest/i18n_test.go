package i18ntest_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"go.yaml.in/yaml/v4"
)

// define 匹配不在注释中的 errorx.Define 调用。
var define = regexp.MustCompile(`(?m)^[^/\n]*errorx\.Define\(errorx\.\w+,\s*"([^"]+)"\)`)

// TestErrorCodesTranslated 确保框架定义的每个业务错误码在内置的每种语言中都有翻译，
// 否则错误响应的 detail 只有英文的状态码描述，例如 Unauthorized。
func TestErrorCodesTranslated(t *testing.T) {
	root := "../../.."
	var codes []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case "testdata", "internal", "node_modules", ".git", "example":
				if path != root {
					return filepath.SkipDir
				}
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, m := range define.FindAllSubmatch(src, -1) {
			codes = append(codes, string(m[1]))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(codes) < 70 {
		t.Fatalf("found only %d error codes, the scan is broken", len(codes))
	}
	for _, locale := range []string{"en", "zh-CN"} {
		b, err := os.ReadFile(filepath.Join(root, "i18n", "locales", locale+".yaml"))
		if err != nil {
			t.Fatal(err)
		}
		var messages map[string]string
		if err := yaml.Unmarshal(b, &messages); err != nil {
			t.Fatal(err)
		}
		for _, code := range slices.Compact(slices.Sorted(slices.Values(codes))) {
			if messages[code] == "" {
				t.Errorf("%s.yaml: no message for %s", locale, code)
			}
		}
	}
}
