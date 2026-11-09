package account

import (
	"context"
	"uuid"
)

// 账号事件类型，见 Service.OnEvent。
const (
	EventRegistered      = "account.registered"       // 注册（含第三方登录自动注册）
	EventLogin           = "account.login"            // 登录成功（网页或 App，创建了会话）
	EventLoginFailed     = "account.login_failed"     // 密码或两步验证码错误；UserID 可能为空，Email 为尝试的邮箱
	EventLogout          = "account.logout"           // 主动退出
	EventSessionRevoked  = "account.session_revoked"  // 在登录设备中注销了某个会话
	EventTokenReused     = "account.token_reused"     // 旧 refresh token 在宽限期后被再次使用，会话已注销
	EventPasswordChanged = "account.password_changed" // 修改或设置了密码
	EventPasswordReset   = "account.password_reset"   // 通过重置邮件设置了新密码
	EventEmailVerified   = "account.email_verified"   // 验证了邮箱
	EventMFAEnabled      = "account.mfa_enabled"      // 启用两步验证
	EventMFADisabled     = "account.mfa_disabled"     // 关闭两步验证
	EventAPIKeyCreated   = "account.api_key_created"  // 创建 API Key，SubjectID 为 Key 的 ID
	EventAPIKeyRevoked   = "account.api_key_revoked"  // 删除 API Key
	EventIdentityLinked  = "account.identity_linked"  // 绑定第三方账号，Provider 为平台名
	EventIdentityRemoved = "account.identity_removed" // 解绑第三方账号
	EventDisabled        = "account.disabled"         // 账号被停用
	EventEnabled         = "account.enabled"          // 账号被重新启用
)

// Event 是账号模块发生的安全相关事件，用于审计日志、安全提醒邮件等。
type Event struct {
	Type      string
	UserID    uuid.UUID // 相关账号；登录失败且邮箱不存在时为零值
	Email     string    // 登录失败时为尝试的邮箱，其他事件为空
	SessionID uuid.UUID // 登录、退出、注销会话时为相关会话
	SubjectID uuid.UUID // API Key 等事件对象的 ID
	Provider  string    // 第三方登录平台
}

// OnEvent 注册事件回调，回调在业务操作完成后同步调用，应尽快返回且不要 panic；
// 需要耗时处理（如发邮件）时在回调中发布消息。多次调用时按注册顺序依次调用。
// 必须在开始处理请求之前调用。
//
//	users.OnEvent(func(ctx context.Context, e account.Event) {
//		audits.Record(ctx, audit.Entry{Action: e.Type, ActorID: e.UserID, ...})
//	})
func (s *Service) OnEvent(fn func(context.Context, Event)) {
	s.listeners = append(s.listeners, fn)
}

func (s *Service) emit(ctx context.Context, e Event) {
	for _, fn := range s.listeners {
		fn(ctx, e)
	}
}
