package account

import (
	"context"
	"net/http"
	"time"
	"uuid"

	"github.com/danielgtaylor/huma/v2"

	"github.com/ryan053857/go-kit/httpx/rest"
)

// 邮箱验证、两步验证、API Key 与第三方账号的接口，挂载示例：
//
//	rest.Post(v1, "/auth/login/mfa", account.LoginMFAEndpoint(users))
//	rest.Post(v1, "/auth/verify-email", account.VerifyEmailEndpoint(users))
//	rest.Post(v1, "/me/email/verification", account.SendVerificationEmailEndpoint(users))
//	rest.Get(v1, "/me/mfa", account.MFAStatusEndpoint(users))
//	rest.Post(v1, "/me/mfa/setup", account.SetupMFAEndpoint(users))
//	rest.Post(v1, "/me/mfa/enable", account.EnableMFAEndpoint(users))
//	rest.Post(v1, "/me/mfa/disable", account.DisableMFAEndpoint(users))
//	rest.Post(v1, "/me/mfa/recovery-codes", account.RegenerateRecoveryCodesEndpoint(users))
//	rest.Get(v1, "/me/api-keys", account.APIKeysEndpoint(users))
//	rest.Post(v1, "/me/api-keys", account.CreateAPIKeyEndpoint(users))
//	rest.Delete(v1, "/me/api-keys/{id}", account.RevokeAPIKeyEndpoint(users))
//	rest.Get(v1, "/me/identities", account.IdentitiesEndpoint(users))
//	rest.Delete(v1, "/me/identities/{id}", account.UnlinkIdentityEndpoint(users))

type loginMFAInput struct {
	clientInput
	Body struct {
		MFAToken string `json:"mfa_token" maxLength:"1024" doc:"登录接口返回的 mfa_token"`
		Code     string `json:"code" maxLength:"32" doc:"验证器 App 显示的 6 位验证码，或一个恢复码"`
	}
}

// LoginMFAEndpoint 完成两步验证登录，返回凭证。
func LoginMFAEndpoint(s *Service) rest.Endpoint[loginMFAInput, tokensOutput] {
	return rest.Endpoint[loginMFAInput, tokensOutput]{
		Operation: huma.Operation{OperationID: "account-login-mfa", Summary: "两步验证登录", Tags: tags},
		Handler: func(ctx context.Context, in *loginMFAInput) (*tokensOutput, error) {
			t, err := s.LoginMFA(ctx, in.Body.MFAToken, in.Body.Code, in.client())
			if err != nil {
				return nil, mapError(err)
			}
			return &tokensOutput{Body: toTokensDTO(t)}, nil
		},
	}
}

type tokenInput struct {
	Body struct {
		Token string `json:"token" maxLength:"256" doc:"验证邮件链接中的 token"`
	}
}

// VerifyEmailEndpoint 用验证邮件中的 token 验证邮箱，返回账号信息。
func VerifyEmailEndpoint(s *Service) rest.Endpoint[tokenInput, userOutput] {
	return rest.Endpoint[tokenInput, userOutput]{
		Operation: huma.Operation{OperationID: "account-verify-email", Summary: "验证邮箱", Tags: tags},
		Handler: func(ctx context.Context, in *tokenInput) (*userOutput, error) {
			u, err := s.VerifyEmail(ctx, in.Body.Token)
			if err != nil {
				return nil, mapError(err)
			}
			return &userOutput{Body: s.UserDTO(u)}, nil
		},
	}
}

// SendVerificationEmailEndpoint 向当前账号的邮箱（重新）发送验证邮件；已验证时什么也不做。
func SendVerificationEmailEndpoint(s *Service) rest.Endpoint[struct{}, struct{}] {
	return rest.Endpoint[struct{}, struct{}]{
		Operation: huma.Operation{OperationID: "account-send-verification-email", Summary: "发送验证邮件", Tags: tags,
			DefaultStatus: http.StatusAccepted, Security: rest.RequireAuth()},
		Handler: func(ctx context.Context, _ *struct{}) (*struct{}, error) {
			p, err := userPrincipal(ctx)
			if err != nil {
				return nil, err
			}
			return nil, mapError(s.RequestEmailVerification(ctx, p.ID))
		},
	}
}

