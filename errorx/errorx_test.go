package errorx_test

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	crdb "github.com/cockroachdb/errors"

	"github.com/go-tenon/tenon/errorx"
)

var (
	errNotFound = errorx.Define(errorx.NotFound, "order.not_found")
	errConflict = errorx.Define(errorx.Conflict, "order.insufficient_stock")
)

func TestIsAsWithStdAndCrdbWrap(t *testing.T) {
	base := errNotFound.New(errorx.Public("order_id", "o1"))
	wrapped := fmt.Errorf("svc: %w", base)
	crdbWrapped := crdb.Wrap(wrapped, "handler")

	if !errors.Is(crdbWrapped, errNotFound) {
		t.Fatal("errors.Is should match template through mixed wraps")
	}
	var e *errorx.Error
	if !errors.As(crdbWrapped, &e) {
		t.Fatal("errors.As *errorx.Error")
	}
	if e.Code() != "order.not_found" || e.Kind() != errorx.NotFound {
		t.Fatalf("got code=%s kind=%v", e.Code(), e.Kind())
	}
	if e.Public()["order_id"] != "o1" {
		t.Fatalf("public=%v", e.Public())
	}

}

var errUnavailable = errorx.Define(errorx.Unavailable, "order.db")

func TestStackOnlyForSystemFailures(t *testing.T) {
	for _, err := range []error{makeUnavailable(), wrapUnavailable()} {
		s := fmt.Sprintf("%+v", err)
		if !strings.Contains(s, "makeUnavailable") && !strings.Contains(s, "wrapUnavailable") {
			t.Fatalf("stack should point at call site, got:\n%s", s)
		}
		if strings.Contains(s, "Define(") {
			t.Fatalf("stack should not be captured at Define:\n%s", s)
		}
	}
	for _, err := range []error{errNotFound.New(), errConflict.Wrap(errors.New("pq: unique violation"))} {
		if s := fmt.Sprintf("%+v", err); strings.Contains(s, "stack trace") {
			t.Fatalf("expected business outcome must not carry a stack:\n%s", s)
		}
	}
}

func makeUnavailable() error {
	return errUnavailable.New()
}

func wrapUnavailable() error {
	return errUnavailable.Wrap(errors.New("dial tcp: connection refused"))
}

func TestWrapPreservesCauseAndFormatChain(t *testing.T) {
	cause := errors.New("pq: unique violation")
	err := errConflict.Wrap(cause, errorx.Public("sku", "sku-1"))
	outer := crdb.Wrap(err, "checkout")

	if !errors.Is(outer, errConflict) {
		t.Fatal("Is template")
	}
	if !errors.Is(outer, cause) {
		t.Fatal("Is cause")
	}
	s := fmt.Sprintf("%+v", outer)
	if !strings.Contains(s, "order.insufficient_stock") || !strings.Contains(s, "pq: unique violation") {
		t.Fatalf("format chain truncated:\n%s", s)
	}
	if err.Error() != "order.insufficient_stock: pq: unique violation" {
		t.Fatalf("Error() must follow wrapper convention: %q", err.Error())
	}
}

func TestTemplatesAreDistinct(t *testing.T) {
	a := errNotFound.New()
	if errors.Is(a, errConflict) {
		t.Fatal("different templates must not match")
	}
}

func TestPublicParamsAndRedaction(t *testing.T) {
	err := errConflict.Wrap(errors.New("secret token=abc"), errorx.Public("sku", "s1"))
	if errorx.PublicOf(err)["sku"] != "s1" {
		t.Fatal("missing public")
	}
	if got := errConflict.New().Error(); got != "order.insufficient_stock" {
		t.Fatalf("Error() without cause: %q", got)
	}
	if !strings.Contains(fmt.Sprintf("%+v", err), "public=map[sku:s1]") {
		t.Fatal("percent+v should include public params")
	}
	redacted := crdb.Redact(err)
	if !strings.Contains(redacted, "order.insufficient_stock") || strings.Contains(redacted, "‹order.insufficient_stock›") || strings.Contains(redacted, "token=abc") {
		t.Fatalf("code must stay safe and cause must be redacted: %s", redacted)
	}
}

func TestRetryAfter(t *testing.T) {
	limited := errorx.Define(errorx.ResourceExhausted, "demo.limited")
	err := crdb.Wrap(limited.New(errorx.RetryAfter(1500*time.Millisecond)), "login")
	if errorx.RetryAfterOf(err) != 1500*time.Millisecond {
		t.Fatalf("retry after=%v", errorx.RetryAfterOf(err))
	}
	if errorx.RetryAfterOf(limited.New()) != 0 || errorx.RetryAfterOf(errors.New("plain")) != 0 {
		t.Fatal("retry after must default to 0")
	}
}

func TestSystemic(t *testing.T) {
	for _, kind := range []errorx.Kind{errorx.InvalidArgument, errorx.Unauthenticated, errorx.PermissionDenied, errorx.NotFound, errorx.Conflict, errorx.ResourceExhausted} {
		if kind.Systemic() {
			t.Fatalf("%v reported as systemic", kind)
		}
	}
	for _, kind := range []errorx.Kind{errorx.Unavailable, errorx.Internal} {
		if !kind.Systemic() {
			t.Fatalf("%v not reported as systemic", kind)
		}
	}
	// 不是业务错误的普通错误按 Internal 处理。
	if !errorx.KindOf(errors.New("db down")).Systemic() {
		t.Fatal("plain error must be systemic")
	}
}
