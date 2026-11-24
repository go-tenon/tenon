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
		{"nats 2.10", checkNATSVersion("2.10.0"), "passed"},
		{"nats 2.15", checkNATSVersion("2.15.1"), "passed"},
		{"nats 3.0", checkNATSVersion("3.0.0"), "passed"},
		{"nats v2.16 beta", checkNATSVersion("v2.16.0-beta.1"), "passed"},
		{"nats 2.9", checkNATSVersion("2.9.25"), "failed"},
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
	if !errors.As(checkNATSVersion("2.9.0"), &p) || !strings.Contains(p.Fix, "2.10") || p.Doc == "" {
		t.Errorf("nats_version problem = %+v", p)
	}
	if !errors.As(checkJetStream(jetstream.ErrJetStreamNotEnabled), &p) || !strings.Contains(p.Fix, "-js") {
		t.Errorf("jetstream problem = %+v", p)
	}
}
