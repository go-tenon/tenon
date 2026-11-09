package main

import (
	"github.com/danielgtaylor/huma/v2"
	"github.com/go-chi/chi/v5"

	"github.com/ryan053857/go-kit/app"
	"github.com/ryan053857/go-kit/httpx/auth"
	"github.com/ryan053857/go-kit/httpx/rest"
	"github.com/ryan053857/go-kit/httpx/web"
	"github.com/ryan053857/go-kit/starter/account"
	"github.com/ryan053857/go-kit/starter/note"
	"github.com/ryan053857/go-kit/starter/user"
	"github.com/ryan053857/go-kit/starter/views"
)

// setup 构造服务并挂载全部路由：本文件就是整个服务的路由表。
func setup(a *app.App, cfg *Config) error {
	web.ErrorHandler = views.ErrorHandler

	users := user.NewStore(cfg.Demo.Name, string(cfg.Demo.Password), string(cfg.Demo.APIToken))
	notes := note.NewStore(a.DB)

	// 中间件：必须在注册路由之前。
	a.Router.Use(auth.LoadSession(a.Sessions, users.Load))

	a.Router.Handle("/static/*", views.Static)
	a.Router.Handle("/vendor/*", views.Vendor)

	// REST API：/api/v1，文档在 /docs。
	api := a.API()
	api.UseMiddleware(rest.Authorize(api, users.VerifyToken))
	v1 := huma.NewGroup(api, "/api/v1")
	rest.Mount(v1, note.APIActions(notes)...)

	// 页面。
	account.MountPages(a.Router, a.Sessions, users)
	a.Router.Group(func(r chi.Router) {
		r.Use(auth.RequireLogin[*user.User]("/login"))
		note.MountPages(r, notes)
	})
	return nil
}
