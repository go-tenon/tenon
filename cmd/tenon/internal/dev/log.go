package dev

import (
	"io"
	"os"
	"regexp"
	"sync"

	"golang.org/x/term"
)

// LogFile 是 tenon dev 运行期间各进程输出的副本：编译错误、应用的日志、前端开发服务器的输出，去掉了颜色。
// 每次 tenon dev 启动时清空，AI 编程助手经 tenon mcp 的 tenon_dev_logs 读取它。
const LogFile = ".tenon/dev.log"

// logLimit 是 LogFile 的大小上限：超过时清空，从头写起。
const logLimit = 8 << 20

// ansi 匹配终端的控制序列（颜色等），写进 LogFile 时去掉。
var ansi = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]`)

// devLog 把写入的内容去掉控制序列后写进文件，文件超过 logLimit 时清空。
type devLog struct {
	mu   sync.Mutex
	f    *os.File
	size int64
}

// openLog 清空并打开 LogFile。
func openLog() (*devLog, error) {
	if err := os.MkdirAll(".tenon", 0o755); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(LogFile, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return nil, err
	}
	return &devLog{f: f}, nil
}

// Write 去掉 p 中的控制序列后写进文件，返回 len(p)。
func (l *devLog) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.size > logLimit {
		if err := l.f.Truncate(0); err != nil {
			return 0, err
		}
		if _, err := l.f.Seek(0, io.SeekStart); err != nil {
			return 0, err
		}
		l.size = 0
	}
	n, err := l.f.Write(ansi.ReplaceAll(p, nil))
	l.size += int64(n)
	if err != nil {
		return 0, err
	}
	return len(p), nil
}

// Close 关闭文件。
func (l *devLog) Close() error { return l.f.Close() }

// isTerminal 报告 w 是否是终端。
func isTerminal(w io.Writer) bool {
	f, ok := w.(*os.File)
	return ok && term.IsTerminal(int(f.Fd()))
}
