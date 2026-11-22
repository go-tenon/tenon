package natsrpc_test

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/danielgtaylor/huma/v2"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/go-tenon/tenon/contract"
	"github.com/go-tenon/tenon/enum"
	"github.com/go-tenon/tenon/httpx/rest"
	"github.com/go-tenon/tenon/identity"
	"github.com/go-tenon/tenon/natsrpc"
	"github.com/go-tenon/tenon/natsrpc/rpccmd"
)

type articleStatus string

var articleStatuses = enum.New("article.status", enum.Item(articleStatus("draft")), enum.Item(articleStatus("published")))

func (articleStatus) Schema(r huma.Registry) *huma.Schema {
	return contract.EnumSchema(r, articleStatuses)
}

// ArticleDTO 同时是 HTTP 接口和 RPC 端点的返回值。
type ArticleDTO struct {
	ID     uuid.UUID     `json:"id" format:"uuid"`
	Title  string        `json:"title" doc:"标题"`
	Status articleStatus `json:"status"`
	Author Author        `json:"author"`
	Source *slog.Source  `json:"source,omitempty"` // 框架以外的类型，名称带包名：SlogSource。
}

type Author struct {
	Name string `json:"name"`
}

type GetArticle struct {
	ID uuid.UUID `json:"id" format:"uuid" doc:"文章 ID"`
}

func docServer() *natsrpc.Server {
	srv := natsrpc.New(natsrpc.Options{
		Prefix: "blog.v1", Version: "1.4.0", Description: "博客",
		Validate: tokens, Accounts: identity.Accounts{"editor": nil},
		Permissions: []identity.Permission{{Name: "articles:write"}, {Name: "articles:read", Description: "查看文章"}},
	})
	natsrpc.Register(srv, natsrpc.Endpoint{Name: "articles.get", Description: "按 ID 获取文章", Permissions: []string{"articles:read"}, Idempotent: true},
		func(context.Context, GetArticle) (*ArticleDTO, error) { return nil, nil })
	natsrpc.Register(srv, natsrpc.Endpoint{Name: "health.ping", Public: true},
		func(context.Context, struct{}) (struct{}, error) { return struct{}{}, nil })
	return srv
}

func asyncAPI(t *testing.T) map[string]any {
	t.Helper()
	data, err := docServer().AsyncAPI()
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	return doc
}

// get 按 / 分隔的路径读取 doc 中的值，路径的一段中可以有 .（例如 articles_get.request）。
func get(t *testing.T, doc any, path ...string) any {
	t.Helper()
	v := doc
	for _, p := range path {
		m, ok := v.(map[string]any)
		if !ok {
			t.Fatalf("%v: %s is not an object", path, p)
		}
		if v, ok = m[p]; !ok {
			t.Fatalf("%v: missing %s", path, p)
		}
	}
	return v
}

