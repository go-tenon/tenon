package editorapi

import (
	"context"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/uptrace/bun"

	"example.com/blog/internal/accounts"
	"example.com/blog/internal/editor"
	"github.com/go-tenon/tenon/apitoken"
	"github.com/go-tenon/tenon/httpx/rest"
	"github.com/go-tenon/tenon/identity"
	"github.com/go-tenon/tenon/login"
)

type LoginInput struct {
	UserAgent string `header:"User-Agent"`
	Body      struct {
		Email    string `json:"email" format:"email"`
		Password string `json:"password" minLength:"1"`
	}
}

type TwoFactorInput struct {
	UserAgent string `header:"User-Agent"`
	Body      struct {
		Pending string `json:"pending" minLength:"1"`
		Code    string `json:"code" minLength:"1"`
	}
}

// LoginOutput 是登录的结果，三者只有一个不为空；登录成功时 refresh token 写入 cookie。
type LoginOutput struct {
	SetCookie *http.Cookie `header:"Set-Cookie"`
	Body      struct {
		AccessToken string `json:"access_token,omitempty"`
		Pending     string `json:"pending,omitempty" doc:"带着它和验证器上的数字调用 /auth/two-factor"`
		Enroll      string `json:"enroll,omitempty" doc:"还没有启用二次验证：带着它调用 /auth/two-factor/enroll"`
	}
}

type EnrollInput struct {
	Body struct {
		Enroll string `json:"enroll" minLength:"1"`
	}
}

type EnrollOutput struct {
	Body struct {
		Secret string `json:"secret"`
		URI    string `json:"uri" doc:"otpauth:// 地址，渲染成二维码"`
	}
}

type CompleteEnrollInput struct {
	UserAgent string `header:"User-Agent"`
	Body      struct {
		Enroll string `json:"enroll" minLength:"1"`
		Code   string `json:"code" minLength:"1"`
	}
}

type CompleteEnrollOutput struct {
	SetCookie *http.Cookie `header:"Set-Cookie"`
	Body      struct {
		AccessToken   string   `json:"access_token"`
		RecoveryCodes []string `json:"recovery_codes"`
	}
}

type RefreshInput struct {
	Cookie string `cookie:"editor_refresh"`
}

type LogoutOutput struct {
	SetCookie *http.Cookie `header:"Set-Cookie"`
}

type VerifyLinkInput struct {
	Body struct {
		Request string `json:"request" minLength:"1"`
		Code    string `json:"code" minLength:"1"`
	}
}

type VerifyLinkOutput struct {
	Body struct {
		Proof string `json:"proof"`
	}
}

type PasswordInput struct {
	Body struct {
		Proof    string `json:"proof" minLength:"1"`
		Password string `json:"password" minLength:"1"`
	}
}

type ConfirmInput struct {
	Body struct {
		Password string `json:"password" minLength:"1"`
	}
}

type ConfirmOutput struct {
	Body struct {
		AccessToken string `json:"access_token" doc:"带有这次确认的新 access token，替换原来的"`
	}
}

type InviteInput struct {
	Body struct {
		Name  string `json:"name" minLength:"1" maxLength:"50"`
		Email string `json:"email" format:"email"`
	}
}

type handlers struct {
	logins *login.Realm
	tokens *apitoken.Store
	jwt    *apitoken.JWT
	cookie apitoken.RefreshCookie
	invite *editor.Invite
}

func Register(api *rest.API, db *bun.DB, acc *accounts.Accounts) {
	h := &handlers{
		logins: acc.Editors, tokens: acc.EditorTokens, jwt: acc.EditorJWT, cookie: acc.EditorCookie,
		invite: editor.NewInvite(db, acc.Editors),
	}
	op := func(id, method, path string, opts ...func(*huma.Operation)) huma.Operation {
		o := huma.Operation{OperationID: id, Method: method, Path: path}
		for _, opt := range opts {
			opt(&o)
		}
		return o
	}
	public := rest.Public()
	signedIn := rest.RequireBearer(api, acc.EditorJWT.Validate)
	rest.Register(api, op("editor-login", http.MethodPost, "/auth/login", public), h.login)
	rest.Register(api, op("editor-two-factor", http.MethodPost, "/auth/two-factor", public), h.twoFactor)
	rest.Register(api, op("editor-enroll", http.MethodPost, "/auth/two-factor/enroll", public), h.enroll)
	rest.Register(api, op("editor-enroll-confirm", http.MethodPost, "/auth/two-factor/enroll/confirm", public), h.completeEnroll)
	rest.Register(api, op("editor-refresh", http.MethodPost, "/auth/refresh", public), h.refresh)
	rest.Register(api, op("editor-password-verify", http.MethodPost, "/auth/password/verify", public), h.verifyLink)
	rest.Register(api, op("editor-password", http.MethodPost, "/auth/password", public), h.setPassword)
	rest.Register(api, op("editor-logout", http.MethodDelete, "/auth/session", signedIn), h.logout)
	rest.Register(api, op("editor-confirm", http.MethodPost, "/account/confirm", signedIn), h.confirm)
	rest.Register(api, op("editor-invite", http.MethodPost, "/editors",
		rest.RequireBearer(api, acc.EditorJWT.Validate, "editors:invite"), rest.Confirmed(15*time.Minute)), h.inviteEditor)
}

