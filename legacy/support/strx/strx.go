// Package strx 提供 strings 与 lo 都没有的字符串函数，目前是脱敏。
//
// 截断、命名风格转换等请直接使用 lo（lo.Ellipsis、lo.SnakeCase、lo.CamelCase …）。
//
// 长度都按字符（rune）计算，中文可以正确处理；
// 不处理由多个 rune 组成的字形（如带肤色修饰的 emoji）。
//
//	strx.Mask("13812345678", 3, 4)      // "138****5678"
//	strx.MaskEmail("alice@example.com") // "a****@example.com"
package strx

import (
	"strings"
	"unicode/utf8"
)

// Mask 保留 s 开头 keepStart 个和结尾 keepEnd 个字符，其余每个字符替换为 '*'。
// 保留部分已覆盖全部字符时，为避免泄露，整个字符串都会被替换，
// 例如 Mask("abc", 2, 2) 返回 "***"。负数按 0 处理。
//
//	Mask("13812345678", 3, 4)        // 手机号  "138****5678"
//	Mask("110101199003071234", 6, 4) // 身份证  "110101********1234"
//	Mask("张三丰", 1, 0)             // 姓名    "张**"
func Mask(s string, keepStart, keepEnd int) string {
	keepStart, keepEnd = max(keepStart, 0), max(keepEnd, 0)
	n := utf8.RuneCountInString(s)
	if keepStart+keepEnd >= n {
		keepStart, keepEnd = 0, 0
	}
	var b strings.Builder
	b.Grow(len(s))
	i := 0
	for _, r := range s {
		if i < keepStart || i >= n-keepEnd {
			b.WriteRune(r)
		} else {
			b.WriteByte('*')
		}
		i++
	}
	return b.String()
}

// MaskEmail 只保留邮箱用户名的第一个字符和完整域名，
// 例如 "alice@example.com" 返回 "a****@example.com"。
// 用户名只有一个字符时整体替换；s 不含 '@' 时按 Mask(s, 1, 0) 处理。
func MaskEmail(s string) string {
	at := strings.LastIndexByte(s, '@')
	if at < 0 {
		return Mask(s, 1, 0)
	}
	return Mask(s[:at], 1, 0) + s[at:]
}
