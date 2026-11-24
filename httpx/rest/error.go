package rest

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"reflect"
	"strconv"
	"strings"

	"github.com/cockroachdb/errors"
	"github.com/danielgtaylor/huma/v2"

	"github.com/go-tenon/tenon/contract"
	"github.com/go-tenon/tenon/errorx"
	"github.com/go-tenon/tenon/httpx"
	"github.com/go-tenon/tenon/i18n"
)

// ErrorModel 在 Huma 原生 Problem Details 上增加业务错误码和字段错误的验证规则。
//
// 只有业务错误带 code 和 params。Huma 产生的错误在写出前由 localizeErrors 转为本类型：
// 字段错误带上验证规则，提示按请求的语言翻译。OpenAPI 统一用本类型描述错误响应。
type ErrorModel struct {
	huma.ErrorModel
	Code   string         `json:"code,omitempty" doc:"Stable business error code. Present only on business errors."`
	Params map[string]any `json:"params,omitempty" doc:"Public parameters of a business error, e.g. methods for auth.method_required. Present only when the error has any."`                     // errorx.Public 附加的公开参数，客户端据此处理错误，例如 auth.method_required 的 methods。
	Errors []*ErrorDetail `json:"errors,omitempty" doc:"Field errors. Present on validation errors, and on business errors that name a field (the entry then carries the business error code)."` // 覆盖 huma.ErrorModel 的同名字段。

	headers http.Header
}

// ErrorDetail 是一条字段错误：Huma 的 message、location、value，加上验证规则 code。
type ErrorDetail struct {
	huma.ErrorDetail
	Code string `json:"code,omitempty" doc:"Validation rule, e.g. validation.min_length, or the business error code. Absent when the rule is not recognized."`
}

// localizeErrors 是 Huma 的 Transformer：把 Huma 产生的错误（验证失败、参数格式错误等）转为 ErrorModel，
// 字段错误由 contract.Describe 识别规则、按请求的语言翻译，"validation failed" 同样翻译。
func localizeErrors(hctx huma.Context, _ string, v any) (any, error) {
	e, ok := v.(*huma.ErrorModel)
	if !ok {
		return v, nil
	}
	ctx := hctx.Context()
	m := &ErrorModel{ErrorModel: *e}
	m.ErrorModel.Errors = nil
	if e.Detail == "validation failed" {
		if msg := i18n.T(ctx, "validation.failed", nil); msg != "validation.failed" {
			m.Detail = msg
		}
	}
	for _, d := range e.Errors {
		fe := contract.Describe(ctx, d)
		value := d.Value
		if fe.Location != d.Location {
			value = nil // 必填错误的位置换成了缺少的字段，原来的值是父对象
		}
		m.Errors = append(m.Errors, &ErrorDetail{
			ErrorDetail: huma.ErrorDetail{Message: fe.Message, Location: fe.Location, Value: value},
			Code:        fe.Code,
		})
	}
	return m, nil
}

// GetHeaders 实现 huma.HeadersError，返回随错误写出的响应头，例如 Retry-After。
func (e *ErrorModel) GetHeaders() http.Header { return e.headers }

// FromError 将处理器错误转换为 HTTP 错误，nil 输入返回 nil：
//   - 业务错误转为带 code 的 ErrorModel，detail 为 HTTP 状态描述，公开参数写入 params
//     （无法序列化为 JSON 时省略），带 errorx.RetryAfter 时设置 Retry-After 响应头（秒，向上取整）；
//   - 其他 huma.StatusError（例如 huma.Error404NotFound）即使被包装也原样返回；
//   - 其余错误转为通用 500，内部错误信息只留在日志中。
func FromError(err error) huma.StatusError {
	if err == nil {
		return nil
	}
	code := errorx.CodeOf(err)
	if code == "" {
		var se huma.StatusError
		if errors.As(err, &se) {
			return se
		}
		return huma.Error500InternalServerError("unexpected error occurred")
	}
	status := httpx.StatusOf(err)
	m := &ErrorModel{
		ErrorModel: huma.ErrorModel{Status: status, Title: http.StatusText(status), Detail: http.StatusText(status)},
		Code:       code,
	}
	if params := errorx.PublicOf(err); len(params) > 0 {
		if _, jerr := json.Marshal(params); jerr == nil {
			m.Params = params
		}
	}
	if d := errorx.RetryAfterOf(err); d > 0 {
		m.headers = http.Header{"Retry-After": {strconv.Itoa(int(math.Ceil(d.Seconds())))}}
	}
	return m
}

