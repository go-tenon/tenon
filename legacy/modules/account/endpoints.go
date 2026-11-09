package account

import (
	"context"
	"net"
	"net/http"
	"time"
	"uuid"

	"github.com/cockroachdb/errors"
	"github.com/uptrace/bun"

	"github.com/danielgtaylor/huma/v2"

	"github.com/ryan053857/go-kit/httpx/auth"
	"github.com/ryan053857/go-kit/httpx/rest"
)

// 以下函数返回账号相关的 REST 接口，由产品选择路径挂载，例如：
//
//	v1 := huma.NewGroup(a.API(), "/api/v1")
//	rest.Post(v1, "/auth/register", account.RegisterEndpoint(users, createUser)) // createUser 由产品实现
//	rest.Post(v1, "/auth/login", account.LoginEndpoint(users))
//	rest.Post(v1, "/auth/refresh", account.RefreshEndpoint(users))
//	rest.Post(v1, "/auth/logout", account.LogoutEndpoint(users))
//	rest.Post(v1, "/auth/forgot-password", account.ForgotPasswordEndpoint(users))
//	rest.Post(v1, "/auth/reset-password", account.ResetPasswordEndpoint(users))
//	rest.Post(v1, "/me/password", account.ChangePasswordEndpoint(users))
//	rest.Put(v1, "/me/locale", account.SetLocaleEndpoint(users))
//	rest.Get(v1, "/me/sessions", account.SessionsEndpoint(users))
//	rest.Delete(v1, "/me/sessions/{id}", account.RevokeSessionEndpoint(users))
//	rest.Put(v1, "/me/avatar", account.SetAvatarEndpoint(avatars))
//	rest.Delete(v1, "/me/avatar", account.RemoveAvatarEndpoint(avatars))
//	a.Router.Handle("/avatars/*", avatars.Handler())
//
// 邮箱验证、两步验证、API Key 与第三方账号的接口见 endpoints_security.go。
// “当前用户”“修改资料”等与用户字段有关的接口由产品实现。
//
// 需要登录的接口声明了 rest.RequireAuth()，网页会话（Cookie）、access token 与 API Key（Bearer）都可以访问，
// 需要在 API 上挂载 rest.Authorize(api, users.VerifyAccessToken, a.Gate)。修改密码、登录设备、两步验证、
// API Key 管理等敏感接口不接受 API Key。
// 登录、注册、找回密码、验证码等接口应在产品中限流（rest.RateLimit）。
//
// 处理函数直接返回领域错误，由 rest.NewErrorWithContext 转换为对应的状态码与多语言提示。

var tags = []string{"账号"}

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

type tokensOutput struct{ Body TokensDTO }

// LoginDTO 是登录的结果：要么直接得到凭证，要么需要两步验证（mfa_required 为 true，
// 此时只返回 mfa_token，让用户输入验证码后调用两步验证登录接口）。
type LoginDTO struct {
	TokensDTO
	MFARequired bool   `json:"mfa_required,omitempty" doc:"为 true 时需要两步验证，凭证字段为空"`
	MFAToken    string `json:"mfa_token,omitempty" doc:"两步验证登录时提交，5 分钟内有效"`
}

type loginOutput struct{ Body LoginDTO }

func principal(ctx context.Context) (*Principal, error) {
	p, ok := auth.From[*Principal](ctx)
	if !ok { // rest.Authorize 未挂载或接口未声明 Security
		return nil, auth.ErrUnauthenticated
	}
	return p, nil
}

// userPrincipal 用于敏感接口：只接受网页会话与 access token，不接受 API Key。
func userPrincipal(ctx context.Context) (*Principal, error) {
	p, err := principal(ctx)
	if err == nil && p.IsAPIKey() {
		return nil, ErrAPIKeyNotAllowed
	}
	return p, err
}

type registeredOutput struct {
	Body struct {
		ID string `json:"id" format:"uuid" doc:"新用户的 ID"`
	}
}

