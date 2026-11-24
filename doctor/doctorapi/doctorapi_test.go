package doctorapi_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/go-tenon/tenon/doctor"
	"github.com/go-tenon/tenon/doctor/doctorapi"
	"github.com/go-tenon/tenon/httpx/rest"
	"github.com/go-tenon/tenon/identity"
)

func TestRegister(t *testing.T) {
	t.Parallel()
	r := chi.NewRouter()
	api := rest.New(r, rest.Options{
		BasePath: "/api",
		Validate: func(_ context.Context, token string) (identity.Subject, error) {
			if token == "ok" {
				return identity.Subject{Type: "admin", ID: "1", Permissions: []string{"system:read"}}, nil
			}
			return identity.Subject{}, identity.ErrUnauthenticated.New()
		},
		Accounts: identity.Accounts{"admin": nil},
	})
	doctorapi.Register(api, api.RequireToken("system:read"), func() []doctor.Group {
		return []doctor.Group{
			{
				Name: "postgres",
				Checks: []doctor.Check{
					{Name: "ping", Run: func(context.Context) error { return nil }},
					{Name: "clock", Run: func(context.Context) error { return doctor.Warn("skew 2s", "sync ntp") }},
				},
			},
		}
	})

	req := httptest.NewRequest(http.MethodGet, "/system/doctor", nil)
	req.Header.Set("Authorization", "Bearer ok")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var report doctor.DoctorReport
	if err := json.Unmarshal(rec.Body.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if len(report.Results) != 2 || report.Results[0].Status != doctor.Passed || report.Results[1].Status != doctor.Warning {
		t.Fatalf("unexpected report: %+v", report)
	}
}
