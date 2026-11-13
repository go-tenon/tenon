package readerapi

import (
	"context"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/uptrace/bun"

	"example.com/blog/internal/accounts"
	"example.com/blog/internal/reader"
	"github.com/go-tenon/tenon/apitoken"
	"github.com/go-tenon/tenon/httpx/rest"
	"github.com/go-tenon/tenon/identity"
	"github.com/go-tenon/tenon/login"
)

type SendCodeInput struct {
	Body struct {
		Phone string `json:"phone" pattern:"^\\+[1-9][0-9]{6,14}$" doc:"E.164 格式，例如 +8613800138000"`
	}
}

type SendCodeOutput struct {
	Body struct {
		RequestID string `json:"request_id"`
	}
}

type VerifyCodeInput struct {
	UserAgent string `header:"User-Agent"`
	Body      struct {
		RequestID string `json:"request_id"`
		Code      string `json:"code"`
	}
}

type SignUpInput struct {
	UserAgent string `header:"User-Agent"`
	Body      struct {
		Proof string `json:"proof" minLength:"1"`
		Name  string `json:"name" minLength:"1" maxLength:"50"`
	}
}

type AppleInput struct {
	UserAgent string `header:"User-Agent"`
	Body      struct {
		IDToken string `json:"id_token" minLength:"1"`
		Nonce   string `json:"nonce,omitempty"`
	}
}

// ConfirmInput 确认操作的是读者本人：验证码，或重新登录 Apple 得到的 ID token。
type ConfirmInput struct {
	Body struct {
		RequestID    string `json:"request_id,omitempty" doc:"POST /account/confirm-code 返回的请求 ID"`
		Code         string `json:"code,omitempty"`
		AppleIDToken string `json:"apple_id_token,omitempty" doc:"没有手机号的读者重新登录 Apple 得到的 ID token"`
		Nonce        string `json:"nonce,omitempty"`
	}
}

type RefreshInput struct {
	Body struct {
		RefreshToken string `json:"refresh_token" minLength:"1"`
	}
}

// SessionOutput 是登录的结果：一对 token；还没有账号时只有注册凭证。
type SessionOutput struct {
	Body struct {
		AccessToken  string    `json:"access_token,omitempty"`
		RefreshToken string    `json:"refresh_token,omitempty" doc:"为空时继续使用已保存的 refresh token"`
		ExpiresAt    time.Time `json:"expires_at,omitzero" doc:"access token 的过期时间"`
		Proof        string    `json:"proof,omitempty" doc:"还没有账号：带着它调用 /auth/sign-up"`
	}
}

func session(pair apitoken.Pair) *SessionOutput {
	out := &SessionOutput{}
	out.Body.AccessToken, out.Body.RefreshToken, out.Body.ExpiresAt = pair.Access, pair.Refresh, pair.ExpiresAt
	return out
}

type handlers struct {
	acc    *accounts.Accounts
	signUp *reader.SignUp
	delete *reader.Delete
}

func Register(api *rest.API, db *bun.DB, acc *accounts.Accounts) {
	h := &handlers{
		acc:    acc,
		signUp: reader.NewSignUp(db, acc.Readers),
		delete: reader.NewDelete(db, acc.Readers, acc.ReaderTokens),
	}
	public := func(id, path string) huma.Operation {
		op := huma.Operation{OperationID: id, Method: http.MethodPost, Path: path}
		rest.Public()(&op)
		return op
	}
	rest.Register(api, public("reader-codes-send", "/auth/codes"), h.sendCode)
	rest.Register(api, public("reader-codes-verify", "/auth/codes/verify"), h.verifyCode)
	rest.Register(api, public("reader-sign-up", "/auth/sign-up"), h.register)
	rest.Register(api, public("reader-apple", "/auth/apple"), h.apple)
	rest.Register(api, public("reader-refresh", "/auth/refresh"), h.refresh)

	signedIn := func(id, method, path string) huma.Operation {
		op := huma.Operation{OperationID: id, Method: method, Path: path}
		rest.RequireBearer(api, acc.ReaderJWT.Validate)(&op)
		return op
	}
	rest.Register(api, signedIn("reader-logout", http.MethodDelete, "/auth/session"), h.logout)
	rest.Register(api, signedIn("reader-confirm-code", http.MethodPost, "/account/confirm-code"), h.sendConfirmCode)
	rest.Register(api, signedIn("reader-delete", http.MethodDelete, "/account"), h.deleteAccount)
}

