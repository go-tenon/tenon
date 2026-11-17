package queueweb

import (
	"context"
	"fmt"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/go-tenon/tenon/i18n"
	"github.com/go-tenon/tenon/page"
	"github.com/go-tenon/tenon/queue"
	"github.com/go-tenon/tenon/web"
)

// defaultPerPage 与 page.OffsetParams 的默认每页条数相同。
const defaultPerPage = 20

type nav int

const (
	navOverview nav = iota
	navFailed
)

// frame 是每个页面共用的页头：导航和外部链接。
type frame struct {
	base    string
	current nav
	links   []link
}

type overviewView struct {
	base   string
	status queue.Status
	// jetStream 报告是否设置了 Options.Admin；stream 是读到的状态，读不到时 streamErr 不为 nil。
	jetStream bool
	stream    queue.StreamStatus
	streamErr error
	topics    []topicRow
}

// streamReady 报告是否读到了 stream 的状态。
func (v overviewView) streamReady() bool {
	return v.jetStream && v.streamErr == nil && v.stream.Exists
}

// topicRow 是概览中 topic 表格的一行：JetStream 中的状态和失败任务数。
type topicRow struct {
	queue.TopicStatus
	inStream bool // JetStream 中有这个 topic：没有时只有失败任务数
	failed   int
}

// topicRows 合并 JetStream 中的 topic 和有失败任务的 topic，按名字排序。
func topicRows(topics []queue.TopicStatus, counts []queue.TopicCount) []topicRow {
	byTopic := map[string]*topicRow{}
	for _, t := range topics {
		byTopic[t.Topic] = &topicRow{TopicStatus: t, inStream: true}
	}
	for _, c := range counts {
		row, ok := byTopic[c.Topic]
		if !ok {
			row = &topicRow{TopicStatus: queue.TopicStatus{Topic: c.Topic}}
			byTopic[c.Topic] = row
		}
		row.failed = c.Count
	}
	rows := make([]topicRow, 0, len(byTopic))
	for _, row := range byTopic {
		rows = append(rows, *row)
	}
	slices.SortFunc(rows, func(a, b topicRow) int { return strings.Compare(a.Topic, b.Topic) })
	return rows
}

// cell 是 JetStream 中的数，topic 不在 JetStream 中时为 "-"。
func (r topicRow) cell(n int) string {
	if !r.inStream {
		return "-"
	}
	return strconv.Itoa(n)
}

type failedView struct {
	base   string
	query  FailedQuery
	counts []queue.TopicCount
	list   page.Offset[queue.FailedJob]
	notice string
}

// url 返回 q 对应的列表地址；与默认值相同的参数省略。
func (v failedView) url(q FailedQuery) string {
	return web.QueryURL(v.base+"/failed", q)
}

func (v failedView) pageURL(p int) string {
	q := v.query
	q.Page = p
	return v.url(q)
}

func (v failedView) topicURL(topic string) string {
	return v.url(FailedQuery{Topic: topic})
}

// bulkURL 返回对当前 topic 全部重试或删除的地址。
func (v failedView) bulkURL(action string) string {
	u := v.base + "/failed/" + action
	if v.query.Topic != "" {
		u += "?" + url.Values{"topic": {v.query.Topic}}.Encode()
	}
	return u
}

// t 按请求的语言翻译面板的文案；context 中没有语言（没有经过 httpx.Locale）时用英文的 fallback。
func t(ctx context.Context, id, fallback string, data ...map[string]any) string {
	var d map[string]any
	if len(data) > 0 {
		d = data[0]
	}
	if s := i18n.T(ctx, id, d); s != id {
		return s
	}
	for k, v := range d {
		fallback = strings.ReplaceAll(fallback, "{{."+k+"}}", fmt.Sprint(v))
	}
	return fallback
}

func sortLinks(links []link) {
	slices.SortFunc(links, func(a, b link) int { return strings.Compare(a.name, b.name) })
}

func formatTime(tm time.Time) string {
	if tm.IsZero() {
		return "-"
	}
	return tm.Local().Format("2006-01-02 15:04:05")
}

// bytes 把字节数写成 1.5 MB 这样的形式。
func formatBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGTPE"[exp])
}

// firstLine 返回错误的第一行，列表中显示它，完整的错误和堆栈在详情页。
func firstLine(s string) string {
	line, _, _ := strings.Cut(s, "\n")
	if len(line) > 160 {
		line = line[:160] + "…"
	}
	return line
}

func total(counts []queue.TopicCount) int {
	n := 0
	for _, c := range counts {
		n += c.Count
	}
	return n
}

// replicas 是副本数，集群中加上 leader。
func replicas(st queue.StreamStatus) string {
	s := strconv.Itoa(st.Replicas)
	if st.Leader != "" {
		s += " (leader " + st.Leader + ")"
	}
	return s
}
