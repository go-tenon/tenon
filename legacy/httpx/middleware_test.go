package httpx

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"strings"
	"testing"
	"uuid"

	"github.com/go-chi/chi/v5"

	"github.com/ryan053857/go-kit/config"
	"github.com/ryan053857/go-kit/logger"
)

func TestMain(m *testing.M) {
	slog.SetDefault(slog.New(slog.DiscardHandler))
	os.Exit(m.Run())
}

// newLogger returns a JSON logger with context-attr support writing to buf.
func newLogger(buf *bytes.Buffer) *slog.Logger {
	return logger.New(logger.Options{Writer: buf, Level: slog.LevelDebug})
}

func decodeLines(t *testing.T, buf *bytes.Buffer) []map[string]any {
	t.Helper()
	var out []map[string]any
	for line := range strings.Lines(buf.String()) {
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("unmarshal %q: %v", line, err)
		}
		out = append(out, m)
	}
	return out
}

func TestRequestID(t *testing.T) {
	var gotID string
	var buf bytes.Buffer
	log := newLogger(&buf)
	h := RequestID(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotID = RequestIDFrom(r.Context())
		log.InfoContext(r.Context(), "inside")
	}))

	t.Run("generates UUIDv7", func(t *testing.T) {
		buf.Reset()
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))

		id, err := uuid.Parse(gotID)
		if err != nil || id[6]>>4 != 7 {
			t.Fatalf("request id %q is not a UUIDv7 (err %v)", gotID, err)
		}
		if got := rec.Header().Get(RequestIDHeader); got != gotID {
			t.Errorf("response header = %q, want %q", got, gotID)
		}
		if lines := decodeLines(t, &buf); lines[0]["request_id"] != gotID {
			t.Errorf("log request_id = %v, want %q", lines[0]["request_id"], gotID)
		}
	})

	for _, tc := range []struct {
		name, in string
		keep     bool
	}{
		{"valid incoming", "gw-1:abc_DEF.42", true},
		{"max length", strings.Repeat("a", 128), true},
		{"too long", strings.Repeat("a", 129), false},
		{"forbidden chars", "abc def", false},
		{"log injection", "abc\ninjected", false},
		{"non-ascii", "请求", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/", nil)
			req.Header.Set(RequestIDHeader, tc.in)
			h.ServeHTTP(httptest.NewRecorder(), req)
			if (gotID == tc.in) != tc.keep {
				t.Errorf("request id = %q, keep incoming = %v", gotID, tc.keep)
			}
		})
	}

	if got := RequestIDFrom(context.Background()); got != "" {
		t.Errorf("RequestIDFrom(empty ctx) = %q, want empty", got)
	}
}

