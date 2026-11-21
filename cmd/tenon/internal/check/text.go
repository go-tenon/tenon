package check

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/cockroachdb/errors"

	"github.com/go-tenon/tenon/cmd/tenon/internal/project"
)

// IgnoreText 写在一行的注释中时，CheckText 不检查这一行，例如 console.warn 中给开发者看的中文。
const IgnoreText = "i18n-ignore"

// CheckText 检查每个前端 src 目录中的 .ts、.tsx 文件，找出注释以外写着汉字的行：界面文字应当写在
// locales/<前端>.<语言>.yaml 中，用 useT 取出。生成的文件（*.gen.ts）、测试（*.test.ts、*.test.tsx）不检查。
func CheckText(root string, cfg *project.Config, exempt []string) ([]Finding, error) {
	var out []Finding
	for _, f := range cfg.Frontends {
		found, err := ScanText(filepath.Join(root, f.Dir, "src"))
		if err != nil {
			return nil, err
		}
		for _, x := range found {
			rel, err := filepath.Rel(root, filepath.Join(root, f.Dir, "src", x.File))
			if err != nil {
				return nil, err
			}
			x.File = filepath.ToSlash(rel)
			if isExempt(x.File, exempt) {
				continue
			}
			out = append(out, x)
		}
	}
	return out, nil
}

// ScanText 检查目录 dir 中的 .ts、.tsx 文件（规则同 CheckText），返回的 File 相对于 dir。目录不存在时返回空。
func ScanText(dir string) ([]Finding, error) {
	var out []Finding
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if errors.Is(err, fs.ErrNotExist) && p == dir {
			return filepath.SkipDir
		}
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == "node_modules" {
				return filepath.SkipDir
			}
			return nil
		}
		name := d.Name()
		if !(strings.HasSuffix(name, ".ts") || strings.HasSuffix(name, ".tsx")) ||
			strings.Contains(name, ".gen.") || strings.Contains(name, ".test.") {
			return nil
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		for _, x := range scanTextSource(string(data)) {
			x.File = filepath.ToSlash(rel)
			out = append(out, x)
		}
		return nil
	})
	return out, errors.Wrap(err, "check text")
}

// scanTextSource 返回 src 中注释以外有汉字的行；注释中写了 IgnoreText 的行不算。
func scanTextSource(src string) []Finding {
	code, ignored := stripComments(src, IgnoreText, true)
	lines := strings.Split(code, "\n")
	original := strings.Split(src, "\n")
	var out []Finding
	for i, l := range lines {
		if ignored[i] || !strings.ContainsFunc(l, func(r rune) bool { return unicode.Is(unicode.Han, r) }) {
			continue
		}
		out = append(out, Finding{Check: "text", Line: i + 1, Message: strings.TrimSpace(original[i])})
	}
	return out
}