func (h *handlers) sendCode(ctx context.Context, in *SendCodeInput) (*SendCodeOutput, error) {
	id, err := h.acc.Readers.SendCode(ctx, login.SignIn, login.Phone(in.Body.Phone))
	if err != nil {
		return nil, err
	}
	out := &SendCodeOutput{}
	out.Body.RequestID = id
	return out, nil
}

func (h *handlers) verifyCode(ctx context.Context, in *VerifyCodeInput) (*SessionOutput, error) {
	out, err := h.acc.Readers.VerifyCode(ctx, in.Body.RequestID, in.Body.Code)
	if err != nil {
		return nil, err
	}
	return h.issue(ctx, out, in.UserAgent)
}

func (h *handlers) register(ctx context.Context, in *SignUpInput) (*SessionOutput, error) {
	sub, err := h.signUp.Handle(ctx, in.Body.Proof, in.Body.Name)
	if err != nil {
		return nil, err
	}
	return h.issue(ctx, login.Outcome{Subject: sub}, in.UserAgent)
}

func (h *handlers) apple(ctx context.Context, in *AppleInput) (*SessionOutput, error) {
	ext, err := h.acc.Apple.IDToken(ctx, in.Body.IDToken, in.Body.Nonce)
	if err != nil {
		return nil, err
	}
	out, err := h.acc.Readers.External(ctx, ext)
	if err != nil {
		return nil, err
	}
	return h.issue(ctx, out, in.UserAgent)
}

// issue 登录成功时签发一对 token；还没有账号时返回注册凭证。
func (h *handlers) issue(ctx context.Context, out login.Outcome, userAgent string) (*SessionOutput, error) {
	if out.Proof != "" {
		resp := &SessionOutput{}
		resp.Body.Proof = out.Proof
		return resp, nil
	}
	pair, err := h.acc.ReaderJWT.Login(ctx, out.Subject, apitoken.Device{Remember: true, UserAgent: userAgent})
	if err != nil {
		return nil, err
	}
	return session(pair), nil
}

func (h *handlers) refresh(ctx context.Context, in *RefreshInput) (*SessionOutput, error) {
	pair, err := h.acc.ReaderJWT.Refresh(ctx, in.Body.RefreshToken)
	if err != nil {
		return nil, err
	}
	return session(pair), nil
}

func (h *handlers) logout(ctx context.Context, _ *struct{}) (*struct{}, error) {
	sub, _ := identity.FromContext(ctx)
	return nil, h.acc.ReaderTokens.Revoke(ctx, sub, sub.Credential)
}

// sendConfirmCode 向读者自己的手机号发送确认身份的验证码。
func (h *handlers) sendConfirmCode(ctx context.Context, _ *struct{}) (*SendCodeOutput, error) {
	sub, _ := identity.FromContext(ctx)
	ids, err := h.acc.Readers.Identifiers(ctx, sub.Ref())
	if err != nil {
		return nil, err
	}
	for _, id := range ids {
		if id.Kind == login.KindPhone {
			requestID, err := h.acc.Readers.SendConfirmCode(ctx, sub.Ref(), id.Identifier)
			if err != nil {
				return nil, err
			}
			out := &SendCodeOutput{}
			out.Body.RequestID = requestID
			return out, nil
		}
	}
	return nil, login.ErrNotFound.New()
}

func (h *handlers) deleteAccount(ctx context.Context, in *ConfirmInput) (*struct{}, error) {
	sub, _ := identity.FromContext(ctx)
	if err := h.confirm(ctx, sub.Ref(), in); err != nil {
		return nil, err
	}
	return nil, h.delete.Handle(ctx, sub.Ref())
}

// confirm 确认操作的是读者本人：校验发到手机的验证码，或者重新登录 Apple，登录的必须是同一个读者。
func (h *handlers) confirm(ctx context.Context, ref identity.Ref, in *ConfirmInput) error {
	if in.Body.AppleIDToken == "" {
		return h.acc.Readers.ConfirmCode(ctx, ref, in.Body.RequestID, in.Body.Code)
	}
	ext, err := h.acc.Apple.IDToken(ctx, in.Body.AppleIDToken, in.Body.Nonce)
	if err != nil {
		return err
	}
	out, err := h.acc.Readers.External(ctx, ext)
	if err != nil {
		return err
	}
	if out.Subject.Ref() != ref {
		return login.ErrInvalidCredentials.New()
	}
	return nil
}
