package clientgen

import (
	"fmt"
	"maps"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/cockroachdb/errors"
	"github.com/danielgtaylor/huma/v2"

	"github.com/go-tenon/tenon/httpx/rest"
)

// resourcesTS 生成 resources.ts：rest.API.Resource 声明的资源，字段的类型、枚举和是否必填取自接口的 schema，
// 显示文字取自翻译文件中 <资源>.title、<资源>.fields.<字段>、<资源>.actions.<操作> 的消息。
// 一种语言只有部分显示文字，或者所有语言都没有时返回警告。
func (t *tsGen) resourcesTS(templates map[string]map[string]string) (string, []string, error) {
	var b strings.Builder
	var warnings []string
	b.WriteString(`/**
 * 资源：服务端用 rest.API.Resource 声明的后台页面，@go-tenon/ui 的资源模型据此渲染列表、表单和操作。
 * operations 是客户端方法的位置（client[分组][方法]），schemas.ts、queries.ts 中的位置相同；
 * 字段的 type、format、enum（enums.ts 中的命名类型）和 required 取自接口的 schema；
 * permissions 是每个接口要求的权限；labels 按语言列出显示文字，缺少时先用默认语言，再用字段名。
 */
export const resources = {
`)
	for _, r := range t.api.Resources() {
		src, warn, err := t.resourceTS(r, templates)
		if err != nil {
			return "", nil, errors.Wrapf(err, "clientgen: resource %s", r.Name)
		}
		warnings = append(warnings, warn...)
		b.WriteString(src)
	}
	b.WriteString("} as const\n")
	return b.String(), warnings, nil
}

// findOp 返回操作 ID 为 id 的客户端方法。
func (t *tsGen) findOp(id string) (*tsOp, error) {
	for _, ops := range t.groups {
		for _, o := range ops {
			if o.ep.OperationID == id {
				return o, nil
			}
		}
	}
	return nil, errors.Newf("operation %s has no client method (event streams and downloads cannot back a resource)", id)
}

