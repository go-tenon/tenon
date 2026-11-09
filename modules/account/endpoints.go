package account

import (
	"context"
	"errors"
	"net"
	"net/http"
	"time"
	"uuid"

	"github.com/danielgtaylor/huma/v2"

	"github.com/ryan053857/go-kit/httpx/auth"
	"github.com/ryan053857/go-kit/httpx/rest"
)

// 以下函数返回账号相关的 REST 接口，由产品选择路径挂载，例如：
//
//	v1 := huma.NewGroup(a.API(), "/api/v1")
//	rest.Post(v1, "/auth/register", account.RegisterEndpoint(users))
//	rest.Post(v1, "/auth/login", account.LoginEndpoint(users))
//	rest.Post(v1, "/auth/refresh", account.RefreshEndpoint(users))
//	rest.Post(v1, "/auth/logout", account.LogoutEndpoint(users))
//	rest.Post(v1, "/auth/forgot-password", account.ForgotPasswordEndpoint(users))
//	rest.Post(v1, "/auth/reset-password", account.ResetPasswordEndpoint(users))
//	rest.Get(v1, "/me", account.MeEndpoint(users))
//	rest.Patch(v1, "/me", account.UpdateMeEndpoint(users))
//	rest.Post(v1, "/me/password", account.ChangePasswordEndpoint(users))
//	rest.Get(v1, "/me/sessions", account.SessionsEndpoint(users))
//	rest.Delete(v1, "/me/sessions/{id}", account.RevokeSessionEndpoint(users))
//	rest.Put(v1, "/me/avatar", account.SetAvatarEndpoint(avatars))
//	rest.Delete(v1, "/me/avatar", account.RemoveAvatarEndpoint(avatars))
//	a.Router.Handle("/avatars/*", avatars.Handler())
//
// 需要登录的接口声明了 rest.RequireAuth()，网页会话（Cookie）与 access token（Bearer）都可以访问，
// 需要在 API 上挂载 rest.Authorize(api, users.VerifyAccessToken)。
// 登录、注册、找回密码等接口建议在产品中加上限流（如 github.com/go-chi/httprate）。

var tags = []string{"账号"}

// UserDTO 是返回给客户端的账号信息。
type UserDTO struct {
	ID          string    `json:"id" format:"uuid"`
	Email       string    `json:"email" format:"email"`
	Name        string    `json:"name"`
	AvatarURL   string    `json:"avatar_url,omitempty" doc:"头像地址，没有头像时省略"`
	CreatedAt   time.Time `json:"created_at"`
	LastLoginAt time.Time `json:"last_login_at,omitzero"`
}

// UserDTO 返回 u 的 UserDTO。
func (s *Service) UserDTO(u *User) UserDTO {
	return UserDTO{ID: u.ID.String(), Email: u.Email, Name: u.Name, AvatarURL: s.AvatarURL(u),
		CreatedAt: u.CreatedAt, LastLoginAt: u.LastLoginAt}
}

// TokensDTO 是登录、刷新返回的凭证。
type TokensDTO struct {
	TokenType        string    `json:"token_type" example:"Bearer"`
	AccessToken      string    `json:"access_token" doc:"放在 Authorization: Bearer 头中访问接口"`
	ExpiresIn        int       `json:"expires_in" doc:"access_token 剩余有效秒数"`
	RefreshToken     string    `json:"refresh_token" doc:"access_token 过期后用于换取新凭证，每次刷新后旧值失效"`
	RefreshExpiresAt time.Time `json:"refresh_expires_at"`
}

func toTokensDTO(t *Tokens) TokensDTO {
	return TokensDTO{
		TokenType:        "Bearer",
		AccessToken:      t.AccessToken,
		ExpiresIn:        int(time.Until(t.AccessExpiresAt).Seconds()),
		RefreshToken:     t.RefreshToken,
		RefreshExpiresAt: t.RefreshExpiresAt,
	}
}

// SessionDTO 是一个登录会话（登录设备）。
type SessionDTO struct {
	ID         string    `json:"id" format:"uuid"`
	Kind       string    `json:"kind" enum:"web,api" doc:"web 为网页登录，api 为 App 等客户端登录"`
	IP         string    `json:"ip"`
	UserAgent  string    `json:"user_agent"`
	CreatedAt  time.Time `json:"created_at"`
	LastSeenAt time.Time `json:"last_seen_at"`
	ExpiresAt  time.Time `json:"expires_at"`
	Current    bool      `json:"current" doc:"是否为发起本次请求的会话"`
}

// clientInput 从请求中取出 IP 与 User-Agent，嵌入到需要它们的输入中。
type clientInput struct {
	UserAgent string `header:"User-Agent"`
	ip        string
}

