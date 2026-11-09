package pages

import (
	"errors"
	"net/http"
	"uuid"

	"github.com/go-chi/chi/v5"

	"github.com/ryan053857/go-kit/httpx/auth"
	"github.com/ryan053857/go-kit/httpx/web"
	"github.com/ryan053857/go-kit/modules/account"
	"github.com/ryan053857/go-kit/modules/notification"
	"github.com/ryan053857/go-kit/starter/views"
)

func (p *Pages) mountNotifications(r chi.Router) {
	r.Method(http.MethodGet, "/notifications", web.HandlerFunc(p.notifications))
	r.Method(http.MethodGet, "/notifications/bell", web.HandlerFunc(p.notificationBell))
	r.Method(http.MethodGet, "/notifications/{id}", web.HandlerFunc(p.openNotification))
	r.Method(http.MethodPost, "/notifications/read-all", web.HandlerFunc(p.readAllNotifications))
}

func (p *Pages) notifications(w http.ResponseWriter, r *http.Request) error {
	pr, _ := auth.From[*account.Principal](r.Context())
	list, _, err := p.Notifications.List(r.Context(), pr.ID, notification.ListOptions{Limit: 50})
	if err != nil {
		return err
	}
	return p.View.Page(w, r, http.StatusOK, "通知", notificationsPage(list))
}

// notificationBell 由页头每 30 秒刷新一次。
func (p *Pages) notificationBell(w http.ResponseWriter, r *http.Request) error {
	pr, _ := auth.From[*account.Principal](r.Context())
	n, err := p.Notifications.UnreadCount(r.Context(), pr.ID)
	if err != nil {
		return err
	}
	return web.Render(w, r, http.StatusOK, views.NotificationBell(n))
}

// openNotification 标记已读后跳转到通知的链接（没有链接时回到通知列表）。
func (p *Pages) openNotification(w http.ResponseWriter, r *http.Request) error {
	pr, _ := auth.From[*account.Principal](r.Context())
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		web.Redirect(w, r, "/notifications")
		return nil
	}
	list, _, err := p.Notifications.List(r.Context(), pr.ID, notification.ListOptions{Limit: 100})
	if err != nil {
		return err
	}
	target := "/notifications"
	for _, n := range list {
		if n.ID == id {
			target = auth.SafeRedirect(n.URL, "/notifications")
		}
	}
	if err := p.Notifications.MarkRead(r.Context(), pr.ID, id); err != nil && !errors.Is(err, notification.ErrNotFound) {
		return err
	}
	web.Redirect(w, r, target)
	return nil
}

func (p *Pages) readAllNotifications(w http.ResponseWriter, r *http.Request) error {
	pr, _ := auth.From[*account.Principal](r.Context())
	if err := p.Notifications.MarkAllRead(r.Context(), pr.ID); err != nil {
		return err
	}
	web.Redirect(w, r, "/notifications")
	return nil
}
