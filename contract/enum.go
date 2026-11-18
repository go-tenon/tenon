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
//	var Statuses = enum.New("article.status", Draft, Published)
//
//	func (Status) Schema(r huma.Registry) *huma.Schema { return contract.EnumSchema(r, Statuses) }
//
// 命名类型的名称是 e.Name()（例如 ArticleStatus），带有 enum（全部取值）和 x-tenon-enum（显示文字的消息前缀）。
// 它出现在 OpenAPI 和 AsyncAPI 文档的 components.schemas 中，MCP 工具和 RPC 端点的参数 schema 则把它放在 $defs 中；
// 取值照常由 Huma 校验，生成的客户端据此得到取值和各语言的显示文字。
// 同名的命名类型已经是另一个枚举或其他类型时 panic。
func EnumSchema(r huma.Registry, e enum.Definition) *huma.Schema {
	name, prefix := e.Name(), e.Prefix()
	if s, ok := r.Map()[name]; ok {
		if s.Extensions["x-tenon-enum"] != prefix {
			panic(fmt.Sprintf("contract: schema %s is already registered for something other than enum %s", name, prefix))
		}
		return &huma.Schema{Ref: ref(r, name)}
	}
	values := e.Strings()
	s := &huma.Schema{Type: huma.TypeString, Enum: make([]any, len(values)), Extensions: map[string]any{"x-tenon-enum": prefix}}
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
