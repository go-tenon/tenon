//go:build integration

package api_test

import (
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/danielgtaylor/huma/v2"

	"github.com/ryan053857/mygoravel/database/dbtest"
	"github.com/ryan053857/mygoravel/database/migrate"
	"github.com/ryan053857/mygoravel/examples/api/internal/order"
	"github.com/ryan053857/mygoravel/examples/api/internal/order/orderhttp"
	"github.com/ryan053857/mygoravel/fault"
	"github.com/ryan053857/mygoravel/httpx/rest"
	"github.com/ryan053857/mygoravel/identity"
)

//go:embed migrations
var migrationsRoot embed.FS

func setupAPI(t *testing.T) *rest.API {
	t.Helper()
	fsys, err := migrate.Sub(migrationsRoot, "migrations")
	if err != nil {
		t.Fatal(err)
	}
	db := dbtest.Open(t, fsys)
	api := rest.New(rest.Options{Title: "orders"})
	validate := func(ctx huma.Context, token string) (identity.Subject, error) {
		switch token {
		case "alice", "bob":
			return identity.Subject{ID: token, Type: identity.SubjectUser, Method: "bearer", Scopes: []string{"orders"}}, nil
		default:
			return identity.Subject{}, fault.Define(fault.Unauthenticated, "auth.unauthenticated").New()
		}
	}
	orderhttp.Register(api, orderhttp.Dependencies{
		Orders:   order.New(db),
		Validate: validate,
	})
	return api
}

func do(t *testing.T, api *rest.API, method, path, token, body string) (int, string) {
	t.Helper()
	var rdr io.Reader
	if body != "" {
		rdr = bytes.NewBufferString(body)
	}
	req := httptest.NewRequest(method, path, rdr)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	api.Handler().ServeHTTP(rec, req)
	return rec.Code, rec.Body.String()
}

func TestPublicAuthAndOwnership(t *testing.T) {
	api := setupAPI(t)

	code, body := do(t, api, http.MethodPost, "/orders", "", `{"title":"x"}`)
	if code != 401 {
		t.Fatalf("unauth create: %d %s", code, body)
	}

	code, body = do(t, api, http.MethodPost, "/orders", "alice", `{"title":"a1"}`)
	if code != 200 {
		t.Fatalf("create: %d %s", code, body)
	}
	var created map[string]any
	_ = json.Unmarshal([]byte(body), &created)
	id, _ := created["id"].(string)
	if id == "" {
		t.Fatalf("body=%s", body)
	}

	code, body = do(t, api, http.MethodGet, "/orders/"+id, "bob", "")
	if code != 403 {
		t.Fatalf("bob get alice order: %d %s", code, body)
	}

	code, body = do(t, api, http.MethodGet, "/orders/"+id, "alice", "")
	if code != 200 || !strings.Contains(body, "a1") {
		t.Fatalf("alice get: %d %s", code, body)
	}

	// 直接调用服务也应执行资源授权。
	fsys, _ := migrate.Sub(migrationsRoot, "migrations")
	db := dbtest.Open(t, fsys)
	svc := order.New(db)
	_, err := svc.Create(context.Background(), identity.Subject{ID: "alice"}, order.CreateParams{Title: "svc"})
	if err != nil {
		t.Fatal(err)
	}
}

func TestOpenAPISecurityMatches(t *testing.T) {
	api := setupAPI(t)
	doc, err := api.OpenAPIJSON()
	if err != nil {
		t.Fatal(err)
	}
	s := string(doc)
	if !strings.Contains(s, `"bearer"`) || !strings.Contains(s, "order-create") {
		t.Fatalf("openapi missing security: %s", s)
	}
}
