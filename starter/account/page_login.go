// Package account 提供登录与退出页面。
package account

import (
	"errors"
	"net/http"

	"github.com/alexedwards/scs/v2"
	"github.com/go-chi/chi/v5"

	"github.com/ryan053857/go-kit/httpx/auth"
	"github.com/ryan053857/go-kit/httpx/web"
	"github.com/ryan053857/go-kit/starter/user"
	"github.com/ryan053857/go-kit/starter/views"
)

// LoginPage 显示登录表单并处理提交。登录成功后跳回 next 参数指定的页面（由 auth.RequireLogin 附带）。
type LoginPage struct {
	Sessions *scs.SessionManager
	Users    *user.Store
}

func (a *LoginPage) Mount(r chi.Router) {
	r.Method(http.MethodGet, "/login", web.HandlerFunc(a.show))
	r.Method(http.MethodPost, "/login", web.HandlerFunc(a.submit))
}

func (a *LoginPage) show(w http.ResponseWriter, r *http.Request) error {
	next := auth.SafeRedirect(r.URL.Query().Get("next"), "/")
	if _, ok := auth.From[*user.User](r.Context()); ok {
		web.Redirect(w, r, next)
		return nil
	}
	return web.Page(w, r, http.StatusOK, views.Layout("登录"), loginForm(next, ""))
}

func (a *LoginPage) submit(w http.ResponseWriter, r *http.Request) error {
	next := auth.SafeRedirect(r.FormValue("next"), "/")
	u, err := a.Users.Authenticate(r.Context(), r.FormValue("name"), r.FormValue("password"))
	if errors.Is(err, auth.ErrUnauthenticated) {
		return web.Page(w, r, http.StatusUnauthorized, views.Layout("登录"), loginForm(next, "用户名或密码错误"))
	}
	if err != nil {
		return err
	}
	if err := auth.Login(r.Context(), a.Sessions, u.ID); err != nil {
		return err
	}
	web.Redirect(w, r, next)
	return nil
}
