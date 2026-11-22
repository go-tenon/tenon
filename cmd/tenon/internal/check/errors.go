package check

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/cockroachdb/errors"
	"go.yaml.in/yaml/v4"

	"github.com/go-tenon/tenon/cmd/tenon/internal/dev"
	"github.com/go-tenon/tenon/cmd/tenon/internal/project"
)

// errorCode 是代码中用 errorx.Define 定义的一条业务错误码。
type errorCode struct {
	code       string
	hasMessage bool // 是否在 errorx.Define 中内联了源语言默认提示
	file       string
	line       int
}

// CheckErrors 检查项目中用 errorx.Define 定义的每个业务错误码是否在服务端翻译文件中齐全：
//   - 多语言项目（tenon.json 的 languages 多于一种）检查每种附加语言的 locales/<语言>.yaml，即使错误带有源语言默认提示也须补齐译文；
//   - 源语言的 locales/<源语言>.yaml 存在时，检查没有内联默认提示的错误码是否都有译文；
//   - 单语言项目尚未创建服务端翻译文件时返回空。
func CheckErrors(root string, cfg *project.Config, exempt []string) ([]Finding, error) {
	locales, err := loadServerLocales(root, cfg)
	if err != nil {
		return nil, err
	}
	if cfg != nil && cfg.MultiLanguage() {
		if locales == nil {
			locales = map[string]map[string]bool{}
		}
		for _, lang := range cfg.AllLanguages()[1:] {
			yamlPath := dev.LocalesDir + "/" + lang + ".yaml"
			ymlPath := dev.LocalesDir + "/" + lang + ".yml"
			if _, ok := locales[yamlPath]; !ok {
				if _, ok := locales[ymlPath]; !ok {
					locales[yamlPath] = map[string]bool{}
				}
			}
		}
	}
	if len(locales) == 0 {
		return nil, nil
	}
	codes, err := scanErrorCodes(root, exempt)
	if err != nil {
		return nil, err
	}
	files := make([]string, 0, len(locales))
	for file := range locales {
		files = append(files, file)
	}
	slices.Sort(files)

	sourceLang := cfg.SourceLanguage()
	var out []Finding
	for _, file := range files {
		msgs := locales[file]
		stem := strings.TrimSuffix(filepath.Base(file), filepath.Ext(file))
		isSource := strings.EqualFold(stem, sourceLang)
		seen := map[string]bool{}
		for _, ec := range codes {
			if seen[ec.code] {
				continue
			}
			seen[ec.code] = true
			if isSource && ec.hasMessage {
				continue
			}
			if !msgs[ec.code] {
				out = append(out, Finding{
					Check:   "errors",
					File:    file,
					Message: fmt.Sprintf("missing translation for %q (defined at %s:%d)", ec.code, ec.file, ec.line),
				})
			}
		}
	}
	return out, nil
}

// loadServerLocales 读取项目 locales 目录中的服务端翻译文件（<语言>.yaml 或 .yml，排除前端的 <前端>.<语言>.yaml），
// 返回文件名（相对于项目根目录）到已定义消息 ID 集合的映射。
func loadServerLocales(root string, cfg *project.Config) (map[string]map[string]bool, error) {
	dir := filepath.Join(root, dev.LocalesDir)
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, errors.Wrap(err, "check errors: read locales")
	}
	frontends := map[string]bool{}
	for _, f := range cfg.Frontends {
		frontends[f.Name] = true
	}
	out := map[string]map[string]bool{}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		ext := filepath.Ext(name)
		if ext != ".yaml" && ext != ".yml" {
			continue
		}
		stem := strings.TrimSuffix(name, ext)
		if prefix, _, ok := strings.Cut(stem, "."); ok && frontends[prefix] {
			continue
		}
		// 跳过任何带点前缀的前端翻译文件，例如已删除或未发现的前端 <name>.<lang>.yaml。
		if strings.Contains(stem, ".") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return nil, errors.Wrap(err, "check errors: read locale")
		}
		var raw map[string]any
		if err := yaml.Unmarshal(data, &raw); err != nil {
			return nil, errors.Wrapf(err, "check errors: parse %s/%s", dev.LocalesDir, name)
		}
		msgs := make(map[string]bool, len(raw))
		for k, v := range raw {
			if hasMessage(v) {
				msgs[k] = true
			}
		}
		out[dev.LocalesDir+"/"+name] = msgs
	}
	return out, nil
}

// hasMessage 报告翻译文件中的一条消息值是否非空（支持纯字符串或 go-i18n 复数对象）。
func hasMessage(v any) bool {
	switch x := v.(type) {
	case string:
		return strings.TrimSpace(x) != ""
	case map[string]any:
		for _, val := range x {
			if s, ok := val.(string); ok && strings.TrimSpace(s) != "" {
				return true
			}
		}
	}
	return false
}

// scanErrorCodes 用 Go AST 扫描项目中所有非测试 .go 文件里的 errorx.Define(..., "<code>") 调用。
func scanErrorCodes(root string, exempt []string) ([]errorCode, error) {
	var out []errorCode
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
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, p, nil, parser.SkipObjectResolution)
		if err != nil {
			return err
		}
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok || (len(call.Args) != 2 && len(call.Args) != 3) {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != "Define" {
				return true
			}
			pkg, ok := sel.X.(*ast.Ident)
			if !ok || pkg.Name != "errorx" {
				return true
			}
			lit, ok := call.Args[1].(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			code, err := strconv.Unquote(lit.Value)
			if err != nil || code == "" {
				return true
			}
			hasMessage := false
			if len(call.Args) == 3 {
				if msgLit, ok := call.Args[2].(*ast.BasicLit); ok && msgLit.Kind == token.STRING {
					if msg, err := strconv.Unquote(msgLit.Value); err == nil && strings.TrimSpace(msg) != "" {
						hasMessage = true
					}
				} else {
					hasMessage = true
				}
			}
			out = append(out, errorCode{code: code, hasMessage: hasMessage, file: rel, line: fset.Position(call.Pos()).Line})
			return true
		})
		return nil
	})
	return out, errors.Wrap(err, "check errors")
}
