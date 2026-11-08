package logger

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
	"testing/slogtest"
)

func decode(t *testing.T, b []byte) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("unmarshal %q: %v", b, err)
	}
	return m
}

func TestContextHandlerConformance(t *testing.T) {
	var buf bytes.Buffer
	slogtest.Run(t,
		func(*testing.T) slog.Handler {
			buf.Reset()
			return NewContextHandler(slog.NewJSONHandler(&buf, nil))
		},
		func(t *testing.T) map[string]any {
			return decode(t, buf.Bytes())
		},
	)
}

func TestNewDefaults(t *testing.T) {
	var buf bytes.Buffer
	log := New(Options{Writer: &buf})

	log.Debug("hidden")
	if buf.Len() != 0 {
		t.Fatalf("debug should be filtered by default level, got %q", buf.String())
	}

	log.Info("hello", "k", "v")
	m := decode(t, buf.Bytes())
	if m["msg"] != "hello" || m["k"] != "v" || m["level"] != "INFO" {
		t.Fatalf("unexpected record: %v", m)
	}
}

func TestNewTextFormat(t *testing.T) {
	var buf bytes.Buffer
	New(Options{Writer: &buf, Format: "TEXT"}).Info("hello")
	if !strings.Contains(buf.String(), "msg=hello") {
		t.Fatalf("expected text output, got %q", buf.String())
	}
}

func TestNewDynamicLevel(t *testing.T) {
	var buf bytes.Buffer
	var level slog.LevelVar
	log := New(Options{Writer: &buf, Level: &level})

	log.Debug("a")
	if buf.Len() != 0 {
		t.Fatalf("debug should be filtered, got %q", buf.String())
	}

	level.Set(slog.LevelDebug)
	log.Debug("b")
	if decode(t, buf.Bytes())["msg"] != "b" {
		t.Fatalf("debug should be enabled after level change, got %q", buf.String())
	}
}

func TestWithAttrs(t *testing.T) {
	var buf bytes.Buffer
	log := New(Options{Writer: &buf})

	parent := WithAttrs(context.Background(), slog.String("request_id", "r1"))
	// 两个兄弟 context 不应互相影响。
	a := WithAttrs(parent, slog.Int("user_id", 1))
	b := WithAttrs(parent, slog.Int("user_id", 2))

	log.InfoContext(a, "x")
	ma := decode(t, buf.Bytes())
	buf.Reset()
	log.InfoContext(b, "x")
	mb := decode(t, buf.Bytes())
	buf.Reset()
	log.InfoContext(parent, "x")
	mp := decode(t, buf.Bytes())

	if ma["request_id"] != "r1" || ma["user_id"] != float64(1) {
		t.Errorf("a: %v", ma)
	}
	if mb["request_id"] != "r1" || mb["user_id"] != float64(2) {
		t.Errorf("b: %v", mb)
	}
	if _, ok := mp["user_id"]; ok || mp["request_id"] != "r1" {
		t.Errorf("parent: %v", mp)
	}
}

func TestWithAttrsEmpty(t *testing.T) {
	ctx := context.Background()
	if WithAttrs(ctx) != ctx {
		t.Fatal("WithAttrs without attrs should return ctx unchanged")
	}
}

func TestContextHandlerPreservedByWith(t *testing.T) {
	var buf bytes.Buffer
	log := New(Options{Writer: &buf}).With("service", "svc")
	ctx := WithAttrs(context.Background(), slog.String("request_id", "r1"))

	log.InfoContext(ctx, "x")
	m := decode(t, buf.Bytes())
	if m["service"] != "svc" || m["request_id"] != "r1" {
		t.Fatalf("unexpected record: %v", m)
	}
}

func TestContextHandlerNilContext(t *testing.T) {
	var buf bytes.Buffer
	h := NewContextHandler(slog.NewJSONHandler(&buf, nil))
	//lint:ignore SA1012 deliberately testing nil ctx
	if err := h.Handle(nil, slog.Record{Message: "x"}); err != nil { //nolint:staticcheck
		t.Fatal(err)
	}
}

func TestNewContextHandlerIdempotent(t *testing.T) {
	h := NewContextHandler(slog.NewJSONHandler(&bytes.Buffer{}, nil))
	if NewContextHandler(h) != h {
		t.Fatal("wrapping twice should return the same handler")
	}
}

func BenchmarkInfoContext(b *testing.B) {
	log := New(Options{Writer: discard{}})
	ctx := WithAttrs(context.Background(),
		slog.String("request_id", "r1"), slog.Int("user_id", 1))
	b.ReportAllocs()
	for b.Loop() {
		log.InfoContext(ctx, "msg", "k", "v")
	}
}

type discard struct{}

func (discard) Write(p []byte) (int, error) { return len(p), nil }
