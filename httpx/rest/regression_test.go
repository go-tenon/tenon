package rest_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cockroachdb/errors"
	"github.com/danielgtaylor/huma/v2"

	"github.com/ryan053857/mygoravel/fault"
	"github.com/ryan053857/mygoravel/httpx"
	"github.com/ryan053857/mygoravel/httpx/rest"
	"github.com/ryan053857/mygoravel/i18n"
	"github.com/ryan053857/mygoravel/identity"
)

func TestMissingSecurityRejectedBeforeFactory(t *testing.T) {
	for _, mode := range []rest.Mode{rest.ModeServe, rest.ModeOpenAPI} {
		func() {
			defer func() {
				if recover() == nil {
					t.Fatal("unmarked endpoint accepted")
				}
			}()
			rest.Register(rest.New(rest.Options{Mode: mode}), huma.Operation{OperationID: "missing", Method: "GET", Path: "/missing"}, func() func(context.Context, *pingIn) (*pingOut, error) { t.Fatal("factory ran"); return nil })
		}()
	}
}
func TestWrappedErrorsAndMapping(t *testing.T) {
	if got := rest.FromFault(fmt.Errorf("outer: %w", errors.WithStack(huma.Error404NotFound("safe")))); got.GetStatus() != 404 {
		t.Fatal(got)
	}
	for kind, status := range map[fault.Kind]int{fault.InvalidArgument: 422, fault.Unauthenticated: 401, fault.PermissionDenied: 403, fault.NotFound: 404, fault.Conflict: 409, fault.Unavailable: 503, fault.Internal: 500} {
		err := fmt.Errorf("context: %w", fault.Define(kind, "demo.code").Wrap(huma.Error400BadRequest("secret")))
		if got := rest.FromFault(err); got.GetStatus() != status {
			t.Fatalf("kind %v: %v", kind, got)
		}
	}
}
func TestProblemSchemaAndValidationAgree(t *testing.T) {
	api := rest.New(rest.Options{})
	op := huma.Operation{OperationID: "safe", Method: "POST", Path: "/safe"}
	rest.Public()(&op)
	rest.Register(api, op, func() func(context.Context, *echoIn) (*echoOut, error) {
		return func(context.Context, *echoIn) (*echoOut, error) {
			return nil, fault.Define(fault.NotFound, "example.missing").New()
		}
	})
	for _, body := range []string{`{"secret":"x"}`, `{"secret":"valid"}`, `{"secret": {"token":"do-not-leak"}}`} {
		req := httptest.NewRequest("POST", "/safe", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rr := httptest.NewRecorder()
		api.Handler().ServeHTTP(rr, req)
		var obj map[string]any
		if err := json.Unmarshal(rr.Body.Bytes(), &obj); err != nil {
			t.Fatal(err)
		}
		schema := api.OpenAPI().Paths["/safe"].Post.Responses["default"].Content["application/problem+json"].Schema
		result := &huma.ValidateResult{}
		huma.Validate(api.OpenAPI().Components.Schemas, schema, &huma.PathBuffer{}, huma.ModeReadFromServer, obj, result)
		if len(result.Errors) > 0 {
			t.Fatalf("response violates documented schema: %v; %s", result.Errors, rr.Body.String())
		}
		if obj["code"] == "" || obj["request_id"] != rr.Header().Get("X-Request-ID") || obj["request_id"] == "" {
			t.Fatal(obj)
		}
		if strings.Contains(rr.Body.String(), `"value"`) || strings.Contains(rr.Body.String(), "do-not-leak") {
			t.Fatal(rr.Body.String())
		}
	}
	raw, _ := api.OpenAPIJSON()
	var doc map[string]any
	_ = json.Unmarshal(raw, &doc)
	schemas := doc["components"].(map[string]any)["schemas"].(map[string]any)
	found := false
	for _, v := range schemas {
		schema := v.(map[string]any)
		props, _ := schema["properties"].(map[string]any)
		if props["status"] != nil {
			found = true
			if props["code"] == nil || props["request_id"] == nil {
				t.Fatal(schema)
			}
		}
		if props["location"] != nil && props["value"] != nil {
			t.Fatal("unsafe validation schema")
		}
	}
	if !found {
		t.Fatal("missing problem schema")
	}
}
func TestBearerRejectsEmptySubjectAndDocumentsNoOAuthScopes(t *testing.T) {
	api := rest.New(rest.Options{})
	op := huma.Operation{OperationID: "auth", Method: http.MethodGet, Path: "/auth"}
	rest.RequireBearer(api, func(huma.Context, string) (identity.Subject, error) { return identity.Subject{}, nil }, "orders")(&op)
	if len(op.Security[0]["bearer"]) != 0 {
		t.Fatal("HTTP bearer cannot carry OAuth scopes")
	}
	rest.Register(api, op, func() func(context.Context, *pingIn) (*pingOut, error) {
		return func(context.Context, *pingIn) (*pingOut, error) {
			t.Fatal("anonymous subject reached handler")
			return nil, nil
		}
	})
	req := httptest.NewRequest("GET", "/auth", nil)
	req.Header.Set("Authorization", "Bearer token")
	rr := httptest.NewRecorder()
	api.Handler().ServeHTTP(rr, req)
	if rr.Code != 401 || !strings.Contains(rr.Body.String(), `"code":"auth.unauthenticated"`) {
		t.Fatal(rr.Code, rr.Body.String())
	}
}

func TestConflictingRouteRejectedBeforeBinding(t *testing.T) {
	for _, mode := range []rest.Mode{rest.ModeServe, rest.ModeOpenAPI} {
		api := rest.New(rest.Options{Mode: mode})
		first := huma.Operation{OperationID: "first", Method: "GET", Path: "/same"}
		rest.Public()(&first)
		rest.Register(api, first, func() func(context.Context, *pingIn) (*pingOut, error) {
			return func(context.Context, *pingIn) (*pingOut, error) { return &pingOut{}, nil }
		})
		second := huma.Operation{OperationID: "second", Method: "GET", Path: "/same"}
		rest.Public()(&second)
		func() {
			defer func() {
				if recover() == nil {
					t.Fatal("route silently overwritten")
				}
			}()
			rest.Register(api, second, func() func(context.Context, *pingIn) (*pingOut, error) {
				t.Fatal("conflict executed factory")
				return nil
			})
		}()
	}
}
func TestLanguageAndLoggerAreInstanceLocal(t *testing.T) {
	var aLog, bLog bytes.Buffer
	for _, tc := range []struct {
		lang     string
		buf      *bytes.Buffer
		expected string
	}{{"en", &aLog, "An unexpected error occurred"}, {"zh-CN", &bLog, "服务暂时无法处理请求"}} {
		bundle, err := i18n.New(i18n.Options{Default: tc.lang, Layers: []i18n.Layer{i18n.FrameworkLayer()}})
		if err != nil {
			t.Fatal(err)
		}
		api := rest.New(rest.Options{Middleware: httpx.MiddlewareOptions{Logger: slog.New(slog.NewJSONHandler(tc.buf, nil)), Languages: bundle}})
		op := huma.Operation{OperationID: "fail", Method: "GET", Path: "/fail"}
		rest.Public()(&op)
		rest.Register(api, op, func() func(context.Context, *pingIn) (*pingOut, error) {
			return func(context.Context, *pingIn) (*pingOut, error) { return nil, errors.New("private-secret-diagnostics") }
		})
		rr := httptest.NewRecorder()
		api.Handler().ServeHTTP(rr, httptest.NewRequest("GET", "/fail", nil))
		if !strings.Contains(rr.Body.String(), tc.expected) {
			t.Fatal(rr.Body.String())
		}
		lines := strings.Split(strings.TrimSpace(tc.buf.String()), "\n")
		if len(lines) != 2 {
			t.Fatal(tc.buf.String())
		}
		for _, line := range lines {
			var entry map[string]any
			if err := json.Unmarshal([]byte(line), &entry); err != nil {
				t.Fatal(err)
			}
			if entry["request_id"] != rr.Header().Get("X-Request-ID") {
				t.Fatal(entry)
			}
			if strings.Contains(line, "private-secret-diagnostics") {
				t.Fatal("error log leaked cause")
			}
		}
	}
	if strings.Count(aLog.String(), "http request") != 1 || strings.Count(bLog.String(), "http request") != 1 {
		t.Fatal("loggers crossed instances")
	}
}
