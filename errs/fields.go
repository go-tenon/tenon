package errs

import (
	"maps"
	"slices"
	"strings"
)

// FieldErrors 表示字段级校验错误，key 为字段路径（如 "email"、"items[0].qty"），
// value 为可以展示给用户的文案。
//
// 它本身就是 error，CodeOf 将其归为 InvalidArgument，可以直接返回：
//
//	fe := errs.FieldErrors{}
//	if exists {
//		fe["email"] = "邮箱已被使用"
//	}
//	if len(fe) > 0 {
//		return fe
//	}
//
// 需要整体文案时再包一层：errs.Wrap(fe, errs.InvalidArgument, "表单填写有误")。
// 字段路径不含 "body."、"query." 等前缀，由传输层按来源补全。
type FieldErrors map[string]string

// Error 按字段名排序输出，保证日志稳定。
func (fe FieldErrors) Error() string {
	if len(fe) == 0 {
		return "invalid argument"
	}
	var b strings.Builder
	for i, k := range slices.Sorted(maps.Keys(fe)) {
		if i > 0 {
			b.WriteString("; ")
		}
		b.WriteString(k)
		b.WriteString(": ")
		b.WriteString(fe[k])
	}
	return b.String()
}
