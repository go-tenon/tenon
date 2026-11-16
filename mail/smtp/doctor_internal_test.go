package smtp

import (
	"net/textproto"
	"strings"
	"testing"

	"github.com/cockroachdb/errors"

	"github.com/go-tenon/tenon/doctor"
)

func TestDialProblemAuth(t *testing.T) {
	t.Parallel()
	cfg := Config{Host: "smtp.example.com", Port: 587}
	err := dialProblem(cfg, errors.Wrap(&textproto.Error{Code: 535, Msg: "5.7.8 authentication failed"}, "auth"))
	var p *doctor.Problem
	if !errors.As(err, &p) || !strings.Contains(p.Summary, "smtp.example.com:587 rejected the username or password") ||
		!strings.Contains(p.Fix, "app password") {
		t.Errorf("535: %v", err)
	}
	other := errors.New("unexpected EOF")
	if got := dialProblem(cfg, other); got != other {
		t.Errorf("unclassified error = %v, want it unchanged", got)
	}
}
