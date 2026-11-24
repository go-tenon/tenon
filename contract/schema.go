package contract

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

// Schema 是一个类型按 Huma 规则生成的独立 JSON Schema，以及校验它所需的 registry。
// MCP 工具（mcpx）和 RPC 端点（natsrpc）用它校验、解码参数，规则与 REST 接口的请求体相同。
type Schema struct {
	registry huma.Registry
	root     *huma.Schema
	mode     huma.ValidateMode
	Type     string         // 顶层的 type，object 表示结构体或 map；类型没有 schema 时为空。
	JSON     map[string]any // 发给客户端的 schema，带 $defs。
}

// NewSchema 为 t 生成 schema，mode 是校验的方向：输入用 huma.ModeWriteToServer，输出用 huma.ModeReadFromServer。
// 每个类型使用自己的 registry，$defs 中只有它用到的类型，名称由 SchemaName 得出，与 OpenAPI 文档相同。
func NewSchema(t reflect.Type, mode huma.ValidateMode) *Schema {
	registry := huma.NewMapRegistry(defsPrefix, SchemaName)
	root := registry.Schema(t, false, "")
	s := &Schema{registry: registry, root: root, mode: mode}
	if root == nil {
		return s
	}
	data, err := json.Marshal(root)
	if err != nil {
		panic(errors.Wrapf(err, "contract: encode schema of %s", t))
	}
	if err := json.Unmarshal(data, &s.JSON); err != nil {
		panic(errors.Wrapf(err, "contract: encode schema of %s", t))
	}
	s.Type, _ = s.JSON["type"].(string)
	if defs := registry.Map(); len(defs) > 0 {
		s.JSON["$defs"] = defs
	}
	return s
}

// Validate 按 schema 校验 raw，通过后把它解码到 dst；raw 为空或为 null 时视为 {}。没有提供的字段先填上 schema 的默认值。
//
// 解码 dst 时保留原始 JSON 数值字面量（不经由 float64 往返），确保超过 2^53 的 int64/uint64 不丢失精度。
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
	filled := false
	if obj, ok := v.(map[string]any); ok {
		filled = s.fillDefaults(s.root, obj)
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
	if filled {
		merged, err := s.fillRawDefaults(s.root, raw)
		if err != nil {
			return nil, err
		}
		raw = merged
	}
	return nil, json.Unmarshal(raw, dst)
}

// fillDefaults 为 obj 中没有提供的字段填上 schema 的 default，嵌套的对象和对象数组同样处理，返回是否实际写入了默认值。
func (s *Schema) fillDefaults(sc *huma.Schema, obj map[string]any) bool {
	if sc != nil && sc.Ref != "" {
		sc = s.registry.SchemaFromRef(sc.Ref)
	}
	if sc == nil {
		return false
	}
	changed := false
	for name, prop := range sc.Properties {
		value, ok := obj[name]
		if !ok && prop.Default != nil {
			obj[name] = prop.Default
			changed = true
			continue
		}
		if nested, isObj := value.(map[string]any); isObj {
			if s.fillDefaults(prop, nested) {
				changed = true
			}
			continue
		}
		if list, isArr := value.([]any); isArr {
			resolved := prop
			if resolved != nil && resolved.Ref != "" {
				resolved = s.registry.SchemaFromRef(resolved.Ref)
			}
			if resolved != nil && resolved.Items != nil {
				for _, elem := range list {
					if nested, isObj := elem.(map[string]any); isObj && s.fillDefaults(resolved.Items, nested) {
						changed = true
					}
				}
			}
		}
	}
	return changed
}

// fillRawDefaults 在原始 JSON 字节上仅补充缺失的默认值字段，保留所有已提供字段的原始 json.RawMessage（不经过 float64 转换）。
func (s *Schema) fillRawDefaults(sc *huma.Schema, raw []byte) ([]byte, error) {
	if sc != nil && sc.Ref != "" {
		sc = s.registry.SchemaFromRef(sc.Ref)
	}
	if sc == nil {
		return raw, nil
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil || obj == nil {
		return raw, nil
	}
	changed := false
	for name, prop := range sc.Properties {
		elem, ok := obj[name]
		if !ok && prop.Default != nil {
			encoded, err := json.Marshal(prop.Default)
			if err != nil {
				return nil, err
			}
			obj[name] = encoded
			changed = true
			continue
		}
		if !ok {
			continue
		}
		resolved := prop
		if resolved != nil && resolved.Ref != "" {
			resolved = s.registry.SchemaFromRef(resolved.Ref)
		}
		if resolved == nil {
			continue
		}
		switch resolved.Type {
		case huma.TypeObject:
			next, err := s.fillRawDefaults(resolved, elem)
			if err != nil {
				return nil, err
			}
			if string(next) != string(elem) {
				obj[name] = next
				changed = true
			}
		case huma.TypeArray:
			if resolved.Items == nil {
				continue
			}
			var items []json.RawMessage
			if json.Unmarshal(elem, &items) != nil {
				continue
			}
			arrChanged := false
			for i, item := range items {
				next, err := s.fillRawDefaults(resolved.Items, item)
				if err != nil {
					return nil, err
				}
				if string(next) != string(item) {
					items[i] = next
					arrChanged = true
				}
			}
			if arrChanged {
				encoded, err := json.Marshal(items)
				if err != nil {
					return nil, err
				}
				obj[name] = encoded
				changed = true
			}
		}
	}
	if !changed {
		return raw, nil
	}
	return json.Marshal(obj)
}
