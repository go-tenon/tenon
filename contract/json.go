package contract

import (
	jsonv1 "encoding/json"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"io"

	"github.com/danielgtaylor/huma/v2"
)

// 接口文档中的数组不可为空：Go 中没有赋值的切片由 MarshalJSON 编码为 []，生成的客户端类型不必再带 | null。
// 请求中写成 null 的数组不能通过校验。字段上的 nullable:"true" 标签照样可以把某个数组声明为可为空。
func init() { huma.DefaultArrayNullable = false }

// jsonOptions 是 v1（encoding/json）的全部语义，只把 nil 的切片编码为 []、nil 的 map 编码为 {}。
var jsonOptions = json.JoinOptions(jsonv1.DefaultOptionsV1(), json.FormatNilSliceAsNull(false), json.FormatNilMapAsNull(false))

// MarshalJSON 按接口文档的约定把 v 编码为 JSON：nil 的切片编码为 []、nil 的 map 编码为 {}，其余与 encoding/json 的
// json.Marshal 相同（包括 omitempty 和 MarshalJSON 方法）。文档中的数组和对象不可为空，客户端不用处理 null。
// HTTP 响应、SSE 事件、RPC 的响应、MCP 工具的结果和 Webhook 的载荷都用它编码。
func MarshalJSON(v any) ([]byte, error) {
	return json.Marshal(v, jsonOptions)
}

// EncodeJSON 按 MarshalJSON 的规则把 v 写到 w，末尾加一个换行，不转义 <、>、&，与 Huma 默认的 JSON 格式相同。
// 它是 rest 的 API 编码响应体的方式。
func EncodeJSON(w io.Writer, v any) error {
	if err := json.MarshalWrite(w, v, jsonOptions, jsontext.EscapeForHTML(false)); err != nil {
		return err
	}
	_, err := w.Write([]byte("\n"))
	return err
}
