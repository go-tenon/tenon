package httpcmd_test

import (
	"bytes"
	"context"
	"net/http"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/a-h/templ"
	"github.com/danielgtaylor/huma/v2"
	"github.com/go-chi/chi/v5"
	"github.com/spf13/cobra"

	"github.com/go-tenon/tenon/command"
	"github.com/go-tenon/tenon/command/httpcmd"
	"github.com/go-tenon/tenon/httpx/rest"
	"github.com/go-tenon/tenon/identity"
	"github.com/go-tenon/tenon/web"
)

func newRoot(cmds ...*cobra.Command) *cobra.Command {
	root := command.Root("api", "test")
	root.AddCommand(cmds...)
	return root
}

func execute(root *cobra.Command, args ...string) (stdout, stderr string, err error) {
	var out, errOut bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&errOut)
	root.SetArgs(args)
	err = root.ExecuteContext(context.Background())
	return out.String(), errOut.String(), err
}

type input struct{}
type output struct{ Body string }

// appRoutes 模拟应用的 routes 函数：JSON API、页面和原生路由都在同一个路由上。
// 命令只读取路由，任何处理器或认证被调用都会使测试失败。
func appRoutes(t *testing.T) func() chi.Routes {
	return func() chi.Routes {
		router := chi.NewRouter()
		api := rest.New(router, rest.Options{Title: "Blog API", Version: "1.0.0", Accounts: identity.Accounts{"user": nil}})
		handler := func(context.Context, *input) (*output, error) {
			t.Fatal("handler must not run")
			return nil, nil
		}
		create := huma.Operation{OperationID: "article-create", Method: http.MethodPost, Path: "/articles", Summary: "创建文章"}
		rest.RequireBearer(api, func(context.Context, string) (identity.Subject, error) {
			t.Fatal("token validator must not run")
			return identity.Subject{}, nil
		}, "articles")(&create)
		rest.Register(api, create, handler)
		list := huma.Operation{OperationID: "article-list", Method: http.MethodGet, Path: "/articles", Summary: "获取文章\t\n列表"}
		rest.Public()(&list)
		rest.Register(api, list, handler)
		router.Get("/livez", func(http.ResponseWriter, *http.Request) {
			t.Fatal("native handler must not run")
		})
		router.Handle("/static/*", http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
			t.Fatal("native handler must not run")
		}))
		page := func(http.ResponseWriter, *http.Request) error {
			t.Fatal("page handler must not run")
			return nil
		}
		site := web.New(web.Options{ErrorPage: errorPage}).Routes(router)
		site.Get("/", page)
		site.Get("/posts/{slug}", page)
		return router
	}
}

func errorPage(int, string) templ.Component { return templ.NopComponent }

// versioned 构造挂着 /v1、/v2 两个 API 的路由。
func versioned() chi.Routes {
	router := chi.NewRouter()
	for _, v := range []string{"v1", "v2"} {
		router.Route("/"+v, func(r chi.Router) {
			api := rest.New(r, rest.Options{Title: "API " + v, BasePath: "/" + v})
			op := huma.Operation{OperationID: "ping", Method: http.MethodGet, Path: "/ping"}
			rest.Public()(&op)
			rest.Register(api, op, func(context.Context, *input) (*output, error) { return nil, nil })
		})
	}
	return router
}

func TestOpenAPI(t *testing.T) {
	root := newRoot(httpcmd.OpenAPI(appRoutes(t)))
	// openapi 不读取配置文件
	out, _, err := execute(root, "openapi", "--config", filepath.Join(t.TempDir(), "missing.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"title":"Blog API"`, `"/articles"`, `"article-create"`} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %s in %s", want, out)
		}
	}
}

