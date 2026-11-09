package user

import (
	"context"
	"time"
	"uuid"

	"github.com/danielgtaylor/huma/v2"
	"github.com/uptrace/bun"

	"github.com/ryan053857/go-kit/httpx/auth"
	"github.com/ryan053857/go-kit/httpx/rest"
)

var tags = []string{"user"}

// DTO 是返回给客户端的当前用户信息。
type DTO struct {
	ID            string    `json:"id" format:"uuid"`
	Email         string    `json:"email" format:"email"`
	Name          string    `json:"name"`
	AvatarURL     string    `json:"avatar_url,omitempty"`
	EmailVerified bool      `json:"email_verified"`
	HasPassword   bool      `json:"has_password"`
	Locale        string    `json:"locale,omitempty"`
	CreatedAt     time.Time `json:"created_at"`
}

// UserDTO 返回 u 的 DTO。
func (s *Store) UserDTO(u *User) DTO {
	return DTO{ID: u.ID.String(), Email: u.Email, Name: u.Name, AvatarURL: s.accounts.AvatarURL(u.AvatarKey),
		EmailVerified: u.EmailVerified(), HasPassword: u.HasPassword(), Locale: u.Locale, CreatedAt: u.CreatedAt}
}

// RegisterInput 是注册接口的输入：产品决定注册时填哪些字段。
type RegisterInput struct {
	Body struct {
		Email    string `json:"email" format:"email" maxLength:"254"`
		Password string `json:"password" minLength:"1" maxLength:"128"`
		Name     string `json:"name,omitempty" maxLength:"100"`
	}
}

// CreateFromAPI 是注册接口的产品逻辑，挂载：
//
//	rest.Post(v1, "/auth/register", account.RegisterEndpoint(users, people.CreateFromAPI))
func (s *Store) CreateFromAPI(ctx context.Context, tx bun.Tx, in *RegisterInput) (uuid.UUID, error) {
	return s.create(ctx, tx, in.Body.Email, in.Body.Password, in.Body.Name)
}

type meOutput struct{ Body DTO }

// MeEndpoint 返回当前用户。
func (s *Store) MeEndpoint() rest.Endpoint[struct{}, meOutput] {
	return rest.Endpoint[struct{}, meOutput]{
		Operation: huma.Operation{OperationID: "user-me", Summary: "当前用户", Tags: tags, Security: rest.RequireAuth()},
		Handler: func(ctx context.Context, _ *struct{}) (*meOutput, error) {
			id, ok := auth.UserID(ctx)
			if !ok {
				return nil, auth.ErrUnauthenticated
			}
			u, err := s.Get(ctx, id)
			if err != nil {
				return nil, err
			}
			return &meOutput{Body: s.UserDTO(u)}, nil
		},
	}
}

type updateMeInput struct {
	Body struct {
		Name string `json:"name" maxLength:"100"`
	}
}

// UpdateMeEndpoint 修改当前用户的资料。
func (s *Store) UpdateMeEndpoint() rest.Endpoint[updateMeInput, meOutput] {
	return rest.Endpoint[updateMeInput, meOutput]{
		Operation: huma.Operation{OperationID: "user-update-me", Summary: "修改资料", Tags: tags, Security: rest.RequireAuth()},
		Handler: func(ctx context.Context, in *updateMeInput) (*meOutput, error) {
			id, ok := auth.UserID(ctx)
			if !ok {
				return nil, auth.ErrUnauthenticated
			}
			u, err := s.UpdateName(ctx, id, in.Body.Name)
			if err != nil {
				return nil, err
			}
			return &meOutput{Body: s.UserDTO(u)}, nil
		},
	}
}
