package web

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/a-h/templ"
	"github.com/danielgtaylor/huma/v2"
	"github.com/go-chi/chi/v5"

	"github.com/shellphy-kit/tools/logger"
)

func text(s string) templ.Component {
	return templ.ComponentFunc(func(_ context.Context, w io.Writer) error {
		_, err := io.WriteString(w, s)
		return err
	})
}

// layout renders its children between <main> tags, like `{ children... }` in a .templ file.
var layout = templ.ComponentFunc(func(ctx context.Context, w io.Writer) error {
	_, _ = io.WriteString(w, "<main>")
	if err := templ.GetChildren(ctx).Render(ctx, w); err != nil {
		return err
	}
	_, err := io.WriteString(w, "</main>")
	return err
})

var failing = templ.ComponentFunc(func(_ context.Context, w io.Writer) error {
	_, _ = io.WriteString(w, "half a page")
	return errors.New("template failed")
})

func TestRender(t *testing.T) {
	rec := httptest.NewRecorder()
	if err := Render(rec, httptest.NewRequest("GET", "/", nil), http.StatusCreated, text("<p>hi</p>")); err != nil {
		t.Fatal(err)
	}
	if rec.Code != 201 || rec.Body.String() != "<p>hi</p>" || rec.Header().Get("Content-Type") != "text/html; charset=utf-8" {
		t.Errorf("got %d %q %v", rec.Code, rec.Body, rec.Header())
	}

	rec = httptest.NewRecorder()
	if err := Render(rec, httptest.NewRequest("GET", "/", nil), 200, failing); err == nil {
		t.Fatal("expected render error")
	}
	if rec.Body.Len() != 0 || rec.Header().Get("Content-Type") != "" {
		t.Errorf("nothing should be written on failure, got %q %v", rec.Body, rec.Header())
	}
}

func TestPage(t *testing.T) {
	for _, tc := range []struct {
		name    string
		headers map[string]string
		want    string
	}{
		{"direct navigation", nil, "<main>content</main>"},
		{"htmx partial", map[string]string{"HX-Request": "true"}, "content"},
		{"hx-boost", map[string]string{"HX-Request": "true", "HX-Boosted": "true"}, "<main>content</main>"},
		{"history restore", map[string]string{"HX-Request": "true", "HX-History-Restore-Request": "true"}, "<main>content</main>"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/", nil)
			for k, v := range tc.headers {
				req.Header.Set(k, v)
			}
			rec := httptest.NewRecorder()
			if err := Page(rec, req, 200, layout, text("content")); err != nil {
				t.Fatal(err)
			}
			if rec.Body.String() != tc.want {
				t.Errorf("body = %q, want %q", rec.Body, tc.want)
			}
			if rec.Header().Get("Vary") != "HX-Request" {
				t.Errorf("Vary = %q", rec.Header().Get("Vary"))
			}
		})
	}
}

func TestHandlerFunc(t *testing.T) {
	var logs bytes.Buffer
	prevLog := slog.Default()
	slog.SetDefault(logger.New(logger.Options{Writer: &logs}))
	prevEH := ErrorHandler
	var gotStatus int
	var gotErr error
	ErrorHandler = func(w http.ResponseWriter, r *http.Request, status int, err error) {
		gotStatus, gotErr = status, err
		_ = Render(w, r, status, text(fmt.Sprintf("error page %d", status)))
	}
	t.Cleanup(func() {
		slog.SetDefault(prevLog)
		ErrorHandler = prevEH
	})

	serve := func(err error) *httptest.ResponseRecorder {
		logs.Reset()
		gotStatus, gotErr = 0, nil
		rec := httptest.NewRecorder()
		HandlerFunc(func(w http.ResponseWriter, r *http.Request) error {
			if err == nil {
				return Render(w, r, 200, text("ok"))
			}
			return err
		}).ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
		return rec
	}

	if rec := serve(nil); rec.Code != 200 || rec.Body.String() != "ok" || gotStatus != 0 {
		t.Errorf("success: %d %q, ErrorHandler status %d", rec.Code, rec.Body, gotStatus)
	}

	notFound := huma.Error404NotFound("order not found")
	rec := serve(fmt.Errorf("load: %w", notFound))
	if rec.Code != 404 || rec.Body.String() != "error page 404" || !errors.Is(gotErr, notFound) {
		t.Errorf("status error: %d %q %v", rec.Code, rec.Body, gotErr)
	}
	if logs.Len() != 0 {
		t.Errorf("4xx must not be logged: %s", logs.String())
	}

	rec = serve(&stackErr{"db down"})
	if rec.Code != 500 || rec.Body.String() != "error page 500" {
		t.Errorf("plain error: %d %q", rec.Code, rec.Body)
	}
	var m map[string]any
	if err := json.Unmarshal(logs.Bytes(), &m); err != nil {
		t.Fatalf("log %q: %v", logs.String(), err)
	}
	if m["level"] != "ERROR" || m["status"] != 500.0 || !strings.Contains(m["err"].(string), "page.go:7") {
		t.Errorf("unexpected log: %v", m)
	}

	// A template failure inside the handler still yields a clean error page.
	rec = httptest.NewRecorder()
	HandlerFunc(func(w http.ResponseWriter, r *http.Request) error {
		return Render(w, r, 200, failing)
	}).ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	if rec.Code != 500 || rec.Body.String() != "error page 500" {
		t.Errorf("render failure: %d %q", rec.Code, rec.Body)
	}
}

