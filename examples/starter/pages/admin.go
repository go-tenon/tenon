package pages

import (
	"net/http"
	"uuid"

	"github.com/go-chi/chi/v5"

	"github.com/ryan053857/go-kit/httpx/web"
	"github.com/ryan053857/go-kit/modules/audit"
	"github.com/ryan053857/go-kit/examples/starter/views"
)

// MountAdmin 注册管理页面，调用方负责挂载 auth.RequireLogin 与 views.Renderer.RequireAdmin。
func (p *Pages) MountAdmin(r chi.Router) {
	r.Method(http.MethodGet, "/admin/settings", web.HandlerFunc(p.adminSettings))
	r.Method(http.MethodPost, "/admin/settings", web.HandlerFunc(p.adminSettings))
	r.Method(http.MethodGet, "/admin/audit", web.HandlerFunc(p.adminAudit))
}

func (p *Pages) adminSettings(w http.ResponseWriter, r *http.Request) error {
	site, err := views.Site.Get(r.Context(), p.Settings)
	if err != nil {
		return err
	}
	errMsg := ""
	if r.Method == http.MethodPost {
		site.Name = r.FormValue("name")
		site.RegistrationOpen = r.FormValue("registration_open") == "on"
		if n := len([]rune(site.Name)); n == 0 || n > 50 {
			errMsg = "站点名称为 1～50 个字"
		} else {
			if err := views.Site.Set(r.Context(), p.Settings, site); err != nil {
				return err
			}
			p.Audit.Log(r.Context(), audit.Entry{Action: "setting.update", ResourceType: "setting", ResourceID: views.Site.Key,
				Summary: "修改站点设置", Data: map[string]any{"name": site.Name, "registration_open": site.RegistrationOpen}})
			p.View.Flash(r.Context(), "已保存")
			web.Redirect(w, r, "/admin/settings")
			return nil
		}
	}
	status := http.StatusOK
	if errMsg != "" {
		status = http.StatusUnprocessableEntity
	}
	return p.View.Page(w, r, status, "系统设置", adminSettingsPage(site, errMsg))
}

func (p *Pages) adminAudit(w http.ResponseWriter, r *http.Request) error {
	logs, _, err := p.Audit.List(r.Context(), audit.Filter{Action: r.URL.Query().Get("action"), Limit: 100})
	if err != nil {
		return err
	}
	// 操作者显示为邮箱。
	actors := map[uuid.UUID]string{}
	for _, l := range logs {
		if _, ok := actors[l.ActorID]; ok || l.ActorID == uuid.Nil() {
			continue
		}
		actors[l.ActorID] = l.ActorID.String()
		if u, err := p.Users.User(r.Context(), l.ActorID); err == nil {
			actors[l.ActorID] = u.Email
		}
	}
	return p.View.Page(w, r, http.StatusOK, "审计日志", adminAuditPage(logs, actors, r.URL.Query().Get("action")))
}
