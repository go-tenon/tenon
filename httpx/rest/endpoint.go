package rest

import (
	"context"
	"maps"
	"net/http"
	"slices"

	"github.com/danielgtaylor/huma/v2"
)

// Endpoint 是一个尚未挂载的接口：处理函数与 OpenAPI 元数据（摘要、标签、Security、错误码……），
// 但不含 Method 与 Path。go-kit 的业务模块以 Endpoint 的形式提供接口，
// 由产品决定是否挂载、挂在哪个路径：
//
//	api := huma.NewGroup(a.API(), "/api/v1")
//	rest.Post(api, "/auth/login", account.Login(users))
//	rest.Get(api, "/auth/sessions", account.ListSessions(users), func(o *huma.Operation) {
//		o.Tags = []string{"账号"}
//	})
//
// opts 在挂载时按顺序修改 Operation，可覆盖 Endpoint 提供的任何元数据。
// OperationID、Summary 为空时与 huma.Get 等便捷函数一样按 method 与 path 自动生成。
type Endpoint[I, O any] struct {
	Operation huma.Operation
	Handler   func(ctx context.Context, in *I) (*O, error)
}

// Get 把 e 挂载为 GET path。
func Get[I, O any](api huma.API, path string, e Endpoint[I, O], opts ...func(*huma.Operation)) {
	Handle(api, http.MethodGet, path, e, opts...)
}

// Post 把 e 挂载为 POST path。
func Post[I, O any](api huma.API, path string, e Endpoint[I, O], opts ...func(*huma.Operation)) {
	Handle(api, http.MethodPost, path, e, opts...)
}

// Put 把 e 挂载为 PUT path。
func Put[I, O any](api huma.API, path string, e Endpoint[I, O], opts ...func(*huma.Operation)) {
	Handle(api, http.MethodPut, path, e, opts...)
}

// Patch 把 e 挂载为 PATCH path。
func Patch[I, O any](api huma.API, path string, e Endpoint[I, O], opts ...func(*huma.Operation)) {
	Handle(api, http.MethodPatch, path, e, opts...)
}

// Delete 把 e 挂载为 DELETE path。
func Delete[I, O any](api huma.API, path string, e Endpoint[I, O], opts ...func(*huma.Operation)) {
	Handle(api, http.MethodDelete, path, e, opts...)
}

// Handle 把 e 挂载为 method path，api 可以是 huma.API 或 *huma.Group。
func Handle[I, O any](api huma.API, method, path string, e Endpoint[I, O], opts ...func(*huma.Operation)) {
	op := e.Operation
	// 同一个 Endpoint 可能被挂载多次，opts 修改的切片与 map 不能影响其他挂载。
	op.Tags = slices.Clone(op.Tags)
	op.Security = slices.Clone(op.Security)
	op.Errors = slices.Clone(op.Errors)
	op.Metadata = maps.Clone(op.Metadata)
	op.Method = method
	op.Path = path
	if op.OperationID == "" {
		op.OperationID = huma.GenerateOperationID(method, path, (*O)(nil))
	}
	if op.Summary == "" {
		op.Summary = huma.GenerateSummary(method, path, (*O)(nil))
	}
	for _, opt := range opts {
		opt(&op)
	}
	huma.Register(api, op, e.Handler)
}
