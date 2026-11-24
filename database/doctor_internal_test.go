package database

import (
	"strings"
	"testing"
	"time"

	"github.com/cockroachdb/errors"

	"github.com/go-tenon/tenon/doctor"
)

func TestCheckPGVersion(t *testing.T) {
	t.Parallel()
	for _, raw := range []string{"180000", "180003", "190001"} {
		if err := checkPGVersion(raw); err != nil {
			t.Errorf("checkPGVersion(%q) = %v, want nil", raw, err)
		}
	}
	for _, raw := range []string{"170005", "160000", "", "not-a-number"} {
		var p *doctor.Problem
		if err := checkPGVersion(raw); !errors.As(err, &p) || p.Warning || !strings.Contains(p.Fix, "18") || p.Doc == "" {
			t.Errorf("checkPGVersion(%q) = %v, want failure with fix and doc", raw, err)
		}
	}
	var p *doctor.Problem
	if errors.As(checkPGVersion("170005"), &p) && !strings.Contains(p.Summary, "17.5") {
		t.Errorf("summary = %q, want 17.5", p.Summary)
	}
}

func TestCheckClock(t *testing.T) {
	t.Parallel()
	before := time.Date(2026, 11, 17, 8, 0, 0, 0, time.UTC)
	after := before.Add(300 * time.Millisecond) // 一次查询的往返
	cases := []struct {
		name   string
		server time.Time
		warn   bool
	}{
		{"within the round trip", before.Add(100 * time.Millisecond), false},
		{"slightly behind", before.Add(-800 * time.Millisecond), false},
		{"slightly ahead", after.Add(time.Second), false},
		{"behind", before.Add(-3 * time.Second), true},
		{"ahead", after.Add(2 * time.Second), true},
	}
	for _, c := range cases {
		err := checkClock(before, after, c.server)
		var p *doctor.Problem
		if got := errors.As(err, &p) && p.Warning; got != c.warn {
			t.Errorf("%s: warning = %v (%v), want %v", c.name, got, err, c.warn)
		}
	}
	var p *doctor.Problem
	if errors.As(checkClock(before, after, before.Add(-3*time.Second)), &p) && p.Summary != "the database clock is 3s behind this host" {
		t.Errorf("summary = %q", p.Summary)
	}
}
