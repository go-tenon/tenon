package identity

import "context"

// SubjectType 描述主体类别。
type SubjectType string

const (
	SubjectUser    SubjectType = "user"
	SubjectService SubjectType = "service"
)

// Subject 是已认证主体；ID 为不透明标识。
type Subject struct {
	ID     string
	Type   SubjectType
	Method string   // 认证方式，如 bearer
	Scopes []string // 已验证 scope
}

type ctxKey struct{}

// WithSubject 将主体写入 context。
func WithSubject(ctx context.Context, sub Subject) context.Context {
	return context.WithValue(ctx, ctxKey{}, sub)
}

// FromContext 读取主体。
func FromContext(ctx context.Context) (Subject, bool) {
	sub, ok := ctx.Value(ctxKey{}).(Subject)
	return sub, ok
}

// HasScope 报告是否具备指定 scope。
func (s Subject) HasScope(scope string) bool {
	for _, sc := range s.Scopes {
		if sc == scope {
			return true
		}
	}
	return false
}
