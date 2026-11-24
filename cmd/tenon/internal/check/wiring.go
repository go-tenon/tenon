package check

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/cockroachdb/errors"

	"github.com/go-tenon/tenon/cmd/tenon/internal/project"
)

// IgnoreWiring 写在一行的注释中时，CheckWiring 不检查这一行。
const IgnoreWiring = "wiring-ignore"

// defaultMaxBodyBytes 是 httpx.DefaultConfig 的默认请求体上限（1 MiB）。
const defaultMaxBodyBytes int64 = 1 << 20

// txStoreCtors 是提供 WithDB 的框架存储构造函数集合（包名 -> 函数名）。
var txStoreCtors = map[string]string{
	"apitoken": "NewStore",
	"authz":    "NewStore",
	"feature":  "NewStore",
	"login":    "New",
	"settings": "New",
}

// maxBodySite 记录一处把 MaxBodyBytes 设置为超过默认 1 MiB 的位置。
type maxBodySite struct {
	file  string
	line  int
	bytes int64
}

// CheckWiring 用静态分析检查项目中常见的框架装配遗漏和反模式：
//   - httpx.Serve 未传入 httpx.OnReady / httpcmd.ServeOptions，导致不停机升级提前上报就绪；
//   - 在 database.RunInTx 事务回调中用 tx 重新构造 store（apitoken/authz/feature/login/settings），丢弃缓存和凭据复查选项；
//   - 手写 "search.sync." 或 "search.reindex." 队列主题字符串（应当调用 Index.SyncTopic() / Index.ReindexTopic()）；
//   - 构造临时 pglimit.New(...).Cleanup / pglock.New(...).Cleanup（应当直接传入包级 Cleanup(db)）；
//   - 操作设置超过 1 MiB 的 MaxBodyBytes 但项目未配置 httpx.BodyLimitFor 或自定义 httpx.BodyLimit；
//   - 开启 SSR 的前端未在 src/start.ts 中安装 @go-tenon/start 的 requestMiddleware。
func CheckWiring(root string, cfg *project.Config, exempt []string) ([]Finding, error) {
	var out []Finding
	var bigBodies []maxBodySite
	hasBodyLimitOverride := false

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
		if !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") || isExempt(rel, exempt) {
			return nil
		}
		findings, sites, hasOverride, err := scanGoWiring(p, rel)
		if err != nil {
			return err
		}
		out = append(out, findings...)
		bigBodies = append(bigBodies, sites...)
		if hasOverride {
			hasBodyLimitOverride = true
		}
		return nil
	})
	if err != nil {
		return nil, errors.Wrap(err, "check wiring")
	}

	if !hasBodyLimitOverride {
		for _, site := range bigBodies {
			out = append(out, Finding{
				Check:   "wiring",
				File:    site.file,
				Line:    site.line,
				Message: fmt.Sprintf("MaxBodyBytes (%d) exceeds default 1 MiB httpx.BodyLimit, and no httpx.BodyLimitFor was found; configure httpx.BodyLimitFor on httpx.Defaults", site.bytes),
			})
		}
	}

	if cfg != nil {
		ssrFindings, err := checkSSRStartMiddleware(root, cfg, exempt)
		if err != nil {
			return nil, errors.Wrap(err, "check wiring")
		}
		out = append(out, ssrFindings...)
	}
	return out, nil
}

