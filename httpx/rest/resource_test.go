package rest_test

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/danielgtaylor/huma/v2"
	"github.com/go-chi/chi/v5"

	"github.com/go-tenon/tenon/httpx/rest"
	"github.com/go-tenon/tenon/page"
)

type resArticle struct {
	ID        string                `json:"id"`
	Title     string                `json:"title"`
	Status    articleStatus         `json:"status"`
	Author    struct{ Name string } `json:"author"`
	Tags      []string              `json:"tags"`
	UpdatedAt string                `json:"updated_at"`
}

type resChanges struct {
	Title      string        `json:"title" minLength:"1"`
	Content    string        `json:"content"`
	Status     articleStatus `json:"status"`
	CategoryID string        `json:"category_id,omitempty"`
	TagIDs     []string      `json:"tag_ids,omitempty"`
	Priority   int           `json:"priority,omitempty"`
}

type resListIn struct {
	page.OffsetParams
	Q          string        `query:"q"`
	Status     articleStatus `query:"status"`
	CategoryID string        `query:"category_id"`
	Sort       string        `query:"sort" enum:"title,-title,-updated_at,updated_at"`
}

type resListOut struct{ Body page.Offset[resArticle] }
type resOneOut struct{ Body resArticle }
type resIDIn struct {
	ID string `path:"id"`
}
type resCreateIn struct{ Body resChanges }
type resUpdateIn struct {
	ID   string `path:"id"`
	Body resChanges
}
type resRenameIn struct {
	ID   string `path:"id"`
	Body struct {
		Title string `json:"title" minLength:"1"`
	}
}
type resSlugIn struct {
	Slug string `path:"slug"`
}

// resourceAPI 登记文章的接口：列表、详情、创建、修改、删除、发布、只改标题，以及一个路径参数不同的接口。
func resourceAPI() *rest.API {
	api := rest.New(chi.NewRouter(), rest.Options{})
	reg := func(id, method, path string) huma.Operation {
		op := huma.Operation{OperationID: id, Method: method, Path: path}
		rest.Public()(&op)
		return op
	}
	ok := func(context.Context, *resIDIn) (*resOneOut, error) { return &resOneOut{}, nil }
	rest.Register(api, reg("article-list", http.MethodGet, "/articles"), func(context.Context, *resListIn) (*resListOut, error) {
		return &resListOut{}, nil
	})
	rest.Register(api, reg("article-get", http.MethodGet, "/articles/{id}"), ok)
	rest.Register(api, reg("article-create", http.MethodPost, "/articles"), func(context.Context, *resCreateIn) (*resOneOut, error) {
		return &resOneOut{}, nil
	})
	rest.Register(api, reg("article-update", http.MethodPut, "/articles/{id}"), func(context.Context, *resUpdateIn) (*resOneOut, error) {
		return &resOneOut{}, nil
	})
	rest.Register(api, reg("article-delete", http.MethodDelete, "/articles/{id}"), ok)
	rest.Register(api, reg("article-publish", http.MethodPost, "/articles/{id}/publish"), ok)
	rest.Register(api, reg("article-rename", http.MethodPut, "/articles/{id}/title"), func(context.Context, *resRenameIn) (*resOneOut, error) {
		return &resOneOut{}, nil
	})
	rest.Register(api, reg("article-by-slug", http.MethodGet, "/articles/by-slug/{slug}"), func(context.Context, *resSlugIn) (*resOneOut, error) {
		return &resOneOut{}, nil
	})
	return api
}

func TestResource(t *testing.T) {
	api := resourceAPI()
	api.Resource(rest.Resource{
		Name: "article", List: "article-list", Get: "article-get", Create: "article-create", Update: "article-update", Delete: "article-delete",
		Filters: []string{"status", "category_id"}, Sort: []string{"updated_at"},
		Actions:    []rest.Action{{Name: "publish", Operation: "article-publish", Confirm: true, When: map[string][]string{"status": {"draft"}}}},
		UpdateWhen: map[string][]string{"status": {"draft"}},
		DeleteWhen: map[string][]string{"status": {"draft"}},
		Refs:       map[string]string{"category_id": "category", "tag_ids": "tag"},
	})
	got := api.Resources()
	if len(got) != 1 {
		t.Fatalf("resources %+v", got)
	}
	r := got[0]
	// 默认的列：列表项中除对象和数组外的字段，按结构体的顺序；默认的表单字段：请求体的字段，按结构体的顺序。
	if !slices.Equal(r.Columns, []string{"id", "title", "status", "updated_at"}) {
		t.Errorf("columns %q", r.Columns)
	}
	if !slices.Equal(r.CreateForm, []string{"title", "content", "status", "category_id", "tag_ids", "priority"}) ||
		!slices.Equal(r.UpdateForm, []string{"title", "content", "status", "category_id", "tag_ids", "priority"}) {
		t.Errorf("forms %q %q", r.CreateForm, r.UpdateForm)
	}
	if r.Key != "id" {
		t.Errorf("key %q", r.Key)
	}

	// 写入 OpenAPI 文档。
	doc, err := api.OpenAPIJSON()
	if err != nil {
		t.Fatal(err)
	}
	var parsed struct {
		Resources []rest.Resource `json:"x-tenon-resources"`
	}
	if err := json.Unmarshal(doc, &parsed); err != nil || len(parsed.Resources) != 1 || parsed.Resources[0].Key != "id" ||
		!parsed.Resources[0].Actions[0].Confirm || !slices.Equal(parsed.Resources[0].Actions[0].When["status"], []string{"draft"}) ||
		!slices.Equal(parsed.Resources[0].UpdateWhen["status"], []string{"draft"}) || !slices.Equal(parsed.Resources[0].DeleteWhen["status"], []string{"draft"}) ||
		parsed.Resources[0].Refs["category_id"] != "category" || parsed.Resources[0].Refs["tag_ids"] != "tag" {
		t.Fatalf("x-tenon-resources %+v %v", parsed.Resources, err)
	}
}

