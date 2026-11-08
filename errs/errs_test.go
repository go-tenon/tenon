package errs

import (
	"context"
	"errors"
	"fmt"
	"io"
	"testing"
)

func TestCodeOf(t *testing.T) {
	cause := io.ErrUnexpectedEOF
	cases := []struct {
		name string
		err  error
		want Code
	}{
		{"nil", nil, ""},
		{"plain", cause, Internal},
		{"new", New(NotFound, "x"), NotFound},
		{"wrap", Wrap(cause, Unavailable, ""), Unavailable},
		{"zero code", &Error{Message: "x"}, Internal},
		{"wrapped by fmt", fmt.Errorf("op: %w", New(PermissionDenied, "")), PermissionDenied},
		{"outer reclassifies", Wrap(New(NotFound, "a"), Internal, "b"), Internal},
		{"field errors", FieldErrors{"email": "bad"}, InvalidArgument},
		{"field errors wrapped", fmt.Errorf("op: %w", FieldErrors{"email": "bad"}), InvalidArgument},
		{"field errors with message", Wrap(FieldErrors{"email": "bad"}, InvalidArgument, "表单有误"), InvalidArgument},
		{"canceled", fmt.Errorf("op: %w", context.Canceled), Canceled},
		{"deadline", fmt.Errorf("op: %w", context.DeadlineExceeded), DeadlineExceeded},
		{"explicit beats context", Wrap(context.DeadlineExceeded, Unavailable, ""), Unavailable},
		{"joined", errors.Join(cause, New(Aborted, "")), Aborted},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := CodeOf(c.err); got != c.want {
				t.Errorf("CodeOf = %q, want %q", got, c.want)
			}
		})
	}
}

func TestMessageOf(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want string
	}{
		{"nil", nil, ""},
		{"plain error is never exposed", errors.New("pq: secret detail"), ""},
		{"new", New(NotFound, "订单不存在"), "订单不存在"},
		{"wrap hides cause", Wrap(errors.New("pq: secret detail"), NotFound, "订单不存在"), "订单不存在"},
		{"wrapped by fmt", fmt.Errorf("op: %w", New(NotFound, "订单不存在")), "订单不存在"},
		{"outermost wins", Wrap(New(NotFound, "inner"), Internal, "outer"), "outer"},
		{"field errors only", FieldErrors{"email": "bad"}, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := MessageOf(c.err); got != c.want {
				t.Errorf("MessageOf = %q, want %q", got, c.want)
			}
		})
	}
}

func TestErrorString(t *testing.T) {
	cause := errors.New("sql: no rows")
	cases := []struct {
		err  error
		want string
	}{
		{Wrap(cause, NotFound, "订单不存在"), "订单不存在: sql: no rows"},
		{New(NotFound, "订单不存在"), "订单不存在"},
		{Wrap(cause, NotFound, ""), "sql: no rows"},
		{New(NotFound, ""), "not_found"},
		{&Error{}, "internal"},
	}
	for _, c := range cases {
		if got := c.err.Error(); got != c.want {
			t.Errorf("Error() = %q, want %q", got, c.want)
		}
	}
}

func TestWrap(t *testing.T) {
	if Wrap(nil, NotFound, "x") != nil {
		t.Error("Wrap(nil) should return nil")
	}
	cause := io.EOF
	err := Wrap(cause, NotFound, "x")
	if !errors.Is(err, io.EOF) {
		t.Error("cause should be reachable with errors.Is")
	}
	fe := FieldErrors{"a": "b"}
	got, ok := errors.AsType[FieldErrors](Wrap(fe, InvalidArgument, "x"))
	if !ok || got["a"] != "b" {
		t.Error("FieldErrors should be reachable with errors.As")
	}
}

func TestFieldErrorsString(t *testing.T) {
	fe := FieldErrors{"name": "不能为空", "email": "已被使用", "age": "必须大于 0"}
	want := "age: 必须大于 0; email: 已被使用; name: 不能为空"
	if got := fe.Error(); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	if got := (FieldErrors{}).Error(); got != "invalid argument" {
		t.Errorf("empty: got %q", got)
	}
}

func TestFieldErrorsErr(t *testing.T) {
	if err := (FieldErrors{}).Err(); err != nil {
		t.Errorf("empty: got %v, want nil", err)
	}
	var nilMap FieldErrors
	if err := nilMap.Err(); err != nil {
		t.Errorf("nil map: got %v, want nil", err)
	}
	err := FieldErrors{"a": "b"}.Err()
	if CodeOf(err) != InvalidArgument {
		t.Errorf("non-empty: got code %q", CodeOf(err))
	}
	if fe, ok := errors.AsType[FieldErrors](err); !ok || fe["a"] != "b" {
		t.Errorf("non-empty: got %v", err)
	}
}

func TestErrorsIsWithFieldErrorsInChain(t *testing.T) {
	// FieldErrors is a map (not comparable); errors.Is must not panic on it.
	err := fmt.Errorf("op: %w", FieldErrors{"a": "b"})
	if errors.Is(err, io.EOF) {
		t.Error("unexpected match")
	}
}
