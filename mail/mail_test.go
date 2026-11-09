package mail

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"
)

func TestNew(t *testing.T) {
	if m, err := New(Config{}); err != nil || m != (Log{}) {
		t.Errorf("default driver = %T, %v", m, err)
	}
	for _, cfg := range []Config{
		{Driver: "pigeon"},
		{Driver: DriverSMTP, From: "a@example.com"},
		{Driver: DriverResend, From: "a@example.com"},
	} {
		if _, err := New(cfg); err == nil {
			t.Errorf("New(%+v) succeeded", cfg)
		}
	}
}

func TestLog(t *testing.T) {
	var buf bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	defer slog.SetDefault(old)

	if err := (Log{}).Send(t.Context(), Message{To: []string{"a@example.com"}, Subject: "hi", Text: "body"}); err != nil {
		t.Fatal(err)
	}
	if s := buf.String(); !strings.Contains(s, "a@example.com") || !strings.Contains(s, "body") {
		t.Errorf("log = %s", s)
	}
	if err := (Log{}).Send(t.Context(), Message{Subject: "no recipients", Text: "x"}); err == nil {
		t.Error("empty To accepted")
	}
}

func TestResend(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/emails" || r.Header.Get("Authorization") != "Bearer re_test" {
			t.Errorf("request %s %v", r.URL.Path, r.Header)
		}
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &got)
		_, _ = w.Write([]byte(`{"id":"1"}`))
	}))
	defer srv.Close()

	cfg := Config{Driver: DriverResend, From: "App <no-reply@example.com>"}
	cfg.Resend.APIKey = "re_test"
	m, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	m.(*resendMailer).client.BaseURL, _ = url.Parse(srv.URL + "/")
	if err := m.Send(t.Context(), Message{To: []string{"a@example.com"}, Subject: "hi", HTML: "<p>x</p>"}); err != nil {
		t.Fatal(err)
	}
	if got["from"] != "App <no-reply@example.com>" || got["subject"] != "hi" || got["html"] != "<p>x</p>" {
		t.Errorf("request body = %v", got)
	}
}

// TestSMTP 需要 Mailpit：
//
//	docker run -d --rm -p 1025:1025 -p 8025:8025 axllent/mailpit
//	MAILPIT_URL=http://localhost:8025 MAILPIT_SMTP=localhost:1025 go test ./mail/
func TestSMTP(t *testing.T) {
	api, addr := os.Getenv("MAILPIT_URL"), os.Getenv("MAILPIT_SMTP")
	if api == "" || addr == "" {
		t.Skip("MAILPIT_URL / MAILPIT_SMTP not set")
	}
	host, portStr, _ := net.SplitHostPort(addr)
	port, _ := strconv.Atoi(portStr)

	cfg := Config{Driver: DriverSMTP, From: "App <no-reply@example.com>"}
	cfg.SMTP.Host, cfg.SMTP.Port, cfg.SMTP.TLS = host, port, "none"
	m, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	subject := "smtp test " + strconv.FormatInt(int64(os.Getpid()), 10)
	if err := m.Send(t.Context(), Message{To: []string{"user@example.com"}, Subject: subject, Text: "plain", HTML: "<b>html</b>"}); err != nil {
		t.Fatal(err)
	}

	resp, err := http.Get(api + "/api/v1/search?query=" + url.QueryEscape(`subject:"`+subject+`"`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var result struct {
		Messages []struct {
			Subject string
			From    struct{ Address string }
		} `json:"messages"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&result)
	if len(result.Messages) != 1 || result.Messages[0].From.Address != "no-reply@example.com" {
		t.Errorf("mailpit messages = %+v", result.Messages)
	}
}
