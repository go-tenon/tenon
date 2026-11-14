package clientgen

import (
	"bytes"
	"encoding/json"
	"maps"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/cockroachdb/errors"
	"github.com/danielgtaylor/huma/v2"

	"github.com/go-tenon/tenon/identity"
)

// schemaKeys 是 schemas.ts 保留的 JSON Schema 关键字：类型、结构和校验规则。说明、示例等只供阅读的字段不保留。
var schemaKeys = map[string]bool{
	"$ref": true, "type": true, "format": true, "default": true, "items": true, "additionalProperties": true,
	"properties": true, "enum": true, "const": true, "minimum": true, "exclusiveMinimum": true, "maximum": true,
	"exclusiveMaximum": true, "multipleOf": true, "minLength": true, "maxLength": true, "pattern": true,
	"patternDescription": true, "minItems": true, "maxItems": true, "uniqueItems": true, "required": true,
	"dependentRequired": true, "minProperties": true, "maxProperties": true, "readOnly": true, "writeOnly": true,
	"oneOf": true, "anyOf": true, "allOf": true, "not": true,
}

// ordered 按分组的顺序返回全部接口：没有分组的在前，分组按名称排序，组内按 operationId 排序。
func (t *tsGen) ordered() [][]*tsOp {
	var out [][]*tsOp
	if len(t.groups[""]) > 0 {
		out = append(out, t.groups[""])
	}
	for _, name := range slices.Sorted(maps.Keys(t.groups)) {
		if name != "" {
			out = append(out, t.groups[name])
		}
	}
	return out
}

// tree 按客户端方法的分组写出一个对象字面量：没有分组的接口是顶层的键，其余写在分组的键之下。
// entry 返回一个接口的值，为空时跳过它；没有接口的分组不出现。
func (t *tsGen) tree(b *strings.Builder, entry func(o *tsOp, indent string) string) {
	for _, ops := range t.ordered() {
		var body strings.Builder
		for _, o := range ops {
			indent := "    "
			if o.group == "" {
				indent = "  "
			}
			if v := entry(o, indent); v != "" {
				body.WriteString(indent + o.method + ": " + v + ",\n")
			}
		}
		if body.Len() == 0 {
			continue
		}
		if ops[0].group == "" {
			b.WriteString(body.String())
		} else {
			b.WriteString("  " + ops[0].group + ": {\n" + body.String() + "  },\n")
		}
	}
}

// schemasTS 生成 schemas.ts：每个接口的请求体和查询参数的 JSON Schema，以及它们引用的命名类型。
func (t *tsGen) schemasTS() (string, error) {
	refs := map[string]bool{}
	var b strings.Builder
	b.WriteString(`/**
 * 每个接口输入的校验规则：请求体（body）和查询参数（query）的 JSON Schema，只保留类型、结构和校验规则。
 * 规则与服务端相同，交给 @go-tenon/client 的校验函数使用；$ref 指向 components 中的命名类型。
 */
export const schemas = {
`)
	var failed error
	t.tree(&b, func(o *tsOp, indent string) string {
		var parts []string
		if rb := o.op.RequestBody; rb != nil && rb.Content["application/json"] != nil {
			s, err := t.schemaJSON(rb.Content["application/json"].Schema, refs, indent+"  ")
			if err != nil {
				failed = err
			}
			parts = append(parts, indent+"  body: "+s)
		}
		query := &huma.Schema{Type: huma.TypeObject, Properties: map[string]*huma.Schema{}}
		for _, p := range o.op.Parameters {
			if p.In == "query" {
				query.Properties[p.Name] = p.Schema
				if p.Required {
					query.Required = append(query.Required, p.Name)
				}
			}
		}
		if len(query.Properties) > 0 {
			s, err := t.schemaJSON(query, refs, indent+"  ")
			if err != nil {
				failed = err
			}
			parts = append(parts, indent+"  query: "+s)
		}
		if len(parts) == 0 {
			return ""
		}
		return "{\n" + strings.Join(parts, ",\n") + ",\n" + indent + "}"
	})
	b.WriteString("} as const\n\n/** schemas 中 $ref 引用的命名类型，键是 OpenAPI 中的名称。 */\nexport const components = {\n")
	all := t.registry.Map()
	for done := map[string]bool{}; len(done) < len(refs); {
		for _, name := range slices.Sorted(maps.Keys(refs)) {
			if done[name] {
				continue
			}
			done[name] = true
			s := all[name]
			if s == nil {
				return "", errors.Newf("clientgen: schema %s is referenced but not registered", name)
			}
			if _, err := t.schemaJSON(s, refs, ""); err != nil { // 收集它引用的类型
				return "", err
			}
		}
	}
	for _, name := range slices.Sorted(maps.Keys(refs)) {
		s, err := t.schemaJSON(all[name], refs, "  ")
		if err != nil {
			return "", err
		}
		b.WriteString("  " + tsKey(name) + ": " + s + ",\n")
	}
	b.WriteString("} as const\n")
	return b.String(), failed
}