func (c *clientInput) Resolve(ctx huma.Context) []error {
	c.ip = ctx.RemoteAddr()
	if host, _, err := net.SplitHostPort(c.ip); err == nil {
		c.ip = host
	}
	return nil
}

func (c *clientInput) client() ClientInfo { return ClientInfo{IP: c.ip, UserAgent: c.UserAgent} }

type userOutput struct{ Body UserDTO }

type tokensOutput struct{ Body TokensDTO }

// mapError 把领域错误转换为 HTTP 错误，其他错误原样返回（500）。
func mapError(err error) error {
	switch {
	case errors.Is(err, ErrInvalidCredentials):
		return huma.Error401Unauthorized("邮箱或密码错误")
	case errors.Is(err, ErrUserDisabled):
		return huma.Error403Forbidden("账号已停用")
	case errors.Is(err, ErrEmailTaken):
		return huma.Error409Conflict("邮箱已被注册")
	case errors.Is(err, ErrWeakPassword):
		return huma.Error422UnprocessableEntity("密码太短")
	case errors.Is(err, ErrInvalidToken):
		return huma.Error401Unauthorized("凭证无效或已过期")
	case errors.Is(err, ErrNotFound):
		return huma.Error404NotFound("不存在")
	case errors.Is(err, ErrInvalidImage):
		return huma.Error422UnprocessableEntity("请上传 JPEG、PNG、GIF 或 WebP 图片（不超过 10 MB）")
	}
	return err
}

func principal(ctx context.Context) (*Principal, error) {
	p, ok := auth.From[*Principal](ctx)
	if !ok { // rest.Authorize 未挂载或接口未声明 Security
		return nil, huma.Error401Unauthorized("未登录")
	}
	return p, nil
}

type registerInput struct {
	Body struct {
		Email    string `json:"email" format:"email" maxLength:"254"`
		Password string `json:"password" minLength:"1" maxLength:"128"`
		Name     string `json:"name,omitempty" maxLength:"100"`
	}
}

// RegisterEndpoint 注册账号，成功返回 201 与账号信息（不自动登录）。
func RegisterEndpoint(s *Service) rest.Endpoint[registerInput, userOutput] {
	return rest.Endpoint[registerInput, userOutput]{
		Operation: huma.Operation{OperationID: "account-register", Summary: "注册", Tags: tags, DefaultStatus: http.StatusCreated},
		Handler: func(ctx context.Context, in *registerInput) (*userOutput, error) {
			u, err := s.Register(ctx, in.Body.Email, in.Body.Password, in.Body.Name)
			if err != nil {
				return nil, mapError(err)
			}
			return &userOutput{Body: s.UserDTO(u)}, nil
		},
	}
}

type loginInput struct {
	clientInput
	Body struct {
		Email    string `json:"email" maxLength:"254"`
		Password string `json:"password" maxLength:"128"`
		Remember bool   `json:"remember,omitempty" doc:"记住我：refresh_token 有效期更长"`
	}
}

// LoginEndpoint 用邮箱密码登录，返回 access token 与 refresh token。
func LoginEndpoint(s *Service) rest.Endpoint[loginInput, tokensOutput] {
	return rest.Endpoint[loginInput, tokensOutput]{
		Operation: huma.Operation{OperationID: "account-login", Summary: "登录", Tags: tags},
		Handler: func(ctx context.Context, in *loginInput) (*tokensOutput, error) {
			t, err := s.Login(ctx, in.Body.Email, in.Body.Password, in.Body.Remember, in.client())
			if err != nil {
				return nil, mapError(err)
			}
			return &tokensOutput{Body: toTokensDTO(t)}, nil
		},
	}
}

type refreshInput struct {
	clientInput
	Body struct {
		RefreshToken string `json:"refresh_token" maxLength:"128"`
	}
}

// RefreshEndpoint 用 refresh token 换取新的凭证。
func RefreshEndpoint(s *Service) rest.Endpoint[refreshInput, tokensOutput] {
	return rest.Endpoint[refreshInput, tokensOutput]{
		Operation: huma.Operation{OperationID: "account-refresh", Summary: "刷新凭证", Tags: tags},
		Handler: func(ctx context.Context, in *refreshInput) (*tokensOutput, error) {
			t, err := s.Refresh(ctx, in.Body.RefreshToken, in.client())
			if err != nil {
				return nil, mapError(err)
			}
			return &tokensOutput{Body: toTokensDTO(t)}, nil
		},
	}
}