func TestResourceForms(t *testing.T) {
	t.Parallel()
	api := resourceAPI()
	// 新建和修改的请求体不同时，两个表单各取自己的字段。
	api.Resource(rest.Resource{Name: "article", List: "article-list", Create: "article-create", Update: "article-rename"})
	r := api.Resources()[0]
	if !slices.Equal(r.CreateForm, []string{"title", "content", "status", "category_id", "tag_ids", "priority"}) || !slices.Equal(r.UpdateForm, []string{"title"}) {
		t.Errorf("forms %q %q", r.CreateForm, r.UpdateForm)
	}
}

func TestResourceRejects(t *testing.T) {
	base := rest.Resource{Name: "article", List: "article-list"}
	for name, tc := range map[string]struct {
		change func(*rest.Resource)
		want   string
	}{
		"name":            {func(r *rest.Resource) { r.Name = "Article" }, "kebab-case"},
		"no list":         {func(r *rest.Resource) { r.List = "" }, "no List operation"},
		"unknown op":      {func(r *rest.Resource) { r.Get = "article-show" }, "article-show is not registered"},
		"list method":     {func(r *rest.Resource) { r.List = "article-create" }, "must be GET"},
		"list shape":      {func(r *rest.Resource) { r.List = "article-get" }, "must not have path parameters"},
		"column":          {func(r *rest.Resource) { r.Columns = []string{"title", "nope"} }, "column nope not found"},
		"filter":          {func(r *rest.Resource) { r.Filters = []string{"author"} }, `filter "author" is not a query parameter`},
		"sort column":     {func(r *rest.Resource) { r.Sort = []string{"nope"} }, "sort column nope not found"},
		"sort value":      {func(r *rest.Resource) { r.Sort = []string{"status"} }, "sort column status is not an allowed value"},
		"form field":      {func(r *rest.Resource) { r.Create = "article-create"; r.CreateForm = []string{"slug"} }, "create form field slug not found"},
		"form without op": {func(r *rest.Resource) { r.UpdateForm = []string{"title"} }, "update form needs the update operation"},
		"create no body":  {func(r *rest.Resource) { r.Create = "article-publish" }, "must not have path parameters"},
		"key mismatch": {func(r *rest.Resource) {
			r.Get = "article-get"
			r.Actions = []rest.Action{{Name: "open", Operation: "article-by-slug"}}
		}, "uses path parameter slug, others use id"},
		"key not a field": {func(r *rest.Resource) { r.Get = "article-by-slug" }, "row key slug"},
		"action op":       {func(r *rest.Resource) { r.Actions = []rest.Action{{Name: "publish"}} }, "has no Operation"},
		"action key":      {func(r *rest.Resource) { r.Actions = []rest.Action{{Name: "copy", Operation: "article-create"}} }, "exactly one path parameter"},
		"when field": {func(r *rest.Resource) {
			r.Actions = []rest.Action{{Name: "publish", Operation: "article-publish", When: map[string][]string{"state": {"draft"}}}}
		}, `when field "state" is not a field`},
		"when empty": {func(r *rest.Resource) {
			r.Actions = []rest.Action{{Name: "publish", Operation: "article-publish", When: map[string][]string{"status": nil}}}
		}, "lists no values"},
		"when enum": {func(r *rest.Resource) {
			r.Actions = []rest.Action{{Name: "publish", Operation: "article-publish", When: map[string][]string{"status": {"archived"}}}}
		}, `when status="archived" is not one of`},
		"update_when without update": {func(r *rest.Resource) { r.UpdateWhen = map[string][]string{"status": {"draft"}} }, "update_when is set but the resource has no update operation"},
		"delete_when field": {func(r *rest.Resource) {
			r.Delete = "article-delete"
			r.DeleteWhen = map[string][]string{"state": {"draft"}}
		}, `delete_when field "state" is not a field`},
		"update_when enum": {func(r *rest.Resource) {
			r.Update = "article-update"
			r.UpdateWhen = map[string][]string{"status": {"archived"}}
		}, `update_when status="archived" is not one of`},
		"ref target name": {func(r *rest.Resource) {
			r.Create = "article-create"
			r.Refs = map[string]string{"category_id": "Category"}
		}, `target resource "Category" must be kebab-case`},
		"ref unknown field": {func(r *rest.Resource) {
			r.Refs = map[string]string{"category_id": "category"}
		}, `ref field "category_id" is not in filters, create_form, or update_form`},
		"ref non-string": {func(r *rest.Resource) {
			r.Create = "article-create"
			r.Refs = map[string]string{"priority": "category"}
		}, `ref field "priority" must be string or array of strings`},
	} {
		t.Run(name, func(t *testing.T) {
			r := base
			tc.change(&r)
			defer func() {
				msg, _ := recover().(string)
				if !strings.Contains(msg, tc.want) {
					t.Errorf("panic %q, want %q", msg, tc.want)
				}
			}()
			resourceAPI().Resource(r)
		})
	}

	// 同名的资源只能声明一次。
	api := resourceAPI()
	api.Resource(base)
	defer func() {
		if msg, _ := recover().(string); !strings.Contains(msg, "declared twice") {
			t.Errorf("panic %q", msg)
		}
	}()
	api.Resource(base)
}
