// Package views 是各页面共用的布局、错误页与静态资源。
package views

import (
	"context"
	"io/fs"
	"net/http"
	"strings"

	"github.com/a-h/templ"
	"github.com/alexedwards/scs/v2"

	"github.com/ryan053857/go-kit/authz"
	"github.com/ryan053857/go-kit/examples/starter/assets"
	"github.com/ryan053857/go-kit/examples/starter/user"
	"github.com/ryan053857/go-kit/httpx/auth"
	"github.com/ryan053857/go-kit/httpx/web"
	"github.com/ryan053857/go-kit/modules/account"
	"github.com/ryan053857/go-kit/modules/notification"
	"github.com/ryan053857/go-kit/modules/setting"
)

// SiteSettings 是管理员在后台修改的站点设置（go-kit 的 setting 模块）。
type SiteSettings struct {
	Name             string `json:"name" minLength:"1" maxLength:"50" doc:"站点名称"`
	RegistrationOpen bool   `json:"registration_open" doc:"是否开放注册"`
}

// Site 是站点设置组。
var Site = setting.Group[SiteSettings]{Key: "site", Default: SiteSettings{Name: "Starter", RegistrationOpen: true}}

// Static 是 assets 包中的静态资源（挂在 /assets/），Vendor 是 web 包内置的 htmx（挂在 /vendor/）。
var (
	Static = mustAssets("/assets/", assets.FS)
	Vendor = mustAssets("/vendor/", web.Vendor)
)

// Page 是布局需要的数据。
type Page struct {
	Title    string
	SiteName string
	User     *user.User // 未登录时为 nil
	// AvatarURL 是当前用户的头像地址，没有头像时为空。
	AvatarURL string
	IsAdmin   bool
	Unread    int    // 未读通知数
	Flash     string // 上一个请求留下的提示，显示为 toast
}

// AdminAccess 是进入管理后台的能力（权限），由角色授予。
const AdminAccess = "admin.access"

// Renderer 渲染整页：加载当前用户、站点设置与闪存提示后套上 Layout。
type Renderer struct {
	Accounts      *account.Service
	People        *user.Store
	Sessions      *scs.SessionManager
	Settings      *setting.Store
	Notifications *notification.Service
	Gate          *authz.Gate
}

const flashKey = "flash"

// Page 渲染标题为 title 的页面；HTMX 局部请求只渲染 content（见 web.Page）。
func (v *Renderer) Page(w http.ResponseWriter, r *http.Request, status int, title string, content templ.Component) error {
	ctx := r.Context()
	site, err := Site.Get(ctx, v.Settings)
	if err != nil {
		return err
	}
	p := Page{Title: title, SiteName: site.Name}
	if id, ok := auth.UserID(ctx); ok {
		u, err := v.People.Get(ctx, id)
		if err != nil {
			return err
		}
		p.User = u
		p.AvatarURL = v.Accounts.AvatarURL(u.AvatarKey)
		p.IsAdmin = v.Gate.Allows(ctx, AdminAccess)
		if p.Unread, err = v.Notifications.UnreadCount(ctx, u.ID); err != nil {
			return err
		}
	}
	if !web.IsPartial(r) {
		p.Flash = v.Sessions.PopString(ctx, flashKey)
	}
	return web.Page(w, r, status, Layout(p), content)
}

// Flash 保存一条提示，在下一个整页请求中以 toast 显示，通常在重定向前调用。
func (v *Renderer) Flash(ctx context.Context, msg string) {
	v.Sessions.Put(ctx, flashKey, msg)
}

// Toast 在 HTMX 请求的响应中弹出提示：把 toast 标记追加到 body，不替换页面内容。
func Toast(w http.ResponseWriter, r *http.Request, msg string, isError bool) error {
	w.Header().Set("HX-Retarget", "body")
	w.Header().Set("HX-Reswap", "beforeend")
	return web.Render(w, r, http.StatusOK, toastMarker(msg, isError))
}

// Error 渲染错误页，在 Setup 中赋值给 web.ErrorHandler。
// 登记过的领域错误显示其（多语言）提示，5xx 只显示通用提示。
func (v *Renderer) Error(w http.ResponseWriter, r *http.Request, status int, err error) {
	msg := web.Message(r, status, err)
	if web.IsHTMX(r) {
		if Toast(w, r, msg, true) == nil {
			return
		}
	}
	if renderErr := web.Page(w, r, status, Layout(Page{Title: http.StatusText(status), SiteName: "Starter"}), errorContent(status, msg)); renderErr != nil {
		http.Error(w, http.StatusText(status), status)
	}
}

// DisplayName 返回用户的名称，未设置时返回邮箱。
func DisplayName(u *user.User) string { return u.DisplayName() }

func mustAssets(prefix string, fsys fs.FS) *web.Assets {
	a, err := web.NewAssets(prefix, fsys)
	if err != nil {
		panic(err)
	}
	return a
}

func initial(name string) string {
	for _, r := range name {
		return strings.ToUpper(string(r))
	}
	return "?"
}
