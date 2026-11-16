package doctorcmd_test

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/go-tenon/tenon/command"
	"github.com/go-tenon/tenon/doctor"
	"github.com/go-tenon/tenon/doctor/doctorcmd"
)

func group(results ...error) doctor.Group {
	g := doctor.Group{Name: "postgres"}
	for i, err := range results {
		g.Checks = append(g.Checks, doctor.Check{Name: string(rune('a' + i)), Run: func(context.Context) error { return err }})
	}
	return g
}

func execute(g doctor.Group, args ...string) (string, error) {
	root := command.Root("api", "test")
	root.AddCommand(doctorcmd.New(g))
	var out, errOut bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&errOut)
	root.SetArgs(append([]string{"doctor"}, args...))
	err := root.ExecuteContext(context.Background())
	return out.String(), err
}

func TestDoctorCommand(t *testing.T) {
	t.Parallel()
	warn := doctor.Warn("no WAL limit", "set max_slot_wal_keep_size")
	fail := doctor.Fail("wal_level is replica", "set wal_level=logical")

	out, err := execute(group(nil, warn))
	if err != nil || !strings.Contains(out, "warn  b: no WAL limit") || !strings.Contains(out, "0 failed, 1 warning, 1 passed, 0 skipped") {
		t.Errorf("warnings only: %v\n%s", err, out)
	}
	if _, err := execute(group(nil, warn), "--strict"); command.ExitCode(err) != command.ExitUnavailable {
		t.Errorf("--strict with a warning: %v", err)
	}
	out, err = execute(group(fail))
	if command.ExitCode(err) != command.ExitUnavailable || !strings.Contains(out, "FAIL  a: wal_level is replica") ||
		!strings.Contains(out, "fix: set wal_level=logical") {
		t.Errorf("failure: %v (exit %d)\n%s", err, command.ExitCode(err), out)
	}
	if _, err := execute(group(nil), "--format", "yaml"); command.ExitCode(err) != command.ExitUsage {
		t.Errorf("unknown format: %v", err)
	}
}

func TestDoctorCommandJSON(t *testing.T) {
	t.Parallel()
	out, err := execute(group(nil, doctor.Fail("wal_level is replica", "set wal_level=logical").See("docs/queue.md")), "--format", "json")
	if command.ExitCode(err) != command.ExitUnavailable {
		t.Fatalf("exit = %d: %v", command.ExitCode(err), err)
	}
	var report doctor.DoctorReport
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	want := doctor.DoctorResult{Group: "postgres", Check: "b", Status: doctor.Failed,
		Summary: "wal_level is replica", Fix: "set wal_level=logical", Doc: "docs/queue.md"}
	if len(report.Results) != 2 || report.Results[1] != want || report.Results[0].Status != doctor.Passed {
		t.Errorf("report = %+v", report)
	}
}
