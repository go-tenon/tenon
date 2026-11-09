package rest

import (
	"context"
	"strings"

	"github.com/cockroachdb/errors"
	"github.com/danielgtaylor/huma/v2"

	"github.com/ryan053857/mygoravel/fault"
)

// Register 登记端点。ModeOpenAPI 时绝不调用 newHandler；ModeServe 时先构造 handler。
// handler 返回的 fault 错误会转换为 ErrorModel。
func Register[I, O any](api *API, op huma.Operation, newHandler func() func(context.Context, *I) (*O, error)) {
	if api == nil {
		panic("rest: nil API")
	}
	if op.Security == nil {
		panic("rest: endpoint must explicitly declare Public or RequireBearer")
	}
	if len(op.Security) > 0 && (op.Metadata == nil || op.Metadata["mygoravel.bearer"] != true) {
		panic("rest: bearer security must bind runtime validation")
	}
	// Compare the existing native OpenAPI paths; do not create a second route registry.
	for path, item := range api.OpenAPI().Paths {
		for _, existing := range []*huma.Operation{item.Get, item.Post, item.Put, item.Patch, item.Delete, item.Head, item.Options, item.Trace} {
			if existing == nil {
				continue
			}
			if existing.OperationID == strings.ReplaceAll(op.OperationID, " ", "-") || (existing.Method == op.Method && routeShape(path) == routeShape(op.Path)) {
				panic("rest: duplicate operation ID or conflicting route")
			}
		}
	}
	var handler func(context.Context, *I) (*O, error)
	if api.mode == ModeOpenAPI {
		handler = func(context.Context, *I) (*O, error) {
			return nil, errors.New("rest: schema-only handler invoked")
		}
	} else {
		if newHandler == nil {
			panic("rest: nil newHandler")
		}
		h := newHandler()
		if h == nil {
			panic("rest: newHandler returned nil")
		}
		handler = func(ctx context.Context, in *I) (*O, error) {
			out, err := h(ctx, in)
			if err == nil {
				return out, nil
			}
			if se := FromFault(err); se != nil {
				if se.GetStatus() >= 500 && api.logger != nil {
					api.logger.ErrorContext(ctx, "request failed", "code", fault.CodeOf(err), "stack", errors.GetReportableStackTrace(err))
				}
				return out, se
			}
			return out, err
		}
	}
	huma.Register(api.API, op, handler)
}

func routeShape(path string) string {
	var b strings.Builder
	inside := false
	for _, c := range path {
		if c == '{' {
			inside = true
			b.WriteString("{}")
		} else if c == '}' {
			inside = false
		} else if !inside {
			b.WriteRune(c)
		}
	}
	return b.String()
}
