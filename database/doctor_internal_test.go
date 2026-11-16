package database

import (
	"testing"
	"time"

	"github.com/cockroachdb/errors"

	"github.com/go-tenon/tenon/doctor"
)

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
