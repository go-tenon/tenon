package queue

import (
	"strings"
	"testing"

	"github.com/cockroachdb/errors"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/go-tenon/tenon/doctor"
)

// verdict 把检查函数的结果归为 passed、warning、failed 或其他错误（error）。
func verdict(err error) string {
	var p *doctor.Problem
	switch {
	case err == nil:
		return "passed"
	case errors.As(err, &p) && p.Warning:
		return "warning"
	case errors.As(err, &p):
		return "failed"
	}
	return "error"
}

func TestDoctorJudgements(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		err  error
		want string
	}{
		{"postgres 18", checkPostgresVersion(180001, "18.1"), "passed"},
		{"postgres 17", checkPostgresVersion(170000, "17.0"), "passed"},
		{"postgres 16", checkPostgresVersion(160004, "16.4"), "failed"},
		{"wal_level logical", checkWALLevel("logical"), "passed"},
		{"wal_level replica", checkWALLevel("replica"), "failed"},
		{"replication limits", checkReplicationLimits(10, 10), "passed"},
		{"no slots", checkReplicationLimits(0, 10), "failed"},
		{"no senders", checkReplicationLimits(10, 0), "failed"},
		{"keep size set", checkWALKeepSize("10GB"), "passed"},
		{"keep size unlimited", checkWALKeepSize("-1"), "warning"},
		{"slot ok", checkSlotStatus(Status{Slot: "s", Exists: true, WALStatus: "reserved"}), "passed"},
		{"slot extended", checkSlotStatus(Status{Slot: "s", Exists: true, WALStatus: "extended"}), "passed"},
		{"slot unreserved", checkSlotStatus(Status{Slot: "s", Exists: true, WALStatus: "unreserved"}), "warning"},
		{"slot lost", checkSlotStatus(Status{Slot: "s", Exists: true, WALStatus: "lost"}), "failed"},
		{"slot missing", checkSlotStatus(Status{Slot: "s"}), "failed"},
		{"nats 2.15", checkNATSVersion("2.15.1"), "passed"},
		{"nats 3.0", checkNATSVersion("3.0.0"), "passed"},
		{"nats v2.16 beta", checkNATSVersion("v2.16.0-beta.1"), "passed"},
		{"nats 2.12", checkNATSVersion("2.12.3"), "failed"},
		{"nats 1.99", checkNATSVersion("1.99.0"), "failed"},
		{"nats unknown", checkNATSVersion(""), "failed"},
		{"jetstream on", checkJetStream(nil), "passed"},
		{"jetstream off", checkJetStream(jetstream.ErrJetStreamNotEnabled), "failed"},
		{"jetstream off for account", checkJetStream(errors.Wrap(jetstream.ErrJetStreamNotEnabledForAccount, "info")), "failed"},
		{"jetstream unreachable", checkJetStream(errors.New("timeout")), "error"},
	}
	for _, c := range cases {
		if got := verdict(c.err); got != c.want {
			t.Errorf("%s: %s (%v), want %s", c.name, got, c.err, c.want)
		}
	}
}

func TestDoctorFixes(t *testing.T) {
	t.Parallel()
	var p *doctor.Problem
	if !errors.As(checkWALLevel("replica"), &p) || !strings.Contains(p.Fix, "wal_level=logical") || p.Doc == "" {
		t.Errorf("wal_level problem = %+v", p)
	}
	if !errors.As(checkSlotStatus(Status{Slot: "s"}), &p) || !strings.Contains(p.Fix, "queue slot recreate") {
		t.Errorf("missing slot problem = %+v", p)
	}
}