func TestOpenAPISpec30(t *testing.T) {
	root := newRoot(httpcmd.OpenAPI(appRoutes(t)))
	out, _, err := execute(root, "openapi", "--spec", "3.0")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"openapi":"3.0.3"`, `"article-create"`} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %s in %s", want, out)
		}
	}
}

// 路由中有多个 API 时，openapi 要求用 --base-path 选择；没有 API 时报用法错误。
func TestOpenAPIChoosesAPIByBasePath(t *testing.T) {
	out, _, err := execute(newRoot(httpcmd.OpenAPI(versioned)), "openapi", "--base-path", "/v2")
	if err != nil || !strings.Contains(out, `"title":"API v2"`) {
		t.Fatalf("err=%v out=%s", err, out)
	}
	for _, tc := range []struct {
		router func() chi.Routes
		args   []string
		want   string
	}{
		{versioned, nil, `choose one by base path: "/v1", "/v2"`},
		{versioned, []string{"--base-path", "/v3"}, `no API with base path "/v3"`},
		{func() chi.Routes { return chi.NewRouter() }, nil, "no API registered"},
	} {
		_, _, err := execute(newRoot(httpcmd.OpenAPI(tc.router)), append([]string{"openapi"}, tc.args...)...)
		if command.ExitCode(err) != command.ExitUsage || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("args=%v err=%v", tc.args, err)
		}
	}
}

func TestRoutes(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want []string
		omit []string
	}{
		{"default", nil, []string{"GET /articles article-list public 获取文章 列表", "POST /articles article-create bearer: articles 创建文章", "GET /livez - unknown -", "GET /posts/{slug} - public -", "* /static/* - unknown -"}, []string{"/docs", "/openapi", "/schemas", "GET /static"}},
		{"all", []string{"--all"}, []string{"GET /articles article-list public 获取文章 列表", "GET /docs - docs -", "GET /openapi.json - docs -", "GET /schemas/{schema} - docs -"}, nil},
		{"method", []string{"--method", "post"}, []string{"POST /articles article-create bearer: articles 创建文章", "* /static/* - unknown -"}, []string{"article-list", "/livez", "/docs"}},
		{"path", []string{"--path", "live"}, []string{"GET /livez - unknown -"}, []string{"article-create", "article-list", "/docs"}},
		{"pages", []string{"--path", "/posts"}, []string{"GET /posts/{slug} - public -"}, []string{"/livez", "article-create"}},
		{"combined", []string{"--path", "articles", "--method", "get"}, []string{"GET /articles article-list public 获取文章 列表"}, []string{"article-create", "/docs"}},
		{"empty", []string{"--path", "missing"}, nil, []string{"article-create", "article-list", "/docs", "/livez"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := newRoot(httpcmd.Routes(appRoutes(t)))
			out, stderr, err := execute(root, append([]string{"routes"}, tc.args...)...)
			if err != nil || stderr != "" {
				t.Fatalf("err=%v stderr=%s", err, stderr)
			}
			lines := strings.Split(strings.TrimSpace(out), "\n")
			for i := range lines {
				lines[i] = strings.Join(strings.Fields(lines[i]), " ")
			}
			got := strings.Join(lines, "\n")
			if lines[0] != "METHOD PATH OPERATION ID ACCESS SUMMARY" {
				t.Fatalf("missing header: %s", got)
			}
			for _, want := range tc.want {
				if !slices.Contains(lines, want) {
					t.Errorf("missing %q in %s", want, got)
				}
			}
			for _, omit := range tc.omit {
				if strings.Contains(got, omit) {
					t.Errorf("unexpected %q in %s", omit, got)
				}
			}
		})
	}
}

func TestUsageErrors(t *testing.T) {
	for _, args := range [][]string{
		{"routes", "extra"},
		{"routes", "--unknown"},
		{"routes", "--path"},
		{"openapi", "extra"},
		{"openapi", "--spec", "2.0"},
	} {
		mustNotBuild := func() chi.Routes { t.Fatal("invalid arguments built the router"); return nil }
		root := newRoot(httpcmd.OpenAPI(mustNotBuild), httpcmd.Routes(mustNotBuild))
		if _, _, err := execute(root, args...); command.ExitCode(err) != command.ExitUsage {
			t.Errorf("args=%v exit=%d err=%v", args, command.ExitCode(err), err)
		}
	}
}

func TestCommands(t *testing.T) {
	cmds := httpcmd.Commands(appRoutes(t))
	var names []string
	for _, c := range cmds {
		names = append(names, c.Name())
	}
	if strings.Join(names, ",") != "openapi,routes,client" {
		t.Fatalf("commands %v", names)
	}
}