// schemaJSON 把 s 写成 JSON（只保留 schemaKeys 中的关键字），记下它引用的命名类型。
func (t *tsGen) schemaJSON(s *huma.Schema, refs map[string]bool, indent string) (string, error) {
	data, err := json.Marshal(s)
	if err != nil {
		return "", errors.Wrap(err, "clientgen: marshal schema")
	}
	var v any
	if err := json.Unmarshal(data, &v); err != nil {
		return "", errors.Wrap(err, "clientgen: marshal schema")
	}
	v = filterSchema(v, refs)
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent(indent, "  ")
	if err := enc.Encode(v); err != nil {
		return "", errors.Wrap(err, "clientgen: marshal schema")
	}
	return strings.TrimSuffix(buf.String(), "\n"), nil
}

// filterSchema 保留 schema 中 schemaKeys 列出的关键字，递归处理子 schema，并把 $ref 指向的名称记入 refs。
func filterSchema(v any, refs map[string]bool) any {
	m, ok := v.(map[string]any)
	if !ok {
		return v
	}
	out := map[string]any{}
	for k, val := range m {
		if !schemaKeys[k] {
			continue
		}
		switch k {
		case "$ref":
			if ref, ok := val.(string); ok {
				refs[strings.TrimPrefix(ref, "#/components/schemas/")] = true
			}
		case "properties":
			props := map[string]any{}
			for name, p := range val.(map[string]any) {
				props[name] = filterSchema(p, refs)
			}
			val = props
		case "items", "additionalProperties", "not":
			val = filterSchema(val, refs)
		case "oneOf", "anyOf", "allOf":
			list := slices.Clone(val.([]any))
			for i := range list {
				list[i] = filterSchema(list[i], refs)
			}
			val = list
		}
		out[k] = val
	}
	return out
}

// messagesTS 生成 messages.ts：各语言 validation. 开头的提示模板。
func messagesTS(templates map[string]map[string]string) string {
	var b strings.Builder
	b.WriteString(`/**
 * 验证提示的模板，按语言和错误码（validation. 开头），与服务端返回的提示相同：框架内置的文案，加上应用翻译文件中的覆盖。
 * 占位符是 Go 模板的写法，例如 {{.Min}}、{{.Max}}、{{.Value}}、{{.Description}}。
 */
export const messages = {
`)
	for _, lang := range slices.Sorted(maps.Keys(templates)) {
		var lines []string
		for _, id := range slices.Sorted(maps.Keys(templates[lang])) {
			if strings.HasPrefix(id, "validation.") {
				lines = append(lines, "    "+strconv.Quote(id)+": "+strconv.Quote(templates[lang][id])+",")
			}
		}
		if len(lines) > 0 {
			b.WriteString("  " + strconv.Quote(lang) + ": {\n" + strings.Join(lines, "\n") + "\n  },\n")
		}
	}
	b.WriteString("} as const\n")
	return b.String()
}

// permissionsTS 生成 permissions.ts：权限目录（x-tenon-permissions）和每个接口要求的权限（x-required-permissions）。
// 设置了权限目录时，接口要求的权限必须都在目录中。
func (t *tsGen) permissionsTS() (string, error) {
	catalog, _ := t.api.OpenAPI().Extensions["x-tenon-permissions"].([]identity.Permission)
	desc := map[string]string{}
	for _, p := range catalog {
		desc[p.Name] = p.Description
	}
	var b strings.Builder
	var failed error
	var routes strings.Builder
	t.tree(&routes, func(o *tsOp, _ string) string {
		required, _ := o.op.Extensions["x-required-permissions"].([]string)
		if len(required) == 0 {
			return ""
		}
		quoted := make([]string, len(required))
		for i, p := range required {
			if _, ok := desc[p]; !ok {
				if len(catalog) > 0 {
					failed = errors.Newf("clientgen: %s requires permission %q, which is not in the permission catalog", o.ep.OperationID, p)
				}
				desc[p] = ""
			}
			quoted[i] = strconv.Quote(p)
		}
		return "[" + strings.Join(quoted, ", ") + "]"
	})
	b.WriteString("/** 权限目录：权限名和说明，来自 rest.Options.Permissions，以及接口要求的权限。 */\nexport const permissions = {\n")
	for _, name := range slices.Sorted(maps.Keys(desc)) {
		b.WriteString("  " + strconv.Quote(name) + ": " + strconv.Quote(desc[name]) + ",\n")
	}
	b.WriteString("} as const\n\n/** 权限名。 */\nexport type Permission = keyof typeof permissions\n\n")
	b.WriteString("/** 每个接口要求的权限，调用者必须全部具备；没有列出的接口不要求权限。 */\nexport const required = {\n")
	b.WriteString(routes.String())
	b.WriteString("} as const satisfies Record<string, readonly Permission[] | Record<string, readonly Permission[]>>\n")
	return b.String(), failed
}

