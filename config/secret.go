package config

import (
	"fmt"
	"io"
	"log/slog"
)

// Secret 是会在打印与序列化时脱敏的字符串。
// 真实值只通过 [Secret.Reveal] 取得，不要依赖类型转换以外的隐式出口用于日志。
type Secret string

const secretMask = "******"

// Reveal 返回原始密钥值。
func (s Secret) Reveal() string { return string(s) }

// String 脱敏；空值仍显示为空，以便区分“未设置”。
func (s Secret) String() string {
	if s == "" {
		return ""
	}
	return secretMask
}

// GoString 覆盖 %#v。
func (s Secret) GoString() string { return `"` + s.String() + `"` }

// MarshalText 供 encoding/json、log/slog 等使用。
func (s Secret) MarshalText() ([]byte, error) { return []byte(s.String()), nil }

// MarshalYAML 供 YAML 编码器优先使用。
func (s Secret) MarshalYAML() (any, error) { return s.String(), nil }

// Format masks all verbs, including hexadecimal formatting.
func (s Secret) Format(state fmt.State, verb rune) {
	v := s.String()
	if verb == 'q' || (verb == 'v' && state.Flag('#')) {
		v = `"` + v + `"`
	}
	_, _ = io.WriteString(state, v)
}
func (s Secret) LogValue() slog.Value { return slog.StringValue(s.String()) }
