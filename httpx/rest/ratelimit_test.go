package rest_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"

	"github.com/ryan053857/go-kit/httpx/rest"
)

func TestRateLimit(t *testing.T) {
	prev := huma.NewErrorWithContext
	huma.NewErrorWithContext = rest.NewErrorWithContext
	t.Cleanup(func() { huma.NewErrorWithContext = prev })
	r := chi.NewRouter()
	api := humachi.New(r, huma.DefaultConfig("Test", "1.0.0"))
	limited := huma.NewGroup(api, "/auth")
	limited.UseMiddleware(rest.RateLimit(api, 2, time.Minute))
	rest.Post(limited, "/a", echo())
	rest.Post(limited, "/b", echo())
	rest.Post(api, "/free", echo())

	do := func(path, ip string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"text":"x"}`))
		req.RemoteAddr = ip + ":1234"
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		return rec
	}

	if rec := do("/auth/a", "10.0.0.1"); rec.Code != 200 || rec.Header().Get("X-RateLimit-Remaining") != "1" {
		t.Fatalf("1st = %d, remaining %q", rec.Code, rec.Header().Get("X-RateLimit-Remaining"))
	}
	if rec := do("/auth/b", "10.0.0.1"); rec.Code != 200 {
		t.Fatalf("2nd = %d", rec.Code)
	}
	rec := do("/auth/a", "10.0.0.1")
	if rec.Code != http.StatusTooManyRequests || rec.Header().Get("Retry-After") == "" ||
		!strings.Contains(rec.Body.String(), "error.too_many_requests") {
		t.Fatalf("3rd = %d %v %s", rec.Code, rec.Header(), rec.Body)
	}
	if rec := do("/auth/a", "10.0.0.2"); rec.Code != 200 {
		t.Fatalf("other ip = %d", rec.Code)
	}
	for range 3 {
		if rec := do("/free", "10.0.0.1"); rec.Code != 200 {
			t.Fatalf("unlimited route = %d", rec.Code)
		}
	}
}
