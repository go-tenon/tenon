package app_test

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/danielgtaylor/huma/v2"

	"github.com/ryan053857/go-kit/app"
	"github.com/ryan053857/go-kit/database"
	"github.com/ryan053857/go-kit/database/dbtest"
	"github.com/ryan053857/go-kit/httpx/rest"
)

type productConfig struct {
	app.Config `yaml:",inline"`
	Greeting   string `yaml:"greeting"`
}

type helloOutput struct {
	Body struct {
		Message string `json:"message"`
	}
}

func helloEndpoint(greeting string) rest.Endpoint[struct{}, helloOutput] {
	return rest.Endpoint[struct{}, helloOutput]{
		Operation: huma.Operation{Summary: "问候"},
		Handler: func(context.Context, *struct{}) (*helloOutput, error) {
			out := &helloOutput{}
			out.Body.Message = greeting
			return out, nil
		},
	}
}

func setup(cfg *productConfig) func(a *app.App) error {
	return func(a *app.App) error {
		// 中间件必须在路由之前挂载。
		a.Router.Use(func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("X-Product", "test")
				next.ServeHTTP(w, r)
			})
		})
		rest.Get(huma.NewGroup(a.API(), "/api"), "/hello", helloEndpoint(cfg.Greeting))
		a.Router.Get("/visit", func(w http.ResponseWriter, r *http.Request) {
			n := a.Sessions.GetInt(r.Context(), "n") + 1
			a.Sessions.Put(r.Context(), "n", n)
			_, _ = w.Write([]byte{byte('0' + n)})
		})
		return nil
	}
}

// productMigrations 模拟产品迁移目录：会话表（从 app 发布）加一张产品表。
func productMigrations(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if _, err := database.PublishMigrations(dir, app.SessionMigrations); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "29990101000000_create_things.sql"), []byte(
		"-- +goose Up\nCREATE TABLE things (id uuid PRIMARY KEY);\n-- +goose Down\nDROP TABLE things;\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestNew(t *testing.T) {
	cfg := productConfig{Config: app.DefaultConfig(), Greeting: "hello"}
	cfg.Database.DSN = dbtest.DSN(t)
	cfg.Session.SecureCookie = false

	a, err := app.New(t.Context(), &cfg.Config, app.Options{
		Name:       "test",
		Version:    "1.0.0",
		Migrations: os.DirFS(productMigrations(t)),
		Setup:      setup(&cfg),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()

	// 迁移已执行。
	var n int
	if err := a.DB.QueryRowContext(t.Context(), "SELECT count(*) FROM things").Scan(&n); err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	a.Router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/hello", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "hello") {
		t.Fatalf("GET /api/hello = %d %s", rec.Code, rec.Body)
	}
	if rec.Header().Get("X-Request-ID") == "" || rec.Header().Get("X-Product") != "test" {
		t.Errorf("middlewares not applied: %v", rec.Header())
	}

	rec = httptest.NewRecorder()
	a.Router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/docs", nil))
	if rec.Code != http.StatusOK {
		t.Errorf("GET /docs = %d", rec.Code)
	}

	// 会话存储在 Postgres 中，跨请求保持。
	rec = httptest.NewRecorder()
	a.Router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/visit", nil))
	cookies := rec.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != "session" {
		t.Fatalf("cookies = %v", cookies)
	}
	req := httptest.NewRequest(http.MethodGet, "/visit", nil)
	req.AddCookie(cookies[0])
	rec = httptest.NewRecorder()
	a.Router.ServeHTTP(rec, req)
	if rec.Body.String() != "2" {
		t.Errorf("second visit = %q", rec.Body)
	}
	if err := a.DB.QueryRowContext(t.Context(), "SELECT count(*) FROM sessions").Scan(&n); err != nil || n != 1 {
		t.Errorf("sessions rows = %d, %v", n, err)
	}
}

func TestCommandMigrations(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "migrations")
	cfg := productConfig{Config: app.DefaultConfig()}
	opts := app.Options{
		Name:          "test",
		MigrationsDir: dir,
		Publish: []database.MigrationSource{{Name: "demo", FS: fstest.MapFS{
			"0001_create_demo.sql": {Data: []byte("-- +goose Up\n-- +goose Down\n")},
		}}},
	}

	// publish 与 create 不读配置文件。
	out := run(t, &cfg, opts, "migrate", "publish", "-c", "missing.yaml")
	if !strings.Contains(out, "_session_0001_create_sessions.sql") || !strings.Contains(out, "_demo_0001_create_demo.sql") {
		t.Errorf("publish output = %s", out)
	}
	if out := run(t, &cfg, opts, "migrate", "publish"); !strings.Contains(out, "nothing to publish") {
		t.Errorf("second publish output = %s", out)
	}
	if out := run(t, &cfg, opts, "migrate", "create", "create_orders"); !strings.Contains(out, "_create_orders.sql") {
		t.Errorf("create output = %s", out)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 3 {
		t.Errorf("files = %v", entries)
	}
}

func TestCommandMigrateStatus(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(cfgPath, []byte("database:\n  dsn: "+string(dbtest.DSN(t))+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := productConfig{Config: app.DefaultConfig()}
	opts := app.Options{Name: "test", Migrations: os.DirFS(productMigrations(t))}

	out := run(t, &cfg, opts, "-c", cfgPath, "migrate", "status")
	if strings.Count(out, "pending") != 2 {
		t.Errorf("status before up = %s", out)
	}
	run(t, &cfg, opts, "-c", cfgPath, "migrate", "up")
	if out := run(t, &cfg, opts, "-c", cfgPath, "migrate", "status"); strings.Count(out, "applied") != 2 {
		t.Errorf("status after up = %s", out)
	}
	if out := run(t, &cfg, opts, "-c", cfgPath, "migrate", "down"); !strings.Contains(out, "create_things") {
		t.Errorf("down output = %s", out)
	}
}

func TestCommandOpenAPI(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.yaml")
	// 数据库不可达也能输出文档。
	if err := os.WriteFile(cfgPath, []byte("database:\n  dsn: postgres://u:p@127.0.0.1:1/db\ngreeting: hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := productConfig{Config: app.DefaultConfig()}
	opts := app.Options{Name: "test", Version: "2.0.0", Setup: setup(&cfg)}

	out := run(t, &cfg, opts, "-c", cfgPath, "openapi", "--format", "json")
	if !strings.Contains(out, `"/api/hello"`) || !strings.Contains(out, `"2.0.0"`) || !strings.Contains(out, `"bearer"`) {
		t.Errorf("openapi = %s", out)
	}
	if cfg.Greeting != "hi" {
		t.Errorf("product config not loaded: %+v", cfg)
	}
}

func run(t *testing.T, cfg app.Configurer, opts app.Options, args ...string) string {
	t.Helper()
	cmd := app.Command(cfg, opts)
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs(args)
	if err := cmd.ExecuteContext(t.Context()); err != nil {
		t.Fatalf("%v: %v\n%s", args, err, out.String())
	}
	return out.String()
}
