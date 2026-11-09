package rest

import (
	"context"

	"github.com/cockroachdb/errors"
	"github.com/danielgtaylor/huma/v2"
)

// Register 登记端点。ModeOpenAPI 时绝不调用 newHandler；ModeServe 时先构造 handler。
// handler 返回的 fault 错误会转换为 ErrorModel。
func Register[I, O any](api *API, op huma.Operation, newHandler func() func(context.Context, *I) (*O, error)) {
	if api == nil {
		panic("rest: nil API")
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
				return out, se
			}
			return out, err
		}
	}
	huma.Register(api.API, op, handler)
}
