package httpx_test

import (
	"context"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/go-tenon/tenon/httpx"
)

func TestDefaultConfigIsValid(t *testing.T) {
	t.Parallel()
	if err := httpx.DefaultConfig().Validate(); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*httpx.Config){
		func(c *httpx.Config) { c.Addr = "" },
		func(c *httpx.Config) { c.Addr = "8080" },
		func(c *httpx.Config) { c.Addr = "not an addr" },
		func(c *httpx.Config) { c.Addr = ":99999" },
		func(c *httpx.Config) { c.Addr = ":http" },
		func(c *httpx.Config) { c.ReadTimeout = 0 },
		func(c *httpx.Config) { c.DrainDelay = -time.Second },
		func(c *httpx.Config) { c.ShutdownTimeout = -time.Second },
		func(c *httpx.Config) { c.PublicURL = "example.com" },
		func(c *httpx.Config) { c.PublicURL = "ftp://example.com" },
		func(c *httpx.Config) { c.PublicURL = "https://" },
		func(c *httpx.Config) { c.PublicURL = "https://:8080" },
		func(c *httpx.Config) { c.PublicURL = "https://user:pass@example.com" },
		func(c *httpx.Config) { c.PublicURL = "https://example.com?a=1" },
		func(c *httpx.Config) { c.PublicURL = "https://example.com#top" },
		func(c *httpx.Config) { c.PublicURL = " https://example.com" },
	} {
		cfg := httpx.DefaultConfig()
		mutate(&cfg)
		if cfg.Validate() == nil {
			t.Fatalf("accepted %+v", cfg)
		}
	}
}

func TestConfigPublicURL(t *testing.T) {
	t.Parallel()
	empty := httpx.DefaultConfig()
	if _, err := empty.BaseURL(); err == nil || !strings.Contains(err.Error(), "not configured") {
		t.Fatalf("empty BaseURL: got %v", err)
	}
	if _, err := empty.URL("reset"); err == nil || !strings.Contains(err.Error(), "not configured") {
		t.Fatalf("empty URL: got %v", err)
	}

	invalid := httpx.DefaultConfig()
	invalid.PublicURL = "ftp://example.com"
	if _, err := invalid.BaseURL(); err == nil {
		t.Fatal("invalid PublicURL accepted by BaseURL")
	}

	for _, tc := range []struct {
		publicURL string
		wantBase  string
		elem      []string
		wantURL   string
	}{
		{
			publicURL: "http://localhost:8080",
			wantBase:  "http://localhost:8080",
			elem:      []string{"/reset-password"},
			wantURL:   "http://localhost:8080/reset-password",
		},
		{
			publicURL: "https://example.com/",
			wantBase:  "https://example.com",
			elem:      []string{"email", "verify", "42"},
			wantURL:   "https://example.com/email/verify/42",
		},
		{
			publicURL: "https://example.com/app///",
			wantBase:  "https://example.com/app",
			elem:      []string{"/t", "acme/"},
			wantURL:   "https://example.com/app/t/acme/",
		},
		{
			publicURL: "http://[::1]:8080/",
			wantBase:  "http://[::1]:8080",
			elem:      nil,
			wantURL:   "http://[::1]:8080",
		},
	} {
		cfg := httpx.DefaultConfig()
		cfg.PublicURL = tc.publicURL
		if err := cfg.Validate(); err != nil {
			t.Fatalf("%q Validate: %v", tc.publicURL, err)
		}
		gotBase, err := cfg.BaseURL()
		if err != nil || gotBase != tc.wantBase {
			t.Fatalf("%q BaseURL: got (%q, %v), want %q", tc.publicURL, gotBase, err, tc.wantBase)
		}
		gotURL, err := cfg.URL(tc.elem...)
		if err != nil || gotURL != tc.wantURL {
			t.Fatalf("%q URL(%v): got (%q, %v), want %q", tc.publicURL, tc.elem, gotURL, err, tc.wantURL)
		}
	}
}

func TestListenAndServeStopsOnCancel(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- httpx.ListenAndServe(ctx, addr("127.0.0.1:0"), http.NotFoundHandler())
	}()
	time.Sleep(20 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("did not stop")
	}
}

func TestListenAndServeReportsListenError(t *testing.T) {
	t.Parallel()
	busy, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer busy.Close()
	err = httpx.ListenAndServe(context.Background(), addr(busy.Addr().String()), http.NotFoundHandler())
	if err == nil || !strings.Contains(err.Error(), "listen") {
		t.Fatal("expected listen error", err)
	}
}

func TestServeRejectsInvalidConfig(t *testing.T) {
	t.Parallel()
	err := httpx.ListenAndServe(context.Background(), httpx.Config{Addr: "127.0.0.1:0"}, http.NotFoundHandler())
	if err == nil || !strings.Contains(err.Error(), "must be positive") {
		t.Fatal("zero timeouts must be rejected, not defaulted", err)
	}
}

func TestServeClosesListenerOnInvalidConfig(t *testing.T) {
	t.Parallel()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	if httpx.Serve(context.Background(), httpx.Config{}, ln, http.NotFoundHandler()) == nil {
		t.Fatal("invalid config accepted")
	}
	if _, err := ln.Accept(); err == nil {
		t.Fatal("listener left open; clients would hang")
	}
}

func TestMiddlewareConfigsValidate(t *testing.T) {
	t.Parallel()
	if err := httpx.DefaultConfig().RateLimit.Validate(); err != nil {
		t.Fatal(err)
	}
	for name, tc := range map[string]struct {
		err  error
		want string
	}{
		"empty prefix": {httpx.ClientIPConfig{TrustedProxies: []netip.Prefix{{}}}.Validate(), "trusted_proxies[0] is not a valid network"},
		"host bits":    {httpx.ClientIPConfig{TrustedProxies: []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8"), netip.MustParsePrefix("10.0.0.5/8")}}.Validate(), "trusted_proxies[1]: 10.0.0.5/8 has host bits set; use 10.0.0.0/8 or 10.0.0.5/32"},
		"ipv6 host":    {httpx.ClientIPConfig{TrustedProxies: []netip.Prefix{netip.MustParsePrefix("fd00::1/64")}}.Validate(), "use fd00::/64 or fd00::1/128"},
		"timeout":      {httpx.TimeoutConfig{}.Validate(), "duration must be positive"},
		"body limit":   {httpx.BodyLimitConfig{MaxBytes: -1}.Validate(), "max_bytes must be positive"},
	} {
		if tc.err == nil || !strings.Contains(tc.err.Error(), tc.want) {
			t.Fatalf("%s: got %v, want %q", name, tc.err, tc.want)
		}
	}
	for _, ok := range []string{"10.0.0.0/8", "10.0.0.5/32", "0.0.0.0/0", "::/0", "fd00::/64"} {
		if err := (httpx.ClientIPConfig{TrustedProxies: []netip.Prefix{netip.MustParsePrefix(ok)}}).Validate(); err != nil {
			t.Fatal(ok, err)
		}
	}
}

func addr(a string) httpx.Config {
	cfg := httpx.DefaultConfig()
	cfg.Addr = a
	return cfg
}