// LogoutEndpoint 注销当前会话。access token 在过期前仍然有效（除非开启 Config.CheckSession），
// 客户端应同时丢弃本地保存的凭证。
func LogoutEndpoint(s *Service) rest.Endpoint[struct{}, struct{}] {
	return rest.Endpoint[struct{}, struct{}]{
		Operation: huma.Operation{OperationID: "account-logout", Summary: "退出登录", Tags: tags,
			DefaultStatus: http.StatusNoContent, Security: rest.RequireAuth()},
		Handler: func(ctx context.Context, _ *struct{}) (*struct{}, error) {
			p, err := principal(ctx)
			if err != nil {
				return nil, err
			}
			if err := s.RevokeSession(ctx, p.ID, p.SessionID); err != nil && !errors.Is(err, ErrNotFound) {
				return nil, err
			}
			return nil, nil
		},
	}
}

type emailInput struct {
	Body struct {
		Email string `json:"email" maxLength:"254"`
	}
}

// ForgotPasswordEndpoint 请求重置密码。无论邮箱是否存在都返回 202，避免泄露注册信息。
func ForgotPasswordEndpoint(s *Service) rest.Endpoint[emailInput, struct{}] {
	return rest.Endpoint[emailInput, struct{}]{
		Operation: huma.Operation{OperationID: "account-forgot-password", Summary: "忘记密码", Tags: tags,
			DefaultStatus: http.StatusAccepted, Description: "邮箱已注册时发送重置密码邮件。"},
		Handler: func(ctx context.Context, in *emailInput) (*struct{}, error) {
			return nil, s.RequestPasswordReset(ctx, in.Body.Email)
		},
	}
}

type resetInput struct {
	Body struct {
		Token       string `json:"token" maxLength:"128" doc:"重置邮件链接中的 token"`
		NewPassword string `json:"new_password" minLength:"1" maxLength:"128"`
	}
}

// ResetPasswordEndpoint 用重置邮件中的 token 设置新密码，成功后该账号的全部会话被注销。
func ResetPasswordEndpoint(s *Service) rest.Endpoint[resetInput, struct{}] {
	return rest.Endpoint[resetInput, struct{}]{
		Operation: huma.Operation{OperationID: "account-reset-password", Summary: "重置密码", Tags: tags,
			DefaultStatus: http.StatusNoContent},
		Handler: func(ctx context.Context, in *resetInput) (*struct{}, error) {
			if err := s.ResetPassword(ctx, in.Body.Token, in.Body.NewPassword); err != nil {
				if errors.Is(err, ErrInvalidToken) {
					return nil, huma.Error400BadRequest("链接无效或已过期")
				}
				return nil, mapError(err)
			}
			return nil, nil
		},
	}
}

// MeEndpoint 返回当前账号信息。
func MeEndpoint(s *Service) rest.Endpoint[struct{}, userOutput] {
	return rest.Endpoint[struct{}, userOutput]{
		Operation: huma.Operation{OperationID: "account-me", Summary: "当前账号", Tags: tags, Security: rest.RequireAuth()},
		Handler: func(ctx context.Context, _ *struct{}) (*userOutput, error) {
			p, err := principal(ctx)
			if err != nil {
				return nil, err
			}
			u, err := s.User(ctx, p.ID)
			if err != nil {
				return nil, mapError(err)
			}
			return &userOutput{Body: s.UserDTO(u)}, nil
		},
	}
}

type updateMeInput struct {
	Body struct {
		Name string `json:"name" maxLength:"100"`
	}
}

// UpdateMeEndpoint 修改当前账号的名称。
func UpdateMeEndpoint(s *Service) rest.Endpoint[updateMeInput, userOutput] {
	return rest.Endpoint[updateMeInput, userOutput]{
		Operation: huma.Operation{OperationID: "account-update-me", Summary: "修改账号信息", Tags: tags, Security: rest.RequireAuth()},
		Handler: func(ctx context.Context, in *updateMeInput) (*userOutput, error) {
			p, err := principal(ctx)
			if err != nil {
				return nil, err
			}
			u, err := s.UpdateName(ctx, p.ID, in.Body.Name)
			if err != nil {
				return nil, mapError(err)
			}
			return &userOutput{Body: s.UserDTO(u)}, nil
		},
	}
}

type changePasswordInput struct {
	Body struct {
		CurrentPassword string `json:"current_password" maxLength:"128"`
		NewPassword     string `json:"new_password" minLength:"1" maxLength:"128"`
	}
}

