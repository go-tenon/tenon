package queue

import (
	"math"
	"strings"
	"testing"
	"time"

	"github.com/cockroachdb/errors"
	"github.com/google/uuid"

	"github.com/go-tenon/tenon/errorx"
)

func TestBackoff(t *testing.T) {
	t.Parallel()
	for attempts, want := range map[int]time.Duration{1: time.Second, 2: 16 * time.Second, 5: 625 * time.Second} {
		for range 100 {
			got := backoff(attempts)
			if got < want*9/10 || got > want*11/10 {
				t.Fatalf("backoff(%d) = %v, want %v ±10%%", attempts, got, want)
			}
		}
	}
	for _, attempts := range []int{18, 1000, math.MaxInt} {
		if got := backoff(attempts); got != maxBackoff {
			t.Fatalf("backoff(%d) = %v, want %v", attempts, got, maxBackoff)
		}
	}
}

func TestRetryDelay(t *testing.T) {
	t.Parallel()
	base := errors.New("rate limited")
	if got := retryDelay(errors.Wrap(RetryAfter(base, 30*time.Second), "send"), 1); got != 30*time.Second {
		t.Fatalf("wrapped RetryAfter = %v", got)
	}
	if got := retryDelay(RetryAfter(base, -time.Second), 3); got != 0 {
		t.Fatalf("negative RetryAfter = %v", got)
	}
	if got := retryDelay(base, 1); got < 900*time.Millisecond || got > 1100*time.Millisecond {
		t.Fatalf("plain error = %v, want backoff", got)
	}
	// errorx.RetryAfter 给出的时间，没有 RetryAfter 时使用。
	limited := errorx.Define(errorx.ResourceExhausted, "demo.limited").New(errorx.RetryAfter(45 * time.Second))
	if got := retryDelay(errors.Wrap(limited, "send"), 1); got != 45*time.Second {
		t.Fatalf("errorx RetryAfter = %v", got)
	}
	if got := retryDelay(RetryAfter(limited, 5*time.Second), 1); got != 5*time.Second {
		t.Fatalf("RetryAfter over errorx = %v", got)
	}
	if got := retryDelay(Retry(limited), 1); got != 45*time.Second {
		t.Fatalf("Retry over errorx = %v", got)
	}
	if got := retryDelay(Retry(base), 2); got < 14*time.Second || got > 18*time.Second {
		t.Fatalf("Retry = %v, want backoff", got)
	}
}

func TestNames(t *testing.T) {
	t.Parallel()
	for name, want := range map[string]bool{
		"mail.send": true, "a": true, "blog.mail-send_v2": true,
		"": false, ".a": false, "a.": false, "a..b": false, "a b": false, "a*": false, "a>": false,
		strings.Repeat("a", 64): true, strings.Repeat("a", 65): false,
	} {
		if got := validName(name); got != want {
			t.Errorf("validName(%q) = %v", name, got)
		}
	}
	for name, want := range map[string]bool{"jobs": true, "blog_prod-1": true, "": false, "a.b": false, strings.Repeat("a", 33): false} {
		if got := validStream(name); got != want {
			t.Errorf("validStream(%q) = %v", name, got)
		}
	}
	if consumerName("blog.mail.send") != "blog_mail_send" {
		t.Error(consumerName("blog.mail.send"))
	}
}

func TestCeilSecond(t *testing.T) {
	t.Parallel()
	base := time.Date(2026, 1, 1, 0, 0, 1, 0, time.UTC)
	if got := ceilSecond(base); !got.Equal(base) {
		t.Errorf("whole second moved to %v", got)
	}
	if got := ceilSecond(base.Add(time.Millisecond)); !got.Equal(base.Add(time.Second)) {
		t.Errorf("rounded to %v", got)
	}
}

func TestMsgID(t *testing.T) {
	t.Parallel()
	id := uuid.Must(uuid.NewV7())
	at := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	// 同一次执行重新发布时 ID 相同，被去重；执行次数或可以执行的时间不同时 ID 不同。
	if msgID(id, 1, at) != msgID(id, 1, at.Add(time.Microsecond)) ||
		msgID(id, 1, at) == msgID(id, 2, at) || msgID(id, 1, at) == msgID(id, 1, at.Add(time.Second)) {
		t.Error("msgID must identify one execution of a job")
	}
}