// MFAStatusDTO 是两步验证状态。
type MFAStatusDTO struct {
	Enabled       bool      `json:"enabled"`
	EnabledAt     time.Time `json:"enabled_at,omitzero"`
	RecoveryCodes int       `json:"recovery_codes" doc:"剩余可用的恢复码数量"`
}

type mfaStatusOutput struct{ Body MFAStatusDTO }

// MFAStatusEndpoint 返回当前账号的两步验证状态。
func MFAStatusEndpoint(s *Service) rest.Endpoint[struct{}, mfaStatusOutput] {
	return rest.Endpoint[struct{}, mfaStatusOutput]{
		Operation: huma.Operation{OperationID: "account-mfa-status", Summary: "两步验证状态", Tags: tags, Security: rest.RequireAuth()},
		Handler: func(ctx context.Context, _ *struct{}) (*mfaStatusOutput, error) {
			p, err := userPrincipal(ctx)
			if err != nil {
				return nil, err
			}
			st, err := s.MFAStatus(ctx, p.ID)
			if err != nil {
				return nil, err
			}
			return &mfaStatusOutput{Body: MFAStatusDTO(st)}, nil
		},
	}
}

type mfaSetupOutput struct {
	Body struct {
		Secret string `json:"secret" doc:"Base32 密钥，无法扫码时手动输入验证器 App"`
		URL    string `json:"url" doc:"otpauth:// 地址，生成二维码供验证器 App 扫描"`
	}
}

// SetupMFAEndpoint 生成新的两步验证密钥（尚未启用），之后用 EnableMFAEndpoint 提交验证码确认。
func SetupMFAEndpoint(s *Service) rest.Endpoint[struct{}, mfaSetupOutput] {
	return rest.Endpoint[struct{}, mfaSetupOutput]{
		Operation: huma.Operation{OperationID: "account-mfa-setup", Summary: "开始绑定两步验证", Tags: tags, Security: rest.RequireAuth()},
		Handler: func(ctx context.Context, _ *struct{}) (*mfaSetupOutput, error) {
			p, err := userPrincipal(ctx)
			if err != nil {
				return nil, err
			}
			setup, err := s.SetupMFA(ctx, p.ID)
			if err != nil {
				return nil, mapError(err)
			}
			out := &mfaSetupOutput{}
			out.Body.Secret, out.Body.URL = setup.Secret, setup.URL
			return out, nil
		},
	}
}

type codeInput struct {
	Body struct {
		Code string `json:"code" maxLength:"32" doc:"验证器 App 显示的 6 位验证码，或一个恢复码"`
	}
}

type recoveryCodesOutput struct {
	Body struct {
		RecoveryCodes []string `json:"recovery_codes" doc:"恢复码，只显示这一次；每个只能使用一次"`
	}
}

// EnableMFAEndpoint 提交验证码启用两步验证，返回恢复码。
func EnableMFAEndpoint(s *Service) rest.Endpoint[codeInput, recoveryCodesOutput] {
	return rest.Endpoint[codeInput, recoveryCodesOutput]{
		Operation: huma.Operation{OperationID: "account-mfa-enable", Summary: "启用两步验证", Tags: tags, Security: rest.RequireAuth()},
		Handler: func(ctx context.Context, in *codeInput) (*recoveryCodesOutput, error) {
			p, err := userPrincipal(ctx)
			if err != nil {
				return nil, err
			}
			codes, err := s.EnableMFA(ctx, p.ID, in.Body.Code)
			if err != nil {
				return nil, mapError(err)
			}
			out := &recoveryCodesOutput{}
			out.Body.RecoveryCodes = codes
			return out, nil
		},
	}
}

// DisableMFAEndpoint 提交验证码（或恢复码）关闭两步验证。
func DisableMFAEndpoint(s *Service) rest.Endpoint[codeInput, struct{}] {
	return rest.Endpoint[codeInput, struct{}]{
		Operation: huma.Operation{OperationID: "account-mfa-disable", Summary: "关闭两步验证", Tags: tags,
			DefaultStatus: http.StatusNoContent, Security: rest.RequireAuth()},
		Handler: func(ctx context.Context, in *codeInput) (*struct{}, error) {
			p, err := userPrincipal(ctx)
			if err != nil {
				return nil, err
			}
			return nil, mapError(s.DisableMFA(ctx, p.ID, in.Body.Code))
		},
	}
}

