package contract_test

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/danielgtaylor/huma/v2"

	"github.com/go-tenon/tenon/contract"
	"github.com/go-tenon/tenon/enum"
)

type articleStatus string

var articleStatuses = enum.New("article.status", articleStatus("draft"), articleStatus("published"))

func (articleStatus) Schema(r huma.Registry) *huma.Schema {
	return contract.EnumSchema(r, articleStatuses)
}

func TestEnumSchemaConflict(t *testing.T) {
	t.Parallel()
	r := huma.NewMapRegistry("#/components/schemas/", huma.DefaultSchemaNamer)
	contract.EnumSchema(r, articleStatuses)
	contract.EnumSchema(r, articleStatuses) // 同一个枚举可以重复登记
	// 前缀和取值都相同的另一份声明（例如两个 Go 类型共用一份枚举）是同一个类型
	contract.EnumSchema(r, enum.New("article.status", articleStatus("draft"), articleStatus("published")))
	// 前缀相同、取值不同：后登记的会悄悄用先登记的取值校验，所以 panic
	func() {
		defer func() {
			if msg, _ := recover().(string); !strings.Contains(msg, "need different prefixes") {
				t.Errorf("same prefix with different values: %v", msg)
			}
		}()
		contract.EnumSchema(r, enum.New("article.status", articleStatus("draft")))
	}()
	r.Map()["OrderState"] = &huma.Schema{Type: huma.TypeObject}
	defer func() {
		if recover() == nil {
			t.Error("conflicting schema did not panic")
		}
	}()
	contract.EnumSchema(r, enum.New("order.state", articleStatus("paid")))
}

type statusArgs struct {
	Status articleStatus `json:"status"`
}

// MCP 工具和 RPC 端点的参数 schema 把枚举放在 $defs 中，引用指向它，取值照常校验。
func TestEnumSchemaInStandaloneSchema(t *testing.T) {
	t.Parallel()
	s := contract.NewSchema(reflect.TypeFor[statusArgs](), huma.ModeWriteToServer)
	data, err := json.Marshal(s.JSON)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`"status":{"$ref":"#/$defs/ArticleStatus"}`,
		`"ArticleStatus":{"enum":["draft","published"],"type":"string","x-tenon-enum":"article.status"}`,
	} {
		if !strings.Contains(string(data), want) {
			t.Errorf("schema missing %s:\n%s", want, data)
		}
	}
	var args statusArgs
	if details, err := s.Validate([]byte(`{"status":"published"}`), &args, ""); err != nil || details != nil || args.Status != "published" {
		t.Fatalf("valid: %v %v %+v", details, err, args)
	}
	details, err := s.Validate([]byte(`{"status":"archived"}`), &args, "")
	if err != nil || len(details) != 1 {
		t.Fatalf("invalid: %v %v", details, err)
	}
	if fe := contract.Describe(context.Background(), details[0]); fe.Location != "status" || fe.Code != "validation.one_of" {
		t.Fatalf("invalid: %+v", fe)
	}
}