func TestRealIP(t *testing.T) {
	trusted := []netip.Prefix{
		netip.MustParsePrefix("10.0.0.0/8"),
		netip.MustParsePrefix("fd00::/8"),
	}

	for _, tc := range []struct {
		name    string
		trusted []netip.Prefix
		remote  string
		xff     []string
		realIP  string
		want    string
	}{
		{"no trusted proxies ignores headers", nil, "1.1.1.1:1234", []string{"9.9.9.9"}, "", "1.1.1.1:1234"},
		{"untrusted peer ignores headers", trusted, "1.1.1.1:1234", []string{"9.9.9.9"}, "", "1.1.1.1:1234"},
		{"trusted peer uses XFF", trusted, "10.0.0.1:1234", []string{"9.9.9.9"}, "", "9.9.9.9"},
		{"rightmost untrusted wins over spoofed left", trusted, "10.0.0.1:1234", []string{"6.6.6.6, 9.9.9.9, 10.0.0.2"}, "", "9.9.9.9"},
		{"multiple XFF headers", trusted, "10.0.0.1:1234", []string{"6.6.6.6", "9.9.9.9, 10.0.0.2"}, "", "9.9.9.9"},
		{"all trusted takes leftmost", trusted, "10.0.0.1:1234", []string{"10.0.0.3, 10.0.0.2"}, "", "10.0.0.3"},
		{"garbage stops parsing", trusted, "10.0.0.1:1234", []string{"9.9.9.9, bogus"}, "", "10.0.0.1:1234"},
		{"X-Real-IP fallback", trusted, "10.0.0.1:1234", nil, "9.9.9.9", "9.9.9.9"},
		{"XFF preferred over X-Real-IP", trusted, "10.0.0.1:1234", []string{"9.9.9.9"}, "8.8.8.8", "9.9.9.9"},
		{"no headers keeps remote", trusted, "10.0.0.1:1234", nil, "", "10.0.0.1:1234"},
		{"IPv6 peer and client", trusted, "[fd00::1]:1234", []string{"2001:db8::1"}, "", "2001:db8::1"},
		{"IPv4-mapped peer", trusted, "[::ffff:10.0.0.1]:1234", []string{"9.9.9.9"}, "", "9.9.9.9"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var got string
			h := RealIP(tc.trusted...)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				got = r.RemoteAddr
			}))
			req := httptest.NewRequest("GET", "/", nil)
			req.RemoteAddr = tc.remote
			for _, v := range tc.xff {
				req.Header.Add("X-Forwarded-For", v)
			}
			if tc.realIP != "" {
				req.Header.Set("X-Real-IP", tc.realIP)
			}
			h.ServeHTTP(httptest.NewRecorder(), req)
			if got != tc.want {
				t.Errorf("RemoteAddr = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestClientIP(t *testing.T) {
	for in, want := range map[string]string{
		"1.2.3.4:80":          "1.2.3.4",
		"1.2.3.4":             "1.2.3.4",
		"[2001:db8::1]:80":    "2001:db8::1",
		"[::ffff:1.2.3.4]:80": "1.2.3.4",
		"not-an-ip":           "not-an-ip",
		"@":                   "@",
	} {
		r := &http.Request{RemoteAddr: in}
		if got := ClientIP(r); got != want {
			t.Errorf("ClientIP(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestAccessLog(t *testing.T) {
	var buf bytes.Buffer
	r := chi.NewRouter()
	r.Use(RequestID, AccessLog(newLogger(&buf)))
	r.With(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := WithLogAttrs(r.Context(), slog.String("user_id", "u-1"))
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}).Get("/users/{id}", func(w http.ResponseWriter, r *http.Request) {
		slog.New(newLogger(&buf).Handler()).InfoContext(r.Context(), "inside")
		_, _ = io.WriteString(w, "hello")
	})
	r.Get("/fail", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	})

	t.Run("success", func(t *testing.T) {
		buf.Reset()
		req := httptest.NewRequest("GET", "/users/42?token=secret", nil)
		req.Header.Set("User-Agent", "test-agent")
		req.RemoteAddr = "1.2.3.4:5678"
		r.ServeHTTP(httptest.NewRecorder(), req)

		lines := decodeLines(t, &buf)
		if len(lines) != 2 {
			t.Fatalf("got %d log lines, want 2: %s", len(lines), buf.String())
		}
		if lines[0]["user_id"] != "u-1" {
			t.Errorf("inner log missing user_id: %v", lines[0])
		}
		m := lines[1]
		want := map[string]any{
			"msg": "http request", "level": "INFO", "method": "GET",
			"path": "/users/42", "query": "token=secret", "route": "/users/{id}", "status": 200.0,
			"bytes": 5.0, "ip": "1.2.3.4", "user_agent": "test-agent", "user_id": "u-1",
		}
		for k, v := range want {
			if m[k] != v {
				t.Errorf("%s = %v, want %v", k, m[k], v)
			}
		}
		if _, ok := m["request_id"].(string); !ok {
			t.Errorf("access log missing request_id: %v", m)
		}
		if _, ok := m["duration_ms"].(float64); !ok {
			t.Errorf("access log missing duration_ms: %v", m)
		}
	})

	t.Run("5xx logs at error level", func(t *testing.T) {
		buf.Reset()
		r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/fail", nil))
		m := decodeLines(t, &buf)[0]
		if m["level"] != "ERROR" || m["status"] != 502.0 {
			t.Errorf("level = %v, status = %v, want ERROR 502", m["level"], m["status"])
		}
	})

	t.Run("not found has empty route", func(t *testing.T) {
		buf.Reset()
		r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/missing", nil))
		m := decodeLines(t, &buf)[0]
		if m["status"] != 404.0 || m["route"] != "" {
			t.Errorf("status = %v, route = %q, want 404 and empty", m["status"], m["route"])
		}
	})
}

func TestWithLogAttrsWithoutAccessLog(t *testing.T) {
	var buf bytes.Buffer
	ctx := WithLogAttrs(context.Background(), slog.String("k", "v"))
	newLogger(&buf).InfoContext(ctx, "x")
	if m := decodeLines(t, &buf)[0]; m["k"] != "v" {
		t.Errorf("k = %v, want v", m["k"])
	}
}

func TestRecover(t *testing.T) {
	var buf bytes.Buffer
	mw := Recover(newLogger(&buf))

	t.Run("panic returns 500 and logs stack", func(t *testing.T) {
		buf.Reset()
		rec := httptest.NewRecorder()
		mw(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
			panic("boom")
		})).ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))

		if rec.Code != http.StatusInternalServerError {
			t.Errorf("status = %d, want 500", rec.Code)
		}
		m := decodeLines(t, &buf)[0]
		err, _ := m["err"].(string)
		if m["level"] != "ERROR" || !strings.HasPrefix(err, "panic: boom") || !strings.Contains(err, "TestRecover") {
			t.Errorf("unexpected log: %v", m)
		}
	})

	t.Run("response already started is kept", func(t *testing.T) {
		rec := httptest.NewRecorder()
		mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusAccepted)
			_, _ = io.WriteString(w, "partial")
			panic("boom")
		})).ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
		if rec.Code != http.StatusAccepted || rec.Body.String() != "partial" {
			t.Errorf("got %d %q, want 202 \"partial\"", rec.Code, rec.Body.String())
		}
	})

	t.Run("upgraded connection gets no response", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "/", nil)
		req.Header.Set("Connection", "keep-alive, Upgrade")
		mw(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
			panic("boom")
		})).ServeHTTP(rec, req)
		if rec.Body.Len() != 0 {
			t.Errorf("body = %q, want empty", rec.Body.String())
		}
	})

	t.Run("ErrAbortHandler is re-panicked without logging", func(t *testing.T) {
		buf.Reset()
		defer func() {
			if r := recover(); r != http.ErrAbortHandler {
				t.Errorf("recovered %v, want http.ErrAbortHandler", r)
			}
			if buf.Len() != 0 {
				t.Errorf("unexpected log: %s", buf.String())
			}
		}()
		mw(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
			panic(http.ErrAbortHandler)
		})).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil))
	})

	t.Run("no panic", func(t *testing.T) {
		buf.Reset()
		rec := httptest.NewRecorder()
		mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(w, "ok")
		})).ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
		if rec.Code != 200 || rec.Body.String() != "ok" || buf.Len() != 0 {
			t.Errorf("got %d %q, log %q", rec.Code, rec.Body.String(), buf.String())
		}
	})
}

