package s3

import (
	"context"
	"net/http"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
	"github.com/cockroachdb/errors"

	"github.com/go-tenon/tenon/doctor"
)

// verdict 执行返回 err 的检查，给出结果：passed、warning、failed、skipped；没有修改方法的失败（检查没能完成）为 error。
func verdict(err error) string {
	res := doctor.Run(context.Background(), doctor.Group{Name: "g", Checks: []doctor.Check{
		{Name: "c", Run: func(context.Context) error { return err }},
	}}).Results[0]
	if res.Status == doctor.Failed && res.Fix == "" {
		return "error"
	}
	return string(res.Status)
}

func TestCheckCORS(t *testing.T) {
	t.Parallel()
	upload := types.CORSRule{
		AllowedOrigins: []string{"https://app.example.com"},
		AllowedMethods: []string{"GET", "PUT"},
		AllowedHeaders: []string{"Content-Type"},
		ExposeHeaders:  []string{"ETag"},
	}
	without := func(edit func(*types.CORSRule)) []types.CORSRule {
		r := upload
		edit(&r)
		return []types.CORSRule{r}
	}
	cases := []struct {
		name    string
		rules   []types.CORSRule
		origins []string
		want    string
	}{
		{"exact origin", []types.CORSRule{upload}, []string{"https://app.example.com"}, "passed"},
		{"origin case", []types.CORSRule{upload}, []string{"https://APP.example.com"}, "passed"},
		{"any origin", without(func(r *types.CORSRule) { r.AllowedOrigins = []string{"*"} }), []string{"https://x.dev"}, "passed"},
		{"wildcard subdomain", without(func(r *types.CORSRule) { r.AllowedOrigins = []string{"https://*.example.com"} }), []string{"https://app.example.com"}, "passed"},
		{"wildcard other domain", without(func(r *types.CORSRule) { r.AllowedOrigins = []string{"https://*.example.com"} }), []string{"https://example.org"}, "failed"},
		{"any header", without(func(r *types.CORSRule) { r.AllowedHeaders = []string{"*"} }), []string{"https://app.example.com"}, "passed"},
		{"other origin", []types.CORSRule{upload}, []string{"https://admin.example.com"}, "failed"},
		{"one of two origins", []types.CORSRule{upload}, []string{"https://app.example.com", "https://admin.example.com"}, "failed"},
		{"no PUT", without(func(r *types.CORSRule) { r.AllowedMethods = []string{"GET"} }), []string{"https://app.example.com"}, "failed"},
		{"no content type", without(func(r *types.CORSRule) { r.AllowedHeaders = nil }), []string{"https://app.example.com"}, "failed"},
		{"etag not exposed", without(func(r *types.CORSRule) { r.ExposeHeaders = nil }), []string{"https://app.example.com"}, "failed"},
		{"no rules", nil, []string{"https://app.example.com"}, "failed"},
	}
	for _, c := range cases {
		if got := verdict(checkCORS(c.rules, c.origins)); got != c.want {
			t.Errorf("%s: %s, want %s", c.name, got, c.want)
		}
	}
}

func TestCORSError(t *testing.T) {
	t.Parallel()
	apiErr := func(code string) error { return &smithy.GenericAPIError{Code: code} }
	cases := map[string]string{
		"NoSuchCORSConfiguration": "failed",
		"NotImplemented":          "skipped",
		"AccessDenied":            "skipped",
		"InternalError":           "error",
	}
	for code, want := range cases {
		if got := verdict(corsError(apiErr(code))); got != want {
			t.Errorf("%s: %s, want %s", code, got, want)
		}
	}
	if got := verdict(corsError(errors.New("connection reset"))); got != "error" {
		t.Errorf("network error: %s", got)
	}
}

func TestCheckPublicResponse(t *testing.T) {
	t.Parallel()
	body := []byte("probe")
	cases := []struct {
		name   string
		status int
		got    []byte
		want   string
	}{
		{"served", http.StatusOK, body, "passed"},
		{"other content", http.StatusOK, []byte("<html>"), "failed"},
		{"private bucket", http.StatusForbidden, nil, "failed"},
		{"wrong path", http.StatusNotFound, nil, "failed"},
	}
	for _, c := range cases {
		if got := verdict(checkPublicResponse(c.status, c.got, body, "https://cdn.example.com")); got != c.want {
			t.Errorf("%s: %s, want %s", c.name, got, c.want)
		}
	}
}
