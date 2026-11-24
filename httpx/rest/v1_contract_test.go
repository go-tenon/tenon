package rest_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/danielgtaylor/huma/v2"
	"github.com/go-chi/chi/v5"

	"github.com/go-tenon/tenon/broadcast"
	"github.com/go-tenon/tenon/errorx"
	"github.com/go-tenon/tenon/httpx/rest"
	"github.com/go-tenon/tenon/identity"
	"github.com/go-tenon/tenon/period"
)

func TestAPIValidateCheckTenantAndFieldLocation(t *testing.T) {
	t.Parallel()
	var nilCreds *broadcast.Credentials
	errTenantDisabled := errorx.Define(errorx.PermissionDenied, "test.tenant_disabled")

	r := chi.NewRouter()
	api := rest.New(r, rest.Options{
		BasePath: "/api",
		Validate: func(_ context.Context, token string) (identity.Subject, error) {
			if token != "ok" {
				return identity.Subject{}, identity.ErrUnauthenticated.New()
			}
			return identity.Subject{Type: "user", ID: "u1", Permissions: []string{"*:read"}}, nil
		},
		Accounts: identity.Accounts{"user": nil},
		Tenant:   func(ctx huma.Context) string { return ctx.Header("X-Tenant") },
		CheckTenant: func(_ context.Context, tenant string) error {
			if tenant == "disabled" {
				return errTenantDisabled.New()
			}
			return nil
		},
		Credentials: nilCreds, // typed-nil 接口指针在构造和请求期间均不触发 panic
	})

	type queryInput struct {
		period.Params
	}
	op := huma.Operation{OperationID: "usage-summary", Method: http.MethodGet, Path: "/usage"}
	api.RequireToken("usage:read")(&op)
	rest.Register(api, op, func(_ context.Context, in *queryInput) (*struct{}, error) {
		if _, err := period.Resolve(in.Params, period.Options{}); err != nil {
			return nil, err
		}
		return nil, nil
	})

	// 1. 动作通配符 *:read 放行 usage:read，且 query 参数业务错误位置映射为 query.tz。
	req := httptest.NewRequest(http.MethodGet, "/usage?tz=Local", nil)
	req.Header.Set("Authorization", "Bearer ok")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var errBody struct {
		Code   string `json:"code"`
		Errors []struct {
			Location string `json:"location"`
			Code     string `json:"code"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &errBody); err != nil {
		t.Fatal(err)
	}
	if errBody.Code != period.ErrInvalidTimeZone.Code() || len(errBody.Errors) != 1 || errBody.Errors[0].Location != "query.tz" {
		t.Fatalf("unexpected error body: %+v", errBody)
	}

	// 2. CheckTenant 在停用租户时返回 403。
	req2 := httptest.NewRequest(http.MethodGet, "/usage", nil)
	req2.Header.Set("Authorization", "Bearer ok")
	req2.Header.Set("X-Tenant", "disabled")
	rec2 := httptest.NewRecorder()
	r.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusForbidden {
		t.Fatalf("disabled tenant status = %d, body = %s", rec2.Code, rec2.Body.String())
	}
}
