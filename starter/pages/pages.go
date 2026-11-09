// Package pages 是账号相关的页面：登录（含两步验证、第三方登录）、注册、找回与重置密码、邮箱验证、
// 退出、账号设置、安全设置，以及通知与管理后台页面。
// 页面属于产品，业务逻辑全部调用 go-kit 的 account.Service；可以随意修改样式与流程。
package pages

import (
	"errors"
	"net/http"
	"net/url"
	"uuid"

	"github.com/alexedwards/scs/v2"
	"github.com/danielgtaylor/huma/v2"
	"github.com/go-chi/chi/v5"

	"github.com/ryan053857/go-kit/httpx/auth"
	"github.com/ryan053857/go-kit/httpx/web"
	"github.com/ryan053857/go-kit/modules/account"
	"github.com/ryan053857/go-kit/modules/audit"
	"github.com/ryan053857/go-kit/modules/notification"
	"github.com/ryan053857/go-kit/modules/setting"
	"github.com/ryan053857/go-kit/starter/views"
)

// Pages 处理账号相关的页面。
type Pages struct {
	Users         *account.Service
	Avatars       *account.Avatars
	Sessions      *scs.SessionManager
	View          *views.Renderer
	Audit         *audit.Service
	Notifications *notification.Service
	Settings      *setting.Store
	// OAuth 是已启用的第三方登录，登录页与安全设置页显示对应的按钮。
	OAuth []account.OAuthProvider
}

// MountPublic 注册不需要登录的页面。limit 是登录、注册等表单提交的限流中间件。
func (p *Pages) MountPublic(r chi.Router, limit func(http.Handler) http.Handler) {
	r.Method(http.MethodGet, "/login", web.HandlerFunc(p.login))
	r.With(limit).Method(http.MethodPost, "/login", web.HandlerFunc(p.login))
	r.Method(http.MethodGet, "/login/mfa", web.HandlerFunc(p.loginMFA))
	r.With(limit).Method(http.MethodPost, "/login/mfa", web.HandlerFunc(p.loginMFA))
	r.Method(http.MethodGet, "/register", web.HandlerFunc(p.register))
	r.With(limit).Method(http.MethodPost, "/register", web.HandlerFunc(p.register))
	r.Method(http.MethodGet, "/forgot-password", web.HandlerFunc(p.forgot))
	r.With(limit).Method(http.MethodPost, "/forgot-password", web.HandlerFunc(p.forgot))
	r.Method(http.MethodGet, "/reset-password", web.HandlerFunc(p.reset))
	r.With(limit).Method(http.MethodPost, "/reset-password", web.HandlerFunc(p.reset))
	r.Method(http.MethodGet, "/verify-email", web.HandlerFunc(p.verifyEmail))
	r.Method(http.MethodPost, "/logout", web.HandlerFunc(p.logout))
}

// MountPrivate 注册需要登录的页面，调用方负责挂载 auth.RequireLogin。
func (p *Pages) MountPrivate(r chi.Router, limit func(http.Handler) http.Handler) {
	r.Method(http.MethodGet, "/settings", web.HandlerFunc(p.settings))
	r.Method(http.MethodPost, "/settings/profile", web.HandlerFunc(p.updateProfile))
	r.Method(http.MethodPost, "/settings/avatar", web.HandlerFunc(p.setAvatar))
	r.Method(http.MethodPost, "/settings/avatar/remove", web.HandlerFunc(p.removeAvatar))
	r.Method(http.MethodPost, "/settings/password", web.HandlerFunc(p.changePassword))
	r.Method(http.MethodPost, "/settings/sessions/{id}/revoke", web.HandlerFunc(p.revokeSession))
	r.With(limit).Method(http.MethodPost, "/settings/email/verification", web.HandlerFunc(p.sendVerification))
	p.mountSecurity(r, limit)
	p.mountNotifications(r)
}

type form struct {
	Email string
	Name  string
	Next  string
	Token string
	Error string
	Done  bool
	OAuth []account.OAuthProvider
}

// oauthErrors 是第三方登录失败时 error 参数对应的提示。
var oauthErrors = map[string]string{
	"denied":         "已取消第三方授权",
	"email_taken":    "该邮箱已注册，请先用密码登录，再在安全设置中绑定第三方账号",
	"email_required": "无法从第三方账号获取已验证的邮箱",
	"identity_taken": "该第三方账号已绑定其他账号",
	"disabled":       "账号已停用",
	"failed":         "第三方登录失败，请重试",
}

