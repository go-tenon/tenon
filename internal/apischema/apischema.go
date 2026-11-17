// Package apischema 按 Huma 的规则为 Go 类型生成 JSON Schema，并按它校验、解码 JSON 参数。
// MCP 工具（mcpx）和 RPC 端点（natsrpc）用它处理参数，规则与 REST 接口的请求体相同：
// 同一个类型可以同时用作三者的输入。
package apischema

import (
	"encoding/json"
	"reflect"
	"strings"

	"github.com/cockroachdb/errors"
	"github.com/danielgtaylor/huma/v2"
)

// defsPrefix 是嵌套类型的引用前缀：每个 schema 自带用到的类型，放在 $defs 中。
const defsPrefix = "#/$defs/"

// ErrInvalidJSON 表示参数不是合法的 JSON。
var ErrInvalidJSON = errors.New("not valid JSON")

// Schema 是一个类型按 Huma 规则生成的 JSON Schema，以及校验它所需的 registry。
type Schema struct {
	registry huma.Registry
	root     *huma.Schema
	mode     huma.ValidateMode
	Type     string         // 顶层的 type，object 表示结构体或 map；类型没有 schema 时为空。
	JSON     map[string]any // 发给客户端的 schema，带 $defs。
}

// New 为 t 生成 schema，mode 是校验的方向：输入用 huma.ModeWriteToServer，输出用 huma.ModeReadFromServer。
// 每个类型使用自己的 registry，$defs 中只有它用到的类型。
func New(t reflect.Type, mode huma.ValidateMode) *Schema {
	registry := huma.NewMapRegistry(defsPrefix, huma.DefaultSchemaNamer)
	root := registry.Schema(t, false, "")
	s := &Schema{registry: registry, root: root, mode: mode}
	if root == nil {
		return s
	}
	data, err := json.Marshal(root)
	if err != nil {
		panic(errors.Wrapf(err, "apischema: encode schema of %s", t))
	}
	if err := json.Unmarshal(data, &s.JSON); err != nil {
		panic(errors.Wrapf(err, "apischema: encode schema of %s", t))
	}
	s.Type, _ = s.JSON["type"].(string)
	if defs := registry.Map(); len(defs) > 0 {
		s.JSON["$defs"] = defs
	}
	return s
}

// Validate 按 schema 校验 raw，通过后把它解码到 dst；raw 为空或为 null 时视为 {}。没有提供的字段先填上 schema 的默认值。
//
// raw 不是合法的 JSON 时返回包装了 ErrInvalidJSON 的错误；不符合 schema 时返回每个字段的错误，
// 错误的位置以 prefix 开头，例如 prefix 为 body 时是 body.items[0].name，为空时是 items[0].name。
func (s *Schema) Validate(raw []byte, dst any, prefix string) ([]*huma.ErrorDetail, error) {
	if len(raw) == 0 || string(raw) == "null" {
		raw = []byte("{}")
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil, errors.Mark(errors.Wrap(err, ErrInvalidJSON.Error()), ErrInvalidJSON)
	}
	if obj, ok := v.(map[string]any); ok {
		s.fillDefaults(s.root, obj)
		var err error
		if raw, err = json.Marshal(obj); err != nil {
			return nil, err
		}
	}
	res := &huma.ValidateResult{}
	huma.Validate(s.registry, s.root, huma.NewPathBuffer([]byte(prefix), 0), s.mode, v, res)
	if len(res.Errors) > 0 {
		details := make([]*huma.ErrorDetail, len(res.Errors))
		for i, err := range res.Errors {
			var d *huma.ErrorDetail
			if !errors.As(err, &d) {
				d = &huma.ErrorDetail{Message: err.Error(), Location: prefix}
			}
			if prefix == "" {
				d.Location = strings.TrimPrefix(d.Location, ".")
			}
			details[i] = d
		}
		return details, nil
	}
	return nil, json.Unmarshal(raw, dst)
}

// fillDefaults 为 obj 中没有提供的字段填上 schema 的 default，嵌套的对象同样处理，与 Huma 处理请求体的方式一致：
// 例如嵌入 page.OffsetParams 的参数，没有传 per_page 时按 20 处理。
func (s *Schema) fillDefaults(sc *huma.Schema, obj map[string]any) {
	if sc != nil && sc.Ref != "" {
		sc = s.registry.SchemaFromRef(sc.Ref)
	}
	if sc == nil {
		return
	}
	for name, prop := range sc.Properties {
		value, ok := obj[name]
		if !ok && prop.Default != nil {
			obj[name] = prop.Default
			continue
		}
		if nested, isObj := value.(map[string]any); isObj {
			s.fillDefaults(prop, nested)
		}
	}
}