func TestSecureHeaders(t *testing.T) {
	rec := httptest.NewRecorder()
	SecureHeaders(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-Frame-Options", "DENY") // handlers can override
	})).ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))

	for k, want := range map[string]string{
		"X-Content-Type-Options": "nosniff",
		"X-Frame-Options":        "DENY",
		"Referrer-Policy":        "strict-origin-when-cross-origin",
	} {
		if got := rec.Header().Get(k); got != want {
			t.Errorf("%s = %q, want %q", k, got, want)
		}
	}
}

func TestRecommendedStack(t *testing.T) {
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(newLogger(&buf))
	defer slog.SetDefault(prev)

	// The order recommended in the package documentation.
	r := chi.NewRouter()
	r.Use(
		RealIP(netip.MustParsePrefix("10.0.0.0/8")),
		RequestID,
		AccessLog(nil),
		Recover(nil),
		SecureHeaders,
	)
	r.Get("/panic", func(http.ResponseWriter, *http.Request) { panic("boom") })

	req := httptest.NewRequest("GET", "/panic", nil)
	req.RemoteAddr = "10.0.0.1:1234"
	req.Header.Set("X-Forwarded-For", "9.9.9.9")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != 500 || rec.Header().Get(RequestIDHeader) == "" || rec.Header().Get("X-Content-Type-Options") == "" {
		t.Fatalf("unexpected response: %d %v", rec.Code, rec.Header())
	}
	lines := decodeLines(t, &buf)
	if len(lines) != 2 {
		t.Fatalf("got %d log lines, want panic + access: %s", len(lines), buf.String())
	}
	panicLog, access := lines[0], lines[1]
	if panicLog["msg"] != "panic recovered" || access["msg"] != "http request" {
		t.Fatalf("unexpected log order: %v / %v", panicLog["msg"], access["msg"])
	}
	if access["status"] != 500.0 || access["ip"] != "9.9.9.9" || access["route"] != "/panic" {
		t.Errorf("unexpected access log: %v", access)
	}
	if panicLog["request_id"] == nil || panicLog["request_id"] != access["request_id"] {
		t.Errorf("request_id mismatch: %v vs %v", panicLog["request_id"], access["request_id"])
	}
}

func TestConfigYAML(t *testing.T) {
	cfg := DefaultConfig()
	err := config.Parse([]byte(`
addr: ":9090"
write_timeout: 0s
shutdown_delay: 5s
trusted_proxies: ["10.0.0.0/8", "127.0.0.1/32", "::1/128"]
`), &cfg)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Addr != ":9090" || cfg.WriteTimeout != 0 || cfg.ShutdownDelay.String() != "5s" {
		t.Errorf("unexpected config: %+v", cfg)
	}
	if cfg.ReadHeaderTimeout != DefaultConfig().ReadHeaderTimeout {
		t.Errorf("default ReadHeaderTimeout lost: %v", cfg.ReadHeaderTimeout)
	}
	if len(cfg.TrustedProxies) != 3 || cfg.TrustedProxies[1] != netip.MustParsePrefix("127.0.0.1/32") {
		t.Errorf("TrustedProxies = %v", cfg.TrustedProxies)
	}

	cfg = DefaultConfig()
	if err := config.Parse([]byte(`trusted_proxies: ["10.0.0.1"]`), &cfg); err == nil {
		t.Error("expected error for a bare IP without prefix length")
	}
}
