// Package apitoken 提供存储在数据库中的 API token。
//
// token 分三类：Login 在用户登录时签发，按签发时的 Config 滑动过期，代表一台设备；
// Create 创建个人访问令牌，供脚本和第三方集成使用，过期时间由创建者指定；
// Authorize 签发 OAuth 令牌，只能用于授权时的资源（ValidateFor）。
// Validate 是 identity.TokenValidator，校验 token 并顺带延长登录 token 的有效期；
// List、Revoke、RevokeOthers、RevokeSubject 管理已签发的 token。
// 表中只保存 token 的 SHA-256，明文只在签发时返回一次；撤销立即生效。不同账号体系需要不同的有效期时，
// 各用自己的 Config 创建 Store 签发，校验用其中任何一个都可以。
//
// 同一个 token 有两种携带方式，由 rest.RequireToken 统一识别：浏览器把登录 token 放在 httpx.SessionCookie
// 返回的 HttpOnly cookie 中，页面上的脚本读不到它；App、脚本和第三方放在 Authorization: Bearer 中。
//
//	tokens := apitoken.NewStore(db, cfg.APIToken)
//	requireUser := rest.RequireToken(api, tokens.Validate)
//	plain, err := tokens.Login(ctx, sub, apitoken.Device{IP: ip, UserAgent: ua})
package apitoken
