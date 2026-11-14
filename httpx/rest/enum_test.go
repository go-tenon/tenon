package rest_test

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/danielgtaylor/huma/v2"

	"github.com/go-tenon/tenon/enum"
	"github.com/go-tenon/tenon/httpx/rest"
	"github.com/go-tenon/tenon/i18n"
)

type articleStatus string

var articleStatuses = enum.New("article.status", articleStatus("draft"), articleStatus("published"))

func (articleStatus) Schema(r huma.Registry) *huma.Schema { return rest.EnumSchema(r, articleStatuses) }

type statusIn struct {
	Status articleStatus `query:"status"`
	Body   struct {
		Status articleStatus `json:"status"`
	}
}

func TestEnumSchema(t *testing.T) {
	bundle, err := i18n.New("en")
	if err != nil {
		t.Fatal(err)
	}
	api := withLanguages(rest.Options{}, bundle)
	op := huma.Operation{OperationID: "article-create", Method: http.MethodPost, Path: "/articles"}
	rest.Public()(&op)
	var got statusIn
	rest.Register(api, op, func(_ context.Context, in *statusIn) (*pingOut, error) {
		got = *in
		return &pingOut{}, nil
	})

	// 命名类型带全部取值和消息前缀，字段引用它。
	doc, err := api.OpenAPIJSON()
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`"ArticleStatus":{"enum":["draft","published"],"type":"string","x-tenon-enum":"article.status"}`,
		`"status":{"$ref":"#/components/schemas/ArticleStatus"}`,
	} {
		if !strings.Contains(string(doc), want) {
			t.Errorf("openapi.json missing %s:\n%s", want, doc)
		}
	}

	if status, p := post(api, "?status=published", `{"status": "draft"}`); status >= 300 || got.Status != "published" || got.Body.Status != "draft" {
		t.Fatalf("valid: %d %+v %+v", status, p, got)
	}
	// 请求体和查询参数中的其他值都被拒绝。
	for query, body := range map[string]string{"?status=archived": `{"status": "draft"}`, "": `{"status": "archived"}`} {
		status, p := post(api, query, body)
		if status != http.StatusUnprocessableEntity || len(p.Errors) != 1 || p.Errors[0].Code != "validation.one_of" {
			t.Errorf("%s %s: %d %+v", query, body, status, p)
		}
	}
}

func TestEnumSchemaConflict(t *testing.T) {
	r := huma.NewMapRegistry("#/components/schemas/", huma.DefaultSchemaNamer)
	rest.EnumSchema(r, articleStatuses)
	rest.EnumSchema(r, articleStatuses) // 同一个枚举可以重复登记
	other := enum.New("article.status", articleStatus("open"))
	func() {
		defer func() {
			if recover() != nil {
				t.Error("same prefix panicked")
			}
		}()
		rest.EnumSchema(r, other)
	}()
	r.Map()["OrderState"] = &huma.Schema{Type: huma.TypeObject}
	defer func() {
		if recover() == nil {
			t.Error("conflicting schema did not panic")
		}
	}()
	rest.EnumSchema(r, enum.New("order.state", articleStatus("paid")))
}
