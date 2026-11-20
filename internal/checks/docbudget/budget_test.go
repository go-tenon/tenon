// Package docbudget_test 检查给 AI 编程助手读的内容的篇幅：每次会话都会读到的 AGENTS.md 模板、技能的描述和文档目录，
// 按需读的技能正文和文档的每一节。上限和理由见 contributing/docs.md 的“预算”一节。
package docbudget_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"unicode/utf8"
)

// root 是仓库的根目录。
const root = "../../.."

// 各项的上限，单位是字符。
const (
	agentsTemplateLimit   = 4500 // 项目 AGENTS.md 的模板
	skillDescriptionLimit = 200  // 技能的 description，助手每次会话都会读到
	skillLimit            = 3500 // 技能的 SKILL.md 全文
	indexLimit            = 5000 // docs/README.md
	sectionLimit          = 3000 // 文档中的一节，从一个锚点到下一个锚点
)

// rewritten 是已经按 contributing/docs.md 重写过的文档目录，其中每一节都要在 sectionLimit 之内。
// 随文档的重写逐个加入，全部重写之后删掉这个列表，检查 docs 下的每一页。
var rewritten = []string{"concepts", "general", "database", "testing", "http", "rpc", "security", "background", "libraries", "features"}

var (
	anchorLine  = regexp.MustCompile(`(?m)^<a id="[^"]+"></a>\s*$`)
	description = regexp.MustCompile(`(?m)^description: (.*)$`)
	packageLine = regexp.MustCompile(`(?m)^包：(.+)$`)
	codeSpan    = regexp.MustCompile("`([^`]+)`")
)

// read 返回仓库中 rel 文件的内容。
func read(t *testing.T, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// TestAlwaysLoaded 检查 AGENTS.md 的模板、文档目录和技能的篇幅。
func TestAlwaysLoaded(t *testing.T) {
	t.Parallel()
	for rel, limit := range map[string]int{
		"cmd/tenon/internal/scaffold/template/project/AGENTS.md.tmpl": agentsTemplateLimit,
		"docs/README.md": indexLimit,
	} {
		if n := utf8.RuneCountInString(read(t, rel)); n > limit {
			t.Errorf("%s has %d characters, limit %d", rel, n, limit)
		}
	}
	skills, err := filepath.Glob(filepath.Join(root, "cmd/tenon/internal/agents/skills/*/SKILL.md"))
	if err != nil || len(skills) == 0 {
		t.Fatalf("no skills: %v", err)
	}
	for _, path := range skills {
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		name := filepath.Base(filepath.Dir(path))
		if n := utf8.RuneCount(b); n > skillLimit {
			t.Errorf("skill %s has %d characters, limit %d", name, n, skillLimit)
		}
		m := description.FindSubmatch(b)
		if m == nil {
			t.Errorf("skill %s has no description", name)
		} else if n := utf8.RuneCount(m[1]); n > skillDescriptionLimit {
			t.Errorf("skill %s: description has %d characters, limit %d", name, n, skillDescriptionLimit)
		}
	}
}

// TestSections 检查文档：rewritten 中的目录每一节不超过 sectionLimit；“包：”一行列出的包都存在。
func TestSections(t *testing.T) {
	t.Parallel()
	err := filepath.WalkDir(filepath.Join(root, "docs"), func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".md") {
			return err
		}
		rel, err := filepath.Rel(filepath.Join(root, "docs"), path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		text := string(b)

		// “包：”一行中的每个包都是模块中的一个目录，其中有 Go 代码。
		for _, m := range packageLine.FindAllStringSubmatch(text, -1) {
			for _, p := range codeSpan.FindAllStringSubmatch(m[1], -1) {
				files, _ := filepath.Glob(filepath.Join(root, filepath.FromSlash(p[1]), "*.go"))
				if len(files) == 0 {
					t.Errorf("%s: package %s does not exist", rel, p[1])
				}
			}
		}

		dir, _, _ := strings.Cut(rel, "/")
		if !slices.Contains(rewritten, dir) {
			return nil
		}
		// 按锚点切成小节，页面开头到第一个锚点之间也算一节。
		bounds := append([]int{0}, starts(text)...)
		bounds = append(bounds, len(text))
		for i := 0; i+1 < len(bounds); i++ {
			section := text[bounds[i]:bounds[i+1]]
			if n := utf8.RuneCountInString(section); n > sectionLimit {
				title, _, _ := strings.Cut(strings.TrimSpace(anchorLine.ReplaceAllString(section, "")), "\n")
				t.Errorf("%s: section %q has %d characters, limit %d", rel, title, n, sectionLimit)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// starts 返回 text 中每个锚点行的起始位置。
func starts(text string) []int {
	var out []int
	for _, loc := range anchorLine.FindAllStringIndex(text, -1) {
		out = append(out, loc[0])
	}
	return out
}