// httpError 转换处理器错误：业务错误的 detail 换成 httpx.ErrorDetail 的译文，
// 错误码写入访问日志，缺少译文时标记 untranslated。
func (a *API) httpError(ctx context.Context, err error) huma.StatusError {
	return a.httpErrorFor(ctx, err, nil)
}

// httpErrorFor 转换处理器错误并按输入结构体的参数位置映射 errorx.Field（如 query.tz、path.id），未匹配时默认为 body.<field>。
func (a *API) httpErrorFor(ctx context.Context, err error, locations map[string]string) huma.StatusError {
	se := FromError(err)
	if m, ok := se.(*ErrorModel); ok && m.Code != "" {
		m.Detail = httpx.ErrorDetail(ctx, err)
		// 业务错误指出了字段（errorx.Field）：同一句提示也作为这个字段的错误，表单把它显示在字段下面。
		if f := errorx.FieldOf(err); f != "" {
			loc := fieldLocation(f, locations)
			m.Errors = []*ErrorDetail{{ErrorDetail: huma.ErrorDetail{Message: m.Detail, Location: loc}, Code: m.Code}}
		}
		if m.Params == nil && len(errorx.PublicOf(err)) > 0 {
			a.logger.WarnContext(ctx, "public error params are not JSON-serializable; omitted from response", "code", m.Code)
		}
	}
	return se
}

// fieldLocation 返回业务错误字段 f 在 HTTP 请求中的完整位置：匹配已登记的 query/path/header/cookie 参数名时加上对应前缀，否则前缀为 body.。
func fieldLocation(f string, locations map[string]string) string {
	if prefix, ok := locations[f]; ok {
		return prefix + "." + f
	}
	if root, _, found := strings.Cut(f, "."); found {
		if prefix, ok := locations[root]; ok {
			return prefix + "." + f
		}
	}
	return "body." + f
}

// inputFieldLocations 扫描请求输入结构体类型 t（含匿名嵌入结构体），收集 query、path、header、cookie 参数名到位置前缀的映射。
func inputFieldLocations(t reflect.Type) map[string]string {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return nil
	}
	locs := map[string]string{}
	collectInputLocations(t, locs)
	if len(locs) == 0 {
		return nil
	}
	return locs
}

// collectInputLocations 递归展开结构体字段，记录非请求体参数的 tag 位置。
func collectInputLocations(t reflect.Type, locs map[string]string) {
	for i := range t.NumField() {
		f := t.Field(i)
		if f.Anonymous {
			ft := f.Type
			for ft.Kind() == reflect.Pointer {
				ft = ft.Elem()
			}
			if ft.Kind() == reflect.Struct {
				collectInputLocations(ft, locs)
				continue
			}
		}
		if !f.IsExported() || f.Name == "Body" || f.Name == "RawBody" {
			continue
		}
		for _, kind := range []string{"query", "path", "header", "cookie"} {
			if tag := f.Tag.Get(kind); tag != "" && tag != "-" {
				name, _, _ := strings.Cut(tag, ",")
				if name != "" {
					locs[name] = kind
				}
				break
			}
		}
	}
}

// writeError 按 Huma 的方式在中间件中写出错误：协商内容类型、执行 Transformer 后序列化。
func writeError(api huma.API, ctx huma.Context, se huma.StatusError) {
	ct, err := api.Negotiate(ctx.Header("Accept"))
	if err != nil {
		ct = "application/json"
	}
	if f, ok := se.(huma.ContentTypeFilter); ok {
		ct = f.ContentType(ct)
	}
	if he, ok := se.(huma.HeadersError); ok {
		for k, values := range he.GetHeaders() {
			for _, v := range values {
				ctx.AppendHeader(k, v)
			}
		}
	}
	status := se.GetStatus()
	body, err := api.Transform(ctx, strconv.Itoa(status), se)
	if err != nil {
		body = se
	}
	ctx.SetHeader("Content-Type", ct)
	ctx.SetStatus(status)
	_ = api.Marshal(ctx.BodyWriter(), ct, body)
}
