package fault

import (
	"fmt"
	"maps"

	"github.com/cockroachdb/errors"
	"github.com/cockroachdb/errors/errbase"
)

// Template 是不可变的业务错误模板，通常定义为包级变量。
type Template struct {
	kind Kind
	code string
}

// Define 创建模板，code 使用 note.not_found 形式的业务错误码。
func Define(kind Kind, code string) *Template {
	return &Template{kind: kind, code: code}
}

// Code 返回稳定错误码。
func (t *Template) Code() string { return t.code }

// Kind 返回错误分类。
func (t *Template) Kind() Kind { return t.kind }

// Error 实现 error，便于 errors.Is(err, ErrXxx)。
func (t *Template) Error() string { return t.code }

// Option 配置一次失败实例的公开信息。
type Option func(*Error)

// Public 附加允许公开的业务参数，具体呈现由边界决定。
func Public(key string, value any) Option {
	return func(e *Error) {
		if e.public == nil {
			e.public = make(map[string]any)
		}
		e.public[key] = value
	}
}

// Error 是一次具体失败；带 cause 与公开参数，堆栈在 New/Wrap 调用处捕获。
type Error struct {
	template *Template
	public   map[string]any
	cause    error
}

// New 在调用处创建带堆栈的失败实例。
func (t *Template) New(opts ...Option) error {
	e := &Error{template: t}
	for _, opt := range opts {
		opt(e)
	}
	return errors.WithStackDepth(e, 1)
}

// Wrap 包装 cause，并在调用处附加堆栈。
func (t *Template) Wrap(cause error, opts ...Option) error {
	e := &Error{template: t, cause: cause}
	for _, opt := range opts {
		opt(e)
	}
	return errors.WithStackDepth(e, 1)
}

// Error 返回稳定业务错误码。
func (e *Error) Error() string { return e.Code() }

// Unwrap 返回内部 cause。
func (e *Error) Unwrap() error { return e.cause }

// Is 匹配同一模板或模板本身。
func (e *Error) Is(target error) bool {
	switch t := target.(type) {
	case *Template:
		return e.template == t
	case *Error:
		return e.template != nil && e.template == t.template
	default:
		return false
	}
}

// Code 返回稳定错误码。
func (e *Error) Code() string {
	if e.template == nil {
		return "fault.internal"
	}
	return e.template.code
}

// Kind 返回分类。
func (e *Error) Kind() Kind {
	if e.template == nil {
		return Internal
	}
	return e.template.kind
}

// Public 返回公开参数的浅拷贝。
func (e *Error) Public() map[string]any { return maps.Clone(e.public) }

// Format 实现 fmt.Formatter，委托给 cockroachdb/errors 扩展协议。
func (e *Error) Format(s fmt.State, verb rune) { errors.FormatError(e, s, verb) }

// FormatError 实现 errors.Formatter，保留 cause 链与安全详情。
func (e *Error) FormatError(p errbase.Printer) error {
	p.Print(e.Error())
	if len(e.public) > 0 && p.Detail() {
		p.Printf("public=%v", e.public)
	}
	return e.cause
}

// CodeOf 从错误链提取业务错误码；没有则返回空。
func CodeOf(err error) string {
	var e *Error
	if errors.As(err, &e) {
		return e.Code()
	}
	var t *Template
	if errors.As(err, &t) {
		return t.Code()
	}
	return ""
}

// KindOf 从错误链提取 Kind；没有则返回 Internal。
func KindOf(err error) Kind {
	var e *Error
	if errors.As(err, &e) {
		return e.Kind()
	}
	var t *Template
	if errors.As(err, &t) {
		return t.Kind()
	}
	return Internal
}

// PublicOf 从错误链提取公开参数。
func PublicOf(err error) map[string]any {
	var e *Error
	if errors.As(err, &e) {
		return e.Public()
	}
	return nil
}
