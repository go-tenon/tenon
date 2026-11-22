package health_test

import (
	"context"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cockroachdb/errors"

	"github.com/go-tenon/tenon/health"
	"github.com/go-tenon/tenon/logging/logtest"
)

func results(r health.Report) map[string]bool {
	ok := map[string]bool{}
	for _, res := range r.Results {
		ok[res.Name] = res.Err == nil
	}
	return ok
}

func TestRun(t *testing.T) {
	t.Parallel()
	var dbDown, cacheDown atomic.Bool
	check := func(down *atomic.Bool) func(context.Context) error {
		return func(context.Context) error {
			if down.Load() {
				return errors.New("connection refused")
			}
			return nil
		}
	}
	rec := logtest.New()
	c := health.New(health.Options{
		Checks: []health.Check{
			{Name: "database", Ping: check(&dbDown)},
			{Name: "cache", Ping: check(&cacheDown), Optional: true},
		},
		Logger: rec.Logger(),
	})
	ctx := context.Background()

	r := c.Run(ctx)
	if !r.Ready || len(r.Results) != 2 || r.Results[0].Name != "database" || !r.Results[1].Optional || !results(r)["cache"] {
		t.Fatalf("healthy: %+v", r)
	}
	// 可降级的依赖失败：仍然就绪，结果中带有原因。
	cacheDown.Store(true)
	if r = c.Run(ctx); !r.Ready || r.Results[1].Err == nil {
		t.Fatalf("cache down: %+v", r)
	}
	// 必需的依赖失败：不就绪。反复检查不重复记录日志。
	dbDown.Store(true)
	if r = c.Run(ctx); r.Ready || results(r)["database"] {
		t.Fatalf("db down: %+v", r)
	}
	c.Run(ctx)
	dbDown.Store(false)
	cacheDown.Store(false)
	c.Run(ctx)

	var events []string
	for _, r := range rec.Records() {
		events = append(events, r.Level.String()+" "+r.Message+" "+r.Attr("check").(string))
	}
	want := []string{
		"WARN readiness check failed cache",
		"WARN readiness check failed database",
		"INFO readiness check recovered database",
		"INFO readiness check recovered cache",
	}
	if len(events) != len(want) {
		t.Fatalf("log %v", events)
	}
	for i := range want {
		if events[i] != want[i] {
			t.Fatalf("log %v, want %v", events, want)
		}
	}
	if r := rec.Records()[1]; r.Attr("error.message") != "connection refused" {
		t.Fatalf("failure record %+v", r)
	}
}

func TestRunTimeoutAndPanic(t *testing.T) {
	t.Parallel()
	block := make(chan struct{})
	defer close(block)
	c := health.New(health.Options{
		Checks: []health.Check{
			// 不理会 ctx 的检查。
			{Name: "stuck", Ping: func(context.Context) error { <-block; return nil }},
			{Name: "slow", Ping: func(ctx context.Context) error { <-ctx.Done(); return ctx.Err() }},
			{Name: "broken", Ping: func(context.Context) error { panic("nil client") }, Optional: true},
			{Name: "fine", Ping: func(context.Context) error { return nil }},
		},
		Timeout: 50 * time.Millisecond,
		Logger:  slog.New(slog.DiscardHandler),
	})
	start := time.Now()
	r := c.Run(context.Background())
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("took %s", elapsed)
	}
	got := results(r)
	if r.Ready || got["stuck"] || got["slow"] || got["broken"] || !got["fine"] {
		t.Fatalf("%+v", r)
	}
}

func TestRunWithoutChecks(t *testing.T) {
	t.Parallel()
	if r := health.New(health.Options{}).Run(context.Background()); !r.Ready || len(r.Results) != 0 {
		t.Fatalf("%+v", r)
	}
}

func TestDefaultLoggerComponent(t *testing.T) {
	// 修改 slog.Default，不能并行。
	rec := logtest.New()
	prev := slog.Default()
	slog.SetDefault(rec.Logger())
	defer slog.SetDefault(prev)

	c := health.New(health.Options{Checks: []health.Check{
		{Name: "database", Ping: func(context.Context) error { return errors.New("down") }},
	}})
	c.Run(context.Background())
	if records := rec.Records(); len(records) != 1 || records[0].Attr("component") != "health" {
		t.Fatalf("records %+v", records)
	}
}

func TestNewPanicsOnBadChecks(t *testing.T) {
	t.Parallel()
	ok := func(context.Context) error { return nil }
	for name, checks := range map[string][]health.Check{
		"no name":   {{Ping: ok}},
		"no ping":   {{Name: "database"}},
		"duplicate": {{Name: "database", Ping: ok}, {Name: "database", Ping: ok}},
	} {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("%s: no panic", name)
				}
			}()
			health.New(health.Options{Checks: checks})
		}()
	}
}
