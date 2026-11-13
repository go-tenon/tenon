package auth

import (
	"context"
	"errors"

	"example.com/blog/internal/user"
	"github.com/go-tenon/tenon/errorx"
	"github.com/go-tenon/tenon/httpx/rest"
	"github.com/go-tenon/tenon/identity"
)

var ErrUnauthenticated = errorx.Define(errorx.Unauthenticated, "auth.unauthenticated")

// Credential 是一个 API token 及其代表的调用者。
type Credential struct {
	Token       string   `yaml:"token"`
	SubjectID   string   `yaml:"subject_id"`
	Permissions []string `yaml:"permissions"`
}

type Config struct {
	Credentials []Credential `yaml:"credentials"`
}

func (c Config) Validate() error {
	seen := make(map[string]bool, len(c.Credentials))
	for _, cred := range c.Credentials {
		if cred.Token == "" || cred.SubjectID == "" {
			return errors.New("credentials: token and subject_id are required")
		}
		if seen[cred.Token] {
			return errors.New("credentials: duplicate token")
		}
		seen[cred.Token] = true
	}
	return nil
}

// Validator 返回一个在配置的凭据中查找 token 的校验器。
func Validator(cfg Config) rest.TokenValidator {
	byToken := make(map[string]Credential, len(cfg.Credentials))
	for _, cred := range cfg.Credentials {
		byToken[cred.Token] = cred
	}
	return func(_ context.Context, token string) (identity.Subject, error) {
		cred, ok := byToken[token]
		if !ok {
			return identity.Subject{}, ErrUnauthenticated.New()
		}
		return identity.Subject{
			Type:        user.Type,
			ID:          cred.SubjectID,
			Method:      "bearer",
			Permissions: cred.Permissions,
		}, nil
	}
}
