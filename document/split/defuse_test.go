package split

import (
	"strings"
	"testing"
)

// pathological 是没有 defuse 时让 goldmark 超线性的输入，参数是大致的字节数。
var pathological = map[string]func(size int) string{
	"nested list":   func(size int) string { return strings.Repeat("- ", size/2) + "x\n" },
	"nested quote":  func(size int) string { return strings.Repeat("> ", size/2) + "x\n" },
	"mixed nesting": func(size int) string { return strings.Repeat("> - 1. ", size/7) + "x\n" },
	"indentation": func(size int) string {
		var b strings.Builder
		for depth := 0; b.Len() < size; depth++ {
			b.WriteString(strings.Repeat("  ", depth) + "- a\n")
		}
		return b.String()
	},
	"link references":  func(size int) string { return strings.Repeat("[a]: b\n", size/7) },
	"long blank line":  func(size int) string { return strings.Repeat(" ", size) + "\n# H\nx" },
	"long indentation": func(size int) string { return strings.Repeat(" ", size) + "x\n" },
	"long empty item":  func(size int) string { return "-" + strings.Repeat(" ", size) + "\n" },
	"backticks": func(size int) string {
		var b strings.Builder
		for k := 1; b.Len() < size; k++ {
			b.WriteString(strings.Repeat("`", k) + "a ")
		}
		return b.String()
	},
	"padded table": func(size int) string {
		columns := size / 6
		return strings.Repeat("|a", columns) + "|\n" + strings.Repeat("|-", columns) + "|\n" + strings.Repeat("x\n", columns)
	},
	"padded table in quote": func(size int) string {
		columns := size / 8
		return "> " + strings.Repeat("|a", columns) + "|\n> " + strings.Repeat("|-", columns) + "|\n" + strings.Repeat("> x\n", columns)
	},
}

func TestDefuseKeepsLength(t *testing.T) {
	t.Parallel()
	for name, generate := range pathological {
		text := generate(4 << 10)
		source := []byte(text)
		defuse(source)
		if len(source) != len(text) || strings.Count(string(source), "\n") != strings.Count(text, "\n") {
			t.Errorf("%s: defuse changed the length or the lines", name)
		}
	}
}

func TestDefuseLeavesOrdinaryMarkdown(t *testing.T) {
	t.Parallel()
	for _, text := range []string{
		"# 标题\n\n> 引用\n> - 列表\n>   1. 编号\n\n" + strings.Repeat("- - - ", 40) + "\n",
		"| a | b |\n| - | - |\n| 1 |\n| 1 | 2 |\n",
		strings.Repeat("[ref]: /url\n", maxReferences),
		"```\n" + strings.Repeat(" ", 200) + "\n" + strings.Repeat(" ", 100) + "code\n```\n",
	} {
		source := []byte(text)
		if defuse(source); string(source) != text {
			t.Errorf("defuse changed %q to %q", text, source)
		}
	}
}
