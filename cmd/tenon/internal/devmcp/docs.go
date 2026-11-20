package devmcp

import (
	"bufio"
	"fmt"
	"io/fs"
	"math"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"unicode"
)

// section 是文档中一个锚点开始的小节，到下一个锚点为止；页面开头到第一个锚点之间是锚点为空的一节。
type section struct {
	page      string         // 页面在文档目录中的路径，例如 http/routing.md
	anchor    string         // 锚点，页面开头的一节为空
	title     string         // 小节的标题；页面开头的一节是页面的标题
	pageTitle string         // 页面的标题
	level     int            // 标题的级别：## 是 2；页面开头的一节是 1
	text      string         // 小节的内容，不含锚点那一行
	terms     map[string]int // 词项的出现次数，标题中的词项按 titleWeight 计
	length    int            // 词项的总数
}

// titleWeight 是标题中的词项相对正文的权重：标题命中的小节排在只有正文命中的前面。
const titleWeight = 3

// pageTitleWeight 是页面标题中的词项计入每个小节时的权重。
const pageTitleWeight = 2

// docs 是文档目录中全部页面的小节和检索用的统计。
type docs struct {
	dir      string // 文档目录在磁盘上的位置
	sections []*section
	pages    map[string][]*section // 每一页的小节，按出现的顺序
	df       map[string]int        // 每个词项出现在多少个小节中
	avgLen   float64               // 小节的平均词项数
}

// anchorLine 匹配独占一行的锚点 <a id="..."></a>。
var anchorLine = regexp.MustCompile(`^<a id="([^"]+)"></a>\s*$`)