func TestDefaultErrorHandler(t *testing.T) {
	rec := httptest.NewRecorder()
	HandlerFunc(func(http.ResponseWriter, *http.Request) error {
		return huma.Error403Forbidden("no")
	}).ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	if rec.Code != 403 || strings.TrimSpace(rec.Body.String()) != "Forbidden" {
		t.Errorf("got %d %q", rec.Code, rec.Body)
	}
}

type stackErr struct{ msg string }

func (e *stackErr) Error() string { return e.msg }
func (e *stackErr) Format(s fmt.State, verb rune) {
	if verb == 'v' && s.Flag('+') {
		_, _ = io.WriteString(s, e.msg+"\n  at page.go:7")
		return
	}
	_, _ = io.WriteString(s, e.msg)
}

func TestRedirect(t *testing.T) {
	rec := httptest.NewRecorder()
	Redirect(rec, httptest.NewRequest("POST", "/orders", nil), "/orders/1")
	if rec.Code != 303 || rec.Header().Get("Location") != "/orders/1" {
		t.Errorf("plain: %d %v", rec.Code, rec.Header())
	}

	req := httptest.NewRequest("POST", "/orders", nil)
	req.Header.Set("HX-Request", "true")
	rec = httptest.NewRecorder()
	Redirect(rec, req, "/orders/1")
	if rec.Code != 200 || rec.Header().Get("HX-Redirect") != "/orders/1" || rec.Header().Get("Location") != "" {
		t.Errorf("htmx: %d %v", rec.Code, rec.Header())
	}
}

func TestTrigger(t *testing.T) {
	rec := httptest.NewRecorder()
	if err := Trigger(rec, "toast", map[string]string{"message": "已保存"}); err != nil {
		t.Fatal(err)
	}
	if err := Trigger(rec, "refresh", nil); err != nil {
		t.Fatal(err)
	}
	if got := rec.Header().Get("HX-Trigger"); got != `{"refresh":null,"toast":{"message":"已保存"}}` {
		t.Errorf("HX-Trigger = %s", got)
	}

	// Merges with a header set elsewhere in the simple comma-separated form.
	rec = httptest.NewRecorder()
	rec.Header().Set("HX-Trigger", "a, b")
	_ = Trigger(rec, "c", 1)
	if got := rec.Header().Get("HX-Trigger"); got != `{"a":null,"b":null,"c":1}` {
		t.Errorf("HX-Trigger = %s", got)
	}

	rec = httptest.NewRecorder()
	rec.Header().Set("HX-Trigger", "{bad json")
	if err := Trigger(rec, "c", nil); err == nil {
		t.Error("expected error for malformed existing header")
	}
}

func TestAssets(t *testing.T) {
	fsys := fstest.MapFS{
		"css/app.css": {Data: []byte("body{}")},
		"app.js":      {Data: []byte("console.log(1)")},
	}
	a, err := NewAssets("/static/", fsys)
	if err != nil {
		t.Fatal(err)
	}
	r := chi.NewRouter()
	r.Handle("/static/*", a)

	url := a.URL("css/app.css")
	if !strings.HasPrefix(url, "/static/css/app.css?v=") || len(url) != len("/static/css/app.css?v=")+12 {
		t.Fatalf("URL = %q", url)
	}
	if a.URL("/css/app.css") != url {
		t.Error("leading slash should be ignored")
	}
	if got := a.URL("missing.css"); got != "/static/missing.css" {
		t.Errorf("URL(missing) = %q", got)
	}

	get := func(path string, hdr ...string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("GET", path, nil)
		for i := 0; i+1 < len(hdr); i += 2 {
			req.Header.Set(hdr[i], hdr[i+1])
		}
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		return rec
	}

	rec := get(url)
	if rec.Code != 200 || rec.Body.String() != "body{}" || !strings.Contains(rec.Header().Get("Content-Type"), "text/css") {
		t.Errorf("versioned: %d %q %v", rec.Code, rec.Body, rec.Header())
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "public, max-age=31536000, immutable" {
		t.Errorf("versioned Cache-Control = %q", cc)
	}

	rec = get("/static/css/app.css")
	etag := rec.Header().Get("ETag")
	if rec.Code != 200 || rec.Header().Get("Cache-Control") != "no-cache" || etag == "" {
		t.Errorf("unversioned: %d %v", rec.Code, rec.Header())
	}
	if rec := get("/static/css/app.css?v=stale"); rec.Header().Get("Cache-Control") != "no-cache" {
		t.Error("stale version must not be cached as immutable")
	}
	if rec := get("/static/css/app.css", "If-None-Match", etag); rec.Code != 304 {
		t.Errorf("conditional request = %d, want 304", rec.Code)
	}
	for _, p := range []string{"/static/missing.css", "/static/css/", "/static/css", "/static/../app.js"} {
		if rec := get(p); rec.Code != 404 {
			t.Errorf("GET %s = %d, want 404", p, rec.Code)
		}
	}

	for _, bad := range []string{"static/", "/static", ""} {
		if _, err := NewAssets(bad, fsys); err == nil {
			t.Errorf("NewAssets(%q) should fail", bad)
		}
	}
}

func TestVendor(t *testing.T) {
	a, err := NewAssets("/vendor/", Vendor)
	if err != nil {
		t.Fatal(err)
	}
	b, err := fs.ReadFile(Vendor, "htmx.min.js")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(b, []byte("var htmx=")) || !strings.Contains(string(b), `version:"`+HTMXVersion+`"`) {
		t.Errorf("htmx.min.js does not look like htmx %s", HTMXVersion)
	}
	if !strings.Contains(a.URL("htmx.min.js"), "?v=") {
		t.Error("htmx.min.js missing from Vendor assets")
	}
}
