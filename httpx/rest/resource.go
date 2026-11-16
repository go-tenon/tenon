package rest

import (
	"fmt"
	"net/http"
	"reflect"
	"regexp"
	"slices"
	"strings"

	"github.com/danielgtaylor/huma/v2"
)

// Resource 声明一个资源的后台页面：列表、详情、表单和操作。它只描述呈现上的选择，引用已经登记的接口；
// 字段的类型、枚举和校验规则来自这些接口的 OpenAPI schema，显示文字来自翻译文件：
//
//   - <Name>.title：资源的名称，例如 article.title: 文章；
//   - <Name>.fields.<字段>：字段的名称，例如 article.fields.title: 标题；
//   - <Name>.actions.<操作>：操作的名称，例如 article.actions.publish: 发布。
//
// 资源写入 OpenAPI 文档的 x-tenon-resources，tenon generate 据此生成前端的 resources.ts，
// 运营后台和 shadcn 的界面用同一份定义渲染。
type Resource struct {
	// Name 是资源的名称，kebab-case，例如 article。
	Name string `json:"name"`
	// List 是列表接口的操作 ID：GET、没有路径参数，响应体有 items 数组（page.Offset 或 page.Cursor）。必须设置。
	List string `json:"list"`
	// Get、Create、Update、Delete 是详情、创建、修改和删除接口的操作 ID，没有的留空。
	// Get、Update、Delete 有且只有一个路径参数，它的名称是列表项中标识一行的字段，例如 /articles/{id} 对应 id。
	Get    string `json:"get,omitempty"`
	Create string `json:"create,omitempty"`
	Update string `json:"update,omitempty"`
	Delete string `json:"delete,omitempty"`
	// Columns 是列表显示的列：列表项的字段。为空时是列表项中除对象和数组外的全部字段。
	Columns []string `json:"columns"`
	// Search 是列表接口中关键词搜索的查询参数，例如 q；为空时没有搜索框。
	Search string `json:"search,omitempty"`
	// Filters 是列表的筛选条件：列表接口的查询参数，例如 status。
	Filters []string `json:"filters,omitempty"`
	// Sort 是可以排序的列。列表接口要有 sort 查询参数，取值是列名，前面加 - 表示降序；
	// sort 限定了取值时，每一列的升序或降序至少有一个在其中。
	Sort []string `json:"sort,omitempty"`
	// Form 是创建和修改的表单字段：请求体的字段。为空时是请求体的全部字段（创建和修改都有时取创建的）。
	Form []string `json:"form,omitempty"`
	// Actions 是一行上的其他操作，例如发布。
	Actions []Action `json:"actions,omitempty"`
	// Key 是标识一行的字段，由 Get、Update、Delete 和 Actions 的路径参数得出，不需要设置。
	Key string `json:"key,omitempty"`
}

// Action 是资源的一行上的操作。
type Action struct {
	// Name 是操作的名称，kebab-case，例如 publish；显示文字是 <资源>.actions.<Name> 的消息。
	Name string `json:"name"`
	// Operation 是接口的操作 ID，有且只有一个路径参数（行的标识），例如 article-publish。
	Operation string `json:"operation"`
	// Confirm 表示执行前先询问。
	Confirm bool `json:"confirm,omitempty"`
	// Danger 表示这是危险的操作，界面用醒目的颜色显示。
	Danger bool `json:"danger,omitempty"`
}

var resourceName = regexp.MustCompile(`^[a-z][a-z0-9]*(-[a-z0-9]+)*$`)

// Resource 声明一个资源，写入 OpenAPI 文档的 x-tenon-resources。引用的接口必须已经用 Register 登记；
// 名称重复、接口不存在或形式不符、字段不存在时 panic，与 Register 一样在启动和生成代码时就报告错误。
func (a *API) Resource(r Resource) {
	if err := a.checkResource(&r); err != nil {
		panic("rest: resource " + r.Name + ": " + err.Error())
	}
	a.resources = append(a.resources, r)
	doc := a.OpenAPI()
	if doc.Extensions == nil {
		doc.Extensions = map[string]any{}
	}
	doc.Extensions["x-tenon-resources"] = slices.Clone(a.resources)
}

