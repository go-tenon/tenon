package main

import (
	"context"
	"net/http"
	"net/url"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/httprate"
	"github.com/go-co-op/gocron/v2"

	"github.com/ryan053857/go-kit/app"
	"github.com/ryan053857/go-kit/httpx"
	"github.com/ryan053857/go-kit/httpx/auth"
	"github.com/ryan053857/go-kit/httpx/rest"
	"github.com/ryan053857/go-kit/httpx/web"
	"github.com/ryan053857/go-kit/messaging"
	"github.com/ryan053857/go-kit/modules/account"
	"github.com/ryan053857/go-kit/modules/audit"
	"github.com/ryan053857/go-kit/modules/export"
	"github.com/ryan053857/go-kit/modules/file"
	"github.com/ryan053857/go-kit/modules/notification"
	"github.com/ryan053857/go-kit/modules/setting"
	"github.com/ryan053857/go-kit/examples/starter/note"
	"github.com/ryan053857/go-kit/examples/starter/pages"
	"github.com/ryan053857/go-kit/examples/starter/views"
)

// setup 构造服务并挂载全部路由、消息处理函数与定时任务：本文件就是整个服务的路由表。
func setup(a *app.App, cfg *Config) error {
	users, err := account.NewService(a.DB, cfg.Account)
	if err != nil {
		return err
	}
	avatars := account.NewAvatars(users, a.Storage)
	files := file.NewService(a.DB, a.Storage, cfg.Files)
	audits := audit.New(a.DB, account.CurrentUserID)
	notices := notification.New(a.DB)
	settings := setting.New(a.DB)
	jobs := export.NewJobs(a.DB, a.Storage)
	notes := note.NewStore(a.DB)
	note.RegisterExport(jobs, notes)
	view := &views.Renderer{Users: users, Sessions: a.Sessions, Settings: settings, Notifications: notices, Admins: cfg.Admins}
	web.ErrorHandler = view.Error

	var providers []account.OAuthProvider
	if cfg.GitHub.ClientID != "" {
		providers = append(providers, account.GitHub(cfg.GitHub, cfg.BaseURL+"/auth/github/callback"))
	}
	oauth := &account.OAuthWeb{Users: users, Sessions: a.Sessions, Providers: providers}

	// 账号事件：写入审计日志；注册后发一条欢迎通知。
	users.OnEvent(audits.AccountEvent)
	users.OnEvent(func(ctx context.Context, e account.Event) {
		if e.Type == account.EventRegistered {
			_ = notices.Send(ctx, notification.Notification{Type: "welcome", Title: "欢迎使用", Body: "可以在账号设置中启用两步验证，保护账号安全。", URL: "/settings/security"}, e.UserID)
		}
	})
	jobs.OnFinish = func(ctx context.Context, job *export.Job) {
		n := notification.Notification{Type: "export.done", Title: "导出完成：" + job.Filename, URL: jobs.URL(job)}
		if job.Status == export.StatusFailed {
			n = notification.Notification{Type: "export.failed", Title: "导出失败：" + job.Filename, Body: "请稍后重试"}
		}
		_ = notices.Send(ctx, n, job.UserID)
	}

	// 消息处理函数。
	handlers := []error{
		messaging.Handle(a.Bus, account.TopicPasswordReset, account.PasswordResetMail{
			Mailer: a.Mailer,
			URL:    func(token string) string { return cfg.BaseURL + "/reset-password?token=" + url.QueryEscape(token) },
		}.Handle),
		messaging.Handle(a.Bus, account.TopicEmailVerification, account.EmailVerificationMail{
			Mailer: a.Mailer,
			URL:    func(token string) string { return cfg.BaseURL + "/verify-email?token=" + url.QueryEscape(token) },
		}.Handle),
		messaging.Handle(a.Bus, export.TopicExport, jobs.Handle),
	}
	for _, err := range handlers {
		if err != nil {
			return err
		}
	}

	// 定时任务：多实例部署时只在一个实例上运行。
	daily := func(hour uint) gocron.JobDefinition {
		return gocron.DailyJob(1, gocron.NewAtTimes(gocron.NewAtTime(hour, 0, 0)))
	}
	cron := []struct {
		name string
		def  gocron.JobDefinition
		fn   func(ctx context.Context) error
	}{
		{"清理未引用的文件", gocron.DurationJob(time.Hour), func(ctx context.Context) error { _, err := files.Cleanup(ctx); return err }},
		{"清理过期的导出文件", daily(3), func(ctx context.Context) error { _, err := jobs.Cleanup(ctx); return err }},
		{"清理 180 天前的审计日志", daily(4), func(ctx context.Context) error { _, err := audits.Prune(ctx, 180*24*time.Hour); return err }},
		{"清理 90 天前的已读通知", daily(4), func(ctx context.Context) error { _, err := notices.Prune(ctx, 90*24*time.Hour); return err }},
	}
	for _, c := range cron {
		if _, err := a.Scheduler.NewJob(c.def, gocron.NewTask(c.fn), gocron.WithName(c.name)); err != nil {
			return err
		}
	}

	// 中间件：必须在注册路由之前。
	a.Router.Use(
		audit.Middleware, // 审计日志记录 IP 与 User-Agent
		users.LoadWebSession(a.Sessions),
		users.LoadAccessToken, // 让文件下载等普通路由也能识别 App 的 Bearer token
	)

	a.Router.Handle("/assets/*", views.Static)
	a.Router.Handle("/vendor/*", views.Vendor)
	a.Router.Handle("/avatars/*", avatars.Handler())
	a.Router.Get("/files/{id}", files.DownloadHandler(file.OwnerOnly(account.CurrentUserID)).ServeHTTP)
	a.Router.Get("/exports/{id}/download", jobs.DownloadHandler(account.CurrentUserID).ServeHTTP)
	a.Router.Get("/auth/{provider}", oauth.Start)
	a.Router.Get("/auth/{provider}/callback", oauth.Callback)

	// REST API：/api/v1，文档在 /docs。网页会话（Cookie）、access token 与 API Key（Bearer）都可以访问。
	api := a.API()
	api.UseMiddleware(rest.Authorize(api, users.VerifyAccessToken))
	v1 := huma.NewGroup(api, "/api/v1")

	authAPI := huma.NewGroup(v1, "/auth") // 不需要登录、容易被暴力尝试的接口：每个 IP 每分钟 10 次
	authAPI.UseMiddleware(rest.RateLimit(api, 10, time.Minute))
	rest.Post(authAPI, "/register", account.RegisterEndpoint(users))
	rest.Post(authAPI, "/login", account.LoginEndpoint(users))
	rest.Post(authAPI, "/login/mfa", account.LoginMFAEndpoint(users))
	rest.Post(authAPI, "/forgot-password", account.ForgotPasswordEndpoint(users))
	rest.Post(authAPI, "/reset-password", account.ResetPasswordEndpoint(users))
	rest.Post(authAPI, "/verify-email", account.VerifyEmailEndpoint(users))
	rest.Post(v1, "/auth/refresh", account.RefreshEndpoint(users))
	rest.Post(v1, "/auth/logout", account.LogoutEndpoint(users))

	rest.Get(v1, "/me", account.MeEndpoint(users))
	rest.Patch(v1, "/me", account.UpdateMeEndpoint(users))
	rest.Post(v1, "/me/password", account.ChangePasswordEndpoint(users))
	rest.Post(v1, "/me/email/verification", account.SendVerificationEmailEndpoint(users))
	rest.Get(v1, "/me/sessions", account.SessionsEndpoint(users))
	rest.Delete(v1, "/me/sessions/{id}", account.RevokeSessionEndpoint(users))
	rest.Put(v1, "/me/avatar", account.SetAvatarEndpoint(avatars))
	rest.Delete(v1, "/me/avatar", account.RemoveAvatarEndpoint(avatars))
	rest.Get(v1, "/me/mfa", account.MFAStatusEndpoint(users))
	rest.Post(v1, "/me/mfa/setup", account.SetupMFAEndpoint(users))
	rest.Post(v1, "/me/mfa/enable", account.EnableMFAEndpoint(users))
	rest.Post(v1, "/me/mfa/disable", account.DisableMFAEndpoint(users))
	rest.Post(v1, "/me/mfa/recovery-codes", account.RegenerateRecoveryCodesEndpoint(users))
	rest.Get(v1, "/me/api-keys", account.APIKeysEndpoint(users))
	rest.Post(v1, "/me/api-keys", account.CreateAPIKeyEndpoint(users))
	rest.Delete(v1, "/me/api-keys/{id}", account.RevokeAPIKeyEndpoint(users))
	rest.Get(v1, "/me/identities", account.IdentitiesEndpoint(users))
	rest.Delete(v1, "/me/identities/{id}", account.UnlinkIdentityEndpoint(users))
	rest.Get(v1, "/me/audit", audit.MineEndpoint(audits, account.CurrentUserID))

	rest.Get(v1, "/notifications", notification.ListEndpoint(notices, account.CurrentUserID))
	rest.Get(v1, "/notifications/unread-count", notification.UnreadCountEndpoint(notices, account.CurrentUserID))
	rest.Post(v1, "/notifications/read-all", notification.MarkAllReadEndpoint(notices, account.CurrentUserID))
	rest.Post(v1, "/notifications/{id}/read", notification.MarkReadEndpoint(notices, account.CurrentUserID))
	rest.Delete(v1, "/notifications/{id}", notification.DeleteEndpoint(notices, account.CurrentUserID))

	rest.Post(v1, "/files", file.UploadEndpoint(files, account.CurrentUserID))
	rest.Post(v1, "/files/uploads", file.CreateUploadEndpoint(files, account.CurrentUserID)) // 直传，仅 S3 存储可用
	rest.Post(v1, "/files/{id}/complete", file.CompleteUploadEndpoint(files, account.CurrentUserID))
	rest.Post(v1, "/exports", export.StartEndpoint(jobs, account.CurrentUserID))
	rest.Get(v1, "/exports", export.JobsEndpoint(jobs, account.CurrentUserID))
	rest.Get(v1, "/exports/{id}", export.JobEndpoint(jobs, account.CurrentUserID))
	rest.Mount(v1, note.APIActions(notes)...)

	// 页面。表单提交限流：每个 IP 每分钟 10 次。
	limit := httprate.LimitBy(10, time.Minute, func(r *http.Request) (string, error) { return httpx.ClientIP(r), nil })
	accountPages := &pages.Pages{Users: users, Avatars: avatars, Sessions: a.Sessions, View: view,
		Audit: audits, Notifications: notices, Settings: settings, OAuth: providers}
	accountPages.MountPublic(a.Router, limit)
	a.Router.Group(func(r chi.Router) {
		r.Use(auth.RequireLogin[*account.Principal]("/login"))
		accountPages.MountPrivate(r, limit)
		note.MountPages(r, notes, jobs, view)
		r.Group(func(r chi.Router) {
			r.Use(view.RequireAdmin)
			accountPages.MountAdmin(r)
		})
	})
	return nil
}
