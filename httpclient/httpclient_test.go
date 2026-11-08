package httpclient

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/shellphy-kit/tools/errs"
)

func TestNewTimeout(t *testing.T) {
	cases := []struct {
		in, want time.Duration
	}{
		{0, DefaultTimeout},
		{5 * time.Second, 5 * time.Second},
		{-1, 0},
	}
	for _, c := range cases {
		if got := New(Options{Timeout: c.in}).Timeout; got != c.want {
			t.Errorf("Timeout %v: got %v, want %v", c.in, got, c.want)
		}
	}
}

func TestNewTransport(t *testing.T) {
	tr := NewTransport()
	if tr == http.DefaultTransport {
		t.Fatal("NewTransport must not return http.DefaultTransport itself")
	}
	if tr.MaxIdleConnsPerHost != 100 {
		t.Errorf("MaxIdleConnsPerHost = %d, want 100", tr.MaxIdleConnsPerHost)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestNewUsesCustomTransport(t *testing.T) {
	called := false
	c := New(Options{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			called = true
			return &http.Response{StatusCode: 204, Body: http.NoBody, Request: r}, nil
		}),
		Logger: slog.New(slog.DiscardHandler),
	})
	resp, err := c.Get("http://example.test/")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if !called {
		t.Error("custom transport was not used")
	}
}

// refusedAddr returns an address nothing listens on.
func refusedAddr(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	l.Close()
	return addr
}

func TestTransportErrorClassification(t *testing.T) {
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(5 * time.Second):
		}
	}))
	defer slow.Close()

	// Accepts connections and closes them without responding.
	hangup, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer hangup.Close()
	go func() {
		for {
			c, err := hangup.Accept()
			if err != nil {
				return
			}
			_, _ = bufio.NewReader(c).ReadString('\n')
			c.Close()
		}
	}()

	quiet := slog.New(slog.DiscardHandler)
	canceled, cancel := context.WithCancel(context.Background())
	cancel()

	cases := []struct {
		name    string
		client  *http.Client
		ctx     context.Context
		url     string
		want    errs.Code
		checkIs error
	}{
		{"connection refused", New(Options{Logger: quiet}), context.Background(),
			"http://" + refusedAddr(t), errs.Unavailable, syscall.ECONNREFUSED},
		{"connection closed", New(Options{Logger: quiet}), context.Background(),
			"http://" + hangup.Addr().String(), errs.Unavailable, nil},
		{"client timeout", New(Options{Timeout: 50 * time.Millisecond, Logger: quiet}), context.Background(),
			slow.URL, errs.DeadlineExceeded, context.DeadlineExceeded},
		{"context canceled", New(Options{Logger: quiet}), canceled,
			slow.URL, errs.Canceled, context.Canceled},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req, _ := http.NewRequestWithContext(c.ctx, http.MethodGet, c.url, nil)
			resp, err := c.client.Do(req)
			if err == nil {
				resp.Body.Close()
				t.Fatal("expected error")
			}
			if got := errs.CodeOf(err); got != c.want {
				t.Errorf("CodeOf = %q, want %q (err: %v)", got, c.want, err)
			}
			if c.checkIs != nil && !errors.Is(err, c.checkIs) {
				t.Errorf("errors.Is(err, %v) = false; original cause must stay reachable", c.checkIs)
			}
		})
	}

	t.Run("context deadline", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		defer cancel()
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, slow.URL, nil)
		_, err := New(Options{Logger: quiet}).Do(req)
		if got := errs.CodeOf(err); got != errs.DeadlineExceeded {
			t.Errorf("CodeOf = %q, want deadline_exceeded (err: %v)", got, err)
		}
	})
}

func TestClassify(t *testing.T) {
	other := errors.New("other")
	cases := []struct {
		name string
		err  error
		want errs.Code
	}{
		{"nil", nil, ""},
		{"net timeout without context", &net.DNSError{IsTimeout: true}, errs.DeadlineExceeded},
		{"net error", &net.DNSError{IsNotFound: true}, errs.Unavailable},
		{"unexpected eof", io.ErrUnexpectedEOF, errs.Unavailable},
		{"canceled stays canceled", context.Canceled, errs.Canceled},
		{"unknown stays internal", other, errs.Internal},
	}
	for _, c := range cases {
		if got := errs.CodeOf(classify(c.err)); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
	if classify(other) != other {
		t.Error("unknown errors must be returned unchanged")
	}
}

type logRecord map[string]any

func captureLogger() (*slog.Logger, func() []logRecord) {
	var buf bytes.Buffer
	l := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	return l, func() []logRecord {
		var out []logRecord
		for line := range strings.SplitSeq(strings.TrimSpace(buf.String()), "\n") {
			if line == "" {
				continue
			}
			var r logRecord
			if err := json.Unmarshal([]byte(line), &r); err != nil {
				panic(err)
			}
			out = append(out, r)
		}
		buf.Reset()
		return out
	}
}

func TestLogging(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/fail" {
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
	defer srv.Close()

	logger, records := captureLogger()
	c := New(Options{Logger: logger})
	host := strings.TrimPrefix(srv.URL, "http://")

	get := func(ctx context.Context, url string) {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if resp, err := c.Do(req); err == nil {
			resp.Body.Close()
		}
	}

	t.Run("success is debug without query", func(t *testing.T) {
		get(context.Background(), srv.URL+"/ok?token=secret")
		rs := records()
		if len(rs) != 1 {
			t.Fatalf("got %d records", len(rs))
		}
		r := rs[0]
		if r["level"] != "DEBUG" || r["method"] != "GET" || r["host"] != host ||
			r["path"] != "/ok" || r["status"] != float64(200) {
			t.Errorf("unexpected record: %v", r)
		}
		if _, ok := r["duration"]; !ok {
			t.Errorf("missing duration: %v", r)
		}
		line, _ := json.Marshal(r)
		if strings.Contains(string(line), "secret") {
			t.Errorf("query leaked into log: %s", line)
		}
	})

	t.Run("5xx is warn", func(t *testing.T) {
		get(context.Background(), srv.URL+"/fail")
		r := records()[0]
		if r["level"] != "WARN" || r["status"] != float64(500) {
			t.Errorf("unexpected record: %v", r)
		}
	})

	t.Run("transport error is warn", func(t *testing.T) {
		get(context.Background(), "http://"+refusedAddr(t)+"/x")
		r := records()[0]
		if r["level"] != "WARN" || r["msg"] != "http client request failed" || r["err"] == nil {
			t.Errorf("unexpected record: %v", r)
		}
	})

	t.Run("cancellation is debug", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		get(ctx, srv.URL+"/ok")
		r := records()[0]
		if r["level"] != "DEBUG" || r["err"] == nil {
			t.Errorf("unexpected record: %v", r)
		}
	})
}

func TestLoggingUsesDefaultLoggerAtRequestTime(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer srv.Close()

	c := New(Options{}) // created before SetDefault
	logger, records := captureLogger()
	prev := slog.Default()
	slog.SetDefault(logger)
	defer slog.SetDefault(prev)

	resp, err := c.Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if len(records()) != 1 {
		t.Error("expected the request to be logged with the current default logger")
	}
}
