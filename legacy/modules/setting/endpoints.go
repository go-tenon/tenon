package setting

import (
	"context"

	"github.com/danielgtaylor/huma/v2"

	"github.com/ryan053857/go-kit/httpx/rest"
)

type output[T any] struct{ Body T }

type input[T any] struct{ Body T }

// GetEndpoint 返回读取一组设置的接口，PutEndpoint 返回保存的接口。T 上的 huma 校验标签
// （minLength、maximum 等）会生成在 OpenAPI 中并在保存时校验：
//
//	rest.Get(admin, "/settings/site", setting.GetEndpoint(settings, Site))
//	rest.Put(admin, "/settings/site", setting.PutEndpoint(settings, Site))
//
// 两个接口都需要 setting.manage 能力；Get 的设置不敏感时，产品可以在挂载时去掉 Security 公开给前端。
func GetEndpoint[T any](s *Store, g Group[T]) rest.Endpoint[struct{}, output[T]] {
	return rest.Endpoint[struct{}, output[T]]{
		Operation: huma.Operation{OperationID: "setting-get-" + g.Key, Summary: "读取设置：" + g.Key,
			Tags: []string{"setting"}, Security: rest.RequireAuth(Manage)},
		Handler: func(ctx context.Context, _ *struct{}) (*output[T], error) {
			v, err := g.Get(ctx, s)
			if err != nil {
				return nil, err
			}
			return &output[T]{Body: v}, nil
		},
	}
}

// PutEndpoint 返回保存一组设置的接口，见 GetEndpoint。
func PutEndpoint[T any](s *Store, g Group[T]) rest.Endpoint[input[T], output[T]] {
	return rest.Endpoint[input[T], output[T]]{
		Operation: huma.Operation{OperationID: "setting-put-" + g.Key, Summary: "保存设置：" + g.Key,
			Tags: []string{"setting"}, Security: rest.RequireAuth(Manage)},
		Handler: func(ctx context.Context, in *input[T]) (*output[T], error) {
			if err := g.Set(ctx, s, in.Body); err != nil {
				return nil, err
			}
			return &output[T]{Body: in.Body}, nil
		},
	}
}
