// Package queuecmd 提供 queue 命令：查看队列在数据库和 JetStream 两侧的状态、列出失败任务。
package queuecmd

import (
	"fmt"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"
	"unicode/utf8"

	"github.com/cockroachdb/errors"
	"github.com/nats-io/nats.go"
	"github.com/spf13/cobra"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/command"
	"github.com/go-tenon/tenon/queue"
)

// errorWidth 是 queue failed 显示的错误信息的最大长度（字符）。
const errorWidth = 80

// Option 调整 New 返回的命令。
type Option func(*options)

// options 是 queue 命令的选项。
type options struct {
	jetStream *jetStream
}

// jetStream 是连接 JetStream 需要的连接和配置。
type jetStream struct {
	nc  *nats.Conn
	cfg queue.Config
}

// WithJetStream 让 queue status 同时显示 JetStream 中的状态：stream 的消息和存储、
// 各 topic 等待处理和处理中的任务。nc 是应用的 NATS 连接，cfg 与 Worker 的相同；和 db 一样，命令执行时才使用。
func WithJetStream(nc *nats.Conn, cfg queue.Config) Option {
	return func(o *options) { o.jetStream = &jetStream{nc: nc, cfg: cfg} }
}

// New 返回 queue 命令。db 是发布任务的数据库：
//
//	queue status                         outbox 待转发与延迟任务数；
//	                                     设置了 WithJetStream 时还有 JetStream 中的任务
//	queue failed [--topic t] [--limit n] 从新到旧列出失败任务
//
// 和其他需要数据库的命令一样，在 command.Bind 中声明：New 只保存参数，命令执行时才使用它。
//
//	queuecmd.New(a.db, queuecmd.WithJetStream(a.nc, a.cfg.Queue))
func New(db bun.IDB, opts ...Option) *cobra.Command {
	var o options
	for _, opt := range opts {
		opt(&o)
	}
	status := &cobra.Command{
		Use:   "status",
		Short: "显示 outbox 待转发、延迟任务和 JetStream 中各 topic 的状态",
		Args:  command.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			s, err := queue.ReadStatus(cmd.Context(), db)
			if err != nil {
				return err
			}
			w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 4, 2, ' ', 0)
			fmt.Fprintf(w, "outbox pending\t%d\n", s.Pending)
			fmt.Fprintf(w, "outbox deferred\t%d\n", s.Deferred)
			if !s.OldestPending.IsZero() {
				fmt.Fprintf(w, "oldest pending\t%s\n", s.OldestPending.Local().Format(time.DateTime))
			}
			if o.jetStream == nil {
				return w.Flush()
			}
			return printJetStream(cmd, w, queue.NewAdmin(o.jetStream.nc, o.jetStream.cfg))
		},
	}

	list := &cobra.Command{
		Use:   "failed",
		Short: "列出失败任务，从新到旧",
		Args:  command.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			// 从 cmd 读取选项：command.Bind 用重新声明的命令的 RunE 执行，选项只解析在 cmd 上。
			topic, _ := cmd.Flags().GetString("topic")
			limit, _ := cmd.Flags().GetInt("limit")
			jobs, err := queue.NewFailedJobs(db).List(cmd.Context(), queue.FailedQuery{Topic: topic, Limit: limit})
			if err != nil {
				return err
			}
			w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 4, 2, ' ', 0)
			fmt.Fprintln(w, "ID\tTOPIC\tATTEMPTS\tFAILED AT\tERROR")
			for _, j := range jobs {
				fmt.Fprintln(w, strings.Join([]string{j.ID.String(), j.Topic, strconv.Itoa(j.Attempts),
					j.FailedAt.Local().Format(time.DateTime), firstLine(j.Error)}, "\t"))
			}
			return w.Flush()
		},
	}
	list.Flags().String("topic", "", "只列出这个 topic 的失败任务")
	list.Flags().Int("limit", 20, "最多列出的条数")

	root := &cobra.Command{Use: "queue", Short: "队列"}
	root.AddCommand(status, list)
	return command.InGroup(root, command.GroupOps)
}

// bytes 把字节数写成便于阅读的形式。
func bytes(n int64) string {
	const unit = 1024
	if n < unit {
		return strconv.FormatInt(n, 10) + " B"
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}

// firstLine 返回错误信息的第一行，超过 errorWidth 个字符时截断。
func firstLine(s string) string {
	s, _, _ = strings.Cut(s, "\n")
	if utf8.RuneCountInString(s) <= errorWidth {
		return s
	}
	return string([]rune(s)[:errorWidth-1]) + "…"
}

// printJetStream 接着 w 中数据库一侧的状态输出 stream 的状态，再输出各 topic 的表格。
func printJetStream(cmd *cobra.Command, w *tabwriter.Writer, admin *queue.Admin) error {
	st, err := admin.Status(cmd.Context())
	if err != nil {
		return errors.Join(w.Flush(), err) // 数据库一侧的状态照常输出
	}
	if !st.Exists {
		fmt.Fprintf(w, "stream\t%s (not created yet: start a worker)\n", st.Stream)
		return w.Flush()
	}
	fmt.Fprintf(w, "stream\t%s\n", st.Stream)
	fmt.Fprintf(w, "messages\t%d (%s)\n", st.Messages, bytes(int64(st.Bytes)))
	replicas := strconv.Itoa(st.Replicas)
	if st.Leader != "" {
		replicas += " (leader " + st.Leader + ")"
	}
	fmt.Fprintf(w, "replicas\t%s\n", replicas)
	if err := w.Flush(); err != nil || len(st.Topics) == 0 {
		return err
	}
	fmt.Fprintln(cmd.OutOrStdout())
	w = tabwriter.NewWriter(cmd.OutOrStdout(), 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "TOPIC\tPENDING\tIN FLIGHT\tREDELIVERED\tLAST DELIVERED\tSTATE")
	for _, t := range st.Topics {
		last := "-"
		if !t.LastDelivered.IsZero() {
			last = t.LastDelivered.Local().Format(time.DateTime)
		}
		fmt.Fprintf(w, "%s\t%d\t%d\t%d\t%s\t%s\n", t.Topic, t.Pending, t.InFlight, t.Redelivered, last, topicState(t))
	}
	return w.Flush()
}

// topicState 是 topic 需要注意的状态，正常时为 "ok"。
func topicState(t queue.TopicStatus) string {
	switch {
	case !t.Handled:
		return "no handler: no worker has registered this topic"
	case t.Paused():
		return "paused until " + t.PausedUntil.Local().Format(time.DateTime)
	case t.Stalled():
		return "stalled: no worker is fetching"
	}
	return "ok"
}