// Resources 按声明的顺序返回已声明的资源，列、表单字段和 Key 已经补全。
func (a *API) Resources() []Resource { return slices.Clone(a.resources) }

// checkResource 检查 r 引用的接口和字段，补全默认的列、表单字段和 Key。
func (a *API) checkResource(r *Resource) error {
	if !resourceName.MatchString(r.Name) {
		return fmt.Errorf("name must be kebab-case, e.g. article")
	}
	if slices.ContainsFunc(a.resources, func(x Resource) bool { return x.Name == r.Name }) {
		return fmt.Errorf("declared twice")
	}
	if slices.ContainsFunc(a.settings, func(x Settings) bool { return x.Name == r.Name }) {
		return fmt.Errorf("settings have the same name; their labels would clash")
	}
	if r.List == "" {
		return fmt.Errorf("no List operation; it is required")
	}
	list, err := a.resourceOp(r.List, http.MethodGet)
	if err != nil {
		return err
	}
	if len(params(list, "path")) > 0 {
		return fmt.Errorf("list operation %s must not have path parameters", r.List)
	}
	item := a.listItem(list)
	if item == nil {
		return fmt.Errorf("list operation %s must respond with an object whose items is an array of objects (page.Offset or page.Cursor)", r.List)
	}
	itemOrder := fieldOrder(a.listItemType(r.List))
	if len(r.Columns) == 0 {
		for _, name := range orderedFields(item, itemOrder) {
			if t := a.resolveSchema(item.Properties[name]).Type; t != huma.TypeObject && t != huma.TypeArray {
				r.Columns = append(r.Columns, name)
			}
		}
	}
	if err := hasFields("column", r.Columns, item); err != nil {
		return err
	}
	query := params(list, "query")
	if r.Search != "" && !slices.Contains(query, r.Search) {
		return fmt.Errorf("search %q is not a query parameter of %s", r.Search, r.List)
	}
	for _, f := range r.Filters {
		if !slices.Contains(query, f) {
			return fmt.Errorf("filter %q is not a query parameter of %s", f, r.List)
		}
	}
	if len(r.Sort) > 0 && !slices.Contains(query, "sort") {
		return fmt.Errorf("sorting needs a sort query parameter on %s", r.List)
	}
	if err := hasFields("sort column", r.Sort, item); err != nil {
		return err
	}
	if len(r.Sort) > 0 {
		// sort 限定了取值时，每一列的升序或降序至少有一个是允许的取值，否则界面排序时得到 422。
		i := slices.IndexFunc(list.Parameters, func(p *huma.Param) bool { return p.In == "query" && p.Name == "sort" })
		if allowed := a.resolveSchema(list.Parameters[i].Schema).Enum; len(allowed) > 0 {
			for _, c := range r.Sort {
				if !slices.Contains(allowed, any(c)) && !slices.Contains(allowed, any("-"+c)) {
					return fmt.Errorf("sort column %s is not an allowed value of the sort parameter of %s (%v)", c, r.List, allowed)
				}
			}
		}
	}

	keyed := []struct{ id, method string }{{r.Get, http.MethodGet}, {r.Update, ""}, {r.Delete, http.MethodDelete}}
	for _, act := range r.Actions {
		if !resourceName.MatchString(act.Name) {
			return fmt.Errorf("action name %q must be kebab-case", act.Name)
		}
		if act.Operation == "" {
			return fmt.Errorf("action %s has no Operation", act.Name)
		}
		keyed = append(keyed, struct{ id, method string }{act.Operation, ""})
	}
	r.Key = ""
	for _, k := range keyed {
		if k.id == "" {
			continue
		}
		op, err := a.resourceOp(k.id, k.method)
		if err != nil {
			return err
		}
		path := params(op, "path")
		if len(path) != 1 {
			return fmt.Errorf("operation %s must have exactly one path parameter, the row key", k.id)
		}
		if r.Key != "" && r.Key != path[0] {
			return fmt.Errorf("operation %s uses path parameter %s, others use %s", k.id, path[0], r.Key)
		}
		r.Key = path[0]
	}
	if r.Key != "" {
		if _, ok := item.Properties[r.Key]; !ok {
			return fmt.Errorf("row key %s (the path parameter) is not a field of the list items", r.Key)
		}
	}

	var bodies []*huma.Schema
	var bodyOrder []string
	for _, id := range []string{r.Create, r.Update} {
		if id == "" {
			continue
		}
		op, err := a.resourceOp(id, "")
		if err != nil {
			return err
		}
		if id == r.Create && len(params(op, "path")) > 0 {
			return fmt.Errorf("create operation %s must not have path parameters", id)
		}
		body := a.requestBody(op)
		if body == nil {
			return fmt.Errorf("operation %s has no JSON object request body", id)
		}
		if bodyOrder == nil {
			bodyOrder = fieldOrder(a.bodyType(id))
		}
		bodies = append(bodies, body)
	}
	if len(r.Form) == 0 && len(bodies) > 0 {
		r.Form = orderedFields(bodies[0], bodyOrder)
	}
	if len(r.Form) > 0 && len(bodies) == 0 {
		return fmt.Errorf("a Form needs a Create or Update operation")
	}
	for _, body := range bodies {
		if err := hasFields("form field", r.Form, body); err != nil {
			return err
		}
	}
	return nil
}

