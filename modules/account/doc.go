// Package account 是账号模块：注册、登录、刷新凭证、退出、找回与修改密码、登录设备管理。
//
// 模块不提供页面，也不注册路由。它提供：
//
//	Service           全部业务逻辑，页面与 API 共用
//	*Endpoint         REST 接口（rest.Endpoint），由产品选择路径挂载
//	PasswordResetMail 重置密码邮件的消息处理函数
//	Migrations        users、user_sessions、password_reset_tokens 三张表
//
// # 认证方式
//
// App 与第三方客户端：登录得到短时的 access token（HS256 JWT，默认 15 分钟）与 refresh token；
// access token 放在 Authorization: Bearer 头中，过期后用 refresh token 换取新的一对。
// refresh token 只在数据库中保存哈希，每次刷新都会轮换；超过宽限期再使用旧值视为泄露，整个会话被注销。
//
// 网页：登录后在 Cookie 会话（scs）中保存会话 ID，见 WebLogin、LoadWebSession、WebLogout。
//
// 两种方式的会话都在 user_sessions 表中，因此“登录设备”列表与踢下线对网页和 App 一视同仁；
// 认证后 context 中都是 *Principal，rest.RequireAuth() 声明的接口两种方式都可以访问。
//
// # 组装
//
//	// 迁移：加入 app.Options.Publish 后运行 go run . migrate publish
//	app.Options{Publish: []database.MigrationSource{account.Migrations}, ...}
//
//	// Setup 中：
//	users, err := account.NewService(a.DB, cfg.Account)
//	a.Router.Use(users.LoadWebSession(a.Sessions))
//
//	api := a.API()
//	api.UseMiddleware(rest.Authorize(api, users.VerifyAccessToken))
//	v1 := huma.NewGroup(api, "/api/v1")
//	rest.Post(v1, "/auth/login", account.LoginEndpoint(users))
//	...（完整列表见 endpoints.go）
//
//	err = messaging.Handle(a.Bus, account.TopicPasswordReset, account.PasswordResetMail{
//		Mailer: a.Mailer,
//		URL:    func(token string) string { return baseURL + "/reset-password?token=" + url.QueryEscape(token) },
//	}.Handle)
//
// 页面中取当前用户：
//
//	p, ok := auth.From[*account.Principal](r.Context())
//	u, err := users.User(r.Context(), p.ID)
//
// # 扩展用户数据
//
// 产品特有的用户字段放在自己的表中（以 user_id 关联 users.id），不要修改 users 表，
// 否则 go-kit 以后的迁移可能与之冲突。
package account
