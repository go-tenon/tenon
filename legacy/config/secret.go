package config

// Secret is a string that hides its value when printed or serialized, so a
// whole configuration can be logged or dumped without leaking credentials.
// Convert it with string(s) where the real value is needed.
//
// It is decoded from YAML like a normal string. When printed with fmt, or
// marshaled to YAML, JSON or text, a non-empty value is shown as "******"
// and an empty value stays empty, so it remains visible whether it was set.
type Secret string

const secretMask = "******"

func (s Secret) String() string {
	if s == "" {
		return ""
	}
	return secretMask
}

// GoString masks the value for the %#v verb as well.
func (s Secret) GoString() string { return `"` + s.String() + `"` }

// MarshalText masks the value for encoding/json, log/slog and YAML.
func (s Secret) MarshalText() ([]byte, error) { return []byte(s.String()), nil }

// MarshalYAML masks the value for YAML encoders that check this interface
// before encoding.TextMarshaler.
func (s Secret) MarshalYAML() (any, error) { return s.String(), nil }
