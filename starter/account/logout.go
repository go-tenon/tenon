package account

import (
	"net/http"

	"github.com/alexedwards/scs/v2"
	"github.com/go-chi/chi/v5"

	"github.com/ryan053857/go-kit/httpx/auth"
	"github.com/ryan053857/go-kit/httpx/web"
	"github.com/ryan053857/go-kit/starter/user"
)

// Logout 销毁会话后跳转到登录页。只接受 POST，跨站请求由 http.CrossOriginProtection 拦截。
type Logout struct {
	Sessions *scs.SessionManager
}

func (a *Logout) Mount(r chi.Router) {
	r.Method(http.MethodPost, "/logout", web.HandlerFunc(a.serve))
}

func (a *Logout) serve(w http.ResponseWriter, r *http.Request) error {
	if err := auth.Logout(r.Context(), a.Sessions); err != nil {
		return err
	}
	web.Redirect(w, r, "/login")
	return nil
}

// MountPages 注册登录与退出页面，它们不需要登录即可访问。
func MountPages(r chi.Router, sessions *scs.SessionManager, users *user.Store) {
	(&LoginPage{Sessions: sessions, Users: users}).Mount(r)
	(&Logout{Sessions: sessions}).Mount(r)
}
