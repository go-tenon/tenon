package smtp_test

import (
	"context"
	"net"
	"strings"
	"testing"

	"github.com/go-tenon/tenon/doctor"
	"github.com/go-tenon/tenon/mail/smtp"
)

func check(t *testing.T, cfg smtp.Config) doctor.Result {
	t.Helper()
	m, err := smtp.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return doctor.Run(context.Background(), smtp.Doctor(m)).Results[0]
}

func TestDoctor(t *testing.T) {
	srv := newServer(t)
	if res := check(t, config(srv.port())); res.Status != doctor.Passed {
		t.Errorf("reachable server: %+v", res)
	}

	cfg := config(srv.port())
	cfg.TLS = smtp.TLSStartTLS
	if res := check(t, cfg); res.Status != doctor.Failed || !strings.Contains(res.Summary, "does not offer STARTTLS") {
		t.Errorf("starttls on a plain server: %+v", res)
	}

	cfg.TLS = smtp.TLSImplicit
	if res := check(t, cfg); res.Status != doctor.Failed || !strings.Contains(res.Summary, "does not speak TLS directly") {
		t.Errorf("implicit TLS on a plain server: %+v", res)
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	closed := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	if res := check(t, config(closed)); res.Status != doctor.Failed || !strings.Contains(res.Summary, "cannot connect") ||
		!strings.Contains(res.Fix, "port 25") {
		t.Errorf("closed port: %+v", res)
	}
	if len(srv.received()) != 0 {
		t.Errorf("the check sent mail: %d", len(srv.received()))
	}
}
