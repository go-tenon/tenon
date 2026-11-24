package httpx_test

import (
	"compress/gzip"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/go-tenon/tenon/config"
	"github.com/go-tenon/tenon/httpx"
	"github.com/go-tenon/tenon/logging/logtest"
)

type ctxTestKey struct{}

func TestDefaultsBeforeAccessLogCompressAndContext(t *testing.T) {
	t.Parallel()
	rec := logtest.New()
	cfg := config.Default[httpx.Config]()

	r := chi.NewRouter()
	r.Use(httpx.Defaults(cfg,
		httpx.Logger(rec.Logger()),
		httpx.BeforeAccessLog(func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				if key := req.URL.Query().Get("key"); key != "" {
					q := req.URL.Query()
					q.Del("key")
					req.URL.RawQuery = q.Encode()
					req.Header.Set("X-Api-Key", key)
				}
				next.ServeHTTP(w, req)
			})
		}),
		httpx.WithCompress(),
		httpx.WithContext(func(ctx context.Context) context.Context {
			return context.WithValue(ctx, ctxTestKey{}, "tracked")
		}),
	)...)

	r.Get("/items", func(w http.ResponseWriter, req *http.Request) {
		if req.Header.Get("X-Api-Key") != "secret-token" {
			t.Errorf("X-Api-Key = %q", req.Header.Get("X-Api-Key"))
		}
		if got, _ := req.Context().Value(ctxTestKey{}).(string); got != "tracked" {
			t.Errorf("context value = %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":"` + strings.Repeat("hello world ", 200) + `"}`))
	})

	req := httptest.NewRequest(http.MethodGet, "/items?key=secret-token&page=1", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	if w.Header().Get("Content-Encoding") != "gzip" {
		t.Fatalf("Content-Encoding = %q, want gzip", w.Header().Get("Content-Encoding"))
	}
	gr, err := gzip.NewReader(w.Body)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(gr)
	if err != nil || !strings.Contains(string(body), "hello world") {
		t.Fatalf("gunzip body = %q, err = %v", body, err)
	}
	entry, ok := rec.Find("http request")
	if !ok {
		t.Fatalf("missing access log record: %+v", rec.Records())
	}
	for k, v := range entry.Attrs {
		if strings.Contains(k, "secret-token") || strings.Contains(strings.TrimSpace(toString(v)), "secret-token") {
			t.Fatalf("access log leaked query secret in %s=%v", k, v)
		}
	}
}

func toString(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}
