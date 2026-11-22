package contract

import (
	"fmt"
	"slices"
	"strings"

	"github.com/danielgtaylor/huma/v2"

	"github.com/go-tenon/tenon/enum"
)

// refPrefixes 是框架中 registry 的引用前缀：OpenAPI 和 AsyncAPI 文档的 components.schemas，以及 NewSchema 的 $defs。
var refPrefixes = []string{"#/components/schemas/", defsPrefix}

// EnumSchema 把 e 登记为 r 中的命名类型并返回对它的引用，在枚举类型的 Schema 方法（huma.SchemaProvider）中调用：
//
//	var Statuses = enum.New("article.status",
//		enum.Item(Draft, "草稿"),
//		enum.Item(Published, "已发布"),
//	)
//
//	func (Status) Schema(r huma.Registry) *huma.Schema { return contract.EnumSchema(r, Statuses) }
//
// 命名类型的名称是 e.Name()（例如 ArticleStatus），带有 enum（全部取值）、x-tenon-enum（显示文字的消息前缀）
// 以及可选的 x-tenon-enum-labels（源语言的默认显示文字）。
// 它出现在 OpenAPI 和 AsyncAPI 文档的 components.schemas 中，MCP 工具和 RPC 端点的参数 schema 则把它放在 $defs 中；
// 取值照常由 Huma 校验，生成的客户端据此得到取值和各语言的显示文字。
//
// 命名类型的名称由前缀得出，所以前缀相同、取值也相同的枚举是同一个类型，可以重复登记（例如两个 Go 类型共用一份枚举）。
// 同名的命名类型是取值不同的枚举（例如“全部角色”和“可以分配的角色”都用了前缀 role）、或者是其他类型时 panic：
// 否则后登记的枚举会悄悄用先登记的取值校验，接受它本不该接受的值。取值不同的枚举要用不同的前缀。
func EnumSchema(r huma.Registry, e enum.Definition) *huma.Schema {
	name, prefix := e.Name(), e.Prefix()
	values := e.Strings()
	if s, ok := r.Map()[name]; ok {
		if s.Extensions["x-tenon-enum"] != prefix {
			panic(fmt.Sprintf("contract: schema %s is already registered for something other than enum %s", name, prefix))
		}
		if !slices.Equal(enumValues(s), values) {
			panic(fmt.Sprintf("contract: enum %s is already registered with values %v, not %v; enums with different values need different prefixes",
				prefix, enumValues(s), values))
		}
		return &huma.Schema{Ref: ref(r, name)}
	}
	ext := map[string]any{"x-tenon-enum": prefix}
	if labels := e.DefaultLabels(); len(labels) > 0 {
		ext["x-tenon-enum-labels"] = labels
	}
	s := &huma.Schema{Type: huma.TypeString, Enum: make([]any, len(values)), Extensions: ext}
	for i, v := range slices.Clone(values) {
		s.Enum[i] = v
	}
	s.PrecomputeMessages()
	r.Map()[name] = s
	return &huma.Schema{Ref: ref(r, name)}
}

// ref 返回 r 中命名类型 name 的引用。Huma 的 registry 不公开引用前缀，这里按 refPrefixes 逐个用 SchemaFromRef 试出来；
// 前缀不在其中时 panic。
func ref(r huma.Registry, name string) string {
	for _, p := range refPrefixes {
		if r.SchemaFromRef(p+name) != nil {
			return p + name
		}
	}
	panic("contract: registry uses a schema reference prefix other than " + strings.Join(refPrefixes, " or "))
}

// enumValues 返回已登记的枚举 schema s 的取值。
func enumValues(s *huma.Schema) []string {
	out := make([]string, len(s.Enum))
	for i, v := range s.Enum {
		out[i] = fmt.Sprint(v)
	}
	return out
}