// RegenerateRecoveryCodesEndpoint 提交验证码生成新的恢复码，旧恢复码作废。
func RegenerateRecoveryCodesEndpoint(s *Service) rest.Endpoint[codeInput, recoveryCodesOutput] {
	return rest.Endpoint[codeInput, recoveryCodesOutput]{
		Operation: huma.Operation{OperationID: "account-mfa-recovery-codes", Summary: "重新生成恢复码", Tags: tags, Security: rest.RequireAuth()},
		Handler: func(ctx context.Context, in *codeInput) (*recoveryCodesOutput, error) {
			p, err := userPrincipal(ctx)
			if err != nil {
				return nil, err
			}
			codes, err := s.RegenerateRecoveryCodes(ctx, p.ID, in.Body.Code)
			if err != nil {
				return nil, mapError(err)
			}
			out := &recoveryCodesOutput{}
			out.Body.RecoveryCodes = codes
			return out, nil
		},
	}
}

// APIKeyDTO 是一个 API Key（不含 token）。
type APIKeyDTO struct {
	ID         string    `json:"id" format:"uuid"`
	Name       string    `json:"name"`
	Hint       string    `json:"hint" doc:"token 末尾 4 位，便于辨认"`
	Scopes     []string  `json:"scopes" doc:"为空表示不限"`
	CreatedAt  time.Time `json:"created_at"`
	LastUsedAt time.Time `json:"last_used_at,omitzero"`
	ExpiresAt  time.Time `json:"expires_at,omitzero" doc:"省略表示永不过期"`
}

func toAPIKeyDTO(k *APIKey) APIKeyDTO {
	return APIKeyDTO{ID: k.ID.String(), Name: k.Name, Hint: k.Hint, Scopes: k.Scopes,
		CreatedAt: k.CreatedAt, LastUsedAt: k.LastUsedAt, ExpiresAt: k.ExpiresAt}
}

type apiKeysOutput struct{ Body []APIKeyDTO }

// APIKeysEndpoint 列出当前账号的 API Key。
func APIKeysEndpoint(s *Service) rest.Endpoint[struct{}, apiKeysOutput] {
	return rest.Endpoint[struct{}, apiKeysOutput]{
		Operation: huma.Operation{OperationID: "account-api-keys", Summary: "API Key 列表", Tags: tags, Security: rest.RequireAuth()},
		Handler: func(ctx context.Context, _ *struct{}) (*apiKeysOutput, error) {
			p, err := userPrincipal(ctx)
			if err != nil {
				return nil, err
			}
			list, err := s.APIKeys(ctx, p.ID)
			if err != nil {
				return nil, err
			}
			out := &apiKeysOutput{Body: make([]APIKeyDTO, 0, len(list))}
			for i := range list {
				out.Body = append(out.Body, toAPIKeyDTO(&list[i]))
			}
			return out, nil
		},
	}
}

type createAPIKeyInput struct {
	Body struct {
		Name      string    `json:"name" minLength:"1" maxLength:"100"`
		Scopes    []string  `json:"scopes,omitempty" maxItems:"50" doc:"限定可访问的 scope，省略表示不限"`
		ExpiresAt time.Time `json:"expires_at,omitzero" doc:"过期时间，省略表示永不过期"`
	}
}

type createAPIKeyOutput struct {
	Body struct {
		APIKeyDTO
		Token string `json:"token" doc:"API Key，只显示这一次；放在 Authorization: Bearer 头中使用"`
	}
}

