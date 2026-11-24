package check

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/cockroachdb/errors"

	"github.com/go-tenon/tenon/cmd/tenon/internal/project"
)

// IgnoreClock 写在一行的注释中时，CheckClock 不检查这一行。
const IgnoreClock = "clock-ignore"

// dbClock 匹配 SQL 中读取数据库当前时间的写法。
var dbClock = regexp.MustCompile(`(?i)\b(now|clock_timestamp|statement_timestamp|transaction_timestamp|sysdate|curdate|curtime|utc_timestamp|utc_date|utc_time|unix_timestamp|unixepoch)\s*\(|\b(current_timestamp|current_date|current_time|localtimestamp|localtime)\b|'now'`)

// builtinClockExempt 是框架内嵌的第三方迁移文件（表结构由上游库决定，例如 trpc-agent-go），默认豁免时钟检查。
var builtinClockExempt = map[string]bool{
	"2026_11_10_193936_create_agent_session_states.sql":       true,
	"2026_11_10_193937_create_agent_session_events.sql":       true,
	"2026_11_10_193938_create_agent_session_track_events.sql": true,
	"2026_11_10_193939_create_agent_session_summaries.sql":    true,
	"2026_11_10_193940_create_agent_app_states.sql":           true,
	"2026_11_10_193941_create_agent_user_states.sql":          true,
}

// CheckClock 检查项目中的 .sql 文件和非测试 .go 文件的字符串字面量，防止 SQL 直接读取数据库时钟：
// 时间应由 Go 的 time.Now 算出后作为参数传入，这样 testing/synctest 的假时钟对查询同样有效。
func CheckClock(root string, exempt []string) ([]Finding, error) {
	var out []Finding
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			if p == root {
				return nil
			}
			if project.SkipDir(d.Name()) || isFrontendTree(rel) || isExempt(rel, exempt) {
				return filepath.SkipDir
			}
			return nil
		}
		if isExempt(rel, exempt) {
			return nil
		}
		switch {
		case strings.HasSuffix(p, ".sql"):
			if builtinClockExempt[d.Name()] {
				return nil
			}
			found, err := scanSQLClock(p, rel)
			if err != nil {
				return err
			}
			out = append(out, found...)
		case strings.HasSuffix(p, ".go") && !strings.HasSuffix(p, "_test.go"):
			found, err := scanGoClock(p, rel)
			if err != nil {
				return err
			}
			out = append(out, found...)
		}
		return nil
	})
	return out, errors.Wrap(err, "check clock")
}

// isFrontendTree 报告目录 rel 是否属于前端、客户端或框架同步目录（不含 Go 业务代码和迁移）。
func isFrontendTree(rel string) bool {
	return rel == project.AppsDir || rel == project.ClientsDir || rel == "packages" || rel == "tenon"
}

// scanSQLClock 检查 SQL 文件中读取数据库时钟的行；包含 IgnoreClock 注释的行跳过。
func scanSQLClock(p, rel string) ([]Finding, error) {
	data, err := os.ReadFile(p)
	if err != nil {
		return nil, err
	}
	var out []Finding
	for i, line := range strings.Split(string(data), "\n") {
		if strings.Contains(line, IgnoreClock) {
			continue
		}
		code, _, _ := strings.Cut(line, "--")
		if m := dbClock.FindString(code); m != "" {
			out = append(out, Finding{
				Check:   "clock",
				File:    rel,
				Line:    i + 1,
				Message: fmt.Sprintf("%q reads the database clock; write a time computed in Go instead", m),
			})
		}
	}
	return out, nil
}

// scanGoClock 检查 Go 源文件中的字符串字面量是否包含数据库时钟函数；对应行带 IgnoreClock 注释时跳过。
func scanGoClock(p, rel string) ([]Finding, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, p, nil, parser.ParseComments)
	if err != nil {
		return nil, err
	}
	ignored := map[int]bool{}
	for _, cg := range f.Comments {
		for _, c := range cg.List {
			if strings.Contains(c.Text, IgnoreClock) {
				ignored[fset.Position(c.Pos()).Line] = true
			}
		}
	}
	var out []Finding
	ast.Inspect(f, func(n ast.Node) bool {
		if _, ok := n.(*ast.ImportSpec); ok {
			return false
		}
		lit, ok := n.(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			return true
		}
		s, err := strconv.Unquote(lit.Value)
		if err != nil {
			return true
		}
		startLine := fset.Position(lit.Pos()).Line
		if ignored[startLine] {
			return true
		}
		for offset, line := range strings.Split(s, "\n") {
			lineNo := startLine + offset
			if ignored[lineNo] {
				continue
			}
			if m := dbClock.FindString(line); m != "" {
				out = append(out, Finding{
					Check:   "clock",
					File:    rel,
					Line:    lineNo,
					Message: fmt.Sprintf("%q reads the database clock; pass time.Now() as a query argument instead", m),
				})
			}
		}
		return true
	})
	return out, nil
}