// resourceTS 返回资源 r 的 TypeScript 描述，以及它用到的类型。
func (t *tsGen) resourceTS(r rest.Resource, templates map[string]map[string]string) (string, []string, error) {
	var b strings.Builder
	b.WriteString("  " + tsKey(r.Name) + ": {\n")
	b.WriteString("    name: " + strconv.Quote(r.Name) + ",\n")
	if r.Key != "" {
		b.WriteString("    key: " + strconv.Quote(r.Key) + ",\n")
	}

	// 接口：客户端方法的位置和要求的权限。
	var ops, perms strings.Builder
	var list *tsOp
	for _, kind := range []struct{ name, id string }{
		{"list", r.List}, {"get", r.Get}, {"create", r.Create}, {"update", r.Update}, {"delete", r.Delete},
	} {
		if kind.id == "" {
			continue
		}
		o, err := t.findOp(kind.id)
		if err != nil {
			return "", nil, err
		}
		if kind.name == "list" {
			list = o
		}
		ops.WriteString("      " + kind.name + ": " + opRef(o) + ",\n")
		perms.WriteString("      " + kind.name + ": " + required(o) + ",\n")
	}
	b.WriteString("    operations: {\n" + ops.String() + "    },\n")
	b.WriteString("    permissions: {\n" + perms.String() + "    },\n")

	// 列：列表项的字段。
	item := t.listItem(list.op)
	if item == nil {
		return "", nil, errors.Newf("list operation %s has no items", r.List)
	}
	b.WriteString("    columns: [\n")
	for _, c := range r.Columns {
		b.WriteString("      " + t.fieldTS(c, item.Properties[c], slices.Contains(item.Required, c), slices.Contains(r.Sort, c)) + ",\n")
	}
	b.WriteString("    ],\n")
	b.WriteString("    filters: [\n")
	for _, f := range r.Filters {
		i := slices.IndexFunc(list.op.Parameters, func(p *huma.Param) bool { return p.In == "query" && p.Name == f })
		b.WriteString("      " + t.fieldTS(f, list.op.Parameters[i].Schema, false, false) + ",\n")
	}
	b.WriteString("    ],\n")

	// 表单：新建和修改各取自己接口的请求体，字段的类型和是否必填也来自各自的请求体。
	for _, f := range []struct {
		key, id string
		fields  []string
	}{
		{"createForm", r.Create, r.CreateForm}, {"updateForm", r.Update, r.UpdateForm},
	} {
		b.WriteString("    " + f.key + ": [\n")
		if f.id != "" {
			o, err := t.findOp(f.id)
			if err != nil {
				return "", nil, err
			}
			body := t.requestBody(o.op)
			for _, name := range f.fields {
				b.WriteString("      " + t.fieldTS(name, body.Properties[name], slices.Contains(body.Required, name), false) + ",\n")
			}
		}
		b.WriteString("    ],\n")
	}

	// 操作。
	b.WriteString("    actions: [\n")
	for _, a := range r.Actions {
		o, err := t.findOp(a.Operation)
		if err != nil {
			return "", nil, err
		}
		when := ""
		if len(a.When) > 0 {
			var conds []string
			for _, field := range slices.Sorted(maps.Keys(a.When)) {
				vals := make([]string, len(a.When[field]))
				for i, v := range a.When[field] {
					vals[i] = strconv.Quote(v)
				}
				conds = append(conds, strconv.Quote(field)+": ["+strings.Join(vals, ", ")+"]")
			}
			when = ", when: { " + strings.Join(conds, ", ") + " }"
		}
		fmt.Fprintf(&b, "      { name: %s, operation: %s, permissions: %s, confirm: %t, danger: %t%s },\n",
			strconv.Quote(a.Name), opRef(o), required(o), a.Confirm, a.Danger, when)
	}
	b.WriteString("    ],\n")

	// 显示文字。
	fields := slices.Clone(r.Columns)
	for _, f := range slices.Concat(r.Filters, r.CreateForm, r.UpdateForm) {
		if !slices.Contains(fields, f) {
			fields = append(fields, f)
		}
	}
	keys := []string{r.Name + ".title"}
	for _, f := range fields {
		keys = append(keys, r.Name+".fields."+f)
	}
	for _, a := range r.Actions {
		keys = append(keys, r.Name+".actions."+a.Name)
	}
	var warnings []string
	var langs strings.Builder
	for _, lang := range slices.Sorted(maps.Keys(templates)) {
		msgs := templates[lang]
		var missing []string
		for _, k := range keys {
			if _, ok := msgs[k]; !ok {
				missing = append(missing, k)
			}
		}
		if len(missing) == len(keys) {
			continue
		}
		if len(missing) > 0 {
			warnings = append(warnings, fmt.Sprintf("resource %s has no %s label for %s", r.Name, lang, strings.Join(missing, ", ")))
		}
		langs.WriteString("      " + strconv.Quote(lang) + ": {\n")
		if title, ok := msgs[r.Name+".title"]; ok {
			langs.WriteString("        title: " + strconv.Quote(title) + ",\n")
		}
		langs.WriteString("        fields: { " + labels(msgs, r.Name+".fields.", fields) + " },\n")
		names := make([]string, len(r.Actions))
		for i, a := range r.Actions {
			names[i] = a.Name
		}
		langs.WriteString("        actions: { " + labels(msgs, r.Name+".actions.", names) + " },\n")
		langs.WriteString("      },\n")
	}
	if langs.Len() == 0 {
		warnings = append(warnings, fmt.Sprintf("resource %s has no labels; add messages %s.title, %s.fields.<field> and %s.actions.<action> to the translations", r.Name, r.Name, r.Name, r.Name))
	}
	b.WriteString("    labels: {\n" + langs.String() + "    },\n")
	b.WriteString("  },\n")
	return b.String(), warnings, nil
}

// labels 写出 names 中有消息 prefix+name 的显示文字。
func labels(msgs map[string]string, prefix string, names []string) string {
	var out []string
	for _, n := range names {
		if l, ok := msgs[prefix+n]; ok {
			out = append(out, tsKey(n)+": "+strconv.Quote(l))
		}
	}
	return strings.Join(out, ", ")
}

