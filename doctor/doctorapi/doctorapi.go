// Package doctorapi 提供运行环境自检的 JSON 接口，复用应用在入口组装的 doctor.Group 列表。
//
// 接口注册在应用的 rest.API 上，路径为 GET /system/doctor，operationId 为 system-doctor，
// 生成的客户端中是 api.system.doctor()。@go-tenon/ui 的 DoctorPanel 是配套的后台页面。
//
//	doctorapi.Register(api, api.RequireToken("system:read"), a.doctor)
package doctorapi

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/go-tenon/tenon/doctor"
	"github.com/go-tenon/tenon/httpx/rest"
)

// output 是自检接口的 JSON 响应包装。
type output struct {
	Body doctor.DoctorReport
}

// Register 在 api 上注册运行环境自检接口 GET /system/doctor（system-doctor），protect 保护该接口，
// 通常是 api.RequireToken("system:read")。groups 每次请求时调用，返回待执行的自检分组；
// 离线构造路由（openapi、client 命令）时不调用 groups，因此可传依赖真实连接的 App 方法。
// api、protect 或 groups 为 nil 时 panic。
func Register(api *rest.API, protect func(*huma.Operation), groups func() []doctor.Group) {
	if api == nil || protect == nil || groups == nil {
		panic("doctorapi: nil API, protect or groups")
	}
	op := huma.Operation{
		OperationID: "system-doctor",
		Method:      http.MethodGet,
		Path:        "/system/doctor",
		Summary:     "运行环境自检",
		Tags:        []string{"system"},
	}
	protect(&op)
	rest.Register(api, op, func(ctx context.Context, _ *struct{}) (*output, error) {
		return &output{Body: doctor.Run(ctx, groups()...)}, nil
	})
}
