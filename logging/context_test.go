package logging_test

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"

	"github.com/go-tenon/tenon/logging"
)

func TestRequestID(t *testing.T) {
	ctx := context.Background()
	if got := logging.RequestID(ctx); got != "" {
		t.Fatalf("empty context: %q", got)
	}
	if got := logging.WithRequestID(ctx, ""); got != ctx {
		t.Fatal("empty id must return ctx unchanged")
	}

	ctx = logging.WithAttrs(ctx, slog.String("user", "alice"))
	first := logging.WithRequestID(ctx, "r1")
	second := logging.WithRequestID(first, "r2")
	if got := logging.RequestID(first); got != "r1" {
		t.Fatalf("first: %q", got)
	}
	if got := logging.RequestID(second); got != "r2" {
		t.Fatalf("the last request ID wins: %q", got)
	}
	// 直接写入 request_id 字段（例如队列还原发布方的字段）同样读得到。
	if got := logging.RequestID(logging.WithAttrs(ctx, slog.String(logging.AttrRequestID, "r3"))); got != "r3" {
		t.Fatalf("attr: %q", got)
	}

	var buf bytes.Buffer
	logging.New(logging.Config{Writer: &buf}).InfoContext(first, "hello")
	if !strings.Contains(buf.String(), `"request_id":"r1"`) {
		t.Fatalf("log: %s", buf.String())
	}
}
