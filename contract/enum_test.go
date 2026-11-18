package contract_test

import (
	"testing"

	"github.com/danielgtaylor/huma/v2"

	"github.com/go-tenon/tenon/contract"
	"github.com/go-tenon/tenon/enum"
)

type articleStatus string

var articleStatuses = enum.New("article.status", articleStatus("draft"), articleStatus("published"))

func TestEnumSchemaConflict(t *testing.T) {
	t.Parallel()
	r := huma.NewMapRegistry("#/components/schemas/", huma.DefaultSchemaNamer)
	contract.EnumSchema(r, articleStatuses)
	contract.EnumSchema(r, articleStatuses) // 同一个枚举可以重复登记
	other := enum.New("article.status", articleStatus("open"))
	func() {
		defer func() {
			if recover() != nil {
				t.Error("same prefix panicked")
			}
		}()
		contract.EnumSchema(r, other)
	}()
	r.Map()["OrderState"] = &huma.Schema{Type: huma.TypeObject}
	defer func() {
		if recover() == nil {
			t.Error("conflicting schema did not panic")
		}
	}()
	contract.EnumSchema(r, enum.New("order.state", articleStatus("paid")))
}
