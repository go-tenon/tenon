package queue

import (
	"testing"
	"time"
)

// SetRelayRetry 在测试期间缩短待命的 Worker 重试复制槽的间隔。
func SetRelayRetry(t testing.TB, d time.Duration) {
	old := relayRetry
	relayRetry = d
	t.Cleanup(func() { relayRetry = old })
}

// SetBeforeFetch 让 w 在每次拉取任务之前调用 fn，在 Run 之前设置。
func SetBeforeFetch(w *Worker, fn func()) { w.beforeFetch = fn }
