package dev

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

// TextFinding 是一行写死在代码中的界面文字。
type TextFinding struct {
	File string // 相对于项目根目录的路径
	Line int    // 行号，从 1 开始
	Text string // 这一行去掉首尾空白后的内容
}

// CheckText 检查每个前端 src 目录中的 .ts、.tsx 文件，找出注释以外写着汉字的行：界面文字应当写在
// locales/<前端>.<语言>.yaml 中，用 i18n.t 取出。生成的文件（*.gen.ts）、测试（*.test.ts、*.test.tsx）不检查。
func CheckText(root string, cfg *project.Config) ([]TextFinding, error) {
	var out []TextFinding
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
			out = append(out, x)
		}
	}
	return out, nil
}

// ScanText 检查目录 dir 中的 .ts、.tsx 文件（规则同 CheckText），返回的 File 相对于 dir。目录不存在时返回空。
func ScanText(dir string) ([]TextFinding, error) {
	var out []TextFinding
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if errors.Is(err, fs.ErrNotExist) && path == dir {
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
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		for _, x := range scanSource(string(data)) {
			x.File = filepath.ToSlash(rel)
			out = append(out, x)
		}
		return nil
	})
	return out, errors.Wrap(err, "check text")
}

// scanSource 返回 src 中注释以外有汉字的行；注释中写了 IgnoreText 的行不算。
func scanSource(src string) []TextFinding {
	code, ignored := stripComments(src)
	lines := strings.Split(code, "\n")
	original := strings.Split(src, "\n")
	var out []TextFinding
	for i, l := range lines {
		if ignored[i] || !strings.ContainsFunc(l, func(r rune) bool { return unicode.Is(unicode.Han, r) }) {
			continue
		}
		out = append(out, TextFinding{Line: i + 1, Text: strings.TrimSpace(original[i])})
	}
	return out
}

// stripComments 把 src 中的 // 和 /* */ 注释换成空白（保留换行，行号不变），字符串和模板字符串中的内容保持原样；
// 同时返回注释中写着 IgnoreText 的行（从 0 开始）。正则表达式字面量按代码处理。
func stripComments(src string) (string, map[int]bool) {
	var b strings.Builder
	ignored := map[int]bool{}
	line := 0
	var quote byte // 当前所在字符串的引号；0 表示不在字符串中
	for i := 0; i < len(src); i++ {
		c := src[i]
		if c == '\n' {
			line++
		}
		if quote != 0 {
			b.WriteByte(c)
			if c == '\\' && i+1 < len(src) {
				i++
				if src[i] == '\n' {
					line++
				}
				b.WriteByte(src[i])
			} else if c == quote {
				quote = 0
			}
			continue
		}
		switch {
		case c == '"' || c == '\'' || c == '`':
			quote = c
			b.WriteByte(c)
		case c == '/' && i+1 < len(src) && src[i+1] == '/':
			end := strings.IndexByte(src[i:], '\n')
			if end < 0 {
				end = len(src) - i
			}
			if strings.Contains(src[i:i+end], IgnoreText) {
				ignored[line] = true
			}
			i += end - 1
		case c == '/' && i+1 < len(src) && src[i+1] == '*':
			end := strings.Index(src[i+2:], "*/")
			if end < 0 {
				end = len(src) - i - 2
			}
			comment := src[i : i+2+end]
			if strings.Contains(comment, IgnoreText) {
				ignored[line] = true
			}
			for _, r := range comment {
				if r == '\n' {
					b.WriteByte('\n')
					line++
				}
			}
			i += 2 + end + 1
		default:
			b.WriteByte(c)
		}
	}
	return b.String(), ignored
}