func TestAsyncAPI(t *testing.T) {
	t.Parallel()
	doc := asyncAPI(t)
	if doc["asyncapi"] != "3.0.0" || get(t, doc, "info", "title") != "blog.v1" || get(t, doc, "info", "version") != "1.4.0" {
		t.Fatalf("header %v %v", doc["asyncapi"], doc["info"])
	}
	// 权限目录按名称排序。
	perms, _ := json.Marshal(doc["x-tenon-permissions"])
	if string(perms) != `[{"description":"查看文章","name":"articles:read"},{"name":"articles:write"}]` {
		t.Fatalf("permissions %s", perms)
	}

	if get(t, doc, "channels", "articles_get", "address") != "blog.v1.articles.get" {
		t.Fatal("channel address")
	}
	op := get(t, doc, "operations", "articles_get").(map[string]any)
	if op["action"] != "receive" || op["description"] != "按 ID 获取文章" || op["x-tenon-idempotent"] != true || op["x-tenon-public"] != false ||
		get(t, op, "bindings", "nats", "queue") != "q" {
		t.Fatalf("operation %v", op)
	}
	if perms, _ := json.Marshal(op["x-required-permissions"]); string(perms) != `["articles:read"]` {
		t.Fatalf("required %s", perms)
	}
	if get(t, doc, "operations", "health_ping", "x-tenon-public") != true {
		t.Fatal("public endpoint")
	}

	// 参数和返回值引用 components.schemas 中的命名类型，嵌套的类型和枚举也在其中。
	request := get(t, doc, "components", "messages", "articles_get.request", "payload").(map[string]any)
	response := get(t, doc, "components", "messages", "articles_get.response", "payload").(map[string]any)
	if request["$ref"] != "#/components/schemas/GetArticle" || response["$ref"] != "#/components/schemas/ArticleDTO" {
		t.Fatalf("payloads %v %v", request, response)
	}
	for _, name := range []string{"GetArticle", "ArticleDTO", "Author", "ArticleStatus", "TenonRPCProblem"} {
		get(t, doc, "components", "schemas", name)
	}
	// 没有返回值的端点，响应没有 payload。
	if _, ok := get(t, doc, "components", "messages", "health_ping.response").(map[string]any)["payload"]; ok {
		t.Fatal("empty response has a payload")
	}

	// 文档中的每个 $ref 都指向存在的对象。
	data, _ := json.Marshal(doc)
	for _, ref := range refs(doc) {
		path := strings.Split(strings.TrimPrefix(ref, "#/"), "/")
		get(t, doc, path...)
	}
	if !bytes.Contains(data, []byte(`"Nats-Service-Error-Code"`)) {
		t.Fatal("error headers missing")
	}
}

func refs(v any) []string {
	var out []string
	switch v := v.(type) {
	case map[string]any:
		for k, x := range v {
			if s, ok := x.(string); ok && k == "$ref" {
				out = append(out, s)
			}
			out = append(out, refs(x)...)
		}
	case []any:
		for _, x := range v {
			out = append(out, refs(x)...)
		}
	}
	return out
}

// 同一个 Go 类型在 OpenAPI 和 AsyncAPI 文档中的 schema 相同。
func TestAsyncAPIMatchesOpenAPI(t *testing.T) {
	t.Parallel()
	api := rest.New(chi.NewRouter(), rest.Options{Title: "blog", Version: "1.0.0"})
	op := huma.Operation{OperationID: "article-get", Method: http.MethodGet, Path: "/articles/{id}"}
	rest.Public()(&op)
	rest.Register(api, op, func(context.Context, *struct {
		ID uuid.UUID `path:"id"`
	}) (*struct{ Body ArticleDTO }, error) {
		return nil, nil
	})
	data, err := api.OpenAPIJSON()
	if err != nil {
		t.Fatal(err)
	}
	var openapi map[string]any
	if err := json.Unmarshal(data, &openapi); err != nil {
		t.Fatal(err)
	}
	asyncapi := asyncAPI(t)
	for _, name := range []string{"ArticleDTO", "Author", "ArticleStatus", "SlogSource"} {
		a, b := get(t, openapi, "components", "schemas", name), get(t, asyncapi, "components", "schemas", name)
		if !reflect.DeepEqual(a, b) {
			t.Errorf("%s differs:\nOpenAPI  %v\nAsyncAPI %v", name, a, b)
		}
	}
}

func TestCommands(t *testing.T) {
	t.Parallel()
	run := func(args ...string) string {
		cmd := rpccmd.New(docServer)
		var out bytes.Buffer
		cmd.SetOut(&out)
		cmd.SetArgs(args)
		if err := cmd.Execute(); err != nil {
			t.Fatal(err)
		}
		return out.String()
	}
	if out := run("asyncapi"); !strings.HasPrefix(out, "{") || !strings.Contains(out, `"asyncapi": "3.0.0"`) {
		t.Fatalf("asyncapi:\n%s", out)
	}
	lines := strings.Split(strings.TrimSpace(run("endpoints")), "\n")
	if len(lines) != 3 || !strings.HasPrefix(lines[0], "SUBJECT") ||
		!strings.Contains(lines[1], "blog.v1.articles.get") || !strings.Contains(lines[1], "articles:read") || !strings.Contains(lines[1], "idempotent") ||
		!strings.Contains(lines[2], "blog.v1.health.ping") || !strings.Contains(lines[2], "(public)") {
		t.Fatalf("endpoints:\n%s", strings.Join(lines, "\n"))
	}
}
