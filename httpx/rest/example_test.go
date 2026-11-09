package rest_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"

	"github.com/ryan053857/go-kit/httpx"
	"github.com/ryan053857/go-kit/httpx/rest"
)

// 以下三个类型通常单独放在 greet/get_greeting.go 一个文件中。

type GetGreetingInput struct {
	Name string `path:"name" maxLength:"30" doc:"要问候的人"`
}

type GetGreetingOutput struct {
	Body struct {
		Message string `json:"message"`
	}
}

type GetGreeting struct {
	Prefix string // 依赖通过字段注入
}

func (a *GetGreeting) Register(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "get-greeting",
		Method:      http.MethodGet,
		Path:        "/greetings/{name}",
		Summary:     "获取问候语",
		Tags:        []string{"greet"},
	}, a.handle)
}

func (a *GetGreeting) handle(ctx context.Context, in *GetGreetingInput) (*GetGreetingOutput, error) {
	out := &GetGreetingOutput{}
	out.Body.Message = a.Prefix + ", " + in.Name
	return out, nil
}

func Example() {
	huma.NewErrorWithContext = rest.LogServerErrors(huma.NewErrorWithContext)

	r := chi.NewRouter()
	r.Use(httpx.RequestID, httpx.AccessLog(nil), httpx.Recover(nil))

	api := humachi.New(r, huma.DefaultConfig("Greeting API", "1.0.0"))
	v1 := huma.NewGroup(api, "/api/v1")
	rest.Mount(v1, &GetGreeting{Prefix: "Hello"})

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/greetings/world", nil))
	fmt.Println(rec.Code)
	// Output: 200
}
