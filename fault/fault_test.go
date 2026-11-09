package fault_test

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	crdb "github.com/cockroachdb/errors"

	"github.com/ryan053857/mygoravel/fault"
)

var (
	errNotFound = fault.Define(fault.NotFound, "order.not_found")
	errConflict = fault.Define(fault.Conflict, "order.insufficient_stock")
)

func TestIsAsWithStdAndCrdbWrap(t *testing.T) {
	base := errNotFound.New(fault.Public("order_id", "o1"))
	wrapped := fmt.Errorf("svc: %w", base)
	crdbWrapped := crdb.Wrap(wrapped, "handler")

	if !errors.Is(crdbWrapped, errNotFound) {
		t.Fatal("errors.Is should match template through mixed wraps")
	}
	var e *fault.Error
	if !errors.As(crdbWrapped, &e) {
		t.Fatal("errors.As *fault.Error")
	}
	if e.Code() != "order.not_found" || e.Kind() != fault.NotFound {
		t.Fatalf("got code=%s kind=%v", e.Code(), e.Kind())
	}
	if e.Public()["order_id"] != "o1" {
		t.Fatalf("public=%v", e.Public())
	}

}

func TestStackPointsAtCallSite(t *testing.T) {
	err := makeNotFound()
	s := fmt.Sprintf("%+v", err)
	if !strings.Contains(s, "makeNotFound") {
		t.Fatalf("stack should include makeNotFound, got:\n%s", s)
	}
	if strings.Contains(s, "Define(") {
		t.Fatalf("stack should not be captured at Define:\n%s", s)
	}
}

func makeNotFound() error {
	return errNotFound.New()
}

func TestWrapPreservesCauseAndFormatChain(t *testing.T) {
	cause := errors.New("pq: unique violation")
	err := errConflict.Wrap(cause, fault.Public("sku", "sku-1"))
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
	if strings.Contains(err.Error(), "pq:") {
		t.Fatalf("public Error() leaked cause: %q", err.Error())
	}
}

func TestTemplatesAreDistinct(t *testing.T) {
	a := errNotFound.New()
	if errors.Is(a, errConflict) {
		t.Fatal("different templates must not match")
	}
}

func TestPublicIsolatedFromCause(t *testing.T) {
	err := errConflict.Wrap(errors.New("secret token=abc"), fault.Public("sku", "s1"))
	if fault.PublicOf(err)["sku"] != "s1" {
		t.Fatal("missing public")
	}
	var fe *fault.Error
	if !errors.As(err, &fe) {
		t.Fatal("as")
	}
	if strings.Contains(fe.Error(), "token=abc") {
		t.Fatalf("Error() leaked cause: %q", fe.Error())
	}
	// %+v 保留 cause，诊断输出由调用边界决定。
	if !strings.Contains(fmt.Sprintf("%+v", err), "token=abc") {
		t.Fatal("percent+v should retain cause for diagnostics")
	}
}
