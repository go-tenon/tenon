// Package pages 是账号相关的页面：登录、注册、找回与重置密码、退出、账号设置。
// 页面属于产品，业务逻辑全部调用 go-kit 的 account.Service；可以随意修改样式与流程。
package pages

import (
	"errors"
	"net/http"
	"uuid"

	"github.com/alexedwards/scs/v2"
	"github.com/danielgtaylor/huma/v2"
	"github.com/go-chi/chi/v5"

	"github.com/ryan053857/go-kit/httpx/auth"
	"github.com/ryan053857/go-kit/httpx/web"
	"github.com/ryan053857/go-kit/modules/account"
	"github.com/ryan053857/go-kit/starter/views"
)

// Pages 处理账号相关的页面。
type Pages struct {
	Users    *account.Service
	Avatars  *account.Avatars
	Sessions *scs.SessionManager
	View     *views.Renderer
}

// MountPublic 注册不需要登录的页面。
func (p *Pages) MountPublic(r chi.Router) {
	r.Method(http.MethodGet, "/login", web.HandlerFunc(p.login))
	r.Method(http.MethodPost, "/login", web.HandlerFunc(p.login))
	r.Method(http.MethodGet, "/register", web.HandlerFunc(p.register))
	r.Method(http.MethodPost, "/register", web.HandlerFunc(p.register))
	r.Method(http.MethodGet, "/forgot-password", web.HandlerFunc(p.forgot))
	r.Method(http.MethodPost, "/forgot-password", web.HandlerFunc(p.forgot))
	r.Method(http.MethodGet, "/reset-password", web.HandlerFunc(p.reset))
	r.Method(http.MethodPost, "/reset-password", web.HandlerFunc(p.reset))
	r.Method(http.MethodPost, "/logout", web.HandlerFunc(p.logout))
}

// MountPrivate 注册需要登录的页面，调用方负责挂载 auth.RequireLogin。
func (p *Pages) MountPrivate(r chi.Router) {
	r.Method(http.MethodGet, "/settings", web.HandlerFunc(p.settings))
	r.Method(http.MethodPost, "/settings/profile", web.HandlerFunc(p.updateProfile))
	r.Method(http.MethodPost, "/settings/avatar", web.HandlerFunc(p.setAvatar))
	r.Method(http.MethodPost, "/settings/avatar/remove", web.HandlerFunc(p.removeAvatar))
	r.Method(http.MethodPost, "/settings/password", web.HandlerFunc(p.changePassword))
	r.Method(http.MethodPost, "/settings/sessions/{id}/revoke", web.HandlerFunc(p.revokeSession))
}

type form struct {
	Email string
	Name  string
	Next  string
	Token string
	Error string
	Done  bool
}

func (p *Pages) login(w http.ResponseWriter, r *http.Request) error {
	f := form{Next: auth.SafeRedirect(r.FormValue("next"), "/"), Email: r.FormValue("email")}
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
			if err := p.Users.WebLogin(r.Context(), p.Sessions, u, r); err != nil {
				return err
			}
			web.Redirect(w, r, f.Next)
			return nil
		}
	}
	return p.View.Page(w, r, statusOf(f), "登录", loginPage(f))
}

func (p *Pages) register(w http.ResponseWriter, r *http.Request) error {
	f := form{Email: r.FormValue("email"), Name: r.FormValue("name")}
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
			p.View.Flash(r.Context(), "注册成功")
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
