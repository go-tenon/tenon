package contract

import (
	"fmt"
	"slices"

	"github.com/danielgtaylor/huma/v2"

	"github.com/go-tenon/tenon/enum"
)

// schemaRefPrefix 是 OpenAPI 文档中命名类型的引用前缀。
const schemaRefPrefix = "#/components/schemas/"

// EnumSchema 把 e 登记为 OpenAPI 的命名类型并返回对它的引用，在枚举类型的 Schema 方法（huma.SchemaProvider）中调用：
//
//	var Statuses = enum.New("article.status", Draft, Published)
//
//	func (Status) Schema(r huma.Registry) *huma.Schema { return contract.EnumSchema(r, Statuses) }
//
// 命名类型的名称是 e.Name()（例如 ArticleStatus），带有 enum（全部取值）和 x-tenon-enum（显示文字的消息前缀），
// 请求中的取值照常由 Huma 校验，生成的客户端据此得到取值和各语言的显示文字。
// 同名的命名类型已经是另一个枚举或其他类型时 panic。
func EnumSchema(r huma.Registry, e enum.Definition) *huma.Schema {
	name, prefix := e.Name(), e.Prefix()
	if s, ok := r.Map()[name]; ok {
		if s.Extensions["x-tenon-enum"] != prefix {
			panic(fmt.Sprintf("contract: schema %s is already registered for something other than enum %s", name, prefix))
		}
		return &huma.Schema{Ref: schemaRefPrefix + name}
	}
	values := e.Strings()
	s := &huma.Schema{Type: huma.TypeString, Enum: make([]any, len(values)), Extensions: map[string]any{"x-tenon-enum": prefix}}
	for i, v := range slices.Clone(values) {
		s.Enum[i] = v
	}
	s.PrecomputeMessages()
	r.Map()[name] = s
	return &huma.Schema{Ref: schemaRefPrefix + name}
}