// ChangePasswordEndpoint 修改密码，成功后除当前会话外的其他会话全部注销。
func ChangePasswordEndpoint(s *Service) rest.Endpoint[changePasswordInput, struct{}] {
	return rest.Endpoint[changePasswordInput, struct{}]{
		Operation: huma.Operation{OperationID: "account-change-password", Summary: "修改密码", Tags: tags,
			DefaultStatus: http.StatusNoContent, Security: rest.RequireAuth()},
		Handler: func(ctx context.Context, in *changePasswordInput) (*struct{}, error) {
			p, err := principal(ctx)
			if err != nil {
				return nil, err
			}
			err = s.ChangePassword(ctx, p.ID, p.SessionID, in.Body.CurrentPassword, in.Body.NewPassword)
			if errors.Is(err, ErrInvalidCredentials) {
				return nil, huma.Error422UnprocessableEntity("当前密码错误")
			}
			return nil, mapError(err)
		},
	}
}

type sessionsOutput struct{ Body []SessionDTO }

// SessionsEndpoint 列出当前账号的登录会话（登录设备）。
func SessionsEndpoint(s *Service) rest.Endpoint[struct{}, sessionsOutput] {
	return rest.Endpoint[struct{}, sessionsOutput]{
		Operation: huma.Operation{OperationID: "account-sessions", Summary: "登录设备", Tags: tags, Security: rest.RequireAuth()},
		Handler: func(ctx context.Context, _ *struct{}) (*sessionsOutput, error) {
			p, err := principal(ctx)
			if err != nil {
				return nil, err
			}
			list, err := s.Sessions(ctx, p.ID)
			if err != nil {
				return nil, err
			}
			out := &sessionsOutput{Body: make([]SessionDTO, 0, len(list))}
			for _, x := range list {
				out.Body = append(out.Body, SessionDTO{
					ID: x.ID.String(), Kind: x.Kind, IP: x.IP, UserAgent: x.UserAgent,
					CreatedAt: x.CreatedAt, LastSeenAt: x.LastSeenAt, ExpiresAt: x.ExpiresAt,
					Current: x.ID == p.SessionID,
				})
			}
			return out, nil
		},
	}
}

type revokeSessionInput struct {
	ID string `path:"id" format:"uuid"`
}

// RevokeSessionEndpoint 注销当前账号的某个会话（踢下线）。
func RevokeSessionEndpoint(s *Service) rest.Endpoint[revokeSessionInput, struct{}] {
	return rest.Endpoint[revokeSessionInput, struct{}]{
		Operation: huma.Operation{OperationID: "account-revoke-session", Summary: "注销登录设备", Tags: tags,
			DefaultStatus: http.StatusNoContent, Security: rest.RequireAuth()},
		Handler: func(ctx context.Context, in *revokeSessionInput) (*struct{}, error) {
			p, err := principal(ctx)
			if err != nil {
				return nil, err
			}
			id, err := uuid.Parse(in.ID)
			if err != nil {
				return nil, huma.Error404NotFound("不存在")
			}
			return nil, mapError(s.RevokeSession(ctx, p.ID, id))
		},
	}
}

type avatarInput struct {
	RawBody huma.MultipartFormFiles[struct {
		File huma.FormFile `form:"file" required:"true" doc:"头像图片：JPEG、PNG、GIF 或 WebP，不超过 10 MB"`
	}]
}

// SetAvatarEndpoint 上传头像（multipart/form-data，字段名 file），返回更新后的账号信息。
func SetAvatarEndpoint(a *Avatars) rest.Endpoint[avatarInput, userOutput] {
	return rest.Endpoint[avatarInput, userOutput]{
		Operation: huma.Operation{OperationID: "account-set-avatar", Summary: "上传头像", Tags: tags,
			Security: rest.RequireAuth(), MaxBodyBytes: MaxAvatarBytes + 1<<20},
		Handler: func(ctx context.Context, in *avatarInput) (*userOutput, error) {
			p, err := principal(ctx)
			if err != nil {
				return nil, err
			}
			f := in.RawBody.Data().File
			defer f.Close()
			u, err := a.Set(ctx, p.ID, f)
			if err != nil {
				return nil, mapError(err)
			}
			return &userOutput{Body: a.users.UserDTO(u)}, nil
		},
	}
}

// RemoveAvatarEndpoint 删除头像，返回更新后的账号信息。
func RemoveAvatarEndpoint(a *Avatars) rest.Endpoint[struct{}, userOutput] {
	return rest.Endpoint[struct{}, userOutput]{
		Operation: huma.Operation{OperationID: "account-remove-avatar", Summary: "删除头像", Tags: tags, Security: rest.RequireAuth()},
		Handler: func(ctx context.Context, _ *struct{}) (*userOutput, error) {
			p, err := principal(ctx)
			if err != nil {
				return nil, err
			}
			u, err := a.Remove(ctx, p.ID)
			if err != nil {
				return nil, mapError(err)
			}
			return &userOutput{Body: a.users.UserDTO(u)}, nil
		},
	}
}