// scanGoWiring 解析单个 Go 文件并检查各项框架装配规则。
func scanGoWiring(p, rel string) ([]Finding, []maxBodySite, bool, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, p, nil, parser.ParseComments)
	if err != nil {
		return nil, nil, false, err
	}
	ignored := ignoredLines(fset, f, IgnoreWiring)

	var out []Finding
	var bigBodies []maxBodySite
	hasBodyLimitOverride := false

	ast.Inspect(f, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.CallExpr:
			line := fset.Position(node.Pos()).Line
			if isPkgCall(node, "httpx", "BodyLimitFor") || isPkgCall(node, "httpx", "BodyLimit") {
				hasBodyLimitOverride = true
			}
			if ignored[line] {
				return true
			}
			if isPkgCall(node, "httpx", "Serve") && !hasServeReloadOption(node) {
				out = append(out, Finding{
					Check:   "wiring",
					File:    rel,
					Line:    line,
					Message: "httpx.Serve is missing httpx.OnReady / httpcmd.ServeOptions; pass httpcmd.ServeOptions(d)... or use httpcmd.Serve for zero-downtime reload",
				})
			}
			if isPkgCall(node, "database", "RunInTx") {
				out = append(out, scanRunInTxBody(fset, rel, node, ignored)...)
			}
			if topic, ok := hardcodedSearchTopic(node); ok {
				out = append(out, Finding{
					Check:   "wiring",
					File:    rel,
					Line:    line,
					Message: fmt.Sprintf("hardcoded search queue topic %q; use idx.SyncTopic() or idx.ReindexTopic()", topic),
				})
			}
		case *ast.SelectorExpr:
			line := fset.Position(node.Pos()).Line
			if ignored[line] {
				return true
			}
			if pkg, ok := throwawayCleanupPkg(node); ok {
				out = append(out, Finding{
					Check:   "wiring",
					File:    rel,
					Line:    line,
					Message: fmt.Sprintf("throwaway %s.New(...).Cleanup; pass %s.Cleanup(db) directly to schedule.Cleanup", pkg, pkg),
				})
			}
		case *ast.AssignStmt:
			line := fset.Position(node.Pos()).Line
			if ignored[line] {
				return true
			}
			for i, lhs := range node.Lhs {
				if i >= len(node.Rhs) {
					continue
				}
				if sel, ok := lhs.(*ast.SelectorExpr); ok && sel.Sel.Name == "MaxBodyBytes" {
					if v, ok := evalPositiveInt(node.Rhs[i]); ok && v > defaultMaxBodyBytes {
						bigBodies = append(bigBodies, maxBodySite{file: rel, line: line, bytes: v})
					}
				}
			}
		case *ast.KeyValueExpr:
			line := fset.Position(node.Pos()).Line
			if ignored[line] {
				return true
			}
			if key, ok := node.Key.(*ast.Ident); ok && key.Name == "MaxBodyBytes" {
				if v, ok := evalPositiveInt(node.Value); ok && v > defaultMaxBodyBytes {
					bigBodies = append(bigBodies, maxBodySite{file: rel, line: line, bytes: v})
				}
			}
		}
		return true
	})
	return out, bigBodies, hasBodyLimitOverride, nil
}

// scanRunInTxBody 检查 database.RunInTx 回调内部是否用事务参数重新构造了带 WithDB 的框架存储。
func scanRunInTxBody(fset *token.FileSet, rel string, call *ast.CallExpr, ignored map[int]bool) []Finding {
	if len(call.Args) < 3 {
		return nil
	}
	fn, ok := call.Args[2].(*ast.FuncLit)
	if !ok || fn.Type.Params == nil {
		return nil
	}
	txName := secondParamName(fn.Type.Params)
	if txName == "" || txName == "_" {
		return nil
	}
	var out []Finding
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		c, ok := n.(*ast.CallExpr)
		if !ok || len(c.Args) == 0 {
			return true
		}
		line := fset.Position(c.Pos()).Line
		if ignored[line] {
			return true
		}
		sel, ok := c.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		pkg, ok := sel.X.(*ast.Ident)
		if !ok {
			return true
		}
		ctor, match := txStoreCtors[pkg.Name]
		if !match || sel.Sel.Name != ctor {
			return true
		}
		arg0, ok := c.Args[0].(*ast.Ident)
		if !ok || arg0.Name != txName {
			return true
		}
		out = append(out, Finding{
			Check:   "wiring",
			File:    rel,
			Line:    line,
			Message: fmt.Sprintf("%s.%s(%s, ...) inside database.RunInTx drops configured options (cache, credentials, crypto); reuse the shared instance via .WithDB(%s)", pkg.Name, ctor, txName, txName),
		})
		return true
	})
	return out
}

// checkSSRStartMiddleware 检查开启 SSR 的前端是否在 src/start.ts 中安装了 requestMiddleware。
func checkSSRStartMiddleware(root string, cfg *project.Config, exempt []string) ([]Finding, error) {
	var out []Finding
	for _, f := range cfg.Frontends {
		if !f.SSR {
			continue
		}
		relTS := f.Dir + "/src/start.ts"
		relTSX := f.Dir + "/src/start.tsx"
		if isExempt(f.Dir, exempt) || isExempt(relTS, exempt) || isExempt(relTSX, exempt) {
			continue
		}
		target := relTS
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(relTS)))
		if errors.Is(err, fs.ErrNotExist) {
			data, err = os.ReadFile(filepath.Join(root, filepath.FromSlash(relTSX)))
			if err == nil {
				target = relTSX
			}
		}
		if errors.Is(err, fs.ErrNotExist) {
			out = append(out, Finding{
				Check:   "wiring",
				File:    relTS,
				Message: fmt.Sprintf("SSR frontend %q is missing src/start.ts with requestMiddleware from @go-tenon/start", f.Name),
			})
			continue
		}
		if err != nil {
			return nil, err
		}
		clean, ignored := stripComments(string(data), IgnoreWiring, true)
		if len(ignored) > 0 {
			continue
		}
		if !strings.Contains(clean, "requestMiddleware") {
			out = append(out, Finding{
				Check:   "wiring",
				File:    target,
				Message: fmt.Sprintf("SSR frontend %q must install requestMiddleware from @go-tenon/start in %s to forward headers and isolate per-request session/i18n state", f.Name, target),
			})
		}
	}
	return out, nil
}

