package command_test

import (
	"bytes"
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/spf13/cobra"

	"github.com/go-tenon/tenon/cleanup"
	"github.com/go-tenon/tenon/command"
)

// serveApp 是 serve 测试的应用：记录 open、各部分的装配、运行和资源的关闭。
type serveApp struct {
	mu       sync.Mutex
	events   *[]string
	cancel   context.CancelFunc // 全部选中的部分都开始运行后调用，模拟收到停止信号
	running  int
	selected int
	failOn   string // 装配这个部分时失败
}

// record 追加一个事件。
func (a *serveApp) record(event string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	*a.events = append(*a.events, event)
}

// part 返回名为 name 的部分：装配时记录 start，运行时记录 run，全部部分都在运行后取消 ctx。
func (a *serveApp) part(name string) func(*command.Daemon) (func(context.Context) error, error) {
	return func(*command.Daemon) (func(context.Context) error, error) {
		a.record("start " + name)
		if name == a.failOn {
			return nil, errors.New("port in use")
		}
		return func(ctx context.Context) error {
			a.record("run " + name)
			a.mu.Lock()
			a.running++
			if a.running == a.selected {
				a.cancel()
			}
			a.mu.Unlock()
			<-ctx.Done()
			return nil
		}, nil
	}
}

// serveFixture 返回 serve 命令：http、worker 是默认部分，realtime 只单独运行。
// selected 是这次会运行的部分数，failOn 是装配时失败的部分。
func serveFixture(events *[]string, cancel context.CancelFunc, selected int, failOn string) *cobra.Command {
	open := func(_ context.Context, cfg settings, stack *cleanup.Stack) (*serveApp, error) {
		*events = append(*events, "open "+cfg.Name)
		stack.MustAdd("app", func(context.Context) error {
			*events = append(*events, "close")
			return nil
		})
		return &serveApp{events: events, cancel: cancel, selected: selected, failOn: failOn}, nil
	}
	return command.Serve(open, func(a *serveApp) []command.Part {
		return []command.Part{
			{Name: "http", Short: "HTTP 服务", Start: a.part("http")},
			{Name: "worker", Short: "队列 worker", Start: a.part("worker")},
			{Name: "realtime", Short: "只承接 WebSocket", Start: a.part("realtime"), Standalone: true},
		}
	})
}

// runServe 执行 serve args，返回事件和错误。
func runServe(t *testing.T, selected int, failOn string, args ...string) ([]string, error) {
	t.Helper()
	var events []string
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	root := newRoot(serveFixture(&events, cancel, selected, failOn))
	root.SetOut(new(bytes.Buffer))
	root.SetErr(new(bytes.Buffer))
	root.SetArgs(append(append([]string{"serve"}, args...), "--config", writeConfig(t, minimalConfig)))
	err := root.ExecuteContext(ctx)
	return events, err
}

func TestServeRunsAllDefaultParts(t *testing.T) {
	t.Parallel()
	for _, args := range [][]string{nil, {command.AllParts}} {
		events, err := runServe(t, 2, "", args...)
		if err != nil {
			t.Fatal(err)
		}
		checkAllParts(t, events)
	}
}

// checkAllParts 检查运行全部部分时的事件。
func checkAllParts(t *testing.T, events []string) {
	t.Helper()
	// 先装配全部部分，再同时运行；Standalone 的 realtime 不在其中；最后关闭资源。
	if want := []string{"open blog", "start http", "start worker"}; !slices.Equal(events[:3], want) {
		t.Fatalf("events = %q, want prefix %q", events, want)
	}
	run := events[3:5]
	slices.Sort(run)
	if !slices.Equal(run, []string{"run http", "run worker"}) || events[5] != "close" || len(events) != 6 {
		t.Fatalf("events = %q", events)
	}
}

func TestServeRunsOnePart(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"worker", "realtime"} {
		events, err := runServe(t, 1, "", name)
		if err != nil {
			t.Fatal(err)
		}
		want := []string{"open blog", "start " + name, "run " + name, "close"}
		if !slices.Equal(events, want) {
			t.Fatalf("serve %s: events = %q, want %q", name, events, want)
		}
	}
}

func TestServeStartFailureRunsNothing(t *testing.T) {
	t.Parallel()
	events, err := runServe(t, 2, "worker")
	if err == nil || !strings.Contains(err.Error(), "start worker") || !strings.Contains(err.Error(), "port in use") {
		t.Fatalf("err = %v", err)
	}
	if want := []string{"open blog", "start http", "start worker", "close"}; !slices.Equal(events, want) {
		t.Fatalf("events = %q, want %q", events, want)
	}
}

func TestServeRejectsUnknownOrExtraParts(t *testing.T) {
	t.Parallel()
	for _, args := range [][]string{{"mailer"}, {"http", "worker"}} {
		events, err := runServe(t, 0, "", args...)
		if command.ExitCode(err) != command.ExitUsage || len(events) != 0 {
			t.Errorf("serve %v: exit=%d err=%v events=%q", args, command.ExitCode(err), err, events)
		}
	}
}

func TestServeHelpListsPartsWithoutOpening(t *testing.T) {
	t.Parallel()
	var events []string
	root := newRoot(serveFixture(&events, func() {}, 0, ""))
	out, _, err := execute(root, "serve", "--help")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"http", "HTTP 服务", "worker", "realtime", "只承接 WebSocket（只单独运行）"} {
		if !strings.Contains(out, want) {
			t.Errorf("help lacks %q:\n%s", want, out)
		}
	}
	if len(events) != 0 {
		t.Fatalf("events = %q", events)
	}
}

func TestServeRejectsInvalidParts(t *testing.T) {
	t.Parallel()
	start := func(*command.Daemon) (func(context.Context) error, error) { return nil, nil }
	for _, parts := range [][]command.Part{
		{{Name: "", Start: start}},
		{{Name: "http", Start: start}, {Name: "http", Start: start}},
		{{Name: "http"}},
		{{Name: command.AllParts, Start: start}},
	} {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("parts %+v: no panic", parts)
				}
			}()
			command.Serve(func(context.Context, settings, *cleanup.Stack) (*serveApp, error) { return nil, nil },
				func(*serveApp) []command.Part { return parts })
		}()
	}
}

// Execute 按 InGroup 的标注分组列出根命令的子命令，没有标注的归入“应用”。
func TestExecuteGroupsCommands(t *testing.T) {
	t.Parallel()
	root := command.Root("blog", "test")
	root.AddCommand(
		serveFixture(new([]string), func() {}, 0, ""),
		command.Config[settings](),
		&cobra.Command{Use: "article", Short: "文章", RunE: func(*cobra.Command, []string) error { return nil }},
	)
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetArgs([]string{"--help"})
	command.Execute(root)
	help := out.String()
	sections := []string{"运行：", "serve", "运维：", "config", "应用：", "article"}
	at := 0
	for _, s := range sections {
		i := strings.Index(help[at:], s)
		if i < 0 {
			t.Fatalf("help lacks %q after offset %d:\n%s", s, at, help)
		}
		at += i
	}
}
