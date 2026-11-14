package doclinks_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var (
	fence  = regexp.MustCompile("(?ms)^```.*?^```")
	code   = regexp.MustCompile("`[^`\n]*`")
	link   = regexp.MustCompile(`\]\(([^)\s]+)\)`)
	anchor = regexp.MustCompile(`<a id="([^"]+)"></a>`)
)

// TestRelativeLinks 检查 README.md、DEVELOPMENT.md 和 docs 下全部文档中的相对链接：指向的文件存在，
// 带锚点时目标文档中有 <a id="锚点"></a>。代码块和行内代码中的内容跳过。
func TestRelativeLinks(t *testing.T) {
	root := "../../.."
	files := []string{filepath.Join(root, "README.md"), filepath.Join(root, "DEVELOPMENT.md")}
	err := filepath.WalkDir(filepath.Join(root, "docs"), func(path string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.HasSuffix(path, ".md") {
			files = append(files, path)
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	anchors := map[string]map[string]bool{}
	read := func(path string) string {
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	anchorsOf := func(path string) map[string]bool {
		if a, ok := anchors[path]; ok {
			return a
		}
		a := map[string]bool{}
		for _, m := range anchor.FindAllStringSubmatch(read(path), -1) {
			a[m[1]] = true
		}
		anchors[path] = a
		return a
	}
	for _, file := range files {
		text := code.ReplaceAllString(fence.ReplaceAllString(read(file), ""), "")
		for _, m := range link.FindAllStringSubmatch(text, -1) {
			target := m[1]
			if strings.Contains(target, "://") || strings.HasPrefix(target, "mailto:") {
				continue
			}
			path, frag, _ := strings.Cut(target, "#")
			dest := file
			if path != "" {
				dest = filepath.Join(filepath.Dir(file), path)
				if _, err := os.Stat(dest); err != nil {
					t.Errorf("%s: broken link %s", file, target)
					continue
				}
			}
			if frag != "" && strings.HasSuffix(dest, ".md") && !anchorsOf(dest)[frag] {
				t.Errorf("%s: no anchor %q in %s", file, frag, dest)
			}
		}
	}
}