// resourceOp 返回操作 ID 为 id 的接口；method 不为空时检查它的方法。
func (a *API) resourceOp(id, method string) (*huma.Operation, error) {
	for _, item := range a.OpenAPI().Paths {
		for _, op := range []*huma.Operation{item.Get, item.Post, item.Put, item.Patch, item.Delete} {
			if op == nil || op.OperationID != id {
				continue
			}
			if method != "" && op.Method != method {
				return nil, fmt.Errorf("operation %s must be %s", id, method)
			}
			return op, nil
		}
	}
	return nil, fmt.Errorf("operation %s is not registered (register it before declaring the resource)", id)
}

// params 返回 op 中位置为 in 的参数名。
func params(op *huma.Operation, in string) []string {
	var out []string
	for _, p := range op.Parameters {
		if p.In == in {
			out = append(out, p.Name)
		}
	}
	return out
}

// resolveSchema 解开 $ref。
func (a *API) resolveSchema(s *huma.Schema) *huma.Schema {
	for s != nil && s.Ref != "" {
		s = a.OpenAPI().Components.Schemas.SchemaFromRef(s.Ref)
	}
	if s == nil {
		return &huma.Schema{}
	}
	return s
}

// listItem 返回列表接口响应体中 items 的元素的 schema；不是这种形式时返回 nil。
func (a *API) listItem(op *huma.Operation) *huma.Schema {
	res := op.Responses[fmt.Sprint(http.StatusOK)]
	if res == nil || res.Content["application/json"] == nil {
		return nil
	}
	body := a.resolveSchema(res.Content["application/json"].Schema)
	items := a.resolveSchema(body.Properties["items"])
	if items.Type != huma.TypeArray {
		return nil
	}
	item := a.resolveSchema(items.Items)
	if item.Type != huma.TypeObject || len(item.Properties) == 0 {
		return nil
	}
	return item
}

// requestBody 返回 op 的 JSON 请求体的 schema；没有或不是对象时返回 nil。
func (a *API) requestBody(op *huma.Operation) *huma.Schema {
	if op.RequestBody == nil || op.RequestBody.Content["application/json"] == nil {
		return nil
	}
	body := a.resolveSchema(op.RequestBody.Content["application/json"].Schema)
	if body.Type != huma.TypeObject || len(body.Properties) == 0 {
		return nil
	}
	return body
}

