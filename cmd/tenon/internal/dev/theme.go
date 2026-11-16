package dev

import (
	"bytes"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/go-tenon/tenon/theme"
)

// ThemeFile 是项目的设计令牌，见 theme 包。
const ThemeFile = "theme/tokens.json"

// ThemeDir 是由令牌生成的前端主题包，例如 @todo/theme：tokens.css 是 shadcn 的 CSS 变量，index.ts 中是
// antd 的令牌。前端用 workspace:* 依赖它。
const ThemeDir = "packages/theme"

// GenerateTheme 从项目 dir 的 ThemeFile 生成 ThemeDir 中的 src/tokens.css 和 src/index.ts；内容没有变化时不改写，
// 没有 package.json 时一并写入。项目没有 ThemeFile 时什么也不做，返回 false。
func GenerateTheme(dir string) (bool, error) {
	data, err := os.ReadFile(filepath.Join(dir, ThemeFile))
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	tokens, err := theme.Parse(data)
	if err != nil {
		return false, &ThemeError{Err: err}
	}
	pkg := filepath.Join(dir, ThemeDir)
	files := map[string][]byte{
		"src/tokens.css": []byte(tokens.CSS()),
		"src/index.ts":   []byte(tokens.TS()),
	}
	if _, err := os.Stat(filepath.Join(pkg, "package.json")); errors.Is(err, fs.ErrNotExist) {
		manifest, _ := json.MarshalIndent(map[string]any{
			"name":    themePackageName(dir),
			"private": true,
			"type":    "module",
			"exports": map[string]string{".": "./src/index.ts", "./tokens.css": "./src/tokens.css"},
		}, "", "  ")
		files["package.json"] = append(manifest, '\n')
	}
	for name, content := range files {
		path := filepath.Join(pkg, filepath.FromSlash(name))
		if old, err := os.ReadFile(path); err == nil && bytes.Equal(old, content) {
			continue
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return false, err
		}
		if err := os.WriteFile(path, content, 0o644); err != nil {
			return false, err
		}
	}
	return true, nil
}

// ThemeError 是令牌文件写错了：报告给用户修改，tenon dev 不因此停止。
type ThemeError struct{ Err error }

func (e *ThemeError) Error() string { return ThemeFile + ": " + e.Err.Error() }
func (e *ThemeError) Unwrap() error { return e.Err }

// themePackageName 是主题包的名称：根目录 package.json 的 name 作为 scope，例如 @todo/theme。
func themePackageName(dir string) string {
	var root struct {
		Name string `json:"name"`
	}
	if b, err := os.ReadFile(filepath.Join(dir, "package.json")); err == nil {
		_ = json.Unmarshal(b, &root)
	}
	if root.Name == "" {
		return "theme"
	}
	return "@" + root.Name + "/theme"
}