func (p *Pages) login(w http.ResponseWriter, r *http.Request) error {
	f := form{Next: auth.SafeRedirect(r.FormValue("next"), "/"), Email: r.FormValue("email"), OAuth: p.OAuth,
		Error: oauthErrors[r.URL.Query().Get("error")]}
	if _, ok := auth.From[*account.Principal](r.Context()); ok {
		web.Redirect(w, r, f.Next)
		return nil
	}
	if r.Method == http.MethodPost {
		u, err := p.Users.Authenticate(r.Context(), f.Email, r.FormValue("password"))
		switch {
		case errors.Is(err, account.ErrInvalidCredentials):
			f.Error = "邮箱或密码错误"
		case errors.Is(err, account.ErrUserDisabled):
			f.Error = "账号已停用"
		case err != nil:
			return err
		default:
			mfa, err := p.Users.WebBeginLogin(r.Context(), p.Sessions, u, r)
			if err != nil {
				return err
			}
			if mfa {
				web.Redirect(w, r, "/login/mfa?next="+url.QueryEscape(f.Next))
			} else {
				web.Redirect(w, r, f.Next)
			}
			return nil
		}
	}
	return p.View.Page(w, r, statusOf(f), "登录", loginPage(f))
}

// loginMFA 是密码正确后输入两步验证码的页面。
func (p *Pages) loginMFA(w http.ResponseWriter, r *http.Request) error {
	f := form{Next: auth.SafeRedirect(r.FormValue("next"), "/")}
	if !p.Users.WebPendingMFA(r.Context(), p.Sessions) {
		web.Redirect(w, r, "/login")
		return nil
	}
	if r.Method == http.MethodPost {
		err := p.Users.WebCompleteMFA(r.Context(), p.Sessions, r.FormValue("code"), r)
		switch {
		case errors.Is(err, account.ErrInvalidMFACode):
			f.Error = "验证码错误"
		case errors.Is(err, account.ErrInvalidToken), errors.Is(err, account.ErrUserDisabled):
			p.View.Flash(r.Context(), "登录已失效，请重新登录")
			web.Redirect(w, r, "/login")
			return nil
		case err != nil:
			return err
		default:
			web.Redirect(w, r, f.Next)
			return nil
		}
	}
	return p.View.Page(w, r, statusOf(f), "两步验证", loginMFAPage(f))
}

func (p *Pages) register(w http.ResponseWriter, r *http.Request) error {
	f := form{Email: r.FormValue("email"), Name: r.FormValue("name"), OAuth: p.OAuth}
	site, err := views.Site.Get(r.Context(), p.Settings)
	if err != nil {
		return err
	}
	if !site.RegistrationOpen {
		return huma.Error403Forbidden("暂未开放注册")
	}
	if r.Method == http.MethodPost {
		u, err := p.Users.Register(r.Context(), f.Email, r.FormValue("password"), f.Name)
		switch {
		case errors.Is(err, account.ErrEmailTaken):
			f.Error = "该邮箱已注册"
		case errors.Is(err, account.ErrWeakPassword):
			f.Error = "密码至少 8 位"
		case err != nil:
			return err
		default:
			if err := p.Users.WebLogin(r.Context(), p.Sessions, u, r); err != nil {
				return err
			}
			p.View.Flash(r.Context(), "注册成功，请查收邮箱验证邮件")
			web.Redirect(w, r, "/")
			return nil
		}
	}
	return p.View.Page(w, r, statusOf(f), "注册", registerPage(f))
}

func (p *Pages) forgot(w http.ResponseWriter, r *http.Request) error {
	f := form{Email: r.FormValue("email")}
	if r.Method == http.MethodPost {
		if err := p.Users.RequestPasswordReset(r.Context(), f.Email); err != nil {
			return err
		}
		f.Done = true
	}
	return p.View.Page(w, r, http.StatusOK, "找回密码", forgotPage(f))
}

func (p *Pages) reset(w http.ResponseWriter, r *http.Request) error {
	f := form{Token: r.FormValue("token")}
	if r.Method == http.MethodPost {
		err := p.Users.ResetPassword(r.Context(), f.Token, r.FormValue("password"))
		switch {
		case errors.Is(err, account.ErrInvalidToken):
			f.Error = "链接无效或已过期，请重新找回密码"
		case errors.Is(err, account.ErrWeakPassword):
			f.Error = "密码至少 8 位"
		case err != nil:
			return err
		default:
			p.View.Flash(r.Context(), "密码已重置，请重新登录")
			web.Redirect(w, r, "/login")
			return nil
		}
	}
	return p.View.Page(w, r, statusOf(f), "重置密码", resetPage(f))
}

// verifyEmail 是验证邮件中的链接。
func (p *Pages) verifyEmail(w http.ResponseWriter, r *http.Request) error {
	_, err := p.Users.VerifyEmail(r.Context(), r.URL.Query().Get("token"))
	switch {
	case errors.Is(err, account.ErrInvalidToken):
		return huma.Error400BadRequest("验证链接无效或已过期，请在账号设置中重新发送")
	case err != nil:
		return err
	}
	p.View.Flash(r.Context(), "邮箱已验证")
	if _, ok := auth.From[*account.Principal](r.Context()); ok {
		web.Redirect(w, r, "/settings")
	} else {
		web.Redirect(w, r, "/login")
	}
	return nil
}

