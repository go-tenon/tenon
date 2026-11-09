// Package rest 提供以 huma 编写 REST API 时的约定与少量胶水代码。
//
// API 直接使用 github.com/danielgtaylor/huma/v2 与 adapters/humachi，
// 本包不包装 huma 的 API，只补充：
//
//	LogServerErrors        5xx 错误带调用栈记录日志（huma 本身不记录错误）
//	Action、Mount          单文件 action 模式的接口约定（产品自己的接口）
//	Endpoint、Get、Post…   go-kit 模块提供的接口，由产品选择路径挂载
//
// # 用法
//
//	// 5xx 日志：显式替换 huma 的全局错误构造函数，在注册任何路由之前调用一次。
//	huma.NewErrorWithContext = rest.LogServerErrors(huma.NewErrorWithContext)
//
//	r := chi.NewRouter()
//	r.Use(httpx.RealIP(...), httpx.RequestID, httpx.AccessLog(nil), httpx.Recover(nil))
//
//	config := huma.DefaultConfig("Order API", "1.0.0")
//	api := humachi.New(r, config)
//
//	v1 := huma.NewGroup(api, "/api/v1")
//	rest.Mount(v1, order.Actions(deps)...)
//
// # Action 模式
//
// 一个接口一个文件：输入、输出、依赖、路由元数据与处理逻辑写在一起。
//
//	// order/create_order.go
//	type CreateOrderInput struct {
//		Body struct {
//			SKU string `json:"sku" minLength:"1"`
//			Qty int    `json:"qty" minimum:"1"`
//		}
//	}
//
//	type CreateOrderOutput struct {
//		Body OrderDTO
//	}
//
//	type CreateOrder struct {
//		Orders *Service
//	}
//
//	func (a *CreateOrder) Register(api huma.API) {
//		huma.Register(api, huma.Operation{
//			OperationID: "create-order",
//			Method:      http.MethodPost,
//			Path:        "/orders",
//			Summary:     "创建订单",
//			Tags:        []string{"order"},
//		}, a.handle)
//	}
//
//	func (a *CreateOrder) handle(ctx context.Context, in *CreateOrderInput) (*CreateOrderOutput, error) {
//		o, err := a.Orders.Create(ctx, in.Body.SKU, in.Body.Qty)
//		switch {
//		case errors.Is(err, ErrOutOfStock):
//			return nil, huma.Error409Conflict("库存不足")
//		case err != nil:
//			return nil, err // 500，由 LogServerErrors 记录带调用栈的日志
//		}
//		return &CreateOrderOutput{Body: toDTO(o)}, nil
//	}
//
// # Endpoint
//
// go-kit 的业务模块不自己注册路由，而是返回 Endpoint：处理函数加 OpenAPI 元数据，不含 Method 与 Path。
// 产品在自己的路由文件中显式挂载，所有路由一目了然，不需要的接口不挂即可：
//
//	rest.Post(v1, "/auth/login", account.Login(users))
//	rest.Post(v1, "/auth/refresh", account.Refresh(users))
//
// # 错误处理
//
// 领域层用 github.com/cockroachdb/errors 定义与包装错误（包装用 errors.Wrap，
// 不用 fmt.Errorf，后者会让日志丢失调用栈）；
// action 用 errors.Is / errors.As 把可预期的错误转换为 huma.Error4xx，
// 其余错误直接返回，由 huma 响应 500。
package rest
