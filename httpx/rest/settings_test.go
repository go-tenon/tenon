package rest_test

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/danielgtaylor/huma/v2"
	"github.com/go-chi/chi/v5"

	"github.com/go-tenon/tenon/httpx/rest"
)

type siteSettings struct {
	Name       string        `json:"name" minLength:"1"`
	SignupOpen bool          `json:"signup_open"`
	Theme      articleStatus `json:"theme"`
}

type siteOut struct{ Body siteSettings }
type siteIn struct{ Body siteSettings }

// settingsAPI 登记站点设置的读取和保存接口，以及一个带路径参数的接口。
func settingsAPI() *rest.API {
	api := rest.New(chi.NewRouter(), rest.Options{})
	reg := func(id, method, path string) huma.Operation {
		op := huma.Operation{OperationID: id, Method: method, Path: path}
		rest.Public()(&op)
		return op
	}
	rest.Register(api, reg("site-settings-get", http.MethodGet, "/settings/site"),
		func(context.Context, *struct{}) (*siteOut, error) { return &siteOut{}, nil })
	rest.Register(api, reg("site-settings-update", http.MethodPut, "/settings/site"),
		func(context.Context, *siteIn) (*siteOut, error) { return &siteOut{}, nil })
	rest.Register(api, reg("tenant-settings-get", http.MethodGet, "/tenants/{id}/settings"),
		func(context.Context, *struct {
			ID string `path:"id"`
		}) (*siteOut, error) {
			return &siteOut{}, nil
		})
	return api
}

func TestSettings(t *testing.T) {
	api := settingsAPI()
	api.Settings(rest.Settings{Name: "site", Get: "site-settings-get", Update: "site-settings-update"})
	got := api.SettingsPages()
	if len(got) != 1 {
		t.Fatalf("settings = %+v", got)
	}
	// 表单字段默认是保存接口请求体的全部字段，按结构体中的顺序。
	if f := strings.Join(got[0].Form, ","); f != "name,signup_open,theme" {
		t.Errorf("form = %s", f)
	}
	ext, _ := api.OpenAPI().Extensions["x-tenon-settings"].([]rest.Settings)
	if len(ext) != 1 || ext[0].Name != "site" {
		t.Errorf("x-tenon-settings = %+v", api.OpenAPI().Extensions["x-tenon-settings"])
	}
}

func TestSettingsRejects(t *testing.T) {
	for _, c := range []struct {
		s    rest.Settings
		want string
	}{
		{rest.Settings{Name: "Site", Get: "site-settings-get", Update: "site-settings-update"}, "kebab-case"},
		{rest.Settings{Name: "site", Get: "site-settings-get"}, "both the get and update operations are required"},
		{rest.Settings{Name: "site", Get: "missing", Update: "site-settings-update"}, "not registered"},
		{rest.Settings{Name: "site", Get: "site-settings-update", Update: "site-settings-update"}, "must be GET"},
		{rest.Settings{Name: "site", Get: "site-settings-get", Update: "site-settings-get"}, "must not be GET"},
		{rest.Settings{Name: "site", Get: "tenant-settings-get", Update: "site-settings-update"}, "must not have path parameters"},
		{rest.Settings{Name: "site", Get: "site-settings-get", Update: "site-settings-update", Form: []string{"title"}}, "form field"},
	} {
		func() {
			defer func() {
				if r := recover(); r == nil || !strings.Contains(r.(string), c.want) {
					t.Errorf("%+v: panic = %v, want %q", c.s, r, c.want)
				}
			}()
			settingsAPI().Settings(c.s)
		}()
	}

	// 同一个名称声明两次。
	api := settingsAPI()
	api.Settings(rest.Settings{Name: "site", Get: "site-settings-get", Update: "site-settings-update"})
	defer func() {
		if r := recover(); r == nil || !strings.Contains(r.(string), "declared twice") {
			t.Errorf("panic = %v", r)
		}
	}()
	api.Settings(rest.Settings{Name: "site", Get: "site-settings-get", Update: "site-settings-update"})
}
