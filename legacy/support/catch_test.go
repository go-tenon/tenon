package support

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/cockroachdb/errors"
)

func TestCatch(t *testing.T) {
	t.Run("returns fn error", func(t *testing.T) {
		if err := Catch(func() error { return io.EOF }); err != io.EOF {
			t.Errorf("err = %v, want io.EOF", err)
		}
	})
	t.Run("nil", func(t *testing.T) {
		if err := Catch(func() error { return nil }); err != nil {
			t.Errorf("err = %v, want nil", err)
		}
	})
	t.Run("panic with value", func(t *testing.T) {
		err := Catch(func() error { panic("boom") })
		var pe *PanicError
		if !errors.As(err, &pe) {
			t.Fatalf("err = %T, want *PanicError", err)
		}
		if pe.Value != "boom" {
			t.Errorf("Value = %v, want boom", pe.Value)
		}
		if !strings.HasPrefix(err.Error(), "panic: boom\n") {
			t.Errorf("Error() = %q, want prefix %q", err.Error(), "panic: boom\n")
		}
		if !strings.Contains(string(pe.Stack), "TestCatch") {
			t.Errorf("Stack does not contain the panicking test function:\n%s", pe.Stack)
		}
	})
	t.Run("panic with error does not unwrap", func(t *testing.T) {
		// A panic is always a bug: its value must not leak a classification
		// (context.Canceled, business errors, ...) through the error chain.
		err := Catch(func() error { panic(context.Canceled) })
		if errors.Is(err, context.Canceled) {
			t.Errorf("errors.Is(err, context.Canceled) = true, want false")
		}
		var pe *PanicError
		if !errors.As(err, &pe) || pe.Value != context.Canceled {
			t.Errorf("Value = %v, want context.Canceled kept on PanicError", pe)
		}
	})
	t.Run("panic nil", func(t *testing.T) {
		err := Catch(func() error { panic(nil) })
		var pe *PanicError
		if !errors.As(err, &pe) {
			t.Fatalf("err = %v, want *PanicError", err)
		}
	})
}
