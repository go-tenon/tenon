package rest

import (
	"fmt"
	"net/http"
	"slices"
	"strconv"

	"github.com/danielgtaylor/huma/v2"
)

// Settings 声明一组设置的页面，例如站点设置：读取和保存设置的两个接口，以及表单中有哪些字段。
// 写入 OpenAPI 文档的 x-tenon-settings，tenon generate 据此生成 settings.ts，运营后台和控制台按它渲染设置表单。
// 字段的类型、枚举、是否必填和校验规则取自接口的 schema；显示文字取自翻译文件中 <名称>.title 和
// <名称>.fields.<字段> 的消息，与资源相同。
//
// 设置属于当前的用户、租户或者平台，由请求本身决定，所以两个接口都没有路径参数，见 settings 包。
type Settings struct {
	Name   string            `json:"name"`           // 名称，kebab-case，例如 site。不能与资源同名：两者的显示文字在同一个命名空间中。
	Get    string            `json:"get"`            // 读取设置的接口的操作 ID：GET，没有路径参数，响应体是包含全部表单字段的 JSON 对象。必须设置。
	Update string            `json:"update"`         // 保存设置的接口的操作 ID：没有路径参数，请求体是 JSON 对象。必须设置。
	Form   []string          `json:"form"`           // 表单的字段：保存接口请求体的字段。为空时是请求体的全部字段，按结构体中的顺序。
	Refs   map[string]string `json:"refs,omitempty"` // 引用另一个资源的表单字段：键是 Form 中的字段名，值是被引用资源的名称。
}

// Settings 声明一组设置的页面，写入 OpenAPI 文档的 x-tenon-settings。引用的接口必须已经用 Register 登记；
// 名称重复、接口不存在或形式不符、字段不存在时 panic，与 Resource 一样在启动和生成代码时就报告错误。
func (a *API) Settings(s Settings) {
	if err := a.checkSettings(&s); err != nil {
		panic("rest: settings " + s.Name + ": " + err.Error())
	}
	a.settings = append(a.settings, s)
	doc := a.OpenAPI()
	if doc.Extensions == nil {
		doc.Extensions = map[string]any{}
	}
	doc.Extensions["x-tenon-settings"] = slices.Clone(a.settings)
}

// SettingsPages 按声明的顺序返回已声明的设置，表单字段已经补全。
func (a *API) SettingsPages() []Settings { return slices.Clone(a.settings) }

// checkSettings 检查 s 引用的接口和字段，补全默认的表单字段。
func (a *API) checkSettings(s *Settings) error {
	if !resourceName.MatchString(s.Name) {
		return fmt.Errorf("name must be kebab-case, e.g. site")
	}
	if slices.ContainsFunc(a.settings, func(x Settings) bool { return x.Name == s.Name }) {
		return fmt.Errorf("declared twice")
	}
	if slices.ContainsFunc(a.resources, func(r Resource) bool { return r.Name == s.Name }) {
		return fmt.Errorf("a resource has the same name; their labels would clash")
	}
	if s.Get == "" || s.Update == "" {
		return fmt.Errorf("both the get and update operations are required")
	}
	get, err := a.resourceOp(s.Get, http.MethodGet)
	if err != nil {
		return err
	}
	update, err := a.resourceOp(s.Update, "")
	if err != nil {
		return err
	}
	if update.Method == http.MethodGet {
		return fmt.Errorf("update operation %s must not be GET", s.Update)
	}
	for _, op := range []*huma.Operation{get, update} {
		if p := params(op, "path"); len(p) > 0 {
			return fmt.Errorf("operation %s must not have path parameters; settings belong to the caller, the tenant or the platform", op.OperationID)
		}
	}
	current := a.responseBody(get)
	if current == nil {
		return fmt.Errorf("get operation %s must respond with a JSON object", s.Get)
	}
	body := a.requestBody(update)
	if body == nil {
		return fmt.Errorf("update operation %s has no JSON object request body", s.Update)
	}
	if len(s.Form) == 0 {
		s.Form = orderedFields(body, fieldOrder(a.bodyType(s.Update)))
	}
	if err := hasFields("form field", s.Form, body); err != nil {
		return err
	}
	// 表单的初始值来自读取的接口，所以表单字段都要在它的响应体中。
	if err := hasFields("form field", s.Form, current); err != nil {
		return fmt.Errorf("%w (in the response of %s)", err, s.Get)
	}
	refSchemas := make(map[string][]*huma.Schema, len(s.Form))
	for _, f := range s.Form {
		refSchemas[f] = []*huma.Schema{body.Properties[f]}
	}
	return a.checkRefs(s.Refs, refSchemas, "form")
}

// responseBody 返回 op 成功时的 JSON 响应体的 schema；没有或不是对象时返回 nil。
func (a *API) responseBody(op *huma.Operation) *huma.Schema {
	for _, code := range []int{http.StatusOK, op.DefaultStatus} {
		res := op.Responses[strconv.Itoa(code)]
		if res == nil || res.Content["application/json"] == nil {
			continue
		}
		if s := a.resolveSchema(res.Content["application/json"].Schema); s != nil && s.Type == huma.TypeObject {
			return s
		}
	}
	return nil
}
