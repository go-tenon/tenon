package gotenberg_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-tenon/tenon/doctor"
	"github.com/go-tenon/tenon/pdf/gotenberg"
)

func TestDoctor(t *testing.T) {
	t.Parallel()
	for _, res := range doctor.Run(context.Background(), gotenberg.Doctor(openGotenberg(t))).Results {
		if res.Status != doctor.Passed {
			t.Errorf("%s = %+v, want passed on the test service", res.Check, res)
		}
	}
}

func TestDoctorProblems(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		handler http.HandlerFunc
		summary string
	}{
		{"chromium down", func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{"status":"down","details":{"chromium":{"status":"down","error":"browser crashed"}}}`))
		}, "chromium: down browser crashed"},
		{"basic auth", func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
		}, "rejected the username or password"},
		{"not gotenberg", func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte("<html>"))
		}, "did not answer like Gotenberg"},
	}
	for _, c := range cases {
		srv := httptest.NewServer(c.handler)
		r, err := gotenberg.New(gotenberg.Config{URL: srv.URL})
		if err != nil {
			t.Fatal(err)
		}
		res := doctor.Run(context.Background(), gotenberg.Doctor(r)).Results[0]
		if res.Status != doctor.Failed || !strings.Contains(res.Summary, c.summary) {
			t.Errorf("%s: %+v", c.name, res)
		}
		srv.Close()
	}
	r, err := gotenberg.New(gotenberg.Config{URL: "http://127.0.0.1:1"})
	if err != nil {
		t.Fatal(err)
	}
	if res := doctor.Run(context.Background(), gotenberg.Doctor(r)).Results; res[0].Status != doctor.Failed ||
		!strings.Contains(res[0].Summary, "cannot reach") || res[1].Status != doctor.Skipped {
		t.Errorf("unreachable: %+v", res)
	}
}
