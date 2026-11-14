package rest_test

import (
	"context"
	"github.com/go-chi/chi/v5"
	"net/http"
	"reflect"
	"testing"

	"github.com/danielgtaylor/huma/v2"

	"github.com/go-tenon/tenon/httpx/rest"
	"github.com/go-tenon/tenon/identity"
)

func TestEndpointsDescribeRegisteredOperations(t *testing.T) {
	api := rest.New(chi.NewRouter(), rest.Options{Accounts: identity.Accounts{"user": nil}})
	get := huma.Operation{OperationID: "count-get", Method: http.MethodGet, Path: "/counts/{id}", Summary: "取计数"}
	rest.RequireBearer(api, func(context.Context, string) (identity.Subject, error) { return identity.Subject{}, nil })(&get)
	rest.Register(api, get, func(context.Context, *countIn) (*minimalOutput, error) { return nil, nil })
	stream := huma.Operation{OperationID: "count-stream", Method: http.MethodGet, Path: "/count"}
	rest.Public()(&stream)
	rest.RegisterSSE(api, stream, map[string]any{"tick": &tick{}}, func(context.Context, *countIn) (*huma.StreamResponse, error) { return nil, nil })

	eps := api.Endpoints()
	if len(eps) != 2 {
		t.Fatalf("%+v", eps)
	}
	if e := eps[0]; e.OperationID != "count-get" || e.Path != "/counts/{id}" || e.Summary != "取计数" || e.Public ||
		e.Input != reflect.TypeFor[countIn]() || e.Output != reflect.TypeFor[minimalOutput]() || e.Events != nil {
		t.Fatalf("%+v", e)
	}
	if e := eps[1]; !e.Public || e.Output != reflect.TypeFor[huma.StreamResponse]() ||
		!reflect.DeepEqual(e.Events, map[string]reflect.Type{"tick": reflect.TypeFor[tick]()}) {
		t.Fatalf("%+v", e)
	}
}