// CreateAPIKeyEndpoint 创建 API Key，返回的 token 只显示这一次。
func CreateAPIKeyEndpoint(s *Service) rest.Endpoint[createAPIKeyInput, createAPIKeyOutput] {
	return rest.Endpoint[createAPIKeyInput, createAPIKeyOutput]{
		Operation: huma.Operation{OperationID: "account-create-api-key", Summary: "创建 API Key", Tags: tags,
			DefaultStatus: http.StatusCreated, Security: rest.RequireAuth()},
		Handler: func(ctx context.Context, in *createAPIKeyInput) (*createAPIKeyOutput, error) {
			p, err := userPrincipal(ctx)
			if err != nil {
				return nil, err
			}
			if !in.Body.ExpiresAt.IsZero() && !in.Body.ExpiresAt.After(time.Now()) {
				return nil, huma.Error422UnprocessableEntity("过期时间必须晚于现在")
			}
			k, token, err := s.CreateAPIKey(ctx, p.ID, in.Body.Name, in.Body.Scopes, in.Body.ExpiresAt)
			if err != nil {
				return nil, err
			}
			out := &createAPIKeyOutput{}
			out.Body.APIKeyDTO, out.Body.Token = toAPIKeyDTO(k), token
			return out, nil
		},
	}
}

type idInput struct {
	ID string `path:"id" format:"uuid"`
}

// RevokeAPIKeyEndpoint 删除 API Key，立即失效。
func RevokeAPIKeyEndpoint(s *Service) rest.Endpoint[idInput, struct{}] {
	return rest.Endpoint[idInput, struct{}]{
		Operation: huma.Operation{OperationID: "account-revoke-api-key", Summary: "删除 API Key", Tags: tags,
			DefaultStatus: http.StatusNoContent, Security: rest.RequireAuth()},
		Handler: func(ctx context.Context, in *idInput) (*struct{}, error) {
			p, err := userPrincipal(ctx)
			if err != nil {
				return nil, err
			}
			id, err := uuid.Parse(in.ID)
			if err != nil {
				return nil, huma.Error404NotFound("不存在")
			}
			return nil, mapError(s.RevokeAPIKey(ctx, p.ID, id))
		},
	}
}

// IdentityDTO 是绑定的第三方账号。
type IdentityDTO struct {
	ID        string    `json:"id" format:"uuid"`
	Provider  string    `json:"provider" example:"github"`
	Email     string    `json:"email"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
}

type identitiesOutput struct{ Body []IdentityDTO }

// IdentitiesEndpoint 列出当前账号绑定的第三方账号。绑定通过网页跳转完成，见 OAuthWeb。
func IdentitiesEndpoint(s *Service) rest.Endpoint[struct{}, identitiesOutput] {
	return rest.Endpoint[struct{}, identitiesOutput]{
		Operation: huma.Operation{OperationID: "account-identities", Summary: "第三方账号", Tags: tags, Security: rest.RequireAuth()},
		Handler: func(ctx context.Context, _ *struct{}) (*identitiesOutput, error) {
			p, err := userPrincipal(ctx)
			if err != nil {
				return nil, err
			}
			list, err := s.Identities(ctx, p.ID)
			if err != nil {
				return nil, err
			}
			out := &identitiesOutput{Body: make([]IdentityDTO, 0, len(list))}
			for _, x := range list {
				out.Body = append(out.Body, IdentityDTO{ID: x.ID.String(), Provider: x.Provider, Email: x.Email,
					Name: x.Name, CreatedAt: x.CreatedAt})
			}
			return out, nil
		},
	}
}

// UnlinkIdentityEndpoint 解绑第三方账号；没有密码时不能解绑最后一个。
func UnlinkIdentityEndpoint(s *Service) rest.Endpoint[idInput, struct{}] {
	return rest.Endpoint[idInput, struct{}]{
		Operation: huma.Operation{OperationID: "account-unlink-identity", Summary: "解绑第三方账号", Tags: tags,
			DefaultStatus: http.StatusNoContent, Security: rest.RequireAuth()},
		Handler: func(ctx context.Context, in *idInput) (*struct{}, error) {
			p, err := userPrincipal(ctx)
			if err != nil {
				return nil, err
			}
			id, err := uuid.Parse(in.ID)
			if err != nil {
				return nil, huma.Error404NotFound("不存在")
			}
			return nil, mapError(s.UnlinkIdentity(ctx, p.ID, id))
		},
	}
}
