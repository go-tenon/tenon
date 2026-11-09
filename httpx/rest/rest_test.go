package rest

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"

	"github.com/ryan053857/go-kit/httpx"
	"github.com/ryan053857/go-kit/logger"
)

// stackErr mimics cockroachdb/errors: %v prints the message, %+v adds a stack.
type stackErr struct{ msg string }

func (e *stackErr) Error() string { return e.msg }

func (e *stackErr) Format(s fmt.State, verb rune) {
	if verb == 'v' && s.Flag('+') {
		_, _ = io.WriteString(s, e.msg+"\n  at order.Create (order.go:42)")
		return
	}
	_, _ = io.WriteString(s, e.msg)
}

type pingAction struct{ err error }

type pingOutput struct {
	Body struct {
		Msg string `json:"msg"`
	}
}

func (a *pingAction) Register(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "ping",
		Method:      http.MethodGet,
		Path:        "/ping",
	}, func(ctx context.Context, _ *struct{}) (*pingOutput, error) {
		if a.err != nil {
			return nil, a.err
		}
		out := &pingOutput{}
		out.Body.Msg = "pong"
		return out, nil
	})
}

type echoAction struct{}

type echoInput struct {
	Name string `path:"name" maxLength:"5"`
}

func (echoAction) Register(api huma.API) {
	huma.Get(api, "/echo/{name}", func(ctx context.Context, in *echoInput) (*pingOutput, error) {
		out := &pingOutput{}
		out.Body.Msg = in.Name
		return out, nil
	})
}

// newAPI builds the stack a product would: chi + httpx middlewares + humachi,
// with LogServerErrors installed, logging JSON into buf.
func newAPI(t *testing.T, buf *bytes.Buffer, actions ...Action) http.Handler {
	t.Helper()

	prevLog := slog.Default()
	slog.SetDefault(logger.New(logger.Options{Writer: buf}))
	prevNew := huma.NewErrorWithContext
	huma.NewErrorWithContext = LogServerErrors(huma.NewErrorWithContext)
	t.Cleanup(func() {
		slog.SetDefault(prevLog)
		huma.NewErrorWithContext = prevNew
	})

	r := chi.NewRouter()
	r.Use(httpx.RequestID)
	api := humachi.New(r, huma.DefaultConfig("Test", "1.0.0"))
	Mount(huma.NewGroup(api, "/api/v1"), actions...)
	return r
}

func do(h http.Handler, path string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
	return rec
}

func TestMount(t *testing.T) {
	var buf bytes.Buffer
	h := newAPI(t, &buf, &pingAction{}, echoAction{})

	if rec := do(h, "/api/v1/ping"); rec.Code != 200 || !strings.Contains(rec.Body.String(), `"pong"`) {
		t.Errorf("/ping = %d %s", rec.Code, rec.Body)
	}
	if rec := do(h, "/api/v1/echo/bob"); rec.Code != 200 || !strings.Contains(rec.Body.String(), `"bob"`) {
		t.Errorf("/echo = %d %s", rec.Code, rec.Body)
	}
}

func TestLogServerErrors(t *testing.T) {
	t.Run("unexpected error is logged with stack and context", func(t *testing.T) {
		var buf bytes.Buffer
		h := newAPI(t, &buf, &pingAction{err: &stackErr{"db timeout"}})

		rec := do(h, "/api/v1/ping")
		if rec.Code != 500 {
			t.Fatalf("status = %d, want 500", rec.Code)
		}
		// Response is huma's default problem+json, unchanged.
		var body huma.ErrorModel
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if body.Detail != "unexpected error occurred" || len(body.Errors) != 1 || body.Errors[0].Message != "db timeout" {
			t.Errorf("unexpected body: %s", rec.Body)
		}

		var m map[string]any
		if err := json.Unmarshal(buf.Bytes(), &m); err != nil {
			t.Fatalf("log %q: %v", buf.String(), err)
		}
		if m["level"] != "ERROR" || m["msg"] != "unexpected error occurred" || m["status"] != 500.0 {
			t.Errorf("unexpected log: %v", m)
		}
		if m["request_id"] != rec.Header().Get(httpx.RequestIDHeader) {
			t.Errorf("log request_id = %v, want %q", m["request_id"], rec.Header().Get(httpx.RequestIDHeader))
		}
		if err, _ := m["err"].(string); !strings.Contains(err, "order.go:42") {
			t.Errorf("err should carry the %%+v stack, got %q", err)
		}
	})

	t.Run("client errors are not logged", func(t *testing.T) {
		var buf bytes.Buffer
		h := newAPI(t, &buf, &pingAction{err: huma.Error404NotFound("nope")}, echoAction{})

		if rec := do(h, "/api/v1/ping"); rec.Code != 404 {
			t.Errorf("status = %d, want 404", rec.Code)
		}
		if rec := do(h, "/api/v1/echo/toolong"); rec.Code != 422 {
			t.Errorf("validation status = %d, want 422", rec.Code)
		}
		if buf.Len() != 0 {
			t.Errorf("unexpected log: %s", buf.String())
		}
	})

	t.Run("multiple errors and nil context", func(t *testing.T) {
		var buf bytes.Buffer
		prev := slog.Default()
		slog.SetDefault(logger.New(logger.Options{Writer: &buf}))
		defer slog.SetDefault(prev)

		var called bool
		next := func(_ huma.Context, status int, msg string, errs ...error) huma.StatusError {
			called = true
			return huma.NewError(status, msg, errs...)
		}
		se := LogServerErrors(next)(nil, 503, "down", &stackErr{"a"}, nil, errors.New("b"))
		if !called || se.GetStatus() != 503 {
			t.Fatalf("next not called correctly: %v %v", called, se)
		}
		var m map[string]any
		if err := json.Unmarshal(buf.Bytes(), &m); err != nil {
			t.Fatal(err)
		}
		if m["err"] != "a\n  at order.Create (order.go:42)\nb" {
			t.Errorf("err = %q", m["err"])
		}
	})
}