// opRef 写出客户端方法的位置，例如 ["article", "list"]；没有分组的方法是 [null, "ping"]。
func opRef(o *tsOp) string {
	group := "null"
	if o.group != "" {
		group = strconv.Quote(o.group)
	}
	return "[" + group + ", " + strconv.Quote(o.method) + "]"
}

// required 写出接口要求的权限。
func required(o *tsOp) string {
	perms, _ := o.op.Extensions["x-required-permissions"].([]string)
	quoted := make([]string, len(perms))
	for i, p := range perms {
		quoted[i] = strconv.Quote(p)
	}
	return "[" + strings.Join(quoted, ", ") + "]"
}

// resolve 解开 $ref。
func (t *tsGen) resolve(s *huma.Schema) *huma.Schema {
	for s != nil && s.Ref != "" {
		s = t.registry.SchemaFromRef(s.Ref)
	}
	if s == nil {
		return &huma.Schema{}
	}
	return s
}

// listItem 返回列表接口响应体中 items 的元素的 schema。
func (t *tsGen) listItem(op *huma.Operation) *huma.Schema {
	res := op.Responses[strconv.Itoa(http.StatusOK)]
	if res == nil || res.Content["application/json"] == nil {
		return nil
	}
	items := t.resolve(t.resolve(res.Content["application/json"].Schema).Properties["items"])
	if items.Type != huma.TypeArray {
		return nil
	}
	return t.resolve(items.Items)
}

// requestBody 返回接口的 JSON 请求体的 schema。
func (t *tsGen) requestBody(op *huma.Operation) *huma.Schema {
	if op.RequestBody == nil || op.RequestBody.Content["application/json"] == nil {
		return &huma.Schema{}
	}
	return t.resolve(op.RequestBody.Content["application/json"].Schema)
}

// fieldTS 写出一个字段：名称、类型、格式、枚举，以及是否必填、能否排序。
func (t *tsGen) fieldTS(name string, s *huma.Schema, req, sortable bool) string {
	parts := []string{"name: " + strconv.Quote(name)}
	enum := ""
	if s != nil && s.Ref != "" {
		target := t.registry.SchemaFromRef(s.Ref)
		if target != nil {
			if _, ok := target.Extensions["x-tenon-enum"]; ok {
				enum = s.Ref[strings.LastIndex(s.Ref, "/")+1:]
			}
		}
	}
	rs := t.resolve(s)
	typ := rs.Type
	if typ == "" {
		typ = "unknown"
	}
	parts = append(parts, "type: "+strconv.Quote(typ))
	if rs.Format != "" {
		parts = append(parts, "format: "+strconv.Quote(rs.Format))
	}
	if typ == huma.TypeArray {
		it := t.resolve(rs.Items)
		if it.Type != "" {
			parts = append(parts, "items: "+strconv.Quote(it.Type))
		}
		if rs.Items != nil && rs.Items.Ref != "" {
			if target := t.registry.SchemaFromRef(rs.Items.Ref); target != nil {
				if _, ok := target.Extensions["x-tenon-enum"]; ok {
					enum = rs.Items.Ref[strings.LastIndex(rs.Items.Ref, "/")+1:]
				}
			}
		}
	}
	if enum != "" {
		parts = append(parts, "enum: "+strconv.Quote(enum))
	} else if len(rs.Enum) > 0 {
		values := make([]string, 0, len(rs.Enum))
		for _, v := range rs.Enum {
			values = append(values, strconv.Quote(fmt.Sprint(v)))
		}
		parts = append(parts, "values: ["+strings.Join(values, ", ")+"]")
	}
	if rs.Nullable {
		parts = append(parts, "nullable: true")
	}
	if req {
		parts = append(parts, "required: true")
	}
	if sortable {
		parts = append(parts, "sortable: true")
	}
	return "{ " + strings.Join(parts, ", ") + " }"
}
