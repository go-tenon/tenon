package queue

import (
	"testing"
	"time"
)

// SetRelayInterval 在测试期间缩短 Worker 轮询 queue_outbox 的基础间隔。
func SetRelayInterval(t testing.TB, d time.Duration) {
	old := relayMinInterval
	relayMinInterval = d
	t.Cleanup(func() { relayMinInterval = old })
}