// loadDocs 读取 dir 中的全部 Markdown 页面，按锚点切成小节并建立检索的统计。
func loadDocs(dir string) (*docs, error) {
	d := &docs{dir: dir, pages: map[string][]*section{}, df: map[string]int{}}
	err := filepath.WalkDir(dir, func(p string, e fs.DirEntry, err error) error {
		if err != nil || e.IsDir() || !strings.HasSuffix(p, ".md") {
			return err
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		d.add(filepath.ToSlash(rel), string(data))
		return nil
	})
	if err != nil {
		return nil, err
	}
	if len(d.sections) == 0 {
		return nil, fmt.Errorf("no documentation in %s", dir)
	}
	total := 0
	for _, s := range d.sections {
		total += s.length
		for t := range s.terms {
			d.df[t]++
		}
	}
	d.avgLen = float64(total) / float64(len(d.sections))
	return d, nil
}

// add 把页面 page 切成小节加入 d。
func (d *docs) add(page, text string) {
	pageTitle := ""
	cur := &section{page: page, level: 1}
	var body strings.Builder
	finish := func() {
		cur.text = strings.TrimSpace(body.String())
		if cur.title == "" {
			cur.title = pageTitle
		}
		cur.pageTitle = pageTitle
		cur.terms = map[string]int{}
		for _, t := range tokens(cur.text) {
			cur.terms[t]++
			cur.length++
		}
		for _, t := range tokens(cur.title) {
			cur.terms[t] += titleWeight
			cur.length += titleWeight
		}
		// 小节属于页面的主题：页面的标题也计入每一个小节，“定时任务”能找到定时任务一页中的小节。
		if cur.anchor != "" {
			for _, t := range tokens(pageTitle) {
				cur.terms[t] += pageTitleWeight
				cur.length += pageTitleWeight
			}
		}
		d.sections = append(d.sections, cur)
		d.pages[page] = append(d.pages[page], cur)
		body.Reset()
	}
	sc := bufio.NewScanner(strings.NewReader(text))
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	// 锚点后面紧接着标题时，小节取这个标题和级别；后面是正文时（段落的锚点），沿用上一个标题，级别低一级，
	// 读上一级小节时一起读出。
	wantTitle := false
	lastTitle, lastLevel := "", 1
	for sc.Scan() {
		line := sc.Text()
		if pageTitle == "" && strings.HasPrefix(line, "# ") {
			pageTitle = strings.TrimSpace(strings.TrimPrefix(line, "# "))
			lastTitle = pageTitle
		}
		if m := anchorLine.FindStringSubmatch(line); m != nil {
			finish()
			cur = &section{page: page, anchor: m[1], title: lastTitle, level: lastLevel + 1}
			wantTitle = true
			continue
		}
		if wantTitle && strings.HasPrefix(line, "#") {
			level := len(line) - len(strings.TrimLeft(line, "#"))
			cur.title, cur.level = strings.TrimSpace(line[level:]), level
			lastTitle, lastLevel = cur.title, level
			wantTitle = false
		} else if strings.TrimSpace(line) != "" {
			wantTitle = false
		}
		body.WriteString(line)
		body.WriteByte('\n')
	}
	finish()
}

// tokens 把 s 切成检索用的词项：字母和数字组成的词转为小写（至少两个字符）；汉字按相邻的两个字切分，
// 只有一个汉字时取这个字。中文不需要分词词典，“访问策略”切成“访问”“问策”“策略”，与查询的切法相同。
func tokens(s string) []string {
	var out []string
	var word []rune
	var han []rune
	flushWord := func() {
		if len(word) >= 2 {
			out = append(out, strings.ToLower(string(word)))
		}
		word = word[:0]
	}
	flushHan := func() {
		if len(han) == 1 {
			out = append(out, string(han))
		}
		for i := 0; i+1 < len(han); i++ {
			out = append(out, string(han[i:i+2]))
		}
		han = han[:0]
	}
	for _, r := range s {
		switch {
		case unicode.Is(unicode.Han, r):
			flushWord()
			han = append(han, r)
		case r < unicode.MaxASCII && (unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_'):
			flushHan()
			word = append(word, r)
		default:
			flushWord()
			flushHan()
		}
	}
	flushWord()
	flushHan()
	return out
}

// hit 是一个检索结果。
type hit struct {
	section *section
	score   float64
}

// search 用 BM25 给小节打分，返回得分最高的 limit 个；没有任何词项命中的小节不返回。
func (d *docs) search(query string, limit int) []hit {
	const k1, b = 1.2, 0.75
	terms := slices.Compact(slices.Sorted(slices.Values(tokens(query))))
	n := float64(len(d.sections))
	var hits []hit
	for _, s := range d.sections {
		score := 0.0
		for _, t := range terms {
			tf := float64(s.terms[t])
			if tf == 0 {
				continue
			}
			df := float64(d.df[t])
			idf := math.Log(1 + (n-df+0.5)/(df+0.5))
			score += idf * tf * (k1 + 1) / (tf + k1*(1-b+b*float64(s.length)/d.avgLen))
		}
		if score > 0 {
			hits = append(hits, hit{s, score})
		}
	}
	slices.SortStableFunc(hits, func(x, y hit) int {
		switch {
		case x.score > y.score:
			return -1
		case x.score < y.score:
			return 1
		}
		return 0
	})
	if len(hits) > limit {
		hits = hits[:limit]
	}
	return hits
}

// snippet 返回 s 中第一处出现 query 的词项的附近的一段文字，空白合并成一个空格。
func snippet(s *section, query string) string {
	text := []rune(strings.Join(strings.Fields(s.text), " "))
	lower := strings.ToLower(string(text))
	pos := -1
	for _, t := range tokens(query) {
		if i := strings.Index(lower, t); i >= 0 && (pos < 0 || i < pos) {
			pos = i
		}
	}
	start := 0
	if pos > 0 {
		start = max(len([]rune(lower[:pos]))-40, 0)
	}
	end := min(start+160, len(text))
	out := string(text[start:end])
	if start > 0 {
		out = "…" + out
	}
	if end < len(text) {
		out += "…"
	}
	return out
}

// read 返回页面 page 的内容：anchor 不为空时是这个小节，连同级别更低的子小节；full 时是整页；
// 都没有时是页面的目录：标题、开头的介绍和每个小节的锚点。dir 是显示给助手的文档目录，例如 .tenon/docs。
func (d *docs) read(dir, page, anchor string, full bool) (string, error) {
	sections, ok := d.pages[page]
	if !ok {
		return "", fmt.Errorf("no page %s in %s; use tenon_search_docs to find one, or read README.md for the index", page, dir)
	}
	if full {
		data, err := os.ReadFile(filepath.Join(d.dir, filepath.FromSlash(page)))
		return string(data), err
	}
	if anchor == "" {
		var b strings.Builder
		fmt.Fprintf(&b, "# %s\n\n%s\n\n%s\n\n", sections[0].pageTitle, path.Join(dir, page), sections[0].text)
		b.WriteString("## 小节\n\n")
		for _, s := range sections[1:] {
			fmt.Fprintf(&b, "%s- #%s %s\n", strings.Repeat("  ", max(s.level-2, 0)), s.anchor, s.title)
		}
		b.WriteString("\n用 anchor 读一个小节，full 读整页。\n")
		return b.String(), nil
	}
	i := slices.IndexFunc(sections, func(s *section) bool { return s.anchor == anchor })
	if i < 0 {
		return "", fmt.Errorf("no anchor %q in %s; read the page without anchor to list its sections", anchor, page)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s#%s\n\n", path.Join(dir, page), anchor)
	b.WriteString(sections[i].text)
	for _, s := range sections[i+1:] {
		if s.level <= sections[i].level {
			break
		}
		b.WriteString("\n\n")
		b.WriteString(s.text)
	}
	b.WriteString("\n")
	return b.String(), nil
}