func (h *handlers) login(ctx context.Context, in *LoginInput) (*LoginOutput, error) {
	out, err := h.logins.Password(ctx, login.Email(in.Body.Email), in.Body.Password)
	if err != nil {
		return nil, err // 密码错误：login.ErrInvalidCredentials，401；连续失败太多次：ratelimit.ErrExceeded，429
	}
	return h.issue(ctx, out, in.UserAgent)
}

func (h *handlers) twoFactor(ctx context.Context, in *TwoFactorInput) (*LoginOutput, error) {
	out, err := h.logins.TwoFactor(ctx, in.Body.Pending, in.Body.Code)
	if err != nil {
		return nil, err // 验证码不正确：login.ErrInvalidCode；输错太多次或超时：login.ErrInvalidProof，重新登录
	}
	return h.issue(ctx, out, in.UserAgent)
}

// issue 把登录的结果交给单页应用：需要二次验证或先启用时返回对应的凭证，登录成功时签发 JWT，refresh token 写入 cookie。
func (h *handlers) issue(ctx context.Context, out login.Outcome, userAgent string) (*LoginOutput, error) {
	resp := &LoginOutput{}
	if out.Pending != "" || out.Enroll != "" {
		resp.Body.Pending, resp.Body.Enroll = out.Pending, out.Enroll
		return resp, nil
	}
	pair, err := h.jwt.Login(ctx, out.Subject, apitoken.Device{UserAgent: userAgent})
	if err != nil {
		return nil, err
	}
	resp.SetCookie, resp.Body.AccessToken = h.cookie.Set(pair), pair.Access
	return resp, nil
}

func (h *handlers) enroll(ctx context.Context, in *EnrollInput) (*EnrollOutput, error) {
	setup, err := h.logins.EnrollTOTP(ctx, in.Body.Enroll, "Blog 编辑")
	if err != nil {
		return nil, err // 超时：login.ErrInvalidProof，重新登录
	}
	resp := &EnrollOutput{}
	resp.Body.Secret, resp.Body.URI = setup.Secret, setup.URI
	return resp, nil
}

func (h *handlers) completeEnroll(ctx context.Context, in *CompleteEnrollInput) (*CompleteEnrollOutput, error) {
	out, codes, err := h.logins.CompleteEnroll(ctx, in.Body.Enroll, in.Body.Code)
	if err != nil {
		return nil, err // 验证码不正确：login.ErrInvalidCode，重新扫码或再输一次
	}
	pair, err := h.jwt.Login(ctx, out.Subject, apitoken.Device{UserAgent: in.UserAgent})
	if err != nil {
		return nil, err
	}
	resp := &CompleteEnrollOutput{SetCookie: h.cookie.Set(pair)}
	resp.Body.AccessToken, resp.Body.RecoveryCodes = pair.Access, codes
	return resp, nil
}

// refresh 用 cookie 中的 refresh token 换新的 access token，并轮换 cookie。
func (h *handlers) refresh(ctx context.Context, in *RefreshInput) (*LoginOutput, error) {
	pair, err := h.jwt.Refresh(ctx, in.Cookie)
	if err != nil {
		return nil, err // 401：没有登录或登录已失效，显示登录页
	}
	resp := &LoginOutput{SetCookie: h.cookie.Set(pair)}
	resp.Body.AccessToken = pair.Access
	return resp, nil
}

// verifyLink 校验邀请和找回密码邮件中的链接。设置密码页打开后才调用它：
// 邮件服务商的安全扫描会预先打开链接，只有页面上的脚本会提交验证码。
func (h *handlers) verifyLink(ctx context.Context, in *VerifyLinkInput) (*VerifyLinkOutput, error) {
	out, err := h.logins.VerifyCode(ctx, in.Body.Request, in.Body.Code)
	if err != nil {
		return nil, err // 链接已失效：login.ErrInvalidCode
	}
	resp := &VerifyLinkOutput{}
	resp.Body.Proof = out.Proof
	return resp, nil
}

// setPassword 用完成凭证设置密码。密码不符合要求时返回 login.ErrWeakPassword，凭证仍然有效，可以重新提交。
func (h *handlers) setPassword(ctx context.Context, in *PasswordInput) (*struct{}, error) {
	_, err := h.logins.ResetPassword(ctx, in.Body.Proof, in.Body.Password)
	return nil, err
}

// logout 撤销这次登录，并清除 cookie。
func (h *handlers) logout(ctx context.Context, _ *struct{}) (*LogoutOutput, error) {
	sub, _ := identity.FromContext(ctx)
	if err := h.tokens.Revoke(ctx, sub, sub.Credential); err != nil {
		return nil, err
	}
	return &LogoutOutput{SetCookie: h.cookie.Clear()}, nil
}

// confirm 确认身份：校验密码后记下确认时间，返回带有这次确认的新 access token，之后 15 分钟内可以邀请编辑。
func (h *handlers) confirm(ctx context.Context, in *ConfirmInput) (*ConfirmOutput, error) {
	sub, _ := identity.FromContext(ctx)
	if err := h.logins.Confirm(ctx, sub.Ref(), in.Body.Password); err != nil {
		return nil, err // 密码错误：login.ErrInvalidCredentials
	}
	pair, err := h.jwt.Confirm(ctx, sub, login.MethodPassword)
	if err != nil {
		return nil, err
	}
	resp := &ConfirmOutput{}
	resp.Body.AccessToken = pair.Access
	return resp, nil
}

func (h *handlers) inviteEditor(ctx context.Context, in *InviteInput) (*struct{}, error) {
	_, err := h.invite.Handle(ctx, in.Body.Name, in.Body.Email)
	return nil, err // 邮箱已被使用：login.ErrTaken
}
