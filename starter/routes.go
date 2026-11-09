package main

import (
	"net/url"

	"github.com/danielgtaylor/huma/v2"
	"github.com/go-chi/chi/v5"

	"github.com/ryan053857/go-kit/app"
	"github.com/ryan053857/go-kit/httpx/auth"
	"github.com/ryan053857/go-kit/httpx/rest"
	"github.com/ryan053857/go-kit/httpx/web"
	"github.com/ryan053857/go-kit/messaging"
	"github.com/ryan053857/go-kit/modules/account"
	"github.com/ryan053857/go-kit/starter/note"
	"github.com/ryan053857/go-kit/starter/pages"
	"github.com/ryan053857/go-kit/starter/views"
)

// setup 构造服务并挂载全部路由与消息处理函数：本文件就是整个服务的路由表。
func setup(a *app.App, cfg *Config) error {
	users, err := account.NewService(a.DB, cfg.Account)
	if err != nil {
		return err
	}
	notes := note.NewStore(a.DB)
	view := &views.Renderer{Users: users, Sessions: a.Sessions}
	web.ErrorHandler = view.Error

	// 消息处理函数。
	err = messaging.Handle(a.Bus, account.TopicPasswordReset, account.PasswordResetMail{
		Mailer: a.Mailer,
		URL:    func(token string) string { return cfg.BaseURL + "/reset-password?token=" + url.QueryEscape(token) },
	}.Handle)
	if err != nil {
		return err
	}

	// 中间件：必须在注册路由之前。
	a.Router.Use(users.LoadWebSession(a.Sessions))

	a.Router.Handle("/assets/*", views.Static)
	a.Router.Handle("/vendor/*", views.Vendor)

	// REST API：/api/v1，文档在 /docs。网页会话（Cookie）与 access token（Bearer）都可以访问。
	api := a.API()
	api.UseMiddleware(rest.Authorize(api, users.VerifyAccessToken))
	v1 := huma.NewGroup(api, "/api/v1")
	rest.Post(v1, "/auth/register", account.RegisterEndpoint(users))
	rest.Post(v1, "/auth/login", account.LoginEndpoint(users))
	rest.Post(v1, "/auth/refresh", account.RefreshEndpoint(users))
	rest.Post(v1, "/auth/logout", account.LogoutEndpoint(users))
	rest.Post(v1, "/auth/forgot-password", account.ForgotPasswordEndpoint(users))
	rest.Post(v1, "/auth/reset-password", account.ResetPasswordEndpoint(users))
	rest.Get(v1, "/me", account.MeEndpoint(users))
	rest.Patch(v1, "/me", account.UpdateMeEndpoint(users))
	rest.Post(v1, "/me/password", account.ChangePasswordEndpoint(users))
	rest.Get(v1, "/me/sessions", account.SessionsEndpoint(users))
	rest.Delete(v1, "/me/sessions/{id}", account.RevokeSessionEndpoint(users))
	rest.Mount(v1, note.APIActions(notes)...)

	// 页面。
	accountPages := &pages.Pages{Users: users, Sessions: a.Sessions, View: view}
	accountPages.MountPublic(a.Router)
	a.Router.Group(func(r chi.Router) {
		r.Use(auth.RequireLogin[*account.Principal]("/login"))
		accountPages.MountPrivate(r)
		note.MountPages(r, notes, view)
	})
	return nil
}