// RegisterEndpoint 注册账号，成功返回 201 与用户 ID（不自动登录）。输入 I 由产品定义（huma 校验其字段），
// create 由产品实现：校验并写 users 表，见 Service.Register。
//
//	type registerInput struct {
//		Body struct {
//			Email    string `json:"email" format:"email" maxLength:"254"`
//			Password string `json:"password" maxLength:"128"`
//			Name     string `json:"name" maxLength:"100"`
//		}
//	}
//	rest.Post(v1, "/auth/register", account.RegisterEndpoint(users,
//		func(ctx context.Context, tx bun.Tx, in *registerInput) (uuid.UUID, error) { ... }))
func RegisterEndpoint[I any](s *Service, create func(ctx context.Context, tx bun.Tx, in *I) (uuid.UUID, error)) rest.Endpoint[I, registeredOutput] {
	return rest.Endpoint[I, registeredOutput]{
		Operation: huma.Operation{OperationID: "account-register", Summary: "注册", Tags: tags, DefaultStatus: http.StatusCreated},
		Handler: func(ctx context.Context, in *I) (*registeredOutput, error) {
			id, err := s.Register(ctx, func(ctx context.Context, tx bun.Tx) (uuid.UUID, error) { return create(ctx, tx, in) })
			if err != nil {
				return nil, err
			}
			out := &registeredOutput{}
			out.Body.ID = id.String()
			return out, nil
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

// LoginEndpoint 用邮箱密码登录，返回 access token 与 refresh token；
// 账号启用了两步验证时返回 mfa_required 与 mfa_token。
func LoginEndpoint(s *Service) rest.Endpoint[loginInput, loginOutput] {
	return rest.Endpoint[loginInput, loginOutput]{
		Operation: huma.Operation{OperationID: "account-login", Summary: "登录", Tags: tags},
		Handler: func(ctx context.Context, in *loginInput) (*loginOutput, error) {
			t, err := s.Login(ctx, in.Body.Email, in.Body.Password, in.Body.Remember, in.client())
			var mfaErr *MFARequiredError
			if errors.As(err, &mfaErr) {
				return &loginOutput{Body: LoginDTO{MFARequired: true, MFAToken: mfaErr.Challenge}}, nil
			}
			if err != nil {
				return nil, err
			}
			return &loginOutput{Body: LoginDTO{TokensDTO: toTokensDTO(t)}}, nil
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
				return nil, err
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
			return nil, s.Logout(ctx, p)
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
			return nil, s.ResetPassword(ctx, in.Body.Token, in.Body.NewPassword)
		},
	}
}

type localeInput struct {
	Body struct {
		Locale string `json:"locale" maxLength:"35" doc:"语言标签，如 zh-CN、en；空串表示跟随请求的 Accept-Language"`
	}
}

// SetLocaleEndpoint 设置当前用户的语言，邮件与通知使用它。
func SetLocaleEndpoint(s *Service) rest.Endpoint[localeInput, struct{}] {
	return rest.Endpoint[localeInput, struct{}]{
		Operation: huma.Operation{OperationID: "account-set-locale", Summary: "设置语言", Tags: tags,
			DefaultStatus: http.StatusNoContent, Security: rest.RequireAuth()},
		Handler: func(ctx context.Context, in *localeInput) (*struct{}, error) {
			p, err := userPrincipal(ctx)
			if err != nil {
				return nil, err
			}
			return nil, s.SetLocale(ctx, p.ID, in.Body.Locale)
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
			p, err := userPrincipal(ctx)
			if err != nil {
				return nil, err
			}
			return nil, s.ChangePassword(ctx, p.ID, p.SessionID, in.Body.CurrentPassword, in.Body.NewPassword)
		},
	}
}

type sessionsOutput struct{ Body []SessionDTO }

// SessionsEndpoint 列出当前账号的登录会话（登录设备）。
func SessionsEndpoint(s *Service) rest.Endpoint[struct{}, sessionsOutput] {
	return rest.Endpoint[struct{}, sessionsOutput]{
		Operation: huma.Operation{OperationID: "account-sessions", Summary: "登录设备", Tags: tags, Security: rest.RequireAuth()},
		Handler: func(ctx context.Context, _ *struct{}) (*sessionsOutput, error) {
			p, err := userPrincipal(ctx)
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
			p, err := userPrincipal(ctx)
			if err != nil {
				return nil, err
			}
			id, err := uuid.Parse(in.ID)
			if err != nil {
				return nil, ErrNotFound
			}
			return nil, s.RevokeSession(ctx, p.ID, id)
		},
	}
}

type avatarInput struct {
	RawBody huma.MultipartFormFiles[struct {
		File huma.FormFile `form:"file" required:"true" doc:"头像图片：JPEG、PNG、GIF 或 WebP，不超过 10 MB"`
	}]
}

type avatarOutput struct {
	Body struct {
		AvatarURL string `json:"avatar_url" doc:"新头像的地址"`
	}
}

// SetAvatarEndpoint 上传头像（multipart/form-data，字段名 file），返回新头像的地址。
func SetAvatarEndpoint(a *Avatars) rest.Endpoint[avatarInput, avatarOutput] {
	return rest.Endpoint[avatarInput, avatarOutput]{
		Operation: huma.Operation{OperationID: "account-set-avatar", Summary: "上传头像", Tags: tags,
			Security: rest.RequireAuth(), MaxBodyBytes: MaxAvatarBytes + 1<<20},
		Handler: func(ctx context.Context, in *avatarInput) (*avatarOutput, error) {
			p, err := principal(ctx)
			if err != nil {
				return nil, err
			}
			f := in.RawBody.Data().File
			defer f.Close()
			u, err := a.Set(ctx, p.ID, f)
			if err != nil {
				return nil, err
			}
			out := &avatarOutput{}
			out.Body.AvatarURL = a.users.AvatarURL(u.AvatarKey)
			return out, nil
		},
	}
}

// RemoveAvatarEndpoint 删除头像。
func RemoveAvatarEndpoint(a *Avatars) rest.Endpoint[struct{}, struct{}] {
	return rest.Endpoint[struct{}, struct{}]{
		Operation: huma.Operation{OperationID: "account-remove-avatar", Summary: "删除头像", Tags: tags,
			DefaultStatus: http.StatusNoContent, Security: rest.RequireAuth()},
		Handler: func(ctx context.Context, _ *struct{}) (*struct{}, error) {
			p, err := principal(ctx)
			if err != nil {
				return nil, err
			}
			_, err = a.Remove(ctx, p.ID)
			return nil, err
		},
	}
}