// ignoredLines 返回文件 f 中注释包含 marker 的行号集合（从 1 开始）。
func ignoredLines(fset *token.FileSet, f *ast.File, marker string) map[int]bool {
	out := map[int]bool{}
	for _, cg := range f.Comments {
		for _, c := range cg.List {
			if strings.Contains(c.Text, marker) {
				out[fset.Position(c.Pos()).Line] = true
			}
		}
	}
	return out
}

// isPkgCall 报告 call 是否是 pkg.fn(...) 调用。
func isPkgCall(call *ast.CallExpr, pkg, fn string) bool {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != fn {
		return false
	}
	id, ok := sel.X.(*ast.Ident)
	return ok && id.Name == pkg
}

// hasServeReloadOption 报告 httpx.Serve 的参数中是否包含热重载选项（OnReady、ServeOptions 或外部传入的切片展开）。
func hasServeReloadOption(call *ast.CallExpr) bool {
	if call.Ellipsis.IsValid() {
		return true
	}
	if len(call.Args) <= 4 {
		return false
	}
	for _, arg := range call.Args[4:] {
		found := false
		ast.Inspect(arg, func(n ast.Node) bool {
			if sel, ok := n.(*ast.SelectorExpr); ok && (sel.Sel.Name == "OnReady" || sel.Sel.Name == "ServeOptions") {
				found = true
				return false
			}
			return true
		})
		if found {
			return true
		}
	}
	return false
}

// hardcodedSearchTopic 报告 call 是否是用硬编码 "search.sync." / "search.reindex." 构造的 queue.Topic。
func hardcodedSearchTopic(call *ast.CallExpr) (string, bool) {
	if len(call.Args) != 1 {
		return "", false
	}
	idx, ok := call.Fun.(*ast.IndexExpr)
	if !ok {
		return "", false
	}
	sel, ok := idx.X.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "Topic" {
		return "", false
	}
	pkg, ok := sel.X.(*ast.Ident)
	if !ok || pkg.Name != "queue" {
		return "", false
	}
	lit, ok := call.Args[0].(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return "", false
	}
	val, err := strconv.Unquote(lit.Value)
	if err != nil {
		return "", false
	}
	if strings.HasPrefix(val, "search.sync.") || strings.HasPrefix(val, "search.reindex.") {
		return val, true
	}
	return "", false
}

// throwawayCleanupPkg 报告 sel 是否为 pglimit.New(...).Cleanup 或 pglock.New(...).Cleanup。
func throwawayCleanupPkg(sel *ast.SelectorExpr) (string, bool) {
	if sel.Sel.Name != "Cleanup" {
		return "", false
	}
	call, ok := sel.X.(*ast.CallExpr)
	if !ok {
		return "", false
	}
	if isPkgCall(call, "pglimit", "New") {
		return "pglimit", true
	}
	if isPkgCall(call, "pglock", "New") {
		return "pglock", true
	}
	return "", false
}

// secondParamName 返回函数参数列表中第二个参数的变量名。
func secondParamName(params *ast.FieldList) string {
	idx := 0
	for _, field := range params.List {
		if len(field.Names) == 0 {
			idx++
			continue
		}
		for _, name := range field.Names {
			if idx == 1 {
				return name.Name
			}
			idx++
		}
	}
	return ""
}

// evalPositiveInt 求值简单的正整数常量表达式（整数、乘法、左移），用于识别 10<<20、10*1024*1024 等写法。
func evalPositiveInt(expr ast.Expr) (int64, bool) {
	switch e := expr.(type) {
	case *ast.ParenExpr:
		return evalPositiveInt(e.X)
	case *ast.BasicLit:
		if e.Kind != token.INT {
			return 0, false
		}
		v, err := strconv.ParseInt(e.Value, 0, 64)
		return v, err == nil && v > 0
	case *ast.BinaryExpr:
		l, lok := evalPositiveInt(e.X)
		r, rok := evalPositiveInt(e.Y)
		if !lok || !rok {
			return 0, false
		}
		switch e.Op {
		case token.SHL:
			if r >= 0 && r < 60 {
				return l << r, true
			}
		case token.MUL:
			return l * r, true
		}
	}
	return 0, false
}