// queriesTS 生成 queries.ts：GET 接口的查询选项和其他接口的修改选项，形式与 TanStack Query 的 useQuery、useMutation 相同。
func (t *tsGen) queriesTS(name string) (string, error) {
	for _, ops := range t.ordered() {
		for _, o := range ops {
			if o.group == "key" || o.group == "" && o.method == "key" {
				return "", errors.Newf("clientgen: operation %s collides with the key of createQueries", o.ep.OperationID)
			}
		}
	}
	var b strings.Builder
	b.WriteString(`import type { Client } from "./api"

/** createQueries 的选项。 */
export interface QueryOptions {
  /** 放在每个 query key 最前面的作用域，例如部署、账号、租户：切换账号或租户后，旧的缓存不再命中。 */
  scope?: () => readonly unknown[]
  /** 修改成功后让查询失效，通常是 TanStack Query 的 QueryClient。 */
  queryClient?: { invalidateQueries(filters: { queryKey: readonly unknown[] }): unknown }
}

/**
 * 为每个接口创建 TanStack Query 的选项。GET 接口返回 { queryKey, queryFn }，交给 useQuery；其他接口返回
 * { mutationFn, onSuccess }，交给 useMutation，成功后让同一分组（operationId 的第一段）的查询失效。
 * query key 是 [...scope, ` + strconv.Quote(name) + `, 分组, 接口, ...参数]。
 */
export function createQueries(client: Client, options: QueryOptions = {}) {
  const key = (...parts: unknown[]): unknown[] => [...(options.scope?.() ?? []), ` + strconv.Quote(name) + `, ...parts]
  const invalidate = (...parts: unknown[]) => options.queryClient?.invalidateQueries({ queryKey: key(...parts) })
  return {
    /** 这个 API 全部查询的 key 前缀。 */
    key: () => key(),
`)
	for _, ops := range t.ordered() {
		var body strings.Builder
		group := ops[0].group
		indent := "      "
		if group == "" {
			indent = "    "
		}
		for _, o := range ops {
			if v := queryEntry(o, indent); v != "" {
				body.WriteString(v)
			}
		}
		if body.Len() == 0 {
			continue
		}
		if group == "" {
			b.WriteString(body.String())
			continue
		}
		b.WriteString("    " + group + ": {\n      /** " + group + " 分组全部查询的 key 前缀。 */\n      key: () => key(" + strconv.Quote(group) + "),\n")
		b.WriteString(body.String() + "    },\n")
	}
	b.WriteString("  }\n}\n")
	return b.String(), nil
}

// queryEntry 返回一个接口在 createQueries 中的条目；事件流和下载接口没有条目。
func queryEntry(o *tsOp, indent string) string {
	if o.events != "" || o.download {
		return ""
	}
	fn := "Client"
	group := o.method // 没有分组的接口以自己为分组
	if o.group != "" {
		fn += "[" + strconv.Quote(o.group) + "]"
		group = o.group
	}
	fn += "[" + strconv.Quote(o.method) + "]"
	call := "client."
	if o.group != "" {
		call += o.group + "."
	}
	call += o.method
	parts := strconv.Quote(group) + ", " + strconv.Quote(o.method)
	if o.group == "" {
		parts = strconv.Quote(o.method)
	}
	doc := tsDoc(o.ep.Method+" "+o.ep.Path, indent)
	if o.ep.Method == http.MethodGet {
		return doc + indent + o.method + ": (...args: Parameters<" + fn + ">) => ({\n" +
			indent + "  queryKey: key(" + parts + ", ...args),\n" +
			indent + "  queryFn: () => " + call + "(...args),\n" + indent + "}),\n"
	}
	args := len(o.pathArgs)
	if o.inputName != "" || o.body != "" || o.formName != "" {
		args++
	}
	var mutationFn string
	switch args {
	case 0:
		mutationFn = "() => " + call + "()"
	case 1:
		mutationFn = "(variables: Parameters<" + fn + ">[0]) => " + call + "(variables)"
	default:
		mutationFn = "(variables: Parameters<" + fn + ">) => " + call + "(...variables)"
	}
	return doc + indent + o.method + ": () => ({\n" +
		indent + "  mutationFn: " + mutationFn + ",\n" +
		indent + "  onSuccess: () => invalidate(" + strconv.Quote(group) + "),\n" + indent + "}),\n"
}