// sendVerification 由 HTMX 调用，重新发送验证邮件。
func (p *Pages) sendVerification(w http.ResponseWriter, r *http.Request) error {
	pr, _ := auth.From[*account.Principal](r.Context())
	if err := p.Users.RequestEmailVerification(r.Context(), pr.ID); err != nil {
		return err
	}
	return views.Toast(w, r, "验证邮件已发送", false)
}

func (p *Pages) logout(w http.ResponseWriter, r *http.Request) error {
	if err := p.Users.WebLogout(r.Context(), p.Sessions); err != nil {
		return err
	}
	web.Redirect(w, r, "/login")
	return nil
}

type settingsData struct {
	User      *account.User
	AvatarURL string
	Sessions  []account.Session
	Current   uuid.UUID
	Error     string // 修改密码的错误
}

func (p *Pages) settingsData(r *http.Request) (*settingsData, error) {
	pr, _ := auth.From[*account.Principal](r.Context())
	u, err := p.Users.User(r.Context(), pr.ID)
	if err != nil {
		return nil, err
	}
	list, err := p.Users.Sessions(r.Context(), pr.ID)
	if err != nil {
		return nil, err
	}
	return &settingsData{User: u, AvatarURL: p.Users.AvatarURL(u), Sessions: list, Current: pr.SessionID}, nil
}

func (p *Pages) settings(w http.ResponseWriter, r *http.Request) error {
	d, err := p.settingsData(r)
	if err != nil {
		return err
	}
	return p.View.Page(w, r, http.StatusOK, "账号设置", settingsPage(d))
}

func (p *Pages) updateProfile(w http.ResponseWriter, r *http.Request) error {
	pr, _ := auth.From[*account.Principal](r.Context())
	if _, err := p.Users.UpdateName(r.Context(), pr.ID, r.FormValue("name")); err != nil {
		return err
	}
	p.View.Flash(r.Context(), "已保存")
	web.Redirect(w, r, "/settings")
	return nil
}

func (p *Pages) setAvatar(w http.ResponseWriter, r *http.Request) error {
	pr, _ := auth.From[*account.Principal](r.Context())
	r.Body = http.MaxBytesReader(w, r.Body, account.MaxAvatarBytes+1<<20)
	f, _, err := r.FormFile("avatar")
	if err != nil {
		p.View.Flash(r.Context(), "请选择图片")
		web.Redirect(w, r, "/settings")
		return nil
	}
	defer f.Close()
	_, err = p.Avatars.Set(r.Context(), pr.ID, f)
	switch {
	case errors.Is(err, account.ErrInvalidImage):
		p.View.Flash(r.Context(), "请上传 JPEG、PNG、GIF 或 WebP 图片（不超过 10 MB）")
	case err != nil:
		return err
	default:
		p.View.Flash(r.Context(), "头像已更新")
	}
	web.Redirect(w, r, "/settings")
	return nil
}

func (p *Pages) removeAvatar(w http.ResponseWriter, r *http.Request) error {
	pr, _ := auth.From[*account.Principal](r.Context())
	if _, err := p.Avatars.Remove(r.Context(), pr.ID); err != nil {
		return err
	}
	p.View.Flash(r.Context(), "头像已删除")
	web.Redirect(w, r, "/settings")
	return nil
}

func (p *Pages) changePassword(w http.ResponseWriter, r *http.Request) error {
	pr, _ := auth.From[*account.Principal](r.Context())
	err := p.Users.ChangePassword(r.Context(), pr.ID, pr.SessionID, r.FormValue("current_password"), r.FormValue("new_password"))
	msg := ""
	switch {
	case errors.Is(err, account.ErrInvalidCredentials):
		msg = "当前密码错误"
	case errors.Is(err, account.ErrWeakPassword):
		msg = "新密码至少 8 位"
	case err != nil:
		return err
	}
	if msg != "" {
		d, err := p.settingsData(r)
		if err != nil {
			return err
		}
		d.Error = msg
		return p.View.Page(w, r, http.StatusUnprocessableEntity, "账号设置", settingsPage(d))
	}
	p.View.Flash(r.Context(), "密码已修改，其他设备已退出登录")
	web.Redirect(w, r, "/settings")
	return nil
}

// revokeSession 由 HTMX 调用，成功后从表格中移除该行并弹出提示。
func (p *Pages) revokeSession(w http.ResponseWriter, r *http.Request) error {
	pr, _ := auth.From[*account.Principal](r.Context())
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		return huma.Error404NotFound("会话不存在")
	}
	if err := p.Users.RevokeSession(r.Context(), pr.ID, id); err != nil {
		if errors.Is(err, account.ErrNotFound) {
			return huma.Error404NotFound("会话不存在")
		}
		return err
	}
	// 主体内容为空（替换掉该行），toast 通过 hx-swap-oob 追加到 body。
	return web.Render(w, r, http.StatusOK, views.ToastOOB("已退出该设备", false))
}

func statusOf(f form) int {
	if f.Error != "" {
		return http.StatusUnprocessableEntity
	}
	return http.StatusOK
}
