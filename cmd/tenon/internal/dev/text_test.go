package dev_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/go-tenon/tenon/cmd/tenon/internal/dev"
	"github.com/go-tenon/tenon/cmd/tenon/internal/project"
)

// TestCheckText 检查写死的界面文字：字符串和 JSX 中的汉字是问题，注释、生成的文件、测试和写了 i18n-ignore 的行不是。
func TestCheckText(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	src := filepath.Join(root, "apps/web/src")
	if err := os.MkdirAll(filepath.Join(src, "routes"), 0o755); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"routes/inbox.tsx": `// 收件箱：注释中的中文不算
/* 多行注释
   中的中文也不算 */
export function Inbox() {
  const url = "https://example.com/a//b" // 字符串中的 // 不是注释
  return <Text>没有会话</Text>
}
const title = "收件箱"
const ok = t("inbox.empty")
console.warn("给开发者看的中文") // i18n-ignore
const tpl = ` + "`共 ${n} 条`" + `
`,
		"messages.gen.ts":  `export const messages = { "zh-CN": { "a": "中文" } }`,
		"inbox.test.tsx":   `expect(screen.getByText("没有会话"))`,
		"routeTree.gen.ts": `// 中文`,
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(src, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cfg := &project.Config{Frontends: []project.Frontend{{Name: "web", Dir: "apps/web"}, {Name: "admin", Dir: "apps/admin"}}}
	found, err := dev.CheckText(root, cfg)
	if err != nil {
		t.Fatal(err)
	}
	want := map[int]bool{6: true, 8: true, 11: true}
	if len(found) != len(want) {
		t.Fatalf("找到：%+v", found)
	}
	for _, f := range found {
		if f.File != "apps/web/src/routes/inbox.tsx" || !want[f.Line] {
			t.Errorf("不应找到：%+v", f)
		}
	}
}

// TestFrameworkUIText 检查 @go-tenon/ui 中没有写死的界面文字：组件的文字都在 i18n 目录的内置文字中。
func TestFrameworkUIText(t *testing.T) {
	t.Parallel()
	found, err := dev.ScanText("../../../../js/ui/src")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range found {
		if filepath.Dir(f.File) == "i18n" {
			continue
		}
		t.Errorf("js/ui/src/%s:%d 写死了界面文字，改为在 i18n/zh-CN.ts、en.ts 中加消息、用 useText 取出：%s", f.File, f.Line, f.Text)
	}
}
