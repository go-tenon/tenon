package setting_test

import (
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"
	"github.com/uptrace/bun"

	"github.com/ryan053857/go-kit/database"
	"github.com/ryan053857/go-kit/database/dbtest"
	"github.com/ryan053857/go-kit/httpx/rest"
	"github.com/ryan053857/go-kit/modules/setting"
)

type site struct {
	Name             string   `json:"name" minLength:"1" maxLength:"50"`
	RegistrationOpen bool     `json:"registration_open"`
	Tags             []string `json:"tags"`
}

var siteGroup = setting.Group[site]{Key: "site", Default: site{Name: "默认", RegistrationOpen: true, Tags: []string{"a"}}}

func newDB(t *testing.T) *bun.DB {
	t.Helper()
	cfg := database.DefaultConfig()
	cfg.DSN = dbtest.DSN(t)
	db, err := database.Open(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	dir := t.TempDir()
	if _, err := database.PublishMigrations(dir, setting.Migrations); err != nil {
		t.Fatal(err)
	}
	if err := database.Migrate(t.Context(), db.DB, os.DirFS(dir)); err != nil {
		t.Fatal(err)
	}
	return db
}

func TestGroup(t *testing.T) {
	db := newDB(t)
	s := setting.New(db)
	ctx := t.Context()

	v, err := siteGroup.Get(ctx, s)
	if err != nil || v.Name != "默认" || !v.RegistrationOpen {
		t.Fatalf("default = %+v, %v", v, err)
	}
	v.Tags[0] = "changed" // 不能影响 Default
	if siteGroup.Default.Tags[0] != "a" {
		t.Error("Default mutated")
	}

	v.Name, v.RegistrationOpen = "新名字", false
	if err := siteGroup.Set(ctx, s, v); err != nil {
		t.Fatal(err)
	}
	got, _ := siteGroup.Get(ctx, s)
	if got.Name != "新名字" || got.RegistrationOpen {
		t.Errorf("after set = %+v", got)
	}

	// 数据库中缺少的字段取默认值（结构体新增字段）。
	if _, err := db.ExecContext(ctx, `UPDATE system_settings SET value = '{"name":"旧版"}' WHERE key = 'site'`); err != nil {
		t.Fatal(err)
	}
	s2 := setting.New(db)
	got, _ = siteGroup.Get(ctx, s2)
	if got.Name != "旧版" || !got.RegistrationOpen || got.Tags[0] != "a" {
		t.Errorf("merged = %+v", got)
	}

	// 缓存：其他进程修改后，TTL 内仍读到旧值；TTL 为 0 时不缓存。
	if _, err := db.ExecContext(ctx, `UPDATE system_settings SET value = '{"name":"外部修改"}' WHERE key = 'site'`); err != nil {
		t.Fatal(err)
	}
	if got, _ = siteGroup.Get(ctx, s2); got.Name != "旧版" {
		t.Errorf("cached = %+v", got)
	}
	s2.TTL = 0
	if got, _ = siteGroup.Get(ctx, s2); got.Name != "外部修改" {
		t.Errorf("uncached = %+v", got)
	}
}

func TestEndpoints(t *testing.T) {
	s := setting.New(newDB(t))
	r := chi.NewRouter()
	api := humachi.New(r, huma.DefaultConfig("t", "1"))
	noSec := func(o *huma.Operation) { o.Security = nil }
	rest.Get(api, "/settings/site", setting.GetEndpoint(s, siteGroup), noSec)
	rest.Put(api, "/settings/site", setting.PutEndpoint(s, siteGroup), noSec)

	do := func(method, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, "/settings/site", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		return rec
	}
	if rec := do("GET", ""); rec.Code != 200 || !strings.Contains(rec.Body.String(), `"name":"默认"`) {
		t.Fatalf("get = %d %s", rec.Code, rec.Body)
	}
	if rec := do("PUT", `{"name":"","registration_open":false,"tags":[]}`); rec.Code != 422 {
		t.Errorf("invalid put = %d %s", rec.Code, rec.Body)
	}
	if rec := do("PUT", `{"name":"产品","registration_open":false,"tags":[]}`); rec.Code != 200 {
		t.Fatalf("put = %d %s", rec.Code, rec.Body)
	}
	if rec := do("GET", ""); !strings.Contains(rec.Body.String(), `"name":"产品"`) {
		t.Errorf("get after put = %s", rec.Body)
	}
}
