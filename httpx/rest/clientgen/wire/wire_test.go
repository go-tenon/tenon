package wire

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestDecodeError(t *testing.T) {
	rec := httptest.NewRecorder()
	rec.Header().Set("Retry-After", "30")
	rec.Header().Set("X-Request-ID", "req-1")
	rec.WriteHeader(http.StatusForbidden)
	rec.WriteString(`{"status":403,"detail":"请用企业账号登录","code":"auth.method_required","params":{"methods":["acme_sso"]}}`)
	e := decodeError(rec.Result()).(*Error)
	methods, _ := e.Params["methods"].([]any)
	if e.Code != "auth.method_required" || len(methods) != 1 || methods[0] != "acme_sso" || e.RetryAfter != 30*time.Second || e.RequestID != "req-1" {
		t.Fatalf("%#v", e)
	}

	res := &http.Response{StatusCode: 404, Header: http.Header{}, Body: http.NoBody}
	if e := decodeError(res).(*Error); e.Params != nil || e.RetryAfter != 0 || e.Detail != "Not Found" {
		t.Fatalf("%#v", e)
	}
}

func TestRetryAfter(t *testing.T) {
	future := time.Now().Add(time.Minute).UTC().Format(http.TimeFormat)
	for v, ok := range map[string]bool{"": false, "0": false, "-5": false, "abc": false, "5": true, future: true, "Mon, 02 Jan 2006 15:04:05 GMT": false} {
		if got := retryAfter(v); (got > 0) != ok || v == "5" && got != 5*time.Second {
			t.Errorf("retryAfter(%q) = %v", v, got)
		}
	}
}
