package check_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-tenon/tenon/cmd/tenon/internal/check"
	"github.com/go-tenon/tenon/cmd/tenon/internal/project"
)

func writeFiles(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for name, content := range files {
		p := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// TestCheckText 检查写死的界面文字：字符串和 JSX 中的汉字是问题，注释、生成的文件、测试和写了 i18n-ignore 的行不是。
func TestCheckText(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeFiles(t, root, map[string]string{
		"apps/web/src/routes/inbox.tsx": `// 收件箱：注释中的中文不算
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
		"apps/web/src/messages.gen.ts":  `export const messages = { "zh-CN": { "a": "中文" } }`,
		"apps/web/src/inbox.test.tsx":   `expect(screen.getByText("没有会话"))`,
		"apps/web/src/routeTree.gen.ts": `// 中文`,
	})
	cfg := &project.Config{Frontends: []project.Frontend{{Name: "web", Dir: "apps/web"}, {Name: "admin", Dir: "apps/admin"}}}
	found, err := check.CheckText(root, cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := map[int]bool{6: true, 8: true, 11: true}
	if len(found) != len(want) {
		t.Fatalf("找到：%+v", found)
	}
	for _, f := range found {
		if f.Check != "text" || f.File != "apps/web/src/routes/inbox.tsx" || !want[f.Line] {
			t.Errorf("不应找到：%+v", f)
		}
	}
}

// TestFrameworkUIText 检查 @go-tenon/ui 中没有写死的界面文字：组件的文字都在 i18n 目录的内置文字中。
func TestFrameworkUIText(t *testing.T) {
	t.Parallel()
	found, err := check.ScanText("../../../../js/ui/src")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range found {
		if filepath.Dir(f.File) == "i18n" {
			continue
		}
		t.Errorf("js/ui/src/%s:%d 写死了界面文字，改为在 i18n/zh-CN.ts、en.ts 中加消息、用 useText 取出：%s", f.File, f.Line, f.Message)
	}
}

// TestCheckClock 检查 SQL 和 Go 字符串中的数据库时钟函数，以及 clock-ignore 和内置第三方迁移豁免。
func TestCheckClock(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeFiles(t, root, map[string]string{
		"migrations/2026_11_13_080000_create_articles.sql": `-- +goose Up
-- created_at 不使用 CURRENT_TIMESTAMP（注释忽略）
CREATE TABLE articles (
    id uuid PRIMARY KEY,
    created_at timestamptz NOT NULL DEFAULT NOW(),
    updated_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP -- clock-ignore
);
`,
		"migrations/2026_11_10_193936_create_agent_sessions.sql": `CREATE TABLE agent_session_states (created_at timestamp DEFAULT CURRENT_TIMESTAMP);`,
		"internal/article/publish.go": `package article

const q1 = "UPDATE articles SET updated_at = NOW()"
const q2 = "SELECT current_timestamp" // clock-ignore
const q3 = "SELECT id FROM articles WHERE created_at < ?"
`,
		"internal/article/publish_test.go": `package article
const testQ = "SELECT NOW()"
`,
		"migrations/legacy/old.sql": `SELECT NOW();`,
	})
	found, err := check.CheckClock(root, []string{"migrations/legacy"})
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 2 {
		t.Fatalf("got %d findings: %+v", len(found), found)
	}
	if found[0].File != "internal/article/publish.go" || found[0].Line != 3 {
		t.Errorf("finding[0] = %+v", found[0])
	}
	if found[1].File != "migrations/2026_11_13_080000_create_articles.sql" || found[1].Line != 5 {
		t.Errorf("finding[1] = %+v", found[1])
	}
}

// TestCheckErrors 检查 errorx.Define 定义的业务错误码在服务端翻译文件中齐全；没有服务端翻译文件时跳过。
func TestCheckErrors(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeFiles(t, root, map[string]string{
		"internal/article/article.go": `package article

import "github.com/go-tenon/tenon/errorx"

var (
	ErrNotFound = errorx.Define(errorx.NotFound, "article.not_found")
	ErrLocked   = errorx.Define(errorx.Conflict, "article.locked")
)
`,
		"locales/web.zh-CN.yaml": "nav.home: 首页\n",
	})
	cfg := &project.Config{Frontends: []project.Frontend{{Name: "web", Dir: "apps/web"}}}
	// 只有前端翻译文件、没有服务端翻译文件时不报错。
	found, err := check.CheckErrors(root, cfg, nil)
	if err != nil || len(found) != 0 {
		t.Fatalf("no server locales: found=%+v err=%v", found, err)
	}

	writeFiles(t, root, map[string]string{
		"locales/zh-CN.yaml": "article.not_found: 文章不存在\n",
		"locales/en.yaml":    "article.not_found: Article not found\narticle.locked: Article is locked\n",
	})
	found, err = check.CheckErrors(root, cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 1 || found[0].File != "locales/zh-CN.yaml" || !strings.Contains(found[0].Message, `"article.locked"`) {
		t.Fatalf("got %+v", found)
	}

	// 带内联源语言默认提示的错误码：源语言 YAML 无需重复写，但多语言项目的附加语言仍须补译文。
	inlineRoot := t.TempDir()
	writeFiles(t, inlineRoot, map[string]string{
		"internal/article/article.go": `package article

import "github.com/go-tenon/tenon/errorx"

var (
	ErrNotFound = errorx.Define(errorx.NotFound, "article.not_found", "文章不存在")
	ErrLocked   = errorx.Define(errorx.Conflict, "article.locked", "文章已锁定")
)
`,
		"locales/zh-CN.yaml": "auth.unauthenticated: 请先登录\n",
		"locales/en.yaml":    "article.not_found: Article not found\n",
	})
	multiCfg := &project.Config{Languages: []string{"zh-CN", "en"}}
	found, err = check.CheckErrors(inlineRoot, multiCfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 1 || found[0].File != "locales/en.yaml" || !strings.Contains(found[0].Message, `"article.locked"`) {
		t.Fatalf("inline multi errors: got %+v", found)
	}
}

// TestCheckColors 检查前端代码中的十六进制和 rgb()/hsl() 硬编码颜色，忽略注释、生成文件、测试、类私有字段和 color-ignore。
func TestCheckColors(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeFiles(t, root, map[string]string{
		"apps/web/src/routes/home.tsx": `// 注释里的 #ff0000 和 rgb(0, 0, 0) 不算
class Box {
  #fee = 1
}
export function Home() {
  const link = "https://example.com/docs#abc"
  const ok = "var(--mantine-color-deep-orange-6)"
  const bad1 = "#ff0000"
  const bad2 = "rgba(0, 0, 0, 0.5)"
  const ignored = "#123456" // color-ignore
  return <div style={{ color: ok }}>{link}{bad1}{bad2}{ignored}</div>
}
`,
		"apps/web/src/styles.css": `/* #ffffff */
.good { color: var(--tn-text); }
.bad { background: #1a2b3c; }
.also-bad { border-color: hsl(210, 50%, 50%); }
.ok { color: #000; /* color-ignore */ }
`,
		"apps/web/src/theme.gen.ts": `export const c = "#ff0000"`,
		"apps/web/src/home.test.ts": `expect(c).toBe("#ff0000")`,
	})
	cfg := &project.Config{Frontends: []project.Frontend{{Name: "web", Dir: "apps/web"}}}
	found, err := check.CheckColors(root, cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 4 {
		t.Fatalf("got %d findings: %+v", len(found), found)
	}
	want := []struct {
		file string
		line int
		sub  string
	}{
		{"apps/web/src/routes/home.tsx", 8, "#ff0000"},
		{"apps/web/src/routes/home.tsx", 9, "rgba("},
		{"apps/web/src/styles.css", 3, "#1a2b3c"},
		{"apps/web/src/styles.css", 4, "hsl("},
	}
	for i, w := range want {
		if found[i].File != w.file || found[i].Line != w.line || !strings.Contains(found[i].Message, w.sub) {
			t.Errorf("finding[%d] = %+v, want %+v", i, found[i], w)
		}
	}
}

// TestCheckDeps 检查前端与客户端的依赖版本是否满足框架包的 peerDependencies。
func TestCheckDeps(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeFiles(t, root, map[string]string{
		".tenon/js/ui/package.json": `{
  "name": "@go-tenon/ui",
  "peerDependencies": {
    "@mantine/core": "^9.8.1",
    "react": "^19.2.0",
    "ai": "^7.0.0"
  },
  "peerDependenciesMeta": {
    "ai": { "optional": true }
  }
}`,
		".tenon/js/electron/package.json": `{
  "name": "@go-tenon/electron",
  "peerDependencies": {
    "electron": ">=45.0.0"
  }
}`,
		"apps/web/package.json": `{
  "dependencies": {
    "@go-tenon/ui": "workspace:*",
    "@mantine/core": "^9.7.0",
    "react": "^19.2.1"
  }
}`,
		"clients/desktop/package.json": `{
  "devDependencies": {
    "@go-tenon/electron": "workspace:*",
    "electron": "45.2.3"
  }
}`,
	})
	cfg := &project.Config{
		Frontends: []project.Frontend{{Name: "web", Dir: "apps/web"}},
		Clients:   []project.Client{{Name: "desktop", Dir: "clients/desktop", Kind: "electron"}},
	}
	found, err := check.CheckDeps(root, cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 1 || found[0].File != "apps/web/package.json" || !strings.Contains(found[0].Message, "@mantine/core") {
		t.Fatalf("got %+v", found)
	}

	// 缺少非可选 peerDependency 时报错。
	writeFiles(t, root, map[string]string{
		"apps/web/package.json": `{
  "dependencies": {
    "@go-tenon/ui": "workspace:*",
    "@mantine/core": "^9.8.1"
  }
}`,
	})
	found, err = check.CheckDeps(root, cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 1 || !strings.Contains(found[0].Message, "missing react") {
		t.Fatalf("missing peer: %+v", found)
	}
}

// TestRunOptions 验证单语言默认跳过 text、多语言自动启用 text、tenon.json 的 check.skip 与 --only 参数。
func TestRunOptions(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeFiles(t, root, map[string]string{
		"apps/web/src/page.tsx": `export const s = "中文"; export const c = "#ff0000"`,
	})
	// 单语言项目（默认）：text 自动跳过，只报告 colors。
	singleCfg := &project.Config{
		Frontends: []project.Frontend{{Name: "web", Dir: "apps/web"}},
	}
	found, err := check.Run(root, singleCfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 1 || found[0].Check != "colors" {
		t.Fatalf("single language should skip text: %+v", found)
	}
	// 多语言项目：自动启用 text 检查。
	multiCfg := &project.Config{
		Frontends: []project.Frontend{{Name: "web", Dir: "apps/web"}},
		Languages: []string{"zh-CN", "en"},
	}
	found, err = check.Run(root, multiCfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 2 || found[0].Check != "text" || found[1].Check != "colors" {
		t.Fatalf("multi language should run text: %+v", found)
	}
	// check.skip 可以跳过多语言项目的 text 检查。
	multiSkip := &project.Config{
		Frontends: []project.Frontend{{Name: "web", Dir: "apps/web"}},
		Languages: []string{"zh-CN", "en"},
		Check:     &project.Check{Skip: []string{"text"}},
	}
	found, err = check.Run(root, multiSkip, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 1 || found[0].Check != "colors" {
		t.Fatalf("skip text: %+v", found)
	}
	// --only 优先于单语言默认规则和 check.skip。
	found, err = check.Run(root, singleCfg, []string{"text"})
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 1 || found[0].Check != "text" {
		t.Fatalf("only text: %+v", found)
	}
	if _, err := check.Run(root, singleCfg, []string{"bogus"}); err == nil {
		t.Fatal("expected error for unknown check")
	}
}
