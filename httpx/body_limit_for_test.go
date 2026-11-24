package httpx_test

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/go-tenon/tenon/config"
	"github.com/go-tenon/tenon/httpx"
)

func TestBodyLimitFor(t *testing.T) {
	t.Parallel()
	cfg := config.Default[httpx.Config]()
	cfg.BodyLimit.MaxBytes = 16

	r := chi.NewRouter()
	r.Use(httpx.Defaults(cfg, httpx.BodyLimitFor(func(req *http.Request) int64 {
		if strings.HasSuffix(req.URL.Path, "/upload") {
			return 64
		}
		return 0
	}))...)

	echo := func(w http.ResponseWriter, req *http.Request) {
		if _, err := io.ReadAll(req.Body); err != nil {
			w.WriteHeader(http.StatusRequestEntityTooLarge)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
	r.Post("/json", echo)
	r.Post("/files/upload", echo)

	// 普通路由受默认 16 字节上限限制。
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/json", bytes.NewReader(make([]byte, 32))))
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("/json status = %d, want 413", rec.Code)
	}

	// 上传路由按 BodyLimitFor 放宽到 64 字节。
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/files/upload", bytes.NewReader(make([]byte, 32))))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("/files/upload 32B status = %d, want 204", rec.Code)
	}

	// 上传路由超过 64 字节仍被拒绝。
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/files/upload", bytes.NewReader(make([]byte, 80))))
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("/files/upload 80B status = %d, want 413", rec.Code)
	}
}
