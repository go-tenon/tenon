package doctor

import (
	"fmt"
	"io"
	"strings"
)

// 每种结果在文本输出中的标记。
var marks = map[Status]string{Passed: "ok", Warning: "warn", Failed: "FAIL", Skipped: "skip"}

// WriteText 把结果写成适合在终端阅读的文本：按组列出每一项，问题下面是修改方法和文档，最后一行是统计。
//
//	queue
//	  ok    version
//	  FAIL  wal_level: wal_level is replica; the queue needs logical
//	        fix: start PostgreSQL with -c wal_level=logical, then restart it
//	        see: docs/background/queue.md#preparing
//
//	1 failed, 0 warnings, 1 passed, 0 skipped
func (r DoctorReport) WriteText(w io.Writer) error {
	var b strings.Builder
	group := ""
	for i, res := range r.Results {
		if i == 0 || res.Group != group {
			if i > 0 {
				b.WriteString("\n")
			}
			group = res.Group
			b.WriteString(group + "\n")
		}
		fmt.Fprintf(&b, "  %-4s  %s", marks[res.Status], res.Check)
		if res.Summary != "" {
			b.WriteString(": " + res.Summary)
		}
		b.WriteString("\n")
		if res.Fix != "" {
			b.WriteString(indent("        ", "fix: ", res.Fix))
		}
		if res.Doc != "" {
			b.WriteString(indent("        ", "see: ", res.Doc))
		}
	}
	if len(r.Results) > 0 {
		b.WriteString("\n")
	}
	fmt.Fprintf(&b, "%d failed, %s, %d passed, %d skipped\n",
		r.Count(Failed), plural(r.Count(Warning), "warning"), r.Count(Passed), r.Count(Skipped))
	_, err := io.WriteString(w, b.String())
	return err
}

// indent 返回以 prefix 和 label 开头的多行 text，第二行起与第一行的内容对齐，每行以换行结束。
func indent(prefix, label, text string) string {
	var b strings.Builder
	for i, line := range strings.Split(text, "\n") {
		if i == 0 {
			b.WriteString(prefix + label + line + "\n")
		} else {
			b.WriteString(prefix + strings.Repeat(" ", len(label)) + line + "\n")
		}
	}
	return b.String()
}

// plural 返回 "1 warning"、"2 warnings" 这样的计数。
func plural(n int, noun string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, noun)
	}
	return fmt.Sprintf("%d %ss", n, noun)
}
