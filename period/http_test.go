package period_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/go-chi/chi/v5"

	"github.com/go-tenon/tenon/httpx/rest"
	"github.com/go-tenon/tenon/period"
)

type reportIn struct {
	period.Params
}

type reportBody struct {
	Period  period.Meta `json:"period"`
	Buckets []string    `json:"buckets"`
}

type reportOut struct {
	Body reportBody
}

func newAPI() *rest.API {
	api := rest.New(chi.NewRouter(), rest.Options{Title: "period"})
	now := time.Date(2026, 11, 22, 10, 0, 0, 0, time.UTC)
	rest.Register(api, huma.Operation{
		Security:    []map[string][]string{},
		OperationID: "report-summary",
		Method:      http.MethodGet,
		Path:        "/summary",
	}, func(_ context.Context, in *reportIn) (*reportOut, error) {
		w, err := period.Resolve(in.Params, period.Options{Now: now, DefaultZone: "Asia/Shanghai", DefaultDays: 7})
		if err != nil {
			return nil, err
		}
		return &reportOut{Body: reportBody{Period: w.Meta(), Buckets: w.Buckets()}}, nil
	})
	return api
}

func TestPeriodOverHTTP(t *testing.T) {
	t.Parallel()
	h := newAPI().Handler()

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/summary?from=2026-11-20&to=2026-11-22&tz=UTC", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var body reportBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Period.From != "2026-11-20" || body.Period.To != "2026-11-22" || body.Period.PrevFrom != "2026-11-17" || len(body.Buckets) != 3 {
		t.Fatalf("unexpected body: %+v", body)
	}

	// 非法参数返回 422
	for _, q := range []string{"from=2026-11-25&to=2026-11-20", "tz=Invalid/Zone", "granularity=quarter"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/summary?"+q, nil))
		if rec.Code != http.StatusUnprocessableEntity {
			t.Fatalf("%s: status=%d body=%s", q, rec.Code, rec.Body.String())
		}
	}
}

func TestPeriodOpenAPI(t *testing.T) {
	t.Parallel()
	doc, err := newAPI().OpenAPIJSON()
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"from"`, `"to"`, `"tz"`, `"granularity"`, `"prev_from"`, `"prev_to"`} {
		if !strings.Contains(string(doc), want) {
			t.Errorf("openapi missing %s", want)
		}
	}
}
