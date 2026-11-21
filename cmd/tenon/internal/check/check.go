// Package check 实现 tenon check：静态检查项目是否遵守框架的约定，给本地提交前和 CI 使用。
package check

import (
	"fmt"
	"path"
	"slices"
	"strings"

	"github.com/go-tenon/tenon/cmd/tenon/internal/project"
)

// Names 是全部检查项的名称，按执行顺序排列。
var Names = project.CheckNames

// Finding 是一条检查发现的问题。
type Finding struct {
	Check   string // 检查项名称，见 Names
	File    string // 相对于项目根目录的路径（正斜杠）
	Line    int    // 行号，从 1 开始；与具体行无关时为 0
	Message string // 问题描述或触发规则的原始行内容
}

// String 按 "<file>:<line>: [<check>] <message>" 格式化，Line 为 0 时省略行号。
func (f Finding) String() string {
	if f.Line > 0 {
		return fmt.Sprintf("%s:%d: [%s] %s", f.File, f.Line, f.Check, f.Message)
	}
	return fmt.Sprintf("%s: [%s] %s", f.File, f.Check, f.Message)
}

// Run 运行项目的静态检查：only 非空时只运行指定的检查项，否则运行除 tenon.json 的 check.skip 之外的全部检查项。
func Run(root string, cfg *project.Config, only []string) ([]Finding, error) {
	selected, err := selectChecks(cfg, only)
	if err != nil {
		return nil, err
	}
	exempts := cfg.CheckExempt()
	var out []Finding
	for _, name := range selected {
		var found []Finding
		switch name {
		case "text":
			found, err = CheckText(root, cfg, exempts)
		case "clock":
			found, err = CheckClock(root, exempts)
		case "errors":
			found, err = CheckErrors(root, cfg, exempts)
		case "colors":
			found, err = CheckColors(root, cfg, exempts)
		case "deps":
			found, err = CheckDeps(root, cfg, exempts)
		}
		if err != nil {
			return nil, err
		}
		out = append(out, found...)
	}
	return out, nil
}

// selectChecks 返回本次要运行的检查项，保持 Names 的顺序；only 中有未知名称时返回错误。
func selectChecks(cfg *project.Config, only []string) ([]string, error) {
	if len(only) > 0 {
		for _, name := range only {
			if !slices.Contains(Names, name) {
				return nil, fmt.Errorf("unknown check %q: must be one of %s", name, strings.Join(Names, ", "))
			}
		}
		var out []string
		for _, name := range Names {
			if slices.Contains(only, name) {
				out = append(out, name)
			}
		}
		return out, nil
	}
	skip := cfg.CheckSkip()
	var out []string
	for _, name := range Names {
		if !slices.Contains(skip, name) {
			out = append(out, name)
		}
	}
	return out, nil
}

// isExempt 报告相对于项目根目录的正斜杠路径 rel 是否命中 patterns 中的豁免规则。
// 规则可以是精确路径、目录前缀（例如 migrations/legacy）或 path.Match 通配模式。
func isExempt(rel string, patterns []string) bool {
	rel = strings.TrimPrefix(path.Clean(rel), "./")
	for _, p := range patterns {
		p = strings.TrimSuffix(strings.TrimPrefix(path.Clean(p), "./"), "/")
		if p == "" {
			continue
		}
		if rel == p || strings.HasPrefix(rel, p+"/") {
			return true
		}
		if ok, _ := path.Match(p, rel); ok {
			return true
		}
		if ok, _ := path.Match(p, path.Base(rel)); ok {
			return true
		}
	}
	return false
}

// stripComments 把源码 src 中的注释换成空白（保留换行，行号不变），字符串和模板字符串中的内容保持原样；
// lineComments 为 true 时识别 // 单行注释（JS/TS），为 false 时只识别 /* */（CSS）。
// 同时返回注释中写着 ignoreMarker 的行（从 0 开始）。
func stripComments(src, ignoreMarker string, lineComments bool) (string, map[int]bool) {
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
		case c == '"' || c == '\'' || (lineComments && c == '`'):
			quote = c
			b.WriteByte(c)
		case lineComments && c == '/' && i+1 < len(src) && src[i+1] == '/':
			end := strings.IndexByte(src[i:], '\n')
			if end < 0 {
				end = len(src) - i
			}
			if ignoreMarker != "" && strings.Contains(src[i:i+end], ignoreMarker) {
				ignored[line] = true
			}
			i += end - 1
		case c == '/' && i+1 < len(src) && src[i+1] == '*':
			end := strings.Index(src[i+2:], "*/")
			if end < 0 {
				end = len(src) - i - 2
			}
			comment := src[i : i+2+end]
			if ignoreMarker != "" && strings.Contains(comment, ignoreMarker) {
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
