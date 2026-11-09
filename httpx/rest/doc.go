// Package rest 提供以 huma 编写 REST API 时的约定与少量胶水代码。
//
// API 直接使用 github.com/danielgtaylor/huma/v2 与 adapters/humachi，
// 本包不包装 huma 的 API，只补充：
//
//	NewErrorWithContext    领域错误转换为状态码、错误码与多语言提示；5xx 带调用栈记录日志
//	Authorize、RequireAuth 按 OpenAPI security 认证，并按声明的能力（authz）授权
//	Action、Mount          单文件 action 模式的接口约定（产品自己的接口）
//	Endpoint、Get、Post…   go-kit 模块提供的接口，由产品选择路径挂载
//	PageInput、Page        游标分页的请求参数与响应
//	RateLimit              按 IP 限流
//
// # 用法
//
//	// 错误处理：替换 huma 的全局错误构造函数，在注册任何路由之前调用一次（app 包已替换）。
//	huma.NewErrorWithContext = rest.NewErrorWithContext
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
//		if err != nil {
//			return nil, err // 领域错误（如 ErrOutOfStock）按登记的状态码响应，其他错误为 500 并记录日志
//		}
//		return &CreateOrderOutput{Body: a.Orders.OrderDTO(o)}, nil
//	}
//
// # Endpoint
//
// go-kit 的业务模块不自己注册路由，而是返回 Endpoint：处理函数加 OpenAPI 元数据，不含 Method 与 Path。
// 产品在自己的路由文件中显式挂载，所有路由一目了然，不需要的接口不挂即可：
//
//	rest.Post(v1, "/auth/login", account.LoginEndpoint(users))
//	rest.Post(v1, "/auth/refresh", account.RefreshEndpoint(users))
//
// # 错误处理
//
// 领域错误用 httpx.DefineError 定义（登记状态码与错误码，错误码同时是多语言消息 ID），
// 包装用 github.com/cockroachdb/errors 的 errors.Wrap（fmt.Errorf 会让日志丢失调用栈）。
// action 直接返回错误，由 NewErrorWithContext 转换：
//
//	var ErrOutOfStock = httpx.DefineError(http.StatusConflict, "order.out_of_stock")
//
// 响应为 RFC 9457 并带错误码：{"status":409,"title":"Conflict","detail":"库存不足","code":"order.out_of_stock"}。
package rest