// sortedFields 按名称返回 s 的字段。
func sortedFields(s *huma.Schema) []string {
	names := make([]string, 0, len(s.Properties))
	for k := range s.Properties {
		names = append(names, k)
	}
	slices.Sort(names)
	return names
}

// orderedFields 按 order（Go 结构体中字段的声明顺序）返回 s 的字段，order 中没有的按名称排在后面。
// OpenAPI 的 properties 没有顺序，默认的列和表单字段要与结构体的顺序相同。
func orderedFields(s *huma.Schema, order []string) []string {
	var out []string
	for _, n := range order {
		if _, ok := s.Properties[n]; ok && !slices.Contains(out, n) {
			out = append(out, n)
		}
	}
	for _, n := range sortedFields(s) {
		if !slices.Contains(out, n) {
			out = append(out, n)
		}
	}
	return out
}

// endpoint 返回操作 ID 为 id 的端点。
func (a *API) endpoint(id string) (Endpoint, bool) {
	i := slices.IndexFunc(a.endpoints, func(e Endpoint) bool { return e.OperationID == id })
	if i < 0 {
		return Endpoint{}, false
	}
	return a.endpoints[i], true
}

// bodyType 返回接口输入的 Body 字段的类型；没有时返回 nil。
func (a *API) bodyType(id string) reflect.Type {
	e, ok := a.endpoint(id)
	if !ok {
		return nil
	}
	return fieldType(e.Input, "Body")
}

// listItemType 返回列表接口输出的 Body 中 items 数组的元素类型；不是这种形式时返回 nil。
func (a *API) listItemType(id string) reflect.Type {
	e, ok := a.endpoint(id)
	if !ok {
		return nil
	}
	body := derefType(fieldType(e.Output, "Body"))
	if body == nil || body.Kind() != reflect.Struct {
		return nil
	}
	for i := range body.NumField() {
		f := body.Field(i)
		if name, _, _ := strings.Cut(f.Tag.Get("json"), ","); name == "items" && f.Type.Kind() == reflect.Slice {
			return f.Type.Elem()
		}
	}
	return nil
}

// derefType 解开指针类型；t 为 nil 时返回 nil。
func derefType(t reflect.Type) reflect.Type {
	for t != nil && t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	return t
}

// fieldType 返回结构体 t 中名为 name 的字段的类型。
func fieldType(t reflect.Type, name string) reflect.Type {
	t = derefType(t)
	if t == nil || t.Kind() != reflect.Struct {
		return nil
	}
	f, ok := t.FieldByName(name)
	if !ok {
		return nil
	}
	return f.Type
}

// fieldOrder 按声明顺序返回结构体 t 的 JSON 字段名，展开嵌入的结构体。
func fieldOrder(t reflect.Type) []string {
	t = derefType(t)
	if t == nil || t.Kind() != reflect.Struct {
		return nil
	}
	var out []string
	for i := range t.NumField() {
		f := t.Field(i)
		tag, _, _ := strings.Cut(f.Tag.Get("json"), ",")
		if tag == "-" {
			continue
		}
		if f.Anonymous && tag == "" {
			out = append(out, fieldOrder(f.Type)...)
			continue
		}
		if !f.IsExported() {
			continue
		}
		if tag == "" {
			tag = f.Name
		}
		out = append(out, tag)
	}
	return out
}

// hasFields 检查 names 都是 s 的字段。
func hasFields(what string, names []string, s *huma.Schema) error {
	var missing []string
	for _, n := range names {
		if _, ok := s.Properties[n]; !ok {
			missing = append(missing, n)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("%s %s not found; fields are %s", what, strings.Join(missing, ", "), strings.Join(sortedFields(s), ", "))
	}
	return nil
}
