package check

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/cockroachdb/errors"

	"github.com/go-tenon/tenon/cmd/tenon/internal/project"
)

// IgnoreColor 写在一行的注释中时，CheckColors 不检查这一行。
const IgnoreColor = "color-ignore"

// colorFunc 匹配 CSS 颜色函数（rgb、rgba、hsl、hsla、oklch、oklab）。
var colorFunc = regexp.MustCompile(`(?i)\b(?:rgba?|hsla?|oklch|oklab)\s*\(`)

// hexColor 匹配十六进制颜色（3、4、6 或 8 位），排除 URL 路径或标识符后紧跟的 #。
var hexColor = regexp.MustCompile(`(?:^|[^a-zA-Z0-9_/:.?&=-])#([0-9a-fA-F]{8}|[0-9a-fA-F]{6}|[0-9a-fA-F]{4}|[0-9a-fA-F]{3})\b`)

// CheckColors 检查每个前端 src 目录中的 .ts、.tsx、.css 文件，找出写死的十六进制颜色和 rgb()/hsl() 等颜色函数：
// 颜色应从 Mantine 主题色板或 --tn-* CSS 变量中取，随品牌色和暗色模式变化。
// 生成的文件（*.gen.*）和测试（*.test.*）不检查。
func CheckColors(root string, cfg *project.Config, exempt []string) ([]Finding, error) {
	var out []Finding
	for _, f := range cfg.Frontends {
		found, err := ScanColors(filepath.Join(root, f.Dir, "src"))
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

// ScanColors 检查目录 dir 中的 .ts、.tsx、.css 文件（规则同 CheckColors），返回的 File 相对于 dir。目录不存在时返回空。
func ScanColors(dir string) ([]Finding, error) {
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
		isCSS := strings.HasSuffix(name, ".css")
		isTS := strings.HasSuffix(name, ".ts") || strings.HasSuffix(name, ".tsx")
		if (!isCSS && !isTS) || strings.Contains(name, ".gen.") || strings.Contains(name, ".test.") {
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
		for _, x := range scanColorSource(string(data), !isCSS) {
			x.File = filepath.ToSlash(rel)
			out = append(out, x)
		}
		return nil
	})
	return out, errors.Wrap(err, "check colors")
}

// scanColorSource 检查源码 src 中注释以外的硬编码颜色；isTS 为 true 时按 JS/TS 语法提取字符串再检查，
// 避免把类私有字段（#fee）误判为颜色；为 false 时按 CSS 逐行检查。
func scanColorSource(src string, isTS bool) []Finding {
	code, ignored := stripComments(src, IgnoreColor, isTS)
	lines := strings.Split(code, "\n")
	original := strings.Split(src, "\n")
	var out []Finding
	if isTS {
		stringLines := extractTSStringLines(code)
		for lineNo := range len(lines) {
			if ignored[lineNo] {
				continue
			}
			text, ok := stringLines[lineNo]
			if !ok {
				continue
			}
			if m := findHardcodedColor(text); m != "" {
				out = append(out, Finding{
					Check:   "colors",
					Line:    lineNo + 1,
					Message: fmt.Sprintf("hard-coded color %q: %s", m, strings.TrimSpace(original[lineNo])),
				})
			}
		}
		return out
	}
	for i, l := range lines {
		if ignored[i] {
			continue
		}
		if m := findHardcodedColor(l); m != "" {
			out = append(out, Finding{
				Check:   "colors",
				Line:    i + 1,
				Message: fmt.Sprintf("hard-coded color %q: %s", m, strings.TrimSpace(original[i])),
			})
		}
	}
	return out
}

// extractTSStringLines 从已去除注释的 TS/TSX 代码中按行号（从 0 开始）提取所有字符串和模板字符串的内容。
func extractTSStringLines(code string) map[int]string {
	byLine := map[int][]byte{}
	line := 0
	var quote byte
	for i := 0; i < len(code); i++ {
		c := code[i]
		if c == '\n' {
			line++
			continue
		}
		if quote == 0 {
			if c == '"' || c == '\'' || c == '`' {
				quote = c
				byLine[line] = append(byLine[line], ' ')
			}
			continue
		}
		if c == '\\' && i+1 < len(code) {
			i++
			if code[i] == '\n' {
				line++
			}
			continue
		}
		if c == quote {
			quote = 0
			continue
		}
		byLine[line] = append(byLine[line], c)
	}
	out := make(map[int]string, len(byLine))
	for k, v := range byLine {
		out[k] = string(v)
	}
	return out
}

// findHardcodedColor 返回 s 中第一个硬编码的颜色写法，没有时返回空。
func findHardcodedColor(s string) string {
	if m := colorFunc.FindString(s); m != "" {
		return strings.TrimSpace(m)
	}
	if m := hexColor.FindStringSubmatch(s); len(m) == 2 {
		return "#" + m[1]
	}
	return ""
}
