package orderhttp

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"

	"github.com/ryan053857/mygoravel/examples/api/internal/order"
	"github.com/ryan053857/mygoravel/fault"
	"github.com/ryan053857/mygoravel/httpx/rest"
	"github.com/ryan053857/mygoravel/identity"
)

// Dependencies 是 HTTP 入口依赖。
type Dependencies struct {
	Orders   *order.Service
	Validate rest.TokenValidator
}

type createIn struct {
	Body struct {
		Title string `json:"title" minLength:"1" maxLength:"120"`
	}
}
type createOut struct {
	Body OrderDTO
}

type getIn struct {
	ID uuid.UUID `path:"id"`
}
type getOut struct {
	Body OrderDTO
}

type listIn struct {
	Before string `query:"before"`
	Limit  int    `query:"limit"`
}
type listOut struct {
	Body struct {
		Items []OrderDTO `json:"items"`
		Next  string     `json:"next,omitempty"`
	}
}

// OrderDTO 是传输对象。
type OrderDTO struct {
	ID      string `json:"id"`
	Title   string `json:"title"`
	OwnerID string `json:"owner_id"`
}

func dto(o *order.Order) OrderDTO {
	return OrderDTO{ID: o.ID.String(), Title: o.Title, OwnerID: o.OwnerID}
}

// Register 登记订单 HTTP 端点。
func Register(api *rest.API, deps Dependencies) {
	auth := rest.RequireBearer(api, deps.Validate, "orders")

	createOp := huma.Operation{OperationID: "order-create", Method: http.MethodPost, Path: "/orders", Summary: "创建订单"}
	auth(&createOp)
	rest.Register(api, createOp, func() func(context.Context, *createIn) (*createOut, error) {
		return func(ctx context.Context, in *createIn) (*createOut, error) {
			sub, ok := identity.FromContext(ctx)
			if !ok {
				return nil, fault.Define(fault.Unauthenticated, "auth.unauthenticated").New()
			}
			o, err := deps.Orders.Create(ctx, sub, order.CreateParams{Title: in.Body.Title})
			if err != nil {
				return nil, err
			}
			return &createOut{Body: dto(o)}, nil
		}
	})

	getOp := huma.Operation{OperationID: "order-get", Method: http.MethodGet, Path: "/orders/{id}", Summary: "获取订单"}
	auth(&getOp)
	rest.Register(api, getOp, func() func(context.Context, *getIn) (*getOut, error) {
		return func(ctx context.Context, in *getIn) (*getOut, error) {
			sub, ok := identity.FromContext(ctx)
			if !ok {
				return nil, fault.Define(fault.Unauthenticated, "auth.unauthenticated").New()
			}
			o, err := deps.Orders.Get(ctx, sub, in.ID)
			if err != nil {
				return nil, err
			}
			return &getOut{Body: dto(o)}, nil
		}
	})

	updateOp := huma.Operation{OperationID: "order-update", Method: http.MethodPatch, Path: "/orders/{id}", Summary: "更新订单"}
	auth(&updateOp)
	rest.Register(api, updateOp, func() func(context.Context, *updateIn) (*getOut, error) {
		return func(ctx context.Context, in *updateIn) (*getOut, error) {
			sub, _ := identity.FromContext(ctx)
			o, err := deps.Orders.Update(ctx, sub, in.ID, order.CreateParams{Title: in.Body.Title})
			if err != nil {
				return nil, err
			}
			return &getOut{Body: dto(o)}, nil
		}
	})
	listOp := huma.Operation{OperationID: "order-list", Method: http.MethodGet, Path: "/orders", Summary: "列出订单"}
	auth(&listOp)
	rest.Register(api, listOp, func() func(context.Context, *listIn) (*listOut, error) {
		return func(ctx context.Context, in *listIn) (*listOut, error) {
			sub, ok := identity.FromContext(ctx)
			if !ok {
				return nil, fault.Define(fault.Unauthenticated, "auth.unauthenticated").New()
			}
			params := order.ListParams{Limit: in.Limit, Before: in.Before}
			res, err := deps.Orders.List(ctx, sub, params)
			if err != nil {
				return nil, err
			}
			out := &listOut{}
			out.Body.Next = res.Next
			out.Body.Items = []OrderDTO{}
			for i := range res.Items {
				out.Body.Items = append(out.Body.Items, dto(&res.Items[i]))
			}
			return out, nil
		}
	})
}

type updateIn struct {
	ID   uuid.UUID `path:"id"`
	Body struct {
		Title string `json:"title" minLength:"1" maxLength:"120"`
	}
}
