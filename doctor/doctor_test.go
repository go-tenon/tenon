package doctor_test

import (
	"context"
	"log/slog"
	"strings"
	"testing"

	"github.com/cockroachdb/errors"

	"github.com/go-tenon/tenon/doctor"
	"github.com/go-tenon/tenon/errorx"
	"github.com/go-tenon/tenon/logging/logtest"
)

func pass(context.Context) error { return nil }

func returns(err error) func(context.Context) error {
	return func(context.Context) error { return err }
}

func statuses(r doctor.DoctorReport) []string {
	var out []string
	for _, res := range r.Results {
		out = append(out, res.Group+"/"+res.Check+"="+string(res.Status))
	}
	return out
}

func TestRun(t *testing.T) {
	t.Parallel()
	report := doctor.Run(context.Background(),
		doctor.Group{Name: "postgres", Checks: []doctor.Check{
			{Name: "connection", Run: pass},
			{Name: "settings", Run: returns(doctor.Fail("wal_level is replica", "set wal_level=logical").See("docs/queue.md"))},
			{Name: "disk", Run: returns(doctor.Warn("no limit", "set a limit"))},
			{Name: "slot", Run: returns(doctor.Skip("migrations have not been applied"))},
		}},
		doctor.Group{Name: "nats", Checks: []doctor.Check{
			{Name: "connection", Run: returns(errors.New("connection refused"))},
			{Name: "jetstream", Run: pass},
		}},
	)
	want := []string{
		"postgres/connection=passed", "postgres/settings=failed", "postgres/disk=warning", "postgres/slot=skipped",
		"nats/connection=failed", "nats/jetstream=skipped",
	}
	if got := statuses(report); strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("results = %v, want %v", got, want)
	}
	settings := report.Results[1]
	if settings.Summary != "wal_level is replica" || settings.Fix != "set wal_level=logical" || settings.Doc != "docs/queue.md" {
		t.Errorf("settings = %+v", settings)
	}
	if got := report.Results[3].Summary; got != "migrations have not been applied" {
		t.Errorf("skip reason = %q", got)
	}
	if got := report.Results[4].Summary; got != "connection refused" {
		t.Errorf("error summary = %q", got)
	}
	if got := report.Results[5].Summary; !strings.Contains(got, "connection") {
		t.Errorf("skipped after error = %q, want it to name the check that could not complete", got)
	}
	if report.Count(doctor.Failed) != 2 || report.Count(doctor.Warning) != 1 || report.Count(doctor.Passed) != 1 || report.Count(doctor.Skipped) != 2 {
		t.Errorf("counts: %d failed, %d warning, %d passed, %d skipped",
			report.Count(doctor.Failed), report.Count(doctor.Warning), report.Count(doctor.Passed), report.Count(doctor.Skipped))
	}
}

func TestRunWrappedProblem(t *testing.T) {
	t.Parallel()
	report := doctor.Run(context.Background(), doctor.Group{Name: "g", Checks: []doctor.Check{
		{Name: "wrapped", Run: returns(errors.Wrap(doctor.Fail("bad", "fix it"), "context"))},
		{Name: "next", Run: pass},
	}})
	if got := statuses(report); got[0] != "g/wrapped=failed" || got[1] != "g/next=passed" {
		t.Fatalf("results = %v: a wrapped problem is still a problem, later checks run", got)
	}
	if report.Results[0].Fix != "fix it" {
		t.Errorf("fix = %q", report.Results[0].Fix)
	}
}

func TestRunPanic(t *testing.T) {
	t.Parallel()
	report := doctor.Run(context.Background(), doctor.Group{Name: "g", Checks: []doctor.Check{
		{Name: "boom", Run: func(context.Context) error { panic("nil db") }},
	}})
	if res := report.Results[0]; res.Status != doctor.Failed || !strings.Contains(res.Summary, "panic: nil db") {
		t.Fatalf("result = %+v", res)
	}
}

func TestRunCanceled(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	report := doctor.Run(ctx, doctor.Group{Name: "g", Checks: []doctor.Check{
		{Name: "stuck", Run: func(context.Context) error { <-release; return nil }}, // 不理会 ctx 的检查
	}})
	if res := report.Results[0]; res.Status != doctor.Failed {
		t.Fatalf("result = %+v, want failed when the context ends", res)
	}
}

func TestErr(t *testing.T) {
	t.Parallel()
	ok := doctor.Run(context.Background(), doctor.Group{Name: "g", Checks: []doctor.Check{
		{Name: "a", Run: pass},
		{Name: "b", Run: returns(doctor.Warn("risky", "tune it"))},
	}})
	if err := ok.Err(); err != nil {
		t.Fatalf("Err with only warnings = %v", err)
	}
	bad := doctor.Run(context.Background(), doctor.Group{Name: "queue", Checks: []doctor.Check{
		{Name: "wal_level", Run: returns(doctor.Fail("wal_level is replica", "set wal_level=logical"))},
	}})
	err := bad.Err()
	if !errors.Is(err, doctor.ErrFailed) || errorx.KindOf(err) != errorx.Unavailable {
		t.Fatalf("Err = %v (kind %v), want doctor.ErrFailed of kind Unavailable", err, errorx.KindOf(err))
	}
	if msg := err.Error(); !strings.HasSuffix(msg, "1 check(s) failed\n  queue/wal_level: wal_level is replica\n    fix: set wal_level=logical") {
		t.Errorf("message = %q", msg)
	}
}

func TestWriteText(t *testing.T) {
	t.Parallel()
	report := doctor.Run(context.Background(),
		doctor.Group{Name: "queue", Checks: []doctor.Check{
			{Name: "version", Run: pass},
			{Name: "wal_level", Run: returns(doctor.Fail("wal_level is replica", "line one\nline two").See("docs/queue.md"))},
		}},
		doctor.Group{Name: "storage", Checks: []doctor.Check{
			{Name: "write", Run: returns(doctor.Skip("no disk"))},
		}},
	)
	var b strings.Builder
	if err := report.WriteText(&b); err != nil {
		t.Fatal(err)
	}
	want := `queue
  ok    version
  FAIL  wal_level: wal_level is replica
        fix: line one
             line two
        see: docs/queue.md

storage
  skip  write: no disk

1 failed, 0 warnings, 1 passed, 1 skipped
`
	if b.String() != want {
		t.Errorf("text =\n%s\nwant\n%s", b.String(), want)
	}
}

func TestRequire(t *testing.T) {
	t.Parallel()
	rec := logtest.New()
	err := doctor.RequireWithLogger(context.Background(), rec.Logger(), doctor.Group{Name: "postgres", Checks: []doctor.Check{
		{Name: "keep", Run: returns(doctor.Warn("no WAL limit", "set max_slot_wal_keep_size"))},
	}})
	if err != nil {
		t.Fatalf("Require with only warnings = %v", err)
	}
	r, ok := rec.Find("doctor warning")
	if !ok || r.Level != slog.LevelWarn || r.Attr("check") != "keep" || r.Attr("fix") != "set max_slot_wal_keep_size" {
		t.Errorf("warning log = %+v, found %v", r, ok)
	}
	err = doctor.Require(context.Background(), doctor.Group{Name: "postgres", Checks: []doctor.Check{
		{Name: "connection", Run: returns(errors.New("refused"))},
	}})
	if !errors.Is(err, doctor.ErrFailed) {
		t.Errorf("Require with a failure = %v", err)
	}
}
