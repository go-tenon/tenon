package config

import (
	"fmt"
	"io"
	"log/slog"
)

// Secret 是会在打印与序列化时脱敏的字符串。
// Reveal 显式返回原值，日志与诊断使用 Secret 本身。
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

// MarshalText 返回脱敏文本，供 JSON 等文本编码器使用。
func (s Secret) MarshalText() ([]byte, error) { return []byte(s.String()), nil }

// MarshalYAML 供 YAML 编码器优先使用。
func (s Secret) MarshalYAML() (any, error) { return s.String(), nil }

// Format 对所有 fmt 格式动词输出脱敏文本。
func (s Secret) Format(state fmt.State, verb rune) {
	v := s.String()
	if verb == 'q' || (verb == 'v' && state.Flag('#')) {
		v = `"` + v + `"`
	}
	_, _ = io.WriteString(state, v)
}

// LogValue 返回供 slog 记录的脱敏值。
func (s Secret) LogValue() slog.Value { return slog.StringValue(s.String()) }
